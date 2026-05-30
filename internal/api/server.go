package api

import (
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

type ServerControl struct {
	StartedAt time.Time
	APIURL    string
	Shutdown  func()
}

func serverStatus(ctrl ServerControl) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"running":    true,
			"pid":        os.Getpid(),
			"api_url":    ctrl.APIURL,
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
