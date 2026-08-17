package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/account"
	"github.com/nite/traio/internal/ai"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/alpaca"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/news"
	"github.com/nite/traio/internal/portfolio"
	"github.com/nite/traio/internal/settings"
	"github.com/nite/traio/internal/store"
)

type Deps struct {
	Brokers              brokerStore
	BrokerRuntime        brokerConnectionRuntime
	OnBrokersChanged     func(context.Context) error
	Watchlists           store.WatchlistRepository
	CandleCache          store.CandleCacheRepository
	Settings             *settings.Manager
	Schwab               *schwab.Client
	Alpaca               *alpaca.Client
	IBKR                 broker.GatewayController
	IBKRGateways         ibkrGatewayRuntime
	Instruments          broker.InstrumentProvider
	Quotes               broker.BatchMarketDataProvider
	Candles              broker.CandleProvider
	BrokerSync           *portfolio.SyncService
	Account              *account.Service
	News                 *news.Service
	AI                   *ai.Service
	APIToken             string
	AllowedAPIHosts      []string
	IBKRLoginProxy       *IBKRLoginProxy
	RuntimeDir           string
	gatewayPortAvailable gatewayPortAvailableFunc
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && !allowedOrigin(origin) && !sameOrigin(origin, c.Request) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origin is not allowed"})
			return
		}
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func sameOrigin(origin string, request *http.Request) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Path == "" && strings.EqualFold(parsed.Host, request.Host)
}

func allowedOrigin(origin string) bool {
	switch origin {
	case "http://localhost:1420", "http://127.0.0.1:1420", "tauri://localhost", "http://tauri.localhost", "https://tauri.localhost":
		return true
	default:
		return false
	}
}

func localAPIMiddleware(token string, allowedHosts ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		host, _, err := net.SplitHostPort(c.Request.Host)
		if err != nil {
			host = c.Request.Host
		}
		if !isAllowedAPIHost(host, allowedHosts) {
			c.AbortWithStatusJSON(http.StatusMisdirectedRequest, gin.H{"error": "API host is not allowed"})
			return
		}

		// The browser login entry is a side-effect-free redirect. All data and
		// control endpoints require the runtime token. WebSockets cannot attach
		// an Authorization header, so only that route accepts it as a subprotocol.
		if c.Request.Method == http.MethodGet && c.FullPath() == "/api/v1/ibkr/gateway/login" {
			c.Next()
			return
		}
		candidate := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if c.FullPath() == "/api/v1/ws" && candidate == "" {
			candidate = websocketToken(c.GetHeader("Sec-WebSocket-Protocol"))
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API token"})
			return
		}
		c.Next()
	}
}

func isAllowedAPIHost(host string, allowedHosts []string) bool {
	if isLoopbackAPIHost(host) {
		return true
	}
	host = normalizeRequestHost(host)
	for _, allowed := range allowedHosts {
		if host != "" && host == normalizeRequestHost(allowed) {
			return true
		}
	}
	return false
}

