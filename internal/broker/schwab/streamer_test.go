package schwab

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nite/traio/internal/config"
)

func TestMergeEquityChangeMessage(t *testing.T) {
	manager := newStreamManager(New(config.SchwabConfig{}))

	first, ok := manager.mergeEquity(map[string]any{
		"key":     "AAPL",
		"delayed": false,
		"1":       201.1,
		"2":       201.2,
		"3":       201.15,
		"8":       float64(12345),
		"10":      205.0,
		"11":      198.0,
		"18":      1.25,
		"42":      0.625,
	})
	if !ok || first.Symbol != "AAPL" || first.Last != 201.15 || first.Volume != 12345 {
		t.Fatalf("unexpected initial quote: %+v", first)
	}

	next, ok := manager.mergeEquity(map[string]any{"key": "AAPL", "1": 202.0})
	if !ok || next.Bid != 202.0 || next.Ask != 201.2 || next.Last != 201.15 {
		t.Fatalf("change message did not preserve prior fields: %+v", next)
	}
}

func TestNormalizeStreamerURL(t *testing.T) {
	if got := normalizeStreamerURL("example.test/ws"); got != "wss://example.test/ws" {
		t.Fatalf("unexpected URL: %s", got)
	}
	if got := normalizeStreamerURL("wss://example.test/ws"); got != "wss://example.test/ws" {
		t.Fatalf("unexpected URL: %s", got)
	}
}

func TestStreamerLoginSubscribeAndQuote(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var serverURL string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/trader/userPreference":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"streamerInfo": []map[string]string{{
					"streamerSocketUrl":      strings.Replace(serverURL, "http://", "ws://", 1) + "/stream",
					"schwabClientCustomerId": "customer",
					"schwabClientCorrelId":   "correl",
					"schwabClientChannel":    "N9",
					"schwabClientFunctionId": "APIAPP",
				}},
			})
		case "/stream":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()

			var login streamRequest
			if err := conn.ReadJSON(&login); err != nil {
				t.Error(err)
				return
			}
			if len(login.Requests) != 1 || login.Requests[0].Command != "LOGIN" ||
				login.Requests[0].Parameters["Authorization"] != "access" {
				t.Errorf("unexpected login request: %+v", login)
				return
			}
			_ = conn.WriteJSON(map[string]any{"response": []any{map[string]any{
				"requestid": "1",
				"content":   map[string]any{"code": 0, "msg": "ok"},
			}}})

			var subscribe streamRequest
			if err := conn.ReadJSON(&subscribe); err != nil {
				t.Error(err)
				return
			}
			if got := subscribe.Requests[0].Parameters["keys"]; got != "AAPL" {
				t.Errorf("unexpected subscription symbols: %s", got)
				return
			}
			_ = conn.WriteJSON(map[string]any{"data": []any{map[string]any{
				"service": "LEVELONE_EQUITIES",
				"content": []any{map[string]any{
					"key": "AAPL", "1": 201.1, "2": 201.2, "3": 201.15, "42": 0.625,
				}},
			}}})
			time.Sleep(100 * time.Millisecond)
		default:
			http.NotFound(w, r)
		}
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local listeners unavailable: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()
	serverURL = server.URL

	client := New(
		config.SchwabConfig{},
		WithHTTPClient(server.Client()),
		WithAPIURLs(server.URL+"/trader", server.URL+"/market"),
	)
	client.SetToken(Token{AccessToken: "access", ExpiresAt: time.Now().Add(time.Hour)})
	quotes, cancel := client.SubscribeQuotes([]string{"aapl"})
	defer cancel()

	select {
	case quote := <-quotes:
		if quote.Symbol != "AAPL" || quote.Last != 201.15 || quote.Bid != 201.1 ||
			quote.ChangePct != 0.625 {
			t.Fatalf("unexpected streamed quote: %+v", quote)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed quote")
	}
}
