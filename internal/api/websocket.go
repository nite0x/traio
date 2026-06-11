package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/schwab"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsQuotes upgrades to WebSocket and forwards normalized Schwab quote updates.
func wsQuotes(client *schwab.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		var quotes <-chan broker.Quote
		cancel := func() {}
		if client != nil {
			quotes, cancel = client.SubscribeQuotes(strings.Split(c.Query("symbols"), ","))
		}
		defer cancel()

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-c.Request.Context().Done():
				return
			case quote, ok := <-quotes:
				if !ok {
					quotes = nil
					continue
				}
				if err := conn.WriteJSON(gin.H{"type": "quote", "quote": quote}); err != nil {
					return
				}
			case t := <-ticker.C:
				if err := conn.WriteJSON(gin.H{
					"type":      "heartbeat",
					"timestamp": t.UTC().Format(time.RFC3339),
				}); err != nil {
					return
				}
			}
		}
	}
}
