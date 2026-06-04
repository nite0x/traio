package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
		Candles:     brokers.Candles,
		Portfolio:   brokers.Portfolio,
		News:        newsSvc,
		AI:          aiSvc,
	}

	router := api.NewRouter(deps, api.ServerControl{
		StartedAt: startedAt,
		APIURL:    fmt.Sprintf("http://127.0.0.1:%d", config.DefaultServerPort),
		Shutdown: func() {
			quit <- syscall.SIGTERM
		},
	})

	if err := runtime.WritePID(baseDir, os.Getpid()); err != nil {
		log.Printf("write pid: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", config.DefaultServerPort)
	apiURL := "http://" + addr
	srv := &http.Server{Addr: addr, Handler: router}
	go func() {
		log.Printf("traio server listening on %s", apiURL)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-quit
	log.Println("shutting down traio-server (IBKR gateway stays running)")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)

	runtime.RemovePID(baseDir)
}
