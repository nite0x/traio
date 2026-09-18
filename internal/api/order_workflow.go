package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/broker"
)

func writeOrderError(c *gin.Context, err error) {
	code, status := "broker_unavailable", http.StatusBadGateway
	var orderErr *broker.OrderError
	if errors.As(err, &orderErr) {
		code = orderErr.Code
		switch code {
		case "invalid_order":
			status = http.StatusBadRequest
		case "duplicate_order", "confirmation_pending":
			status = http.StatusConflict
		case "order_rejected":
			status = http.StatusUnprocessableEntity
		}
	}
	c.JSON(status, gin.H{"error": err.Error(), "code": code})
}
func bindOrder(c *gin.Context) (placeOrderRequest, bool) {
	var req placeOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order request"})
		return req, false
	}
	if req.ConnectionID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "connection_id must be a positive integer"})
		return req, false
	}
	if err := broker.ValidateOrder(req.OrderRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_order"})
		return req, false
	}
	return req, true
}
func previewOrder(trading *broker.TradingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if trading == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "trading is not configured"})
			return
		}
		req, ok := bindOrder(c)
		if !ok {
			return
		}
		preview, err := trading.PreviewOrder(c.Request.Context(), req.ConnectionID, req.OrderRequest)
		if err != nil {
			writeOrderError(c, err)
			return
		}
		c.JSON(http.StatusOK, preview)
	}
}
func replyOrder(trading *broker.TradingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if trading == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "trading is not configured"})
			return
		}
		var req struct {
			ConnectionID int64  `json:"connection_id"`
			AccountID    string `json:"account_id"`
			Confirmed    *bool  `json:"confirmed"`
		}
		if c.ShouldBindJSON(&req) != nil || req.ConnectionID <= 0 || strings.TrimSpace(req.AccountID) == "" || req.Confirmed == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "connection_id, account_id and confirmed are required"})
			return
		}
		order, err := trading.ReplyOrder(c.Request.Context(), req.ConnectionID, req.AccountID, c.Param("reply_id"), *req.Confirmed)
		if err != nil {
			writeOrderError(c, err)
			return
		}
		c.JSON(http.StatusOK, order)
	}
}
func tradingRead(trading *broker.TradingService, kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if trading == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "trading is not configured"})
			return
		}
		id, _, ok := orderQuery(c)
		if !ok {
			return
		}
		var result any
		var err error
		switch kind {
		case "attempt":
			result, err = trading.OrderAttempt(c.Request.Context(), id, c.Query("account_id"), c.Param("client_order_id"))
		case "accounts":
			result, err = trading.TradingAccounts(c.Request.Context(), id)
		case "instruments":
			result, err = trading.SearchOrderInstruments(c.Request.Context(), id, c.Query("q"))
		case "instrument":
			result, err = trading.OrderInstrument(c.Request.Context(), id, c.Param("conid"))
		case "quote":
			c.Header("Cache-Control", "no-store")
			result, err = trading.OrderQuote(c.Request.Context(), id, c.Param("conid"))
		}
		if err != nil {
			writeOrderError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}
