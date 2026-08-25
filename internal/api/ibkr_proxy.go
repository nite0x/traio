package api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	ibkrLoginTicketTTL  = time.Minute
	ibkrProxySessionTTL = 30 * time.Minute
	ibkrProxyCookieName = "traio_ibkr_proxy"
)

// ibkrGatewayTargetResolver resolves a connection to its private, loopback-only
// Client Portal Gateway. The target is never accepted from an HTTP request.
type ibkrGatewayTargetResolver interface {
	IBKRGatewayTarget(context.Context, int64) (*url.URL, bool, error)
}

type ibkrLoginTicket struct {
	connectionID int64
	workspaceID  int64
	userID       int64
	expiresAt    time.Time
}

type ibkrProxySession struct {
	connectionID int64
	workspaceID  int64
	userID       int64
	expiresAt    time.Time
}

// IBKRLoginProxy exposes a Client Portal Gateway through a configured public
// URL. A single-use ticket establishes a short-lived, HttpOnly proxy session.
// The URL may include a path prefix so the proxy can share the API origin.
type IBKRLoginProxy struct {
	externalURL *url.URL
	resolver    ibkrGatewayTargetResolver
	transport   http.RoundTripper

	mu       sync.Mutex
	tickets  map[string]ibkrLoginTicket
	sessions map[string]ibkrProxySession
	now      func() time.Time
}

// NewIBKRLoginProxy returns nil when externalURL is empty, preserving the
// existing desktop behavior that opens the local Gateway URL directly.
func NewIBKRLoginProxy(externalURL string, resolver ibkrGatewayTargetResolver) (*IBKRLoginProxy, error) {
	externalURL = strings.TrimSpace(externalURL)
	if externalURL == "" {
		return nil, nil
	}
	if resolver == nil {
		return nil, fmt.Errorf("IBKR login proxy requires a gateway resolver")
	}
	parsed, err := url.Parse(externalURL)
	if err != nil {
		return nil, fmt.Errorf("parse IBKR proxy URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("IBKR proxy URL must use http or https")
	}
	if parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("IBKR proxy URL must not contain credentials, query, or fragment")
	}
	if parsed.RawPath != "" {
		return nil, fmt.Errorf("IBKR proxy URL path must not contain escapes")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	return &IBKRLoginProxy{
		externalURL: parsed,
		resolver:    resolver,
		transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: true, // Safe only after validateIBKRGatewayTarget pins the target to loopback.
			},
		},
		tickets:  map[string]ibkrLoginTicket{},
		sessions: map[string]ibkrProxySession{},
		now:      time.Now,
	}, nil
}

func (p *IBKRLoginProxy) ExternalURL() string {
	if p == nil {
		return ""
	}
	return p.externalURL.String()
}

// IssueLoginURL creates a one-minute, single-use browser entry URL.
func (p *IBKRLoginProxy) IssueLoginURL(connectionID int64) (string, error) {
	return p.IssueLoginURLForPrincipal(connectionID, 0, 0)
}

// IssueLoginURLForPrincipal binds a one-time Gateway ticket to the user and
// workspace that requested it. Zero identity values are reserved for local
// desktop compatibility and tests.
func (p *IBKRLoginProxy) IssueLoginURLForPrincipal(connectionID, workspaceID, userID int64) (string, error) {
	if p == nil || connectionID <= 0 {
		return "", fmt.Errorf("IBKR login proxy is not configured")
	}
	token, err := randomProxyToken()
	if err != nil {
		return "", err
	}
	now := p.now()
	p.mu.Lock()
	p.cleanupLocked(now)
	p.tickets[token] = ibkrLoginTicket{connectionID: connectionID, workspaceID: workspaceID, userID: userID, expiresAt: now.Add(ibkrLoginTicketTTL)}
	p.mu.Unlock()

	entry := *p.externalURL
	entry.Path = joinIBKRProxyPath(p.externalURL.Path, "/login")
	entry.RawQuery = url.Values{"ticket": {token}}.Encode()
	return entry.String(), nil
}

// Middleware dispatches the configured proxy URL before the JSON API's CORS
// and loopback Host checks. Other hosts and paths continue through the router.
func (p *IBKRLoginProxy) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p == nil || !p.shouldHandle(c.Request) {
			c.Next()
			return
		}
		p.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}
}

