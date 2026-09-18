package ibkr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/config"
)

func orderTestRequest() broker.OrderRequest {
	return broker.OrderRequest{AccountID: "DU1", Symbol: "AAPL", InstrumentID: "265598", AssetClass: "equity", Currency: "USD", Side: "buy", OrderType: "limit", Quantity: 2, LimitPrice: 150, TimeInForce: "day", ClientOrderID: "test-order-1"}
}
func orderTestClient(t *testing.T, handle http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer order-test-secret" {
			t.Error("missing Gateway proxy token")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/api/iserver/accounts":
			fmt.Fprint(w, `{"accounts":["DU1","DU2"],"isPaper":true,"acctProps":{"DU1":{"supportsFractions":false}},"aliases":{"DU1":"Paper account"}}`)
		case "/v1/api/iserver/contract/265598/info":
			fmt.Fprint(w, `{"con_id":265598,"symbol":"AAPL","instrument_type":"STK","currency":"USD","exchange":"SMART","company_name":"APPLE INC"}`)
		default:
			handle(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return New(config.IBKRConfig{GatewayURL: server.URL, GatewayToken: "order-test-secret"})
}

func TestIBKROrderInstrumentUsesContractInfoSchema(t *testing.T) {
	for _, tc := range []struct {
		name      string
		edit      func(map[string]any)
		wantError string
	}{
		{"stock", func(raw map[string]any) {}, ""},
		{"ETF", func(raw map[string]any) { raw["symbol"] = "SPY"; raw["company_name"] = "SPDR S&P 500 ETF TRUST" }, ""},
		{"option", func(raw map[string]any) { raw["instrument_type"] = "OPT"; raw["secType"] = "STK" }, "该合约类型为 OPT"},
		{"missing type", func(raw map[string]any) { delete(raw, "instrument_type"); raw["secType"] = "STK" }, "缺少证券类型"},
		{"wrong conid", func(raw map[string]any) { raw["con_id"] = 123 }, "合约编号"},
		{"missing currency", func(raw map[string]any) { delete(raw, "currency") }, "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Same field names returned by both live Gateways and documented for /info.
			raw := map[string]any{"con_id": 265598, "symbol": "AAPL", "company_name": "APPLE INC", "instrument_type": "STK", "currency": "USD", "exchange": "SMART"}
			tc.edit(raw)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/api/iserver/contract/265598/info" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_ = json.NewEncoder(w).Encode(raw)
			}))
			defer server.Close()
			client := New(config.IBKRConfig{GatewayURL: server.URL})
			instrument, err := client.OrderInstrument(t.Context(), "265598")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if instrument.ConID != 265598 || instrument.SecType != "STK" || instrument.Symbol != raw["symbol"] || instrument.Currency != "USD" || instrument.Exchange != "SMART" || instrument.Name != raw["company_name"] {
				t.Fatalf("incorrect contract: %#v", instrument)
			}
		})
	}
}

func TestIBKROrderSearchIncludesListingExchange(t *testing.T) {
	client := orderTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api/iserver/secdef/search" || r.URL.Query().Get("secType") != "STK" || r.URL.Query().Get("symbol") != "AAPL" {
			t.Errorf("unexpected search: %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `[{"conid":"265598","companyName":"APPLE INC","symbol":"AAPL","description":"NASDAQ","sections":[{"secType":"STK"},{"secType":"OPT"}]},{"conid":"532640894","companyName":"APPLE INC-CDR","symbol":"AAPL","description":"TSE","sections":[{"secType":"STK"}]},{"conid":"123","symbol":"AAPL","sections":[{"secType":"OPT"}]}]`)
	})
	results, err := client.SearchOrderInstruments(t.Context(), "AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Exchange != "NASDAQ" || results[1].Exchange != "TSE" || results[0].Name != "APPLE INC" {
		t.Fatalf("incorrect search results: %#v", results)
	}
}