func websocketToken(header string) string {
	parts := strings.Split(header, ",")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) != "traio" {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func isLoopbackAPIHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(strings.ToLower(host)), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func NewRouter(deps Deps, serverCtrl ServerControl) *gin.Engine {
	portAvailable := deps.gatewayPortAvailable
	if portAvailable == nil {
		portAvailable = loopbackGatewayPortAvailable
	}
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())
	if deps.IBKRLoginProxy != nil {
		r.Use(deps.IBKRLoginProxy.Middleware())
	}
	r.Use(corsMiddleware())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "traio"})
	})
	registerAdminRoutes(r)

	// API authentication is temporarily disabled to avoid blocking local debugging.
	// Restore the middleware below when authentication is enabled again.
	// v1 := r.Group("/api/v1", localAPIMiddleware(deps.APIToken, deps.AllowedAPIHosts...))
	v1 := r.Group("/api/v1")
	{
		v1.GET("/brokers", listBrokerProviders(deps.Brokers))
		v1.PUT("/brokers/:code", updateBrokerProvider(deps.Brokers, deps.OnBrokersChanged))
		v1.GET("/broker-connections", listBrokerConnections(deps.Brokers))
		v1.GET("/broker-connections/:connection_id", getBrokerConnection(deps.Brokers))
		v1.POST("/brokers/:code/connections", createBrokerConnection(deps.Brokers, deps.OnBrokersChanged))
		v1.PUT("/broker-connections/:connection_id", updateBrokerConnection(deps.Brokers, deps.OnBrokersChanged))
		v1.GET("/broker-connections/:connection_id/delete-impact", brokerConnectionDeleteImpact(deps.Brokers))
		v1.DELETE("/broker-connections/:connection_id", deleteBrokerConnection(deps.Brokers, deps.OnBrokersChanged))
		v1.POST("/broker-connections/:connection_id/login", beginBrokerConnectionLogin(deps.BrokerRuntime, deps.IBKRLoginProxy))
		v1.GET("/broker-connections/:connection_id/auth/status", brokerConnectionLoginStatus(deps.BrokerRuntime, deps.IBKRLoginProxy))
		v1.POST("/broker-connections/:connection_id/oauth/exchange", exchangeBrokerConnectionOAuthCode(deps.BrokerRuntime))
		v1.GET("/broker-connections/:connection_id/accounts", listBrokerConnectionAccounts(deps.Brokers))
		v1.POST("/broker-connections/:connection_id/sync", syncBrokerConnection(deps.Brokers, deps.BrokerSync))
		v1.GET("/broker-accounts", listBrokerAccounts(deps.Brokers))
		v1.GET("/ibkr/gateways", listIBKRGateways(deps.Brokers))
		v1.POST("/ibkr/gateways", createIBKRGateway(deps.Brokers, deps.OnBrokersChanged, deps.RuntimeDir, portAvailable))
		v1.GET("/ibkr/gateways/defaults", ibkrGatewayDefaults(deps.Brokers, deps.RuntimeDir, portAvailable))
		v1.GET("/ibkr/gateways/:gateway_id", getIBKRGateway(deps.Brokers))
		v1.PUT("/ibkr/gateways/:gateway_id", updateIBKRGateway(deps.Brokers, deps.OnBrokersChanged))
		v1.DELETE("/ibkr/gateways/:gateway_id", deleteIBKRGateway(deps.Brokers, deps.IBKRGateways, deps.OnBrokersChanged, deps.RuntimeDir))
		v1.GET("/ibkr/gateways/:gateway_id/status", ibkrManagedGatewayStatus(deps.IBKRGateways))
		v1.GET("/ibkr/gateways/:gateway_id/login", ibkrManagedGatewayLogin(deps.IBKRGateways))
		v1.POST("/ibkr/gateways/:gateway_id/start", ibkrManagedGatewayStart(deps.IBKRGateways))
		v1.POST("/ibkr/gateways/:gateway_id/stop", ibkrManagedGatewayStop(deps.IBKRGateways))
		v1.POST("/ibkr/gateways/:gateway_id/reconnect", ibkrManagedGatewayReconnect(deps.IBKRGateways))
		v1.POST("/ibkr/gateways/:gateway_id/upgrade", ibkrManagedGatewayUpgrade(deps.IBKRGateways))
		v1.POST("/ibkr/gateways/:gateway_id/rollback", ibkrManagedGatewayRollback(deps.IBKRGateways))
		v1.GET("/watchlist/groups", listWatchlistGroups(deps.Watchlists))
		v1.GET("/watchlist/groups/:group_id/items", listWatchlistItems(deps.Watchlists))
		v1.POST("/watchlist/groups/:group_id/items", upsertWatchlistItem(deps.Watchlists))
		v1.DELETE("/watchlist/groups/:group_id/items/:symbol", deleteWatchlistItem(deps.Watchlists))
		v1.GET("/instruments/search", searchInstruments(deps.Instruments))
		v1.GET("/quotes", listQuotes(deps.Quotes))
		v1.GET("/quotes/symbols", listQuotesBySymbol(deps.Schwab))
		v1.GET("/quotes/:symbol", getQuote(deps.Schwab, deps.Instruments, deps.Quotes))
		v1.GET("/quotes/:symbol/history", getHistory(deps.CandleCache, deps.Instruments, deps.Candles))
		v1.GET("/portfolio/overview", portfolioOverview(deps.BrokerSync))
		v1.GET("/portfolio/positions", portfolioPositions(deps.BrokerSync))
		v1.GET("/portfolio/positions/:position_id", portfolioPosition(deps.BrokerSync))
		v1.GET("/portfolio/cash", portfolioCash(deps.BrokerSync))
		v1.POST("/portfolio/sync", syncBrokers(deps.BrokerSync))
		v1.GET("/portfolio/sync-status", brokerSyncStatus(deps.BrokerSync))
		v1.GET("/account/equity", accountEquity(deps.Account))
		v1.GET("/news/:symbol", getNews(deps.News))
		v1.POST("/orders", placeOrder())
		v1.GET("/ws", wsQuotes(deps.Schwab))
		v1.GET("/schwab/status", schwabStatus(deps.Schwab))
		v1.GET("/schwab/oauth/url", schwabOAuthURL(deps.Schwab))
		v1.POST("/schwab/oauth/exchange", schwabOAuthExchange(deps.Schwab))
		v1.GET("/alpaca/status", alpacaStatus(deps.Alpaca))

		v1.GET("/ibkr/gateway/status", ibkrGatewayStatus(deps.IBKR))
		v1.GET("/ibkr/gateway/login", ibkrGatewayLogin(deps.IBKR))
		v1.POST("/ibkr/gateway/start", ibkrGatewayStart(deps.IBKR, deps.BrokerSync))
		v1.POST("/ibkr/gateway/stop", ibkrGatewayStop(deps.IBKR, deps.BrokerSync))
		v1.POST("/ibkr/gateway/reconnect", ibkrGatewayReconnect(deps.IBKR, deps.BrokerSync))
		v1.POST("/ibkr/gateway/upgrade", ibkrGatewayUpgrade(deps.IBKR, deps.BrokerSync))
		v1.POST("/ibkr/gateway/rollback", ibkrGatewayRollback(deps.IBKR, deps.BrokerSync))

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

func listWatchlistGroups(st store.WatchlistRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		groups, err := st.ListWatchlistGroups(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, groups)
	}
}