func (p *IBKRLoginProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p == nil || !sameHost(r.Host, p.externalURL.Host) {
		http.Error(w, "IBKR proxy host is not allowed", http.StatusMisdirectedRequest)
		return
	}
	gatewayPath, ok := p.gatewayPath(r)
	if !ok {
		http.Error(w, "IBKR Gateway path is not available through the login proxy", http.StatusForbidden)
		return
	}
	if gatewayPath == "/login" {
		p.consumeLoginTicket(w, r)
		return
	}
	if !allowedIBKRLoginPath(gatewayPath) {
		http.Error(w, "IBKR Gateway path is not available through the login proxy", http.StatusForbidden)
		return
	}

	connectionID, ok := p.authorizeSession(r)
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "IBKR login session is missing or expired", http.StatusUnauthorized)
		return
	}
	target, isIBKR, err := p.resolver.IBKRGatewayTarget(r.Context(), connectionID)
	if err != nil {
		http.Error(w, "IBKR Gateway is unavailable", http.StatusBadGateway)
		return
	}
	if !isIBKR {
		http.Error(w, "broker connection is not IBKR", http.StatusBadRequest)
		return
	}
	if err := validateIBKRGatewayTarget(target); err != nil {
		http.Error(w, "IBKR Gateway target is not allowed", http.StatusBadGateway)
		return
	}
	proxyRequest := r.Clone(r.Context())
	proxyURL := *r.URL
	proxyURL.Path = gatewayPath
	proxyURL.RawPath = ""
	proxyRequest.URL = &proxyURL
	p.reverseProxy(target).ServeHTTP(w, proxyRequest)
}

func (p *IBKRLoginProxy) shouldHandle(r *http.Request) bool {
	if !sameHost(r.Host, p.externalURL.Host) {
		return false
	}
	if _, ok := p.gatewayPath(r); !ok {
		return false
	}
	return true
}

func (p *IBKRLoginProxy) gatewayPath(r *http.Request) (string, bool) {
	prefix := p.externalURL.Path
	requestPath := r.URL.Path
	if prefix == "" {
		return requestPath, true
	}
	if requestPath == prefix {
		return "/", true
	}
	if strings.HasPrefix(requestPath, prefix+"/") {
		return strings.TrimPrefix(requestPath, prefix), true
	}
	// Gateway pages use some absolute asset and form-action paths. Once a
	// proxy session exists, dispatch only the narrow login allowlist for them.
	if _, err := r.Cookie(ibkrProxyCookieName); err == nil && allowedIBKRLoginPath(requestPath) {
		return requestPath, true
	}
	return "", false
}

// RevokeConnection removes browser entry points once status polling confirms
// that the Gateway is authenticated.
func (p *IBKRLoginProxy) RevokeConnection(connectionID int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for token, ticket := range p.tickets {
		if ticket.connectionID == connectionID {
			delete(p.tickets, token)
		}
	}
	for token, session := range p.sessions {
		if session.connectionID == connectionID {
			delete(p.sessions, token)
		}
	}
}

func allowedIBKRLoginPath(path string) bool {
	if path == "/" {
		return true
	}
	for _, prefix := range []string{
		"/sso/",
		"/css/",
		"/images/",
		"/scripts/",
		"/portal.proxy/",
		"/credential.recovery/",
		"/en/",
		"/Universal/",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (p *IBKRLoginProxy) consumeLoginTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("ticket"))
	now := p.now()
	p.mu.Lock()
	p.cleanupLocked(now)
	ticket, ok := p.tickets[token]
	if ok {
		delete(p.tickets, token)
	}
	p.mu.Unlock()
	if !ok || token == "" || !ticket.expiresAt.After(now) {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "IBKR login ticket is invalid or expired", http.StatusUnauthorized)
		return
	}

	// Resolve and validate before establishing a browser session. This also
	// rejects a connection that was deleted or changed after ticket issuance.
	target, isIBKR, err := p.resolver.IBKRGatewayTarget(r.Context(), ticket.connectionID)
	if err != nil || !isIBKR || validateIBKRGatewayTarget(target) != nil {
		http.Error(w, "IBKR Gateway is unavailable", http.StatusBadGateway)
		return
	}
	sessionToken, err := randomProxyToken()
	if err != nil {
		http.Error(w, "create IBKR login session", http.StatusInternalServerError)
		return
	}
	expiresAt := now.Add(ibkrProxySessionTTL)
	p.mu.Lock()
	p.sessions[sessionToken] = ibkrProxySession{connectionID: ticket.connectionID, workspaceID: ticket.workspaceID, userID: ticket.userID, expiresAt: expiresAt}
	p.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     ibkrProxyCookieName,
		Value:    sessionToken,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(ibkrProxySessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   p.externalURL.Scheme == "https",
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, joinIBKRProxyPath(p.externalURL.Path, "/sso/Login"), http.StatusFound)
}

