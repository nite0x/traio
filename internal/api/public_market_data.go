package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/market"
)

// Public symbol-based data does not acquire a broker lease unless the request
// identifies the exact owning connection. source=broker without connection_id
// retains the historical compatibility handlers.
func publicMarketRoute(provider market.SymbolProvider, action string, legacy gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		source, err := market.ParseSource(c.Query("source"))
		if err != nil {
			writeMarketDataError(c, err)
			return
		}
		connectionID, err := optionalPositiveInt64(c.Query("connection_id"))
		if err != nil {
			writeMarketDataError(c, err)
			return
		}
		fallbackUsage := market.UsageLiveQuote
		if action == "history" {
			fallbackUsage = market.UsageHistoricalChart
		}
		usage, err := market.ParseUsage(c.Query("usage"), fallbackUsage)
		if err != nil {
			writeMarketDataError(c, err)
			return
		}

		// Preserve the established broker route when no account/connection was
		// supplied. New policy calls never pick an arbitrary broker connection.
		if (source == market.SourceBroker && (provider == nil || connectionID == 0 || action == "history")) ||
			(source == market.SourceAuto && provider == nil) {
			legacy(c)
			return
		}
		if provider == nil {
			writeMarketDataError(c, market.NewProviderError(market.ErrorNotConfigured, "", action, "market data service is not available", nil))
			return
		}

		request := market.Request{Usage: usage, Source: source, ConnectionID: connectionID}
		ctx := c.Request.Context()
		symbol := strings.ToUpper(strings.TrimSpace(c.Param("symbol")))
		policy, policyEnabled := provider.(market.PolicyProvider)
		var result any
		switch action {
		case "quotes":
			var quotes []broker.Quote
			if policyEnabled {
				quotes, err = policy.GetQuotesFor(ctx, strings.Split(c.Query("symbols"), ","), request)
			} else {
				quotes, err = provider.GetQuotes(ctx, strings.Split(c.Query("symbols"), ","))
			}
			result = quotes
			c.Header("X-Market-Data-Source", quoteSourcesHeader(quotes))
		case "quote":
			var quote *broker.Quote
			if policyEnabled {
				quote, err = policy.GetQuoteFor(ctx, symbol, request)
			} else {
				quote, err = provider.GetQuote(ctx, symbol)
			}
			result = quote
			if quote != nil {
				c.Header("X-Market-Data-Source", quote.Source)
			}
		case "history":
			period := c.DefaultQuery("period", "1m")
			bar := c.Query("bar")
			if bar == "" {
				bar = periodToBar[period]
			}
			if policyEnabled {
				var history market.History
				history, err = policy.GetHistoryFor(ctx, symbol, period, bar, c.Query("refresh") == "1", request)
				result = history
				c.Header("X-Market-Data-Source", history.Source)
			} else {
				var candles []broker.Candle
				candles, err = provider.GetHistory(ctx, symbol, period, bar, c.Query("refresh") == "1")
				legacySource := source
				if legacySource == market.SourceAuto {
					legacySource = market.SourceYahoo
				}
				attribution := ""
				if legacySource == market.SourceYahoo {
					attribution = "Data sourced by Yahoo Finance"
				}
				result = market.History{Symbol: symbol, Period: period, Bar: bar, Source: string(legacySource),
					Status: market.StatusProvisional, Delayed: true, Currency: "", AdjustmentType: market.AdjustmentUnadjusted,
					Attribution: attribution, Candles: candles}
				c.Header("X-Market-Data-Source", string(legacySource))
			}
		default:
			err = market.NewProviderError(market.ErrorInvalidRequest, "", action, "unsupported market data action", market.ErrInvalidRequest)
		}
		if err != nil {
			writeMarketDataError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func optionalPositiveInt64(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, market.NewProviderError(market.ErrorInvalidRequest, "", "route", "connection_id must be a positive integer", market.ErrInvalidRequest)
	}
	return id, nil
}

func quoteSourcesHeader(quotes []broker.Quote) string {
	source := ""
	for _, quote := range quotes {
		if source == "" {
			source = quote.Source
			continue
		}
		if source != quote.Source {
			return "mixed"
		}
	}
	return source
}

func writeMarketDataError(c *gin.Context, err error) {
	code, provider := market.ErrorDetails(err)
	status := http.StatusBadGateway
	switch code {
	case market.ErrorInvalidRequest:
		status = http.StatusBadRequest
	case market.ErrorNotConfigured:
		status = http.StatusServiceUnavailable
	case market.ErrorAuthentication:
		status = http.StatusUnauthorized
	case market.ErrorPermission:
		status = http.StatusForbidden
	case market.ErrorRateLimited:
		status = http.StatusTooManyRequests
	case market.ErrorSecurityNotFound:
		status = http.StatusNotFound
	}
	payload := gin.H{"error": err.Error(), "code": code}
	if provider != "" {
		payload["provider"] = provider
	}
	c.JSON(status, payload)
}
