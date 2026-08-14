package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nite/traio/internal/broker"
)

type fakeIBKRProxyRuntime struct {
	target   *url.URL
	ibkr     bool
	loginURL string
}

func (f *fakeIBKRProxyRuntime) BeginConnectionLogin(context.Context, int64, string) (broker.LoginAction, error) {
	loginURL := f.loginURL
	if loginURL == "" {
		loginURL = "https://localhost:5680/sso/Login"
	}
	return broker.LoginAction{URL: loginURL}, nil
}

func (f *fakeIBKRProxyRuntime) ConnectionLoginStatus(context.Context, int64) (broker.LoginAction, error) {
	return broker.LoginAction{Authenticated: true, AccountID: "U123"}, nil
}

func (f *fakeIBKRProxyRuntime) ExchangeConnectionOAuthCode(context.Context, int64, string) error {
	return nil
}

func (f *fakeIBKRProxyRuntime) IBKRGatewayTarget(context.Context, int64) (*url.URL, bool, error) {
	return f.target, f.ibkr, nil
}

func TestIBKRLoginProxyUsesSingleUseTicketAndProxiesGateway(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sso/Login" {
			t.Fatalf("upstream path: got %q", r.URL.Path)
		}
		if _, err := r.Cookie(ibkrProxyCookieName); err == nil {
			t.Fatal("proxy session cookie leaked to Gateway")
		}
		http.SetCookie(w, &http.Cookie{
			Name: "JSESSIONID", Value: "gateway-session", Domain: ".ibkr.com", Path: "/sso", Secure: true,
		})
		_, _ = w.Write([]byte("gateway login"))
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeIBKRProxyRuntime{target: target, ibkr: true}
	proxy, err := NewIBKRLoginProxy("https://ibkr.example.test", runtime)
	if err != nil {
		t.Fatal(err)
	}

	loginURL, err := proxy.IssueLoginURL(42)
	if err != nil {
		t.Fatal(err)
	}
	parsedLoginURL, err := url.Parse(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	entryReq := httptest.NewRequest(http.MethodGet, parsedLoginURL.RequestURI(), nil)
	entryReq.Host = "ibkr.example.test"
	entryRes := httptest.NewRecorder()
	proxy.ServeHTTP(entryRes, entryReq)
	if entryRes.Code != http.StatusFound {
		t.Fatalf("ticket exchange: got %d %s", entryRes.Code, entryRes.Body.String())
	}
	if got := entryRes.Header().Get("Location"); got != "/sso/Login" {
		t.Fatalf("ticket redirect: got %q", got)
	}
	result := entryRes.Result()
	cookies := result.Cookies()
	if len(cookies) != 1 || cookies[0].Name != ibkrProxyCookieName || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("unexpected proxy session cookie: %#v", cookies)
	}

	replayReq := httptest.NewRequest(http.MethodGet, parsedLoginURL.RequestURI(), nil)
	replayReq.Host = "ibkr.example.test"
	replayRes := httptest.NewRecorder()
	proxy.ServeHTTP(replayRes, replayReq)
	if replayRes.Code != http.StatusUnauthorized {
		t.Fatalf("ticket replay: got %d", replayRes.Code)
	}

	gatewayReq := httptest.NewRequest(http.MethodGet, "/sso/Login", nil)
	gatewayReq.Host = "ibkr.example.test"
	gatewayReq.AddCookie(cookies[0])
	gatewayRes := httptest.NewRecorder()
	proxy.ServeHTTP(gatewayRes, gatewayReq)
	if gatewayRes.Code != http.StatusOK || gatewayRes.Body.String() != "gateway login" {
		t.Fatalf("gateway proxy: got %d %q", gatewayRes.Code, gatewayRes.Body.String())
	}
	setCookie := gatewayRes.Header().Get("Set-Cookie")
	if strings.Contains(strings.ToLower(setCookie), "domain=") {
		t.Fatalf("upstream cookie domain was not removed: %s", setCookie)
	}
}

func TestIBKRLoginProxyRejectsNonLoopbackGateway(t *testing.T) {
	target, _ := url.Parse("https://gateway.internal.example:5680")
	proxy, err := NewIBKRLoginProxy("https://ibkr.example.test", &fakeIBKRProxyRuntime{target: target, ibkr: true})
	if err != nil {
		t.Fatal(err)
	}
	loginURL, err := proxy.IssueLoginURL(42)
	if err != nil {
		t.Fatal(err)
	}
	parsedLoginURL, _ := url.Parse(loginURL)
	req := httptest.NewRequest(http.MethodGet, parsedLoginURL.RequestURI(), nil)
	req.Host = "ibkr.example.test"
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("unsafe target: got %d %s", res.Code, res.Body.String())
	}
}