func listWatchlistItems(st store.WatchlistRepository) gin.HandlerFunc {
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

func upsertWatchlistItem(st store.WatchlistRepository) gin.HandlerFunc {
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

func deleteWatchlistItem(st store.WatchlistRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, ok := parseGroupID(c)
		if !ok {
			return
		}
		if err := st.DeleteWatchlistItem(c.Request.Context(), groupID, c.Param("symbol")); err != nil {
			if errors.Is(err, store.ErrNotFound) {
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

func listQuotesBySymbol(client *schwab.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "schwab quotes are not available"})
			return
		}
		symbols := strings.Split(c.Query("symbols"), ",")
		quotes, err := client.GetQuotes(c.Request.Context(), symbols)
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

func portfolioOverview(svc *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "portfolio overview is not available"})
			return
		}
		snapshot, err := svc.Snapshot(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"summary": snapshot.Summary, "allocations": snapshot.Allocations,
			"warnings": snapshot.Warnings,
		})
	}
}

func portfolioPositions(svc *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "portfolio positions are not available"})
			return
		}
		positions, err := svc.AggregatedPositions(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, positions)
	}
}

func portfolioPosition(svc *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "portfolio position is not available"})
			return
		}
		position, err := svc.AggregatedPosition(c.Request.Context(), c.Param("position_id"))
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, position)
	}
}

func portfolioCash(svc *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "portfolio cash is not available"})
			return
		}
		snapshot, err := svc.Snapshot(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"summary": snapshot.Summary, "cash_balances": snapshot.CashBalances,
			"warnings": snapshot.Warnings,
		})
	}
}

func syncBrokers(svc *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker sync is not available"})
			return
		}
		if err := svc.Sync(c.Request.Context()); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "synced"})
	}
}

