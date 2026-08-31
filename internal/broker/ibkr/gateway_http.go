package ibkr

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type gatewayAuthTransport struct {
	base  http.RoundTripper
	token string
}

func (t gatewayAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	if t.token != "" && request.Header.Get("Authorization") == "" {
		request.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(request)
}

func newGatewayHTTPClient(gatewayURL, proxyToken string, timeout time.Duration) *http.Client {
	transport := &http.Transport{}
	if parsed, err := url.Parse(gatewayURL); err == nil && isLoopbackHost(parsed.Hostname()) && parsed.Scheme == "https" {
		// Client Portal Gateway commonly uses a development certificate locally.
		// Relax verification only for a loopback destination.
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true} //nolint:gosec
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: gatewayAuthTransport{base: transport, token: strings.TrimSpace(proxyToken)},
	}
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
