package api

import (
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/runtime"
)

type ServerControl struct {
	BaseDir   string
	StartedAt time.Time
	Shutdown  func()
}

func serverStatus(ctrl ServerControl) gin.HandlerFunc {
	return func(c *gin.Context) {
		ep, err := runtime.ReadEndpoint(ctrl.BaseDir)
		apiURL := ""
		if err == nil {
			apiURL = ep.APIURL
		}
		c.JSON(http.StatusOK, gin.H{
			"running":    true,
			"pid":        os.Getpid(),
			"api_url":    apiURL,
			"started_at": ctrl.StartedAt.UTC().Format(time.RFC3339),
		})
	}
}

func serverShutdown(ctrl ServerControl) gin.HandlerFunc {
	return func(c *gin.Context) {
		if ctrl.Shutdown == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "shutdown not configured"})
			return
		}
		go ctrl.Shutdown()
		c.JSON(http.StatusAccepted, gin.H{"status": "shutting_down"})
	}
}
