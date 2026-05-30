package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsQuotes upgrades to WebSocket for real-time quote streaming (phase 1).
func wsQuotes() gin.HandlerFunc {
	return func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-c.Request.Context().Done():
				return
			case t := <-ticker.C:
				_ = conn.WriteJSON(gin.H{
					"type":      "heartbeat",
					"timestamp": t.UTC().Format(time.RFC3339),
				})
			}
		}
	}
}
