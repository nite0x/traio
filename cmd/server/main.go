package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nite/traio/internal/ai"
	"github.com/nite/traio/internal/api"
	traioauth "github.com/nite/traio/internal/auth"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/news"
	"github.com/nite/traio/internal/runtime"
	"github.com/nite/traio/internal/settings"
	"github.com/nite/traio/internal/store"
)

func main() {
	flag.Parse()

	baseDir := config.ResolveRuntimeDir()
	instanceLock, err := runtime.AcquireInstanceLock(baseDir)
	if err != nil {
		log.Fatalf("instance lock: %v", err)
	}
	defer instanceLock.Close()

	apiToken, err := runtime.LoadOrCreateAPIToken(baseDir)
	if err != nil {
		log.Fatalf("API token: %v", err)
	}
	database := config.ResolveBootstrapDatabase(baseDir)
	st, err := store.OpenRepository(database.Driver, database.DataSource)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	authConfig, err := config.ResolveAuthConfig()
	if err != nil {
		log.Fatalf("authentication config: %v", err)
	}
	authService, err := traioauth.NewService(context.Background(), st, authConfig)
	if err != nil {
		log.Fatalf("authentication: %v", err)
	}

	settingsMgr := settings.NewManager(st, baseDir)
	if err := settingsMgr.Load(context.Background()); err != nil {
		log.Fatalf("settings: %v", err)
	}

	cfg := settingsMgr.Get()

	connections, err := runtime.BuildConnectionManager(cfg, st, baseDir)
	if err != nil {
		log.Fatalf("brokers: %v", err)
	}
	brokerSync := runtime.BuildBrokerSync(st, connections)
	brokerSync.SetSyncConfig(cfg.BrokerSync)
	accountEquity := runtime.BuildAccountEquity(connections)
	newsSvc := news.New(cfg.Finnhub)
	aiSvc := ai.New(cfg.Claude)

	settingsMgr.OnApply(func(updated config.Config) {
		if err := connections.ApplyConfig(context.Background(), updated); err != nil {
			log.Printf("apply broker config: %v", err)
			return
		}
		brokerSync.SetSyncConfig(updated.BrokerSync)
		brokerSync.Invalidate()
		newsSvc.SetConfig(updated.Finnhub)
		aiSvc.SetConfig(updated.Claude)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	brokerSync.StartBackground(ctx, 0)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	startedAt := time.Now()
	deps := api.Deps{
		Brokers:     st,
		Connections: connections,
		OnBrokersChanged: func(ctx context.Context) error {
			if err := connections.Reload(ctx); err != nil {
				return err
			}
			brokerSync.SetSources(connections.SyncSources()...)
			accountEquity.SetSources(connections.AccountSources()...)
			brokerSync.Invalidate()
			return nil
		},
		Watchlists:      st,
		CandleCache:     st,
		Settings:        settingsMgr,
		Instruments:     connections.MarketData,
		Quotes:          connections.MarketData,
		Candles:         connections.MarketData,
		BrokerSync:      brokerSync,
		Account:         accountEquity,
		News:            newsSvc,
		AI:              aiSvc,
		APIToken:        apiToken,
		AllowedAPIHosts: config.ResolveAllowedAPIHosts(),
		AllowedOrigins:  config.ResolveAllowedOrigins(),
		WebDir:          config.ResolveWebDir(),
		Auth:            authService,
		Trading:         connections.Trading,
	}

	addr := config.ResolveServerListenAddr()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen on %s: %v", addr, err)
	}
	defer listener.Close()
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		log.Fatalf("unexpected listener address %T", listener.Addr())
	}
	apiURL := config.LocalAPIURL(tcpAddr.Port)
	if err := runtime.WriteAPIURL(baseDir, apiURL); err != nil {
		log.Fatalf("write API URL: %v", err)
	}
	defer runtime.RemoveAPIURL(baseDir)

	router := api.NewRouter(deps, api.ServerControl{
		StartedAt: startedAt,
		APIURL:    apiURL,
		Shutdown: func() {
			quit <- syscall.SIGTERM
		},
	})

	if err := runtime.WritePID(baseDir, os.Getpid()); err != nil {
		log.Printf("write pid: %v", err)
	}

	srv := &http.Server{Handler: router}
	go func() {
		log.Printf("traio server listening on %s (%s)", addr, apiURL)
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-quit
	log.Println("shutting down traio-server")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	runtime.RemovePID(baseDir)
}
