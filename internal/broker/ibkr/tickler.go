package ibkr

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"
)

// StartTickler keeps the IBKR session alive with periodic tickle requests.
func (g *GatewayManager) StartTickler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.tickle()
			}
		}
	}()
}

func (g *GatewayManager) tickle() {
	resp, err := g.httpClient.Post(
		g.config.GatewayURL+"/v1/api/tickle",
		"application/json",
		strings.NewReader("{}"),
	)
	if err != nil {
		log.Printf("[IBKR] tickle failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[IBKR] tickle status %d", resp.StatusCode)
	}
}

// StartHealthMonitor periodically checks gateway health and restarts on failure.
func (g *GatewayManager) StartHealthMonitor(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := g.secureRuntimePermissions(); err != nil {
					g.audit("gateway.permissions", "error", err.Error())
				}
				if !g.isHealthy() {
					log.Println("[IBKR] gateway unhealthy, restarting...")
					if err := g.restart(); err != nil {
						log.Printf("[IBKR] restart failed: %v", err)
					}
				}
			}
		}
	}()
}
