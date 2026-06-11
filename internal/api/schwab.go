package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/portfolio"
)

func schwabStatus(client *schwab.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "schwab is not available"})
			return
		}
		_, authenticated := client.Token()
		c.JSON(http.StatusOK, gin.H{
			"authenticated": authenticated,
			"stream":        client.StreamStatus(),
		})
	}
}

func schwabOAuthURL(client *schwab.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "schwab is not available"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"url": client.AuthURL(c.Query("state"))})
	}
}

type schwabExchangeRequest struct {
	Code        string `json:"code"`
	CallbackURL string `json:"callback_url"`
}

func schwabOAuthExchange(client *schwab.Client, portfolioSvc *portfolio.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "schwab is not available"})
			return
		}
		var req schwabExchangeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		code := strings.TrimSpace(req.Code)
		if code == "" && strings.TrimSpace(req.CallbackURL) != "" {
			callback, err := schwab.ParseCallbackURL(req.CallbackURL)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			code = callback.Code
		}
		if code == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "code or callback_url is required"})
			return
		}
		if _, err := client.ExchangeCodeForToken(c.Request.Context(), code); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if portfolioSvc != nil {
			portfolioSvc.InvalidatePositions()
		}
		c.JSON(http.StatusOK, gin.H{"status": "authenticated"})
	}
}
