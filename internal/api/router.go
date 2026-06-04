package api

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/ai"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/news"
	"github.com/nite/traio/internal/portfolio"
	"github.com/nite/traio/internal/settings"
	"github.com/nite/traio/internal/store"
)

type Deps struct {
	Store       *store.Store
	Settings    *settings.Manager
	Schwab      *schwab.Client
	IBKR        broker.GatewayController
	Instruments broker.InstrumentProvider
	Quotes      broker.BatchMarketDataProvider
	Candles     broker.CandleProvider
	Portfolio   *portfolio.Service
	News        *news.Service
	AI          *ai.Service
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "http://localhost:1420")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func NewRouter(deps Deps, serverCtrl ServerControl) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger(), corsMiddleware())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "traio"})
	})

	v1 := r.Group("/api/v1")
	{
		v1.GET("/watchlist/groups", listWatchlistGroups(deps.Store))
		v1.GET("/watchlist/groups/:group_id/items", listWatchlistItems(deps.Store))
		v1.POST("/watchlist/groups/:group_id/items", upsertWatchlistItem(deps.Store))
		v1.DELETE("/watchlist/groups/:group_id/items/:symbol", deleteWatchlistItem(deps.Store))
		v1.GET("/instruments/search", searchInstruments(deps.Instruments))
		v1.GET("/quotes", listQuotes(deps.Quotes))
		v1.GET("/quotes/:symbol", getQuote(deps.Schwab, deps.Instruments, deps.Quotes))
		v1.GET("/quotes/:symbol/history", getHistory(deps.Instruments, deps.Candles))
		v1.GET("/positions", listPositions(deps.Portfolio))
		v1.GET("/account/equity", accountEquity(deps.Portfolio))
		v1.GET("/news/:symbol", getNews(deps.News))
		v1.POST("/orders", placeOrder(deps.Portfolio))
		v1.GET("/ws", wsQuotes())

		v1.GET("/ibkr/gateway/status", ibkrGatewayStatus(deps.IBKR))
		v1.POST("/ibkr/gateway/start", ibkrGatewayStart(deps.IBKR, deps.Portfolio))
		v1.POST("/ibkr/gateway/stop", ibkrGatewayStop(deps.IBKR, deps.Portfolio))
		v1.POST("/ibkr/gateway/reconnect", ibkrGatewayReconnect(deps.IBKR, deps.Portfolio))

		v1.GET("/server/status", serverStatus(serverCtrl))
		v1.POST("/server/shutdown", serverShutdown(serverCtrl))

		v1.GET("/settings", getSettings(deps.Settings))
		v1.PUT("/settings", putSettings(deps.Settings))
		v1.GET("/settings/defaults", getSettingsDefaults(deps.Settings))
	}

	return r
}

func parseConIDsParam(value string) ([]int64, error) {
	if strings.TrimSpace(value) == "" {
		return []int64{}, nil
	}
	parts := strings.Split(value, ",")
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		conID, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || conID <= 0 {
			return nil, fmt.Errorf("invalid conid %q", part)
		}
		out = append(out, conID)
	}
	return out, nil
}

func parseGroupID(c *gin.Context) (int64, bool) {
	groupID, err := strconv.ParseInt(c.Param("group_id"), 10, 64)
	if err != nil || groupID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return 0, false
	}
	return groupID, true
}

func listWatchlistGroups(st *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		groups, err := st.ListWatchlistGroups(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, groups)
	}
}

func listWatchlistItems(st *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, ok := parseGroupID(c)
		if !ok {
			return
		}
		items, err := st.ListWatchlistItems(c.Request.Context(), groupID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, items)
	}
}

type watchlistItemRequest struct {
	Symbol   string `json:"symbol"`
	ConID    int64  `json:"conid"`
	Name     string `json:"name"`
	SecType  string `json:"sec_type"`
	Exchange string `json:"exchange"`
	Currency string `json:"currency"`
	Tags     string `json:"tags"`
	Notes    string `json:"notes"`
}

func upsertWatchlistItem(st *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, ok := parseGroupID(c)
		if !ok {
			return
		}
		var req watchlistItemRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		item, err := st.UpsertWatchlistItem(c.Request.Context(), store.WatchlistItem{
			GroupID:  groupID,
			Symbol:   req.Symbol,
			ConID:    req.ConID,
			Name:     req.Name,
			SecType:  req.SecType,
			Exchange: req.Exchange,
			Currency: req.Currency,
			Tags:     req.Tags,
			Notes:    req.Notes,
		})
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, item)
	}
}

func deleteWatchlistItem(st *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, ok := parseGroupID(c)
		if !ok {
			return
		}
		if err := st.DeleteWatchlistItem(c.Request.Context(), groupID, c.Param("symbol")); err != nil {
			if err == sql.ErrNoRows {
				c.JSON(http.StatusNotFound, gin.H{"error": "watchlist item not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func searchInstruments(provider broker.InstrumentProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "instrument search is not available"})
			return
		}
		query := strings.TrimSpace(c.Query("q"))
		if query == "" {
			c.JSON(http.StatusOK, []broker.Instrument{})
			return
		}
		results, err := provider.SearchInstruments(c.Request.Context(), query)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, results)
	}
}

func listQuotes(provider broker.BatchMarketDataProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "quote snapshots are not available"})
			return
		}
		conIDs, err := parseConIDsParam(c.Query("conids"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if len(conIDs) == 0 {
			c.JSON(http.StatusOK, []broker.Quote{})
			return
		}
		quotes, err := provider.GetQuotesByConID(c.Request.Context(), conIDs)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, quotes)
	}
}

