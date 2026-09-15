package ibkr

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nite/traio/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchActivityHistoryUsesIsolatedFlexTransport(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Scheme != "https" || req.URL.Host != "ndcdyn.interactivebrokers.com" {
			t.Fatalf("unexpected Flex origin: %s", req.URL.Redacted())
		}
		if got := req.Header.Get("Authorization"); got != "" {
			t.Fatalf("Gateway authorization crossed into Flex request: %q", got)
		}
		if req.URL.Query().Get("t") != "flex-secret" {
			t.Fatal("Flex token missing")
		}
		switch req.URL.Path {
		case "/AccountManagement/FlexWebService/SendRequest":
			if req.URL.Query().Get("q") != "ACTIVITY-QUERY" || req.URL.Query().Get("fd") != "20260901" || req.URL.Query().Get("td") != "20260902" || req.URL.Query().Get("p") != "" {
				t.Fatalf("unexpected SendRequest query: %s", req.URL.RawQuery)
			}
			return flexHTTPResponse(http.StatusOK, `<FlexStatementResponse><Status>Success</Status><ReferenceCode>REF1</ReferenceCode></FlexStatementResponse>`), nil
		case "/AccountManagement/FlexWebService/GetStatement":
			if req.URL.Query().Get("q") != "REF1" || req.URL.Query().Get("fd") != "20260901" || req.URL.Query().Get("td") != "20260902" {
				t.Fatalf("unexpected GetStatement query: %s", req.URL.RawQuery)
			}
			return flexHTTPResponse(http.StatusOK, `<FlexQueryResponse><FlexStatements><FlexStatement accountId="U1"><CashTransactions><CashTransaction transactionID="C1" type="Dividend" currency="USD" amount="1" dateTime="20260901;120000"/></CashTransactions></FlexStatement></FlexStatements></FlexQueryResponse>`), nil
		default:
			t.Fatalf("unexpected path: %s", req.URL.Path)
			return nil, nil
		}
	})
	c := New(config.IBKRConfig{
		GatewayURL: "https://localhost:5000", GatewayToken: "gateway-secret",
		FlexToken: "flex-secret", FlexActivityQueryID: "ACTIVITY-QUERY",
		FlexBaseURL: defaultFlexBaseURL,
	})
	records, err := c.FetchActivityHistory(withFlexTransport(t.Context(), rt), "2026-09-01", "2026-09-02")
	if err != nil {
		t.Fatalf("FetchActivityHistory: %v", err)
	}
	if calls != 2 || len(records) != 1 || records[0].Key != "C1" {
		t.Fatalf("calls=%d records=%#v", calls, records)
	}
}

func TestHistoricalEquityKeepsNAVPeriodOnIsolatedTransport(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("Authorization") != "" {
			t.Fatal("Gateway bearer leaked to Flex")
		}
		if calls == 1 {
			if req.URL.Query().Get("q") != "NAV-QUERY" || req.URL.Query().Get("p") != "365" {
				t.Fatalf("NAV period/query changed: %s", req.URL.RawQuery)
			}
			return flexHTTPResponse(http.StatusOK, `<FlexStatementResponse><Status>Success</Status><ReferenceCode>NAVREF</ReferenceCode></FlexStatementResponse>`), nil
		}
		if req.URL.Query().Get("q") != "NAVREF" || req.URL.Query().Get("p") != "365" {
			t.Fatalf("unexpected statement query: %s", req.URL.RawQuery)
		}
		return flexHTTPResponse(http.StatusOK, `<FlexStatementResponse><FlexStatements><FlexStatement><NetAssetValue reportDate="20260901" currency="USD" endingValue="123.45"/></FlexStatement></FlexStatements></FlexStatementResponse>`), nil
	})
	c := New(config.IBKRConfig{GatewayURL: "https://localhost:5000", GatewayToken: "gateway-secret", FlexToken: "flex-secret", FlexQueryID: "NAV-QUERY", FlexBaseURL: defaultFlexBaseURL})
	points, err := c.HistoricalEquity(withFlexTransport(t.Context(), rt))
	if err != nil || len(points) != 1 || points[0].Value != 123.45 {
		t.Fatalf("points=%#v err=%v", points, err)
	}
}

func TestFlexTransportRejectsUntrustedDestinationsRedirectsAndSecretErrors(t *testing.T) {
	for _, raw := range []string{
		"http://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService",
		"https://evil.example/AccountManagement/FlexWebService",
		"https://ndcdyn.interactivebrokers.com:443/AccountManagement/FlexWebService",
		"https://user@ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService",
		"https://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService?",
		"https://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService/other",
	} {
		if err := ValidateFlexBaseURL(raw); err == nil {
			t.Fatalf("ValidateFlexBaseURL(%q) succeeded", raw)
		}
	}

	redirectCalls := 0
	redirect := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		redirectCalls++
		resp := flexHTTPResponse(http.StatusFound, "redirect")
		resp.Header.Set("Location", "https://evil.example/capture")
		resp.Request = req
		return resp, nil
	})
	c := New(config.IBKRConfig{FlexToken: "top-secret-token", FlexActivityQueryID: "Q", FlexBaseURL: defaultFlexBaseURL})
	_, err := c.FetchActivityHistory(withFlexTransport(t.Context(), redirect), "", "")
	if err == nil || redirectCalls != 1 {
		t.Fatalf("redirect err=%v calls=%d", err, redirectCalls)
	}

	leaky := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("request failed: " + req.URL.String())
	})
	_, err = c.FetchActivityHistory(withFlexTransport(context.Background(), leaky), "", "")
	if err == nil || strings.Contains(err.Error(), "top-secret-token") || strings.Contains(err.Error(), "?t=") {
		t.Fatalf("network error leaked URL secret: %v", err)
	}

	upstream := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return flexHTTPResponse(http.StatusOK, `<FlexStatementResponse><Status>Fail</Status><ErrorCode>1015</ErrorCode><ErrorMessage>token=top-secret-token</ErrorMessage></FlexStatementResponse>`), nil
	})
	_, err = c.FetchActivityHistory(withFlexTransport(t.Context(), upstream), "", "")
	if err == nil || strings.Contains(err.Error(), "top-secret-token") || !strings.Contains(err.Error(), "1015") {
		t.Fatalf("upstream error handling: %v", err)
	}

	oversized := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return flexHTTPResponse(http.StatusOK, strings.Repeat("x", MaxActivityXMLBytes+1)), nil
	})
	_, err = c.FetchActivityHistory(withFlexTransport(t.Context(), oversized), "", "")
	if err == nil || !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("oversized Flex response error: %v", err)
	}
}

func TestFetchActivityHistoryValidatesConfigurationAndRange(t *testing.T) {
	c := New(config.IBKRConfig{})
	if _, err := c.FetchActivityHistory(t.Context(), "2026-01-01", "2026-01-02"); err == nil || !strings.Contains(err.Error(), "source_not_configured") {
		t.Fatalf("missing configuration error = %v", err)
	}
	c.cfg.FlexToken = "t"
	c.cfg.FlexActivityQueryID = "q"
	c.cfg.FlexBaseURL = defaultFlexBaseURL
	for _, dates := range [][2]string{{"2026-01-02", "2026-01-01"}, {"2026-01-01", ""}, {"bad", "2026-01-01"}, {"2025-01-01", "2026-01-01"}} {
		if _, err := c.FetchActivityHistory(t.Context(), dates[0], dates[1]); err == nil {
			t.Fatalf("range %q..%q accepted", dates[0], dates[1])
		}
	}
}

func flexHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