func brokerSyncStatus(svc *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker sync is not available"})
			return
		}
		statuses, err := svc.SyncStatus(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, statuses)
	}
}

func accountEquity(svc *account.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account equity is not available"})
			return
		}
		points, summary, err := svc.Timeline(c.Request.Context())
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

func placeOrder() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "place order not implemented"})
	}
}

// periodToBar returns a sensible default bar size for a given period.
var periodToBar = map[string]string{
	"1d": "5min",
	"5d": "30min",
	"1m": "1h",
	"3m": "1d",
	"6m": "1d",
	"1y": "1d",
	"2y": "1w",
	"5y": "1w",
}

func getHistory(st store.CandleCacheRepository, instruments broker.InstrumentProvider, candles broker.CandleProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		if candles == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "candle data not available"})
			return
		}
		symbol := strings.ToUpper(strings.TrimSpace(c.Param("symbol")))
		period := c.DefaultQuery("period", "1m")
		bar := c.DefaultQuery("bar", "")
		forceRefresh := c.Query("refresh") == "1"

		if bar == "" {
			if b, ok := periodToBar[period]; ok {
				bar = b
			} else {
				bar = "1d"
			}
		}

		// Cache lookup (skip when client requests a forced refresh)
		if st != nil && !forceRefresh {
			if cached, err := st.GetCachedCandles(c.Request.Context(), symbol, period, bar); err == nil && cached != nil {
				c.Header("X-Cache", "HIT")
				c.JSON(http.StatusOK, gin.H{
					"symbol":  symbol,
					"period":  period,
					"bar":     bar,
					"candles": cached,
				})
				return
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

		// Persist to cache (best-effort, don't fail the request on cache write error)
		if st != nil && len(bars) > 0 {
			_ = st.SetCachedCandles(c.Request.Context(), instrument.Symbol, instrument.ConID, period, bar, bars)
		}

		c.Header("X-Cache", "MISS")
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

// ibkrGatewayLogin is a browser entry point for manual IBKR authentication.
// Credentials and 2FA stay on the Gateway-hosted IBKR page; Traio only makes
// sure the local Gateway is available before redirecting the browser to it.
func ibkrGatewayLogin(gw broker.GatewayController) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gw == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ibkr gateway not configured"})
			return
		}
		loginURL := strings.TrimSpace(gw.LoginURL())
		if loginURL == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ibkr gateway login URL is not available"})
			return
		}
		c.Redirect(http.StatusFound, loginURL)
	}
}

func ibkrGatewayReconnect(gw broker.GatewayController, brokerSync *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gw == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ibkr gateway not configured"})
			return
		}
		if brokerSync != nil {
			brokerSync.Invalidate()
		}
		if err := gw.Reconnect(); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "reconnected"})
	}
}

func ibkrGatewayStart(gw broker.GatewayController, brokerSync *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gw == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ibkr gateway not configured"})
			return
		}
		if brokerSync != nil {
			brokerSync.Invalidate()
		}
		if err := gw.StartGateway(context.Background()); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "started"})
	}
}

func ibkrGatewayStop(gw broker.GatewayController, brokerSync *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gw == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ibkr gateway not configured"})
			return
		}
		if brokerSync != nil {
			brokerSync.Invalidate()
		}
		keepSession := c.Query("keep_session") == "true"
		if err := gw.StopGateway(keepSession); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		status := "stopped"
		if keepSession {
			status = "detached"
		}
		c.JSON(http.StatusOK, gin.H{"status": status})
	}
}

func ibkrGatewayUpgrade(gw broker.GatewayController, brokerSync *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gw == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ibkr gateway not configured"})
			return
		}
		if brokerSync != nil {
			brokerSync.Invalidate()
		}
		if err := gw.Upgrade(c.Request.Context()); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "upgraded"})
	}
}

func ibkrGatewayRollback(gw broker.GatewayController, brokerSync *portfolio.SyncService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if gw == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ibkr gateway not configured"})
			return
		}
		if brokerSync != nil {
			brokerSync.Invalidate()
		}
		if err := gw.Rollback(c.Request.Context()); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "rolled_back"})
	}
}