func TestIBKROrderPayloadValidation(t *testing.T) {
	for _, tc := range []struct {
		kind        string
		limit, stop float64
		upstream    string
		price, aux  float64
	}{
		{"market", 0, 0, "MKT", 0, 0}, {"limit", 150, 0, "LMT", 150, 0}, {"stop", 0, 140, "STP", 140, 0}, {"stop_limit", 139, 140, "STP LMT", 139, 140},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			r := orderTestRequest()
			r.OrderType, r.LimitPrice, r.StopPrice = tc.kind, tc.limit, tc.stop
			p, err := ibkrOrderPayload(r)
			if err != nil {
				t.Fatal(err)
			}
			if p["orderType"] != tc.upstream || floatValue(p["price"]) != tc.price || floatValue(p["auxPrice"]) != tc.aux {
				t.Fatalf("wrong broker payload: %#v", p)
			}
		})
	}
	for _, edit := range []func(*broker.OrderRequest){
		func(r *broker.OrderRequest) { r.Quantity = math.NaN() }, func(r *broker.OrderRequest) { r.LimitPrice = math.Inf(1) }, func(r *broker.OrderRequest) { r.Notional = -1 },
		func(r *broker.OrderRequest) { r.OrderType = "trailing_stop"; r.TrailPrice = 1 }, func(r *broker.OrderRequest) { r.InstrumentID = "AAPL" },
		func(r *broker.OrderRequest) { r.ClientOrderID = "" }, func(r *broker.OrderRequest) { r.TimeInForce = "typo" }, func(r *broker.OrderRequest) { r.StopPrice = 140 },
		func(r *broker.OrderRequest) { r.AssetClass = "option" }, func(r *broker.OrderRequest) { r.OrderType = "market"; r.LimitPrice = 0; r.ExtendedHours = true },
	} {
		r := orderTestRequest()
		edit(&r)
		if _, err := ibkrOrderPayload(r); err == nil {
			t.Fatalf("accepted invalid order: %#v", r)
		}
	}
}
func TestIBKRPreviewUsesSnapshotAndNeverPlaces(t *testing.T) {
	var calls []string
	c := orderTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/v1/api/iserver/marketdata/snapshot":
			fmt.Fprint(w, `[{"conid":265598,"31":"150"}]`)
		case "/v1/api/iserver/account/DU1/orders/whatif":
			var body map[string][]map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["orders"][0]["cOID"] != "test-order-1" {
				t.Error("missing reference")
			}
			fmt.Fprint(w, `{"amount":{"amount":"300 USD","commission":"1 USD","total":"301 USD"},"initial":{"current":"0","change":"150","after":"150"},"position":{"current":"0","change":"2","after":"2"},"warn":"Market data delayed","error":null}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	preview, err := c.PreviewOrder(t.Context(), orderTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Total != "301 USD" || preview.InitialMargin.Change != "150" || len(preview.Warnings) != 1 || len(calls) != 2 || !strings.HasSuffix(calls[0], "snapshot") {
		t.Fatalf("bad preview: %#v %v", preview, calls)
	}
}
func TestIBKRMultiStepConfirmationAndDuplicateSubmission(t *testing.T) {
	var placements, replies atomic.Int32
	c := orderTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/iserver/account/DU1/orders":
			placements.Add(1)
			fmt.Fprint(w, `[{"id":"warning-1","message":["Check price"],"messageIds":["o163"]}]`)
		case "/v1/api/iserver/reply/warning-1":
			replies.Add(1)
			fmt.Fprint(w, `[{"id":"warning-2","message":["No market data"]}]`)
		case "/v1/api/iserver/reply/warning-2":
			replies.Add(1)
			fmt.Fprint(w, `[{"order_id":"9007199254740993","order_status":"PreSubmitted"}]`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	request := orderTestRequest()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			order, err := c.PlaceOrder(context.Background(), request)
			if err != nil || order.Confirmation == nil {
				t.Errorf("place: %#v %v", order, err)
			}
		})
	}
	wg.Wait()
	if placements.Load() != 1 || replies.Load() != 0 {
		t.Fatal("duplicate submission or automatic warning confirmation")
	}
	other := request
	other.ClientOrderID = "different"
	if _, err := c.PlaceOrder(t.Context(), other); err == nil {
		t.Fatal("placed another order while confirmation pending")
	}
	changed := request
	changed.Quantity = 3
	if _, err := c.PlaceOrder(t.Context(), changed); err == nil {
		t.Fatal("same client ID accepted changed payload")
	}
	if _, err := c.ReplyOrder(t.Context(), "DU2", "warning-1", true); err == nil {
		t.Fatal("cross-account confirmation accepted")
	}
	next, err := c.ReplyOrder(t.Context(), "DU1", "warning-1", true)
	if err != nil || next.Confirmation.ReplyID != "warning-2" {
		t.Fatalf("next: %#v %v", next, err)
	}
	_, _ = c.ReplyOrder(t.Context(), "DU1", "warning-1", true)
	order, err := c.ReplyOrder(t.Context(), "DU1", "warning-2", true)
	if err != nil || order.ID != "9007199254740993" || order.Status != "open" || order.Quantity != 2 || order.Symbol != "AAPL" || order.Confirmation != nil {
		t.Fatalf("order: %#v %v", order, err)
	}
	again, err := c.PlaceOrder(t.Context(), request)
	if err != nil || again.ID != order.ID || placements.Load() != 1 || replies.Load() != 2 {
		t.Fatal("replay sent duplicate broker mutation")
	}
}
func TestIBKRRejectedUnknownAndRedirectResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"rejection", `{"error":"Insufficient funds order-test-secret"}`, "order_rejected", 200},
		{"array rejection", `[{"error":"Rejected order-test-secret"}]`, "order_rejected", 200},
		{"empty", `[]`, "order_outcome_unknown", 200}, {"malformed", `<html>order-test-secret</html>`, "order_outcome_unknown", 200},
		{"redirect", `{}`, "order_outcome_unknown", 307}, {"gateway error", `{"error":"order-test-secret"}`, "order_outcome_unknown", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := orderTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/unexpected")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			for i := 0; i < 2; i++ {
				_, err := c.PlaceOrder(t.Context(), orderTestRequest())
				var oe *broker.OrderError
				if !errors.As(err, &oe) || oe.Code != tc.code || strings.Contains(err.Error(), "order-test-secret") {
					t.Fatalf("unsafe or wrong error: %v", err)
				}
			}
			if calls != 1 {
				t.Fatal("mutation retried or redirect followed")
			}
		})
	}
}
func TestIBKROrderAccountAndContractValidation(t *testing.T) {
	c := orderTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("invalid order reached broker mutation: %s", r.URL.Path)
		w.WriteHeader(500)
	})
	for _, edit := range []func(*broker.OrderRequest){func(r *broker.OrderRequest) { r.AccountID = "UNKNOWN" }, func(r *broker.OrderRequest) { r.Symbol = "MSFT" }, func(r *broker.OrderRequest) { r.Currency = "HKD" }, func(r *broker.OrderRequest) { r.Quantity = .5 }} {
		req := orderTestRequest()
		edit(&req)
		if _, err := c.PlaceOrder(t.Context(), req); err == nil {
			t.Fatalf("validation bypass: %#v", req)
		}
	}
}
func TestIBKRCancellationAccountOwnershipAndPendingStatus(t *testing.T) {
	deletes := 0
	c := orderTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"orders":[{"acct":"DU1","orderId":42,"status":"Submitted","ticker":"AAPL","totalSize":2}],"snapshot":true}`)
			return
		}
		deletes++
		fmt.Fprint(w, `{"order_id":"42","msg":"Request was submitted"}`)
	})
	for _, id := range []string{"-1", "0", "abc"} {
		if err := c.CancelOrder(t.Context(), "DU1", id); err == nil {
			t.Fatal("invalid/global cancellation accepted")
		}
	}
	if err := c.CancelOrder(t.Context(), "DU2", "42"); err == nil {
		t.Fatal("canceled another account's order")
	}
	c.orderState.requestedAt = time.Time{}
	if err := c.CancelOrder(t.Context(), "DU1", "42"); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 {
		t.Fatalf("unexpected cancel count %d", deletes)
	}
	if normalizeIBKRStatus("PendingCancel") != "pending_cancel" || normalizeIBKRStatus("Inactive") != "rejected" {
		t.Fatal("wrong lifecycle status")
	}
}

