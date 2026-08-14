package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	adminweb "github.com/nite/traio/web/admin"
)

func registerAdminRoutes(r *gin.Engine) {
	serve := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(adminweb.IndexHTML()))
	}

	r.GET("/admin", serve)
	r.GET("/admin/", serve)
	r.GET("/admin/gateways", serve)
}
