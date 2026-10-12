package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nite/traio/internal/ai"
	"github.com/nite/traio/internal/api"
	traioauth "github.com/nite/traio/internal/auth"
	"github.com/nite/traio/internal/bootstrap"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/history"
	"github.com/nite/traio/internal/market"
	"github.com/nite/traio/internal/market/tiingo"
	"github.com/nite/traio/internal/market/yahoo"
	"github.com/nite/traio/internal/news"
	"github.com/nite/traio/internal/performance"
	"github.com/nite/traio/internal/runtime"
	"github.com/nite/traio/internal/settings"
	"github.com/nite/traio/internal/store"
)

func main() {
	flag.Parse()

	baseDir := config.ResolveRuntimeDir()
	startup, err := bootstrap.Load(context.Background(), baseDir)
	if err != nil {
		log.Fatalf("startup configuration: %v", err)
	}
	for _, warning := range startup.Warnings {
		log.Printf("startup configuration: %s", warning)
	}
	instanceLock, err := runtime.AcquireInstanceLock(baseDir)
	if err != nil {
		log.Fatalf("instance lock: %v", err)
	}
	defer instanceLock.Close()

	apiToken, err := runtime.LoadOrCreateAPIToken(baseDir)
	if err != nil {
		log.Fatalf("API token: %v", err)
	}
	marketDataSecrets, err := market.NewSecretCipher(apiToken)
	if err != nil {
		log.Fatalf("market data credentials: %v", err)
	}
	database := startup.Database
	st, err := store.OpenRepository(database.Driver, database.DataSource)
	if err != nil {
		log.Fatal("store: unable to initialize configured database; check connection, permissions and schema")
	}
	defer st.Close()
	authConfig := startup.Auth
	authService, err := traioauth.NewService(context.Background(), st, authConfig)
	if err != nil {
		log.Fatalf("authentication: %s", traioauth.InitializationErrorMessage(err))
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
	marketData := market.NewService(connections.MarketData, nil, yahoo.New())
	reloadTiingo := func(ctx context.Context) error {
		provider, loadErr := st.GetBrokerProviderRuntimeConfig(ctx, "TIINGO")
		if loadErr != nil {
			return loadErr
		}
		enabled, _ := provider.Config["enabled"].(bool)
		if !enabled {
			marketData.SetTiingo(nil)
			return nil
		}
		encrypted := strings.TrimSpace(provider.Secrets["api_token"])
		if encrypted == "" {
			return market.NewProviderError(market.ErrorNotConfigured, "tiingo", "configure", "API token is not configured", nil)
		}
		token, decryptErr := marketDataSecrets.Decrypt(encrypted, "tiingo/api_token")
		if decryptErr != nil {
			return fmt.Errorf("Tiingo credential cannot be decrypted; save the token again")
		}
		baseURL, _ := provider.Config["base_url"].(string)
		client, clientErr := tiingo.New(tiingo.Config{APIToken: token, BaseURL: baseURL})
		if clientErr != nil {
			return clientErr
		}
		marketData.SetTiingo(client)
		return nil
	}
	if err := reloadTiingo(context.Background()); err != nil {
		log.Fatalf("Tiingo market data: %v", err)
	}
	brokerSync := runtime.BuildBrokerSync(st, connections)
	brokerSync.SetSyncConfig(cfg.BrokerSync)
	historyService := history.New(st)
	historyService.SetSyncConfig(cfg.BrokerSync)
	performanceMode, err := performance.ParseRetentionMode(os.Getenv("TRAIO_PERFORMANCE_RETENTION_MODE"))
	if err != nil {
		log.Fatalf("performance retention: %v", err)
	}
	if performanceMode == performance.RetentionLicensedPersistence && !strings.EqualFold(strings.TrimSpace(os.Getenv("TRAIO_MARKET_DATA_PERSISTENCE_LICENSED")), "true") {
		log.Fatal("performance retention: licensed_persistence requires TRAIO_MARKET_DATA_PERSISTENCE_LICENSED=true and applicable written permission")
	}
	performanceService := performance.New(st, performance.NewMarketValuationProvider(marketData), performanceMode)
	historyService.SetOnHistoryChanged(performanceService.RefreshAccounts)
	historyService.SetSessionResolver(connections.AcquireSession)
	accountEquity := runtime.BuildAccountEquity(connections)
	newsSvc := news.New(cfg.Finnhub)
	aiSvc := ai.New(cfg.Claude)

	settingsMgr.OnApply(func(updated config.Config) {
		if err := connections.ApplyConfig(context.Background(), updated); err != nil {
			log.Printf("apply broker config: %v", err)
			return
		}
		brokerSync.SetSyncConfig(updated.BrokerSync)
		historyService.SetSyncConfig(updated.BrokerSync)
		brokerSync.Invalidate()
		newsSvc.SetConfig(updated.Finnhub)
		aiSvc.SetConfig(updated.Claude)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	brokerSync.StartBackground(ctx, 0)
	stopTradingEvents := connections.StartTradingEvents(ctx, brokerSync)
	defer stopTradingEvents()
	historyService.Start(ctx)
	performanceService.Start(ctx)

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
		Watchlists:          st,
		CandleCache:         st,
		Settings:            settingsMgr,
		Instruments:         connections.MarketData,
		Quotes:              connections.MarketData,
		Candles:             connections.MarketData,
		PublicMarketData:    marketData,
		MarketDataSecrets:   marketDataSecrets,
		OnMarketDataChanged: reloadTiingo,
		BrokerSync:          brokerSync,
		Account:             accountEquity,
		News:                newsSvc,
		AI:                  aiSvc,
		APIToken:            apiToken,
		AllowedAPIHosts:     config.ResolveAllowedAPIHosts(),
		AllowedOrigins:      config.ResolveAllowedOrigins(),
		WebDir:              config.ResolveWebDir(),
		Auth:                authService,
		Trading:             connections.Trading,
		History:             historyService,
		Performance:         performanceService,
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
