package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	traioauth "github.com/nite/traio/internal/auth"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/ibkr"
	"github.com/nite/traio/internal/config"
)

func TestOrderHTTPWorkflow(t *testing.T) {
	var mutations atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer api-test-proxy" {
			t.Error("missing Gateway token")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/v1/api/iserver/accounts":
			fmt.Fprint(w, `{"accounts":["DU1"],"isPaper":true}`)
		case "/v1/api/iserver/contract/265598/info":
			fmt.Fprint(w, `{"con_id":265598,"symbol":"AAPL","instrument_type":"STK","currency":"USD","exchange":"SMART"}`)
		case "/v1/api/iserver/marketdata/snapshot":
			fmt.Fprint(w, `[{"conid":265598,"31":"150","84":"149.99","86":"150.01","6509":"R"}]`)
		case "/v1/api/iserver/account/DU1/orders/whatif":
			fmt.Fprint(w, `{"amount":{"total":"301 USD","commission":"1 USD"}}`)
		case "/v1/api/iserver/account/DU1/orders":
			mutations.Add(1)
			fmt.Fprint(w, `[{"id":"reply-one","message":["Confirm price"]}]`)
		case "/v1/api/iserver/reply/reply-one":
			mutations.Add(1)
			fmt.Fprint(w, `[{"order_id":"42","order_status":"Submitted"}]`)
		case "/v1/api/iserver/account/orders":
			fmt.Fprint(w, `{"orders":[{"acct":"DU1","orderId":42,"status":"Submitted","ticker":"AAPL","totalSize":2}],"snapshot":true}`)
		case "/v1/api/iserver/account/DU1/order/42":
			mutations.Add(1)
			fmt.Fprint(w, `{"msg":"Request was submitted"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer gateway.Close()
	trading := broker.NewTradingService()
	trading.Replace(map[int64]broker.TradingProvider{7: ibkr.NewBroker(config.IBKRConfig{GatewayURL: gateway.URL, GatewayToken: "api-test-proxy"})})
	router := NewRouter(Deps{Trading: trading}, ServerControl{})
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	payload := `{"connection_id":7,"account_id":"DU1","client_order_id":"api-order-1","symbol":"AAPL","instrument_id":"265598","currency":"USD","side":"buy","order_type":"limit","quantity":2,"limit_price":150,"time_in_force":"day"}`
	call("GET", "/api/v1/trading/accounts?connection_id=7", "", 200)
	quoteResponse := call("GET", "/api/v1/trading/instruments/265598/quote?connection_id=7", "", 200)
	var quote broker.OrderQuote
	if err := json.Unmarshal(quoteResponse, &quote); err != nil || quote.Last == nil || *quote.Last != 150 || quote.ConID != 265598 {
		t.Fatalf("quote route failed: %s", quoteResponse)
	}
	call("GET", "/api/v1/trading/instruments/265598/quote?connection_id=999", "", 502)
	contract := call("GET", "/api/v1/trading/instruments/265598?connection_id=7", "", 200)
	var instrument broker.Instrument
	if err := json.Unmarshal(contract, &instrument); err != nil || instrument.ConID != 265598 || instrument.SecType != "STK" || instrument.Currency != "USD" {
		t.Fatalf("contract selection failed: %s", contract)
	}
	call("POST", "/api/v1/orders/preview", payload, 200)
	if mutations.Load() != 0 {
		t.Fatal("preview placed order")
	}
	response := call("POST", "/api/v1/orders", payload, 202)
	var pending broker.Order
	_ = json.Unmarshal(response, &pending)
	if pending.Confirmation == nil || pending.ID != "" {
		t.Fatal("warning reported as submitted order")
	}
	call("POST", "/api/v1/orders", payload, 202)
	if mutations.Load() != 1 {
		t.Fatal("duplicate HTTP request reached broker")
	}
	recovered := call("GET", "/api/v1/orders/attempts/api-order-1?connection_id=7&account_id=DU1", "", 200)
	if !strings.Contains(string(recovered), "reply-one") {
		t.Fatal("could not recover warning after browser refresh")
	}
	call("POST", "/api/v1/orders/reply/reply-one", `{"connection_id":7,"account_id":"DU1"}`, 400)
	call("POST", "/api/v1/orders/reply/reply-one", `{"connection_id":7,"account_id":"DU2","confirmed":true}`, 400)
	response = call("POST", "/api/v1/orders/reply/reply-one", `{"connection_id":7,"account_id":"DU1","confirmed":true}`, 200)
	var placed broker.Order
	_ = json.Unmarshal(response, &placed)
	if placed.ID != "42" || placed.Status != "open" {
		t.Fatalf("bad final order: %#v", placed)
	}
	call("DELETE", "/api/v1/orders/-1?connection_id=7&account_id=DU1", "", 400)
	call("DELETE", "/api/v1/orders/42?connection_id=7&account_id=DU1", "", 204)
	if mutations.Load() != 3 {
		t.Fatalf("unexpected broker mutations: %d", mutations.Load())
	}
	call("POST", "/api/v1/orders", strings.Replace(payload, `"quantity":2`, `"quantity":-2`, 1), 400)
	call("POST", "/api/v1/orders", strings.Replace(payload, `"connection_id":7`, `"connection_id":-1`, 1), 400)
}

func TestInteractiveOrderRoutesRequireTradePermission(t *testing.T) {
	for _, route := range []struct {
		method, path string
		handler      gin.HandlerFunc
	}{
		{"POST", "/orders", placeOrder(nil)}, {"POST", "/orders/preview", previewOrder(nil)}, {"POST", "/orders/reply/:reply_id", replyOrder(nil)}, {"DELETE", "/orders/:order_id", cancelOrder(nil)},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				p := traioauth.LocalPrincipal()
				p.Role = "viewer"
				setPrincipal(c, p)
				c.Next()
			})
			r.Handle(route.method, route.path, requirePermission(traioauth.PermissionTrade), route.handler)
			w := httptest.NewRecorder()
			path := strings.ReplaceAll(strings.ReplaceAll(route.path, ":reply_id", "reply-one"), ":order_id", "42")
			r.ServeHTTP(w, httptest.NewRequest(route.method, path, strings.NewReader(`{}`)))
			if w.Code != http.StatusForbidden {
				t.Fatalf("viewer allowed order mutation: %d", w.Code)
			}
		})
	}
}