func TestIBKRDeclineWarningAndRecoverReadOnly(t *testing.T) {
	replies := 0
	c := orderTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/reply/") {
			replies++
			var decision map[string]bool
			_ = json.NewDecoder(r.Body).Decode(&decision)
			if decision["confirmed"] {
				t.Error("decline was confirmed")
			}
			fmt.Fprint(w, `{"message":"Order cancelled"}`)
			return
		}
		fmt.Fprint(w, `[{"id":"decline-1","message":["Check price"]}]`)
	})
	req := orderTestRequest()
	_, _ = c.PlaceOrder(t.Context(), req)
	state, err := c.OrderAttempt(t.Context(), req.AccountID, req.ClientOrderID)
	if err != nil || state.Order.Confirmation == nil || replies != 0 {
		t.Fatalf("recovery mutated order or lost pending state: %#v %v", state, err)
	}
	if _, err := c.OrderAttempt(t.Context(), "DU2", req.ClientOrderID); err == nil {
		t.Fatal("cross-account recovery allowed")
	}
	order, err := c.ReplyOrder(t.Context(), req.AccountID, "decline-1", false)
	if err != nil || order.Status != "not_submitted" || c.orderFlow.pending != nil {
		t.Fatalf("decline: %#v %v", order, err)
	}
	_, _ = c.ReplyOrder(t.Context(), req.AccountID, "decline-1", false)
	if replies != 1 {
		t.Fatal("decline sent twice")
	}
}
func TestIBKRUncertainReplyIsNeverResent(t *testing.T) {
	replies := 0
	c := orderTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/reply/") {
			replies++
			w.WriteHeader(503)
			fmt.Fprint(w, `{}`)
			return
		}
		fmt.Fprint(w, `[{"id":"unknown-1","message":["Check price"]}]`)
	})
	req := orderTestRequest()
	_, _ = c.PlaceOrder(t.Context(), req)
	for i := 0; i < 2; i++ {
		if _, err := c.ReplyOrder(t.Context(), req.AccountID, "unknown-1", true); err == nil {
			t.Fatal("unknown outcome reported successful")
		}
	}
	state, err := c.OrderAttempt(t.Context(), req.AccountID, req.ClientOrderID)
	if err != nil || state.Code != "order_outcome_unknown" || replies != 1 {
		t.Fatalf("uncertain reply lost or retried: %#v %v calls=%d", state, err, replies)
	}
}