func TestIBKRLoginProxyDoesNotExposeGatewayAPI(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("Gateway API request must not reach upstream")
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	proxy, err := NewIBKRLoginProxy("https://ibkr.example.test", &fakeIBKRProxyRuntime{target: target, ibkr: true})
	if err != nil {
		t.Fatal(err)
	}
	loginURL, _ := proxy.IssueLoginURL(42)
	parsedLoginURL, _ := url.Parse(loginURL)
	entryReq := httptest.NewRequest(http.MethodGet, parsedLoginURL.RequestURI(), nil)
	entryReq.Host = "ibkr.example.test"
	entryRes := httptest.NewRecorder()
	proxy.ServeHTTP(entryRes, entryReq)
	cookie := entryRes.Result().Cookies()[0]

	apiReq := httptest.NewRequest(http.MethodGet, "/v1/api/portfolio/accounts", nil)
	apiReq.Host = "ibkr.example.test"
	apiReq.AddCookie(cookie)
	apiRes := httptest.NewRecorder()
	proxy.ServeHTTP(apiRes, apiReq)
	if apiRes.Code != http.StatusForbidden {
		t.Fatalf("Gateway API path: got %d %s", apiRes.Code, apiRes.Body.String())
	}
}

func TestIBKRLoginProxySupportsLocalHTTPOrigin(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name: "JSESSIONID", Value: "gateway-session", Path: "/sso", Secure: true, SameSite: http.SameSiteNoneMode,
		})
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	proxy, err := NewIBKRLoginProxy("http://127.0.0.1:8081", &fakeIBKRProxyRuntime{target: target, ibkr: true})
	if err != nil {
		t.Fatal(err)
	}
	loginURL, _ := proxy.IssueLoginURL(42)
	parsedLoginURL, _ := url.Parse(loginURL)
	entryReq := httptest.NewRequest(http.MethodGet, parsedLoginURL.RequestURI(), nil)
	entryReq.Host = "127.0.0.1:8081"
	entryRes := httptest.NewRecorder()
	proxy.ServeHTTP(entryRes, entryReq)
	proxySession := entryRes.Result().Cookies()[0]
	if proxySession.Secure {
		t.Fatal("local HTTP proxy session must not require Secure transport")
	}

	gatewayReq := httptest.NewRequest(http.MethodGet, "/sso/Login", nil)
	gatewayReq.Host = "127.0.0.1:8081"
	gatewayReq.AddCookie(proxySession)
	gatewayRes := httptest.NewRecorder()
	proxy.ServeHTTP(gatewayRes, gatewayReq)
	if gatewayRes.Code != http.StatusOK {
		t.Fatalf("local HTTP Gateway proxy: got %d %s", gatewayRes.Code, gatewayRes.Body.String())
	}
	gatewayCookies := gatewayRes.Result().Cookies()
	if len(gatewayCookies) != 1 || gatewayCookies[0].Secure || gatewayCookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected local HTTP Gateway cookie: %#v", gatewayCookies)
	}
}

func TestConnectionLoginReturnsConfiguredIBKRProxyURL(t *testing.T) {
	target, _ := url.Parse("https://127.0.0.1:5680")
	runtime := &fakeIBKRProxyRuntime{target: target, ibkr: true}
	proxy, err := NewIBKRLoginProxy("https://ibkr.example.test", runtime)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Deps{BrokerRuntime: runtime, IBKRLoginProxy: proxy}, ServerControl{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/broker-connections/42/login", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("connection login: got %d %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"url":"https://ibkr.example.test/login?ticket=`) {
		t.Fatalf("connection login did not return proxy URL: %s", res.Body.String())
	}
}

func TestConnectionLoginKeepsRemoteGatewayURL(t *testing.T) {
	target, _ := url.Parse("https://gateway.example.test")
	runtime := &fakeIBKRProxyRuntime{
		target: target, ibkr: true, loginURL: "https://gateway.example.test/sso/Login",
	}
	proxy, err := NewIBKRLoginProxy("https://ibkr.example.test", runtime)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Deps{BrokerRuntime: runtime, IBKRLoginProxy: proxy}, ServerControl{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/broker-connections/42/login", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"url":"https://gateway.example.test/sso/Login"`) {
		t.Fatalf("remote Gateway URL was replaced by local proxy: got %d %s", res.Code, res.Body.String())
	}
}

func TestConnectionLoginStatusDoesNotReturnPrivateGatewayURL(t *testing.T) {
	target, _ := url.Parse("https://127.0.0.1:5680")
	runtime := &fakeIBKRProxyRuntime{target: target, ibkr: true}
	router := NewRouter(Deps{BrokerRuntime: runtime}, ServerControl{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/broker-connections/42/auth/status", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"authenticated":true`) || strings.Contains(res.Body.String(), "localhost") {
		t.Fatalf("connection auth status: got %d %s", res.Code, res.Body.String())
	}
}

func TestIBKRProxyHostIsDispatchedBeforeAPIMiddleware(t *testing.T) {
	target, _ := url.Parse("https://127.0.0.1:5680")
	runtime := &fakeIBKRProxyRuntime{target: target, ibkr: true}
	proxy, err := NewIBKRLoginProxy("https://ibkr.example.test", runtime)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Deps{APIToken: strings.Repeat("a", 32), IBKRLoginProxy: proxy}, ServerControl{})
	req := httptest.NewRequest(http.MethodGet, "/sso/Login", nil)
	req.Host = "ibkr.example.test"
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), "IBKR login session") {
		t.Fatalf("proxy host dispatch: got %d %s", res.Code, res.Body.String())
	}
}
