package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/settings"
)

func getSettings(mgr *settings.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if mgr == nil {
			c.JSON(http.StatusOK, config.Default("."))
			return
		}
		c.JSON(http.StatusOK, mgr.Get())
	}
}

func putSettings(mgr *settings.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if mgr == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "settings unavailable"})
			return
		}
		var cfg config.Config
		if err := c.ShouldBindJSON(&cfg); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := mgr.Save(c.Request.Context(), cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":  "saved",
			"message": "部分项（如服务端口）需重启 Traio 后生效",
			"settings": mgr.Get(),
		})
	}
}

func getSettingsDefaults(mgr *settings.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		baseDir := "."
		if mgr != nil {
			baseDir = mgr.BaseDir()
		}
		c.JSON(http.StatusOK, config.Default(baseDir))
	}
}
