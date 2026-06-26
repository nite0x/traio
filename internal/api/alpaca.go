package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/broker/alpaca"
)

func alpacaStatus(client *alpaca.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "alpaca is not available"})
			return
		}
		configured := client.Configured()
		status := gin.H{
			"configured": configured,
		}
		if !configured {
			c.JSON(http.StatusOK, status)
			return
		}
		summary, err := client.AccountSummary(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "configured": true})
			return
		}
		status["account_id"] = summary.AccountID
		status["equity"] = summary.NetLiquidation
		status["currency"] = summary.Currency
		c.JSON(http.StatusOK, status)
	}
}
