package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/market"
)

// Public symbol-based data does not acquire a broker connection lease. Clients
// may explicitly select source=broker to retain the broker-specific routes.
func publicMarketRoute(provider market.SymbolProvider, action string, legacy gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		// The provider owns freshness. Browsers and reverse proxies must not
		// retain a second, independently stale copy of market responses.
		c.Header("Cache-Control", "no-store")
		source := strings.ToLower(strings.TrimSpace(c.Query("source")))
		if source == "broker" || source == "" && provider == nil {
			legacy(c)
			return
		}
		if source != "" && source != "yahoo" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "source must be yahoo or broker"})
			return
		}
		if provider == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Yahoo market data is not available"})
			return
		}
		c.Header("X-Market-Data-Source", "yahoo")
		ctx := c.Request.Context()
		symbol := strings.ToUpper(strings.TrimSpace(c.Param("symbol")))
		var result any
		var err error
		switch action {
		case "quotes":
			result, err = provider.GetQuotes(ctx, strings.Split(c.Query("symbols"), ","))
		case "quote":
			result, err = provider.GetQuote(ctx, symbol)
		case "history":
			period := c.DefaultQuery("period", "1m")
			bar := c.Query("bar")
			if bar == "" {
				bar = periodToBar[period]
			}
			bars, historyErr := provider.GetHistory(ctx, symbol, period, bar, c.Query("refresh") == "1")
			err = historyErr
			result = gin.H{"symbol": symbol, "period": period, "bar": bar, "source": "yahoo", "candles": bars}
		}
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, market.ErrInvalidRequest) {
				status = http.StatusBadRequest
			}
			if errors.Is(err, market.ErrNotFound) {
				status = http.StatusNotFound
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}