func (p *IBKRLoginProxy) authorizeSession(r *http.Request) (int64, bool) {
	cookie, err := r.Cookie(ibkrProxyCookieName)
	if err != nil || cookie.Value == "" {
		return 0, false
	}
	now := p.now()
	p.mu.Lock()
	p.cleanupLocked(now)
	session, ok := p.sessions[cookie.Value]
	p.mu.Unlock()
	if !ok || !session.expiresAt.After(now) {
		return 0, false
	}
	return session.connectionID, true
}

func (p *IBKRLoginProxy) cleanupLocked(now time.Time) {
	for token, ticket := range p.tickets {
		if !ticket.expiresAt.After(now) {
			delete(p.tickets, token)
		}
	}
	for token, session := range p.sessions {
		if !session.expiresAt.After(now) {
			delete(p.sessions, token)
		}
	}
}

func (p *IBKRLoginProxy) reverseProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = p.transport
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
		stripTraioCookies(req)
		// The Gateway is private. Do not forward client-controlled proxy headers.
		req.Header.Del("Forwarded")
		req.Header.Del("X-Forwarded-For")
		req.Header.Del("X-Forwarded-Host")
		req.Header.Del("X-Forwarded-Proto")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		rewriteGatewayCookies(resp, p.externalURL.Scheme == "https")
		rewriteGatewayLocation(resp, target, p.externalURL)
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "IBKR Gateway proxy request failed", http.StatusBadGateway)
	}
	return proxy
}

func rewriteGatewayCookies(resp *http.Response, secureOrigin bool) {
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return
	}
	resp.Header.Del("Set-Cookie")
	for _, cookie := range cookies {
		if cookie.Name == ibkrProxyCookieName {
			continue
		}
		// Upstream sometimes emits Domain=.ibkr.com, which a Traio proxy origin
		// cannot set. Convert every Gateway cookie to a host-only cookie.
		cookie.Domain = ""
		cookie.Secure = secureOrigin
		if !secureOrigin && cookie.SameSite == http.SameSiteNoneMode {
			// SameSite=None requires Secure in modern browsers. The local HTTP
			// proxy is same-origin, so Lax is sufficient for development use.
			cookie.SameSite = http.SameSiteLaxMode
		}
		resp.Header.Add("Set-Cookie", cookie.String())
	}
}

func stripTraioCookies(req *http.Request) {
	cookies := req.Cookies()
	req.Header.Del("Cookie")
	for _, cookie := range cookies {
		if strings.HasPrefix(strings.ToLower(cookie.Name), "traio_") {
			continue
		}
		req.AddCookie(cookie)
	}
}

func rewriteGatewayLocation(resp *http.Response, target, external *url.URL) {
	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location == "" {
		return
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return
	}
	if parsed.IsAbs() {
		if !sameHost(parsed.Host, target.Host) {
			return
		}
		parsed.Scheme = external.Scheme
		parsed.Host = external.Host
	} else if parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
		return
	}
	parsed.Path = joinIBKRProxyPath(external.Path, parsed.Path)
	resp.Header.Set("Location", parsed.String())
}

func joinIBKRProxyPath(prefix, suffix string) string {
	prefix = strings.TrimRight(prefix, "/")
	if suffix == "" || suffix == "/" {
		if prefix == "" {
			return "/"
		}
		return prefix + "/"
	}
	return prefix + "/" + strings.TrimLeft(suffix, "/")
}

func validateIBKRGatewayTarget(target *url.URL) error {
	if target == nil || target.Scheme != "https" || target.User != nil {
		return fmt.Errorf("Gateway target must be an HTTPS URL without credentials")
	}
	host := strings.Trim(strings.ToLower(strings.TrimSpace(target.Hostname())), "[]")
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("Gateway target must be loopback")
		}
	}
	if target.Port() == "" {
		return fmt.Errorf("Gateway target must include a port")
	}
	return nil
}

func sameHost(left, right string) bool {
	return strings.EqualFold(normalizeRequestHost(left), normalizeRequestHost(right))
}

func normalizeRequestHost(value string) string {
	value = strings.TrimSpace(value)
	if host, port, err := net.SplitHostPort(value); err == nil {
		if (port == "80" || port == "443") && host != "" {
			return strings.Trim(strings.ToLower(host), "[]")
		}
	}
	return strings.Trim(strings.ToLower(value), "[]")
}

func randomProxyToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate IBKR proxy token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