func getQuote(
	schwabClient *schwab.Client,
	instruments broker.InstrumentProvider,
	quotes broker.BatchMarketDataProvider,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		symbol := strings.TrimSpace(c.Param("symbol"))
		if symbol == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "symbol is required"})
			return
		}
		if instruments != nil && quotes != nil {
			results, err := instruments.SearchInstruments(c.Request.Context(), symbol)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
				return
			}
			instrument, ok := preferredInstrument(symbol, results)
			if !ok {
				c.JSON(http.StatusNotFound, gin.H{"error": "instrument not found"})
				return
			}
			snapshots, err := quotes.GetQuotesByConID(c.Request.Context(), []int64{instrument.ConID})
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
				return
			}
			if len(snapshots) == 0 {
				c.JSON(http.StatusNotFound, gin.H{"error": "quote not found"})
				return
			}
			if snapshots[0].Symbol == "" {
				snapshots[0].Symbol = instrument.Symbol
			}
			c.JSON(http.StatusOK, snapshots[0])
			return
		}

		if schwabClient == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "quote provider is not available"})
			return
		}
		q, err := schwabClient.GetQuote(c.Request.Context(), symbol)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, q)
	}
}

func preferredInstrument(symbol string, results []broker.Instrument) (broker.Instrument, bool) {
	if len(results) == 0 {
		return broker.Instrument{}, false
	}
	for _, result := range results {
		if strings.EqualFold(result.Symbol, symbol) {
			return result, true
		}
	}
	return results[0], true
}

func listPositions(svc *portfolio.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		pos, err := svc.AllPositions(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, pos)
	}
}

func accountEquity(svc *portfolio.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "portfolio service is not available"})
			return
		}
		points, summary, err := svc.AccountTimeline(c.Request.Context())
		if err != nil && len(points) == 0 && summary.NetLiquidation == 0 {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		payload := gin.H{
			"points":  points,
			"summary": summary,
		}
		if err != nil {
			payload["warning"] = err.Error()
		}
		c.JSON(http.StatusOK, payload)
	}
}

func getNews(svc *news.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		symbol := c.Param("symbol")
		articles, err := svc.BySymbol(c.Request.Context(), symbol, 20)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, articles)
	}
}

func placeOrder(svc *portfolio.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "place order not implemented"})
	}
}

// periodToBar returns a sensible default bar size for a given period.
var periodToBar = map[string]string{
	"1d":  "5min",
	"5d":  "30min",
	"1m":  "1h",
	"3m":  "1d",
	"6m":  "1d",
	"1y":  "1d",
	"2y":  "1w",
	"5y":  "1w",
}

func getHistory(instruments broker.InstrumentProvider, candles broker.CandleProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		if candles == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "candle data not available"})
			return
		}
		symbol := strings.TrimSpace(c.Param("symbol"))
		period := c.DefaultQuery("period", "1m")
		bar := c.DefaultQuery("bar", "")

		if bar == "" {
			if b, ok := periodToBar[period]; ok {
				bar = b
			} else {
				bar = "1d"
			}
		}

		// Resolve symbol → conid
		if instruments == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "instrument search not available"})
			return
		}
		results, err := instruments.SearchInstruments(c.Request.Context(), symbol)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		instrument, ok := preferredInstrument(symbol, results)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "instrument not found"})
			return
		}

		bars, err := candles.GetCandles(c.Request.Context(), instrument.ConID, period, bar)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"symbol":  instrument.Symbol,
			"conid":   instrument.ConID,
			"period":  period,
			"bar":     bar,
			"candles": bars,
		})
	}
}

func ibkrGatewayStatus(gw broker.GatewayController) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gw == nil {
			c.JSON(http.StatusOK, gin.H{
				"running":             false,
				"authenticated":       false,
				"account":             "",
				"session_age_seconds": 0,
			})
			return
		}
		c.JSON(http.StatusOK, gw.Status())
	}
}

func ibkrGatewayReconnect(gw broker.GatewayController, portfolioSvc *portfolio.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gw == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ibkr gateway not configured"})
			return
		}
		if portfolioSvc != nil {
			portfolioSvc.InvalidatePositions()
		}
		go gw.Reconnect()
		c.JSON(http.StatusAccepted, gin.H{"status": "reconnecting"})
	}
}

func ibkrGatewayStart(gw broker.GatewayController, portfolioSvc *portfolio.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gw == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ibkr gateway not configured"})
			return
		}
		if portfolioSvc != nil {
			portfolioSvc.InvalidatePositions()
		}
		go func() {
			if err := gw.StartGateway(context.Background()); err != nil {
				log.Printf("[IBKR] gateway start: %v", err)
			}
		}()
		c.JSON(http.StatusAccepted, gin.H{"status": "starting"})
	}
}

func ibkrGatewayStop(gw broker.GatewayController, portfolioSvc *portfolio.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gw == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ibkr gateway not configured"})
			return
		}
		if portfolioSvc != nil {
			portfolioSvc.InvalidatePositions()
		}
		keepSession := c.Query("keep_session") == "true"
		go gw.StopGateway(keepSession)
		status := "stopped"
		if keepSession {
			status = "detached"
		}
		c.JSON(http.StatusAccepted, gin.H{"status": status})
	}
}
