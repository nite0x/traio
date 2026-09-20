package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/performance"
)

type performanceAPI interface {
	Calculate(context.Context, performance.Query) (performance.Series, error)
	Recalculate(context.Context, performance.Query) (performance.Series, error)
}

func getDailyPerformance(svc performanceAPI) gin.HandlerFunc {
	return dailyPerformanceHandler(svc, false)
}

func recalculateDailyPerformance(svc performanceAPI) gin.HandlerFunc {
	return dailyPerformanceHandler(svc, true)
}

func dailyPerformanceHandler(svc performanceAPI, recalculate bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "performance unavailable"})
			return
		}
		accountID, err := strconv.ParseInt(strings.TrimSpace(c.Param("account_id")), 10, 64)
		if err != nil || accountID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": performance.ErrInvalidRequest.Error()})
			return
		}
		query := performance.Query{
			AccountID: accountID,
			From:      strings.TrimSpace(c.Query("from")), To: strings.TrimSpace(c.Query("to")),
			BaseCurrency: strings.TrimSpace(c.Query("base_currency")),
		}
		var series performance.Series
		if recalculate {
			series, err = svc.Recalculate(c.Request.Context(), query)
		} else {
			series, err = svc.Calculate(c.Request.Context(), query)
		}
		if err != nil {
			switch {
			case errors.Is(err, performance.ErrInvalidRequest):
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			case errors.Is(err, performance.ErrAccountNotFound):
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "calculate performance"})
			}
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, series)
	}
}
