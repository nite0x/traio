package api

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// registerWebAppRoutes serves a Vite production build and falls back to its
// index for client-side routes. API and admin paths deliberately retain their
// normal 404 responses instead of being mistaken for frontend routes.
func registerWebAppRoutes(r *gin.Engine, webDir string) {
	webDir = strings.TrimSpace(webDir)
	if webDir == "" {
		return
	}
	root, err := filepath.Abs(webDir)
	if err != nil {
		log.Printf("web app: resolve %s: %v", webDir, err)
		return
	}
	indexPath := filepath.Join(root, "index.html")
	if info, err := os.Stat(indexPath); err != nil || info.IsDir() {
		log.Printf("web app: %s is not a readable frontend build", indexPath)
		return
	}

	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusNotFound)
			return
		}
		if reservedWebPath(c.Request.URL.Path) {
			c.Status(http.StatusNotFound)
			return
		}
		setWebSecurityHeaders(c)

		requestPath := filepath.Clean("/" + c.Request.URL.Path)
		relativePath := strings.TrimPrefix(requestPath, "/")
		candidate := filepath.Join(root, filepath.FromSlash(relativePath))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if strings.HasPrefix(requestPath, "/assets/") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				c.Header("Cache-Control", "no-cache")
			}
			c.File(candidate)
			return
		}

		c.Header("Cache-Control", "no-cache")
		c.File(indexPath)
	})
}

func setWebSecurityHeaders(c *gin.Context) {
	c.Header("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; script-src 'self'; font-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
}

func reservedWebPath(path string) bool {
	path = filepath.Clean("/" + path)
	return path == "/health" ||
		path == "/admin" || strings.HasPrefix(path, "/admin/") ||
		path == "/api" || strings.HasPrefix(path, "/api/")
}
