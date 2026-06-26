//go:build ios

// Package mobile is the gomobile-bound entry point for running the Traio Go
// backend in-process on iOS. Unlike the desktop build (cmd/server), which runs
// as a standalone process launched via Process.start, iOS forbids spawning
// external executables — so the backend must run inside the app process as a
// library. gomobile bind compiles this package into Traio.xcframework, and the
// Flutter side calls StartServer over a MethodChannel.
//
// The iOS build is Schwab-only: it excludes the ibkr package entirely (no Java
// gateway, chromedp, or os/exec — all forbidden in the iOS sandbox) via the
// build-tag split in internal/runtime.
package mobile

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/nite/traio/internal/ai"
	"github.com/nite/traio/internal/api"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/news"
	"github.com/nite/traio/internal/runtime"
	"github.com/nite/traio/internal/settings"
	"github.com/nite/traio/internal/store"
)

var (
	mu      sync.Mutex
	srv     *http.Server
	runPort int
)

// StartServer boots the backend HTTP server on a loopback port inside the app
// process and returns the chosen port. dataDir must be a writable directory
// (the iOS app's Documents directory, passed in from Flutter) used for the
// SQLite database. Calling it again while already running is a no-op that
// returns the existing port.
//
// gomobile exposes this as -[TraioMobile startServer:error:] returning an int.
func StartServer(dataDir string) (int, error) {
	mu.Lock()
	defer mu.Unlock()

	if srv != nil {
		return runPort, nil // already running; return the existing port
	}

	st, err := store.Open(filepath.Join(dataDir, "traio.db"))
	if err != nil {
		return 0, err
	}

	settingsMgr := settings.NewManager(st, dataDir)
	if err := settingsMgr.Load(context.Background()); err != nil {
		st.Close()
		return 0, err
	}
	cfg := settingsMgr.Get()

	brokers := runtime.BuildBrokers(cfg, st) // iOS: Schwab-only, gateway == nil
	positions := runtime.BuildPositionSync(st, brokers)
	accountEquity := runtime.BuildAccountEquity(brokers)
	newsSvc := news.New(cfg.Finnhub)
	aiSvc := ai.New(cfg.Claude)
	syncCtx := context.Background()
	positions.StartBackground(syncCtx, 0)

	settingsMgr.OnApply(func(updated config.Config) {
		brokers.ApplyConfig(updated)
		positions.Invalidate()
		newsSvc.SetConfig(updated.Finnhub)
		aiSvc.SetConfig(updated.Claude)
	})

	deps := api.Deps{
		Store:       st,
		Settings:    settingsMgr,
		Schwab:      brokers.Schwab,
		Alpaca:      brokers.Alpaca,
		IBKR:        brokers.Gateway, // nil on iOS; /ibkr/* routes degrade gracefully
		Instruments: brokers.Instruments,
		Quotes:      brokers.Quotes,
		Candles:     brokers.Candles,
		Positions:   positions,
		Account:     accountEquity,
		News:        newsSvc,
		AI:          aiSvc,
	}

	// No pid/endpoint files and no signal handling: on iOS the OS owns the
	// process lifecycle and there is no second process to coordinate with.
	router := api.NewRouter(deps, api.ServerControl{
		StartedAt: time.Now(),
		Shutdown:  nil, // shutdown is driven by the app lifecycle, not an HTTP call
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0") // random free loopback port
	if err != nil {
		st.Close()
		return 0, err
	}

	srv = &http.Server{Handler: router}
	runPort = ln.Addr().(*net.TCPAddr).Port
	go func() {
		_ = srv.Serve(ln)
	}()

	return runPort, nil
}
