package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
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

	settingsMgr := settings.NewManager(st, baseDir)
	if err := settingsMgr.Load(context.Background()); err != nil {
		log.Fatalf("settings: %v", err)
	}

	cfg := settingsMgr.Get()

	brokers, err := runtime.BuildBrokers(cfg, st, baseDir)
	if err != nil {
		log.Fatalf("brokers: %v", err)
	}
	brokerSync := runtime.BuildBrokerSync(st, brokers)
	brokerSync.SetSyncConfig(cfg.BrokerSync)
	accountEquity := runtime.BuildAccountEquity(brokers)
	newsSvc := news.New(cfg.Finnhub)
	aiSvc := ai.New(cfg.Claude)

	settingsMgr.OnApply(func(updated config.Config) {
		if err := brokers.ApplyConfig(context.Background(), updated); err != nil {
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

	go func() {
		if err := brokers.StartGateway(ctx); err != nil {
			log.Printf("ibkr gateway: %v", err)
			return
		}
		brokerSync.Invalidate()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	startedAt := time.Now()
	ibkrLoginProxy, err := api.NewIBKRLoginProxy(config.ResolveIBKRProxyURL(), brokers)
	if err != nil {
		log.Fatalf("IBKR login proxy: %v", err)
	}
	deps := api.Deps{
		Brokers:       st,
		BrokerRuntime: brokers,
		OnBrokersChanged: func(ctx context.Context) error {
			if err := brokers.Reload(ctx); err != nil {
				return err
			}
			brokerSync.SetSources(brokers.SyncSources()...)
			accountEquity.SetSources(brokers.AccountSources()...)
			brokerSync.Invalidate()
			return nil
		},
		Watchlists:      st,
		CandleCache:     st,
		Settings:        settingsMgr,
		Schwab:          brokers.Schwab,
		Alpaca:          brokers.Alpaca,
		IBKR:            brokers.Gateway,
		IBKRGateways:    brokers,
		Instruments:     brokers.Instruments,
		Quotes:          brokers.Quotes,
		Candles:         brokers.Candles,
		BrokerSync:      brokerSync,
		Account:         accountEquity,
		News:            newsSvc,
		AI:              aiSvc,
		APIToken:        apiToken,
		AllowedAPIHosts: config.ResolveAllowedAPIHosts(),
		IBKRLoginProxy:  ibkrLoginProxy,
		RuntimeDir:      baseDir,
	}

	router := api.NewRouter(deps, api.ServerControl{
		StartedAt: startedAt,
		APIURL:    config.ResolveServerAPIURL(),
		Shutdown: func() {
			quit <- syscall.SIGTERM
		},
	})

	if err := runtime.WritePID(baseDir, os.Getpid()); err != nil {
		log.Printf("write pid: %v", err)
	}

	addr := config.ResolveServerListenAddr()
	apiURL := config.ResolveServerAPIURL()
	srv := &http.Server{Addr: addr, Handler: router}
	go func() {
		log.Printf("traio server listening on %s (%s)", addr, apiURL)
		if ibkrLoginProxy != nil {
			log.Printf("IBKR login proxy enabled at %s", ibkrLoginProxy.ExternalURL())
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-quit
	log.Println("shutting down traio-server")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	if err := brokers.ShutdownGateways(); err != nil {
		log.Printf("shutdown IBKR gateways: %v", err)
	}

	runtime.RemovePID(baseDir)
}
