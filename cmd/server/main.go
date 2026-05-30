package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/nite/traio/internal/ai"
	"github.com/nite/traio/internal/api"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/news"
	"github.com/nite/traio/internal/runtime"
	"github.com/nite/traio/internal/settings"
	"github.com/nite/traio/internal/store"
)

func main() {
	flag.Parse()

	baseDir := config.ResolveRuntimeDir()
	bootstrapDB := filepath.Join(baseDir, "data", "traio.db")

	st, err := store.Open(bootstrapDB)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	settingsMgr := settings.NewManager(st, baseDir)
	if err := settingsMgr.Load(context.Background()); err != nil {
		log.Fatalf("settings: %v", err)
	}

	cfg := settingsMgr.Get()

	brokers := runtime.BuildBrokers(cfg)
	newsSvc := news.New(cfg.Finnhub)
	aiSvc := ai.New(cfg.Claude)

	settingsMgr.OnApply(func(updated config.Config) {
		brokers.ApplyConfig(updated)
		newsSvc.SetConfig(updated.Finnhub)
		aiSvc.SetConfig(updated.Claude)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := brokers.StartGateway(ctx); err != nil {
			log.Printf("ibkr gateway: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	startedAt := time.Now()
	deps := api.Deps{
		Store:       st,
		Settings:    settingsMgr,
		Schwab:      brokers.Schwab,
		IBKR:        brokers.Gateway,
		Instruments: brokers.Instruments,
		Quotes:      brokers.Quotes,
		Portfolio:   brokers.Portfolio,
		News:        newsSvc,
		AI:          aiSvc,
	}

	router := api.NewRouter(deps, api.ServerControl{
		BaseDir:   baseDir,
		StartedAt: startedAt,
		Shutdown: func() {
			quit <- syscall.SIGTERM
		},
	})

	host := cfg.Server.Host
	if host == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		log.Fatalf("listen: unexpected addr type")
	}
	ep := runtime.Endpoint{
		Host: tcpAddr.IP.String(),
		Port: tcpAddr.Port,
	}
	if ep.Host == "0.0.0.0" || ep.Host == "::" {
		ep.Host = host
	}
	ep.APIURL = "http://" + net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port))
	if err := runtime.WriteEndpoint(baseDir, ep); err != nil {
		log.Printf("write endpoint: %v", err)
	}
	if err := runtime.WritePID(baseDir, os.Getpid()); err != nil {
		log.Printf("write pid: %v", err)
	}

	srv := &http.Server{Handler: router}
	go func() {
		log.Printf("traio server listening on %s", ep.APIURL)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-quit
	log.Println("shutting down traio-server (IBKR gateway stays running)")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)

	runtime.RemoveEndpoint(baseDir)
	runtime.RemovePID(baseDir)
}
