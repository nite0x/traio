package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrokerSyncRoutes(t *testing.T) {
	router := NewRouter(Deps{}, ServerControl{})
	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}

	for _, route := range []string{
		"POST /api/v1/brokers/sync",
		"GET /api/v1/brokers/sync-status",
		"GET /api/v1/ibkr/gateway/login",
		"POST /api/v1/ibkr/gateway/upgrade",
		"POST /api/v1/ibkr/gateway/rollback",
	} {
		if !routes[route] {
			t.Fatalf("missing broker sync route %s", route)
		}
	}
}

type fakeGatewayController struct {
	startCalls int
	startErr   error
	loginURL   string
}

func (f *fakeGatewayController) Status() any { return nil }

func (f *fakeGatewayController) LoginURL() string { return f.loginURL }

func (f *fakeGatewayController) StartGateway(context.Context) error {
	f.startCalls++
	return f.startErr
}

func (f *fakeGatewayController) StopGateway(bool) error { return nil }

func (f *fakeGatewayController) Reconnect() error { return nil }

func (f *fakeGatewayController) Upgrade(context.Context) error { return nil }

func (f *fakeGatewayController) Rollback(context.Context) error { return nil }

func TestIBKRGatewayLoginRedirectsWithoutStartingGateway(t *testing.T) {
	gw := &fakeGatewayController{loginURL: "https://localhost:5680/sso/Login"}
	router := NewRouter(Deps{IBKR: gw}, ServerControl{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ibkr/gateway/login", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("expected redirect, got status %d body %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Location"); got != gw.loginURL {
		t.Fatalf("unexpected login redirect %q", got)
	}
	if gw.startCalls != 0 {
		t.Fatalf("login redirect must be side-effect free, got %d start calls", gw.startCalls)
	}
}

func TestIBKRGatewayStartReturnsOperationError(t *testing.T) {
	gw := &fakeGatewayController{startErr: context.DeadlineExceeded}
	router := NewRouter(Deps{IBKR: gw}, ServerControl{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ibkr/gateway/start", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected synchronous gateway error, got %d: %s", res.Code, res.Body.String())
	}
	if gw.startCalls != 1 {
		t.Fatalf("expected one start call, got %d", gw.startCalls)
	}
}

func TestLocalAPIMiddlewareRequiresToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	gw := &fakeGatewayController{}
	router := NewRouter(Deps{IBKR: gw, APIToken: token}, ServerControl{})

	tests := []struct {
		name   string
		host   string
		token  string
		status int
	}{
		{name: "missing token", host: "127.0.0.1:38180", status: http.StatusUnauthorized},
		{name: "wrong token", host: "127.0.0.1:38180", token: "wrong", status: http.StatusUnauthorized},
		{name: "valid token", host: "127.0.0.1:38180", token: token, status: http.StatusOK},
		{name: "non-local host", host: "example.com", token: token, status: http.StatusMisdirectedRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/ibkr/gateway/status", nil)
			req.Host = tt.host
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tt.status {
				t.Fatalf("expected %d, got %d: %s", tt.status, res.Code, res.Body.String())
			}
		})
	}
}

func TestWebsocketToken(t *testing.T) {
	if got := websocketToken("traio, abc123"); got != "abc123" {
		t.Fatalf("unexpected token %q", got)
	}
	for _, header := range []string{"", "abc123", "other, abc123", "traio, one, two"} {
		if got := websocketToken(header); got != "" {
			t.Fatalf("expected invalid header %q to be rejected, got %q", header, got)
		}
	}
}
