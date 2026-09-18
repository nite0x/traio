package ibkr

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
)

func TestOrderQuotePreflightAndContractIdentity(t *testing.T) {
	calls := 0
	client := orderTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/api/iserver/marketdata/snapshot" || r.URL.Query().Get("conids") != "265598" || !strings.Contains(r.URL.Query().Get("fields"), "6509") {
			t.Fatalf("unexpected quote request: %s %s", r.Method, r.URL)
		}
		calls++
		if calls == 1 {
			fmt.Fprint(w, `[{"conid":123,"31":"999"},{"conid":265598,"server_id":"q0"}]`)
			return
		}
		fmt.Fprint(w, `[{"conid":265598,"31":"331.23","84":"331.10","86":"331.18","82":"-1.85","83":-0.56,"7741":"333.08","6509":"DpB","_updated":1789476087004}]`)
	})
	quote, err := client.OrderQuote(t.Context(), "265598")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || quote.Last == nil || *quote.Last != 331.23 || quote.Ask == nil || *quote.Ask != 331.18 || quote.ChangePct == nil || *quote.ChangePct != -.56 || quote.Availability != "DpB" || quote.UpdatedAt == "" {
		t.Fatalf("bad quote: %#v", quote)
	}
	if _, err := client.OrderQuote(t.Context(), "-1"); err == nil || calls != 2 {
		t.Fatal("invalid contract reached Gateway")
	}
	client.orderFlow.pending = &orderAttempt{}
	if _, err := client.OrderQuote(t.Context(), "265598"); err == nil || calls != 2 {
		t.Fatal("quote interrupted a pending warning")
	}
}

func TestOrderQuoteMissingSubscriptionDoesNotInventPrices(t *testing.T) {
	calls := 0
	client := orderTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `[{"conid":265598,"6509":"N","31":"N/A","84":"0","86":"-1"}]`)
	})
	quote, err := client.OrderQuote(t.Context(), "265598")
	if err != nil || calls != 1 || quote.Last != nil || quote.Bid != nil || quote.Ask != nil || quote.UpdatedAt != "" {
		t.Fatalf("missing price became a quote: %#v %v", quote, err)
	}
}

func TestOrderQuotePrefixesAndInvalidNumbers(t *testing.T) {
	for _, tc := range []struct{ raw, kind string }{{"C1,234.50", "close"}, {"H 1234.50", "halted"}} {
		quote := normalizeOrderQuote(1, map[string]any{"31": tc.raw, "6509": "Z", "83": "0%"})
		if quote.Last == nil || *quote.Last != 1234.5 || quote.LastKind != tc.kind || quote.ChangePct == nil || *quote.ChangePct != 0 {
			t.Fatalf("bad prefixed quote: %#v", quote)
		}
	}
	for _, raw := range []any{nil, "", "N/A", "NaN", "Infinity", math.Inf(-1), 0, -1} {
		if orderQuoteNumber(raw, true) != nil {
			t.Fatalf("accepted invalid price %v", raw)
		}
	}
}
