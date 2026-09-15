package ibkr

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/config"
)

const defaultFlexBaseURL = "https://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService"

// ValidateFlexBaseURL accepts only documented IBKR service origins and exact
// service paths. Tests inject an HTTP transport, never a runtime allowlist bypass.
func ValidateFlexBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Port() != "" || u.RawPath != "" {
		return errors.New("flex_base_url must be a trusted IBKR HTTPS service URL")
	}
	host := strings.ToLower(u.Hostname())
	if host != "ndcdyn.interactivebrokers.com" && host != "gdcdyn.interactivebrokers.com" {
		return errors.New("flex_base_url must use a trusted IBKR report host")
	}
	if strings.TrimRight(u.Path, "/") != "/AccountManagement/FlexWebService" {
		return errors.New("flex_base_url must use the IBKR FlexWebService path")
	}
	return nil
}

type flexTransportContextKey struct{}

// withFlexTransport is test-only injection of a transport; request URLs still
// satisfy the production allowlist and no arbitrary configured host is trusted.
func withFlexTransport(ctx context.Context, rt http.RoundTripper) context.Context {
	return context.WithValue(ctx, flexTransportContextKey{}, rt)
}

func newFlexHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (c *Client) flexRequestBody(ctx context.Context, operation string, query url.Values) ([]byte, error) {
	base := strings.TrimRight(c.cfg.FlexBaseURL, "/")
	if base == "" {
		base = defaultFlexBaseURL
	}
	if e := ValidateFlexBaseURL(base); e != nil {
		return nil, e
	}
	if operation != "SendRequest" && operation != "GetStatement" {
		return nil, errors.New("invalid Flex operation")
	}
	u, _ := url.Parse(base + "/" + operation)
	u.RawQuery = query.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return nil, errors.New("ibkr flex: invalid request")
	}
	req.Header.Set("User-Agent", "Java")
	// This client must never reuse Gateway authorization or relaxed local TLS.
	hc := c.flexClient
	if hc == nil {
		hc = newFlexHTTPClient()
	}
	if testTransport, ok := ctx.Value(flexTransportContextKey{}).(http.RoundTripper); ok {
		testClient := *hc
		testClient.Transport = testTransport
		hc = &testClient
	}
	resp, e := hc.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("ibkr flex: network request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ibkr flex: HTTP status %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, MaxActivityXMLBytes+1))
	if e != nil {
		return nil, errors.New("ibkr flex: could not read response")
	}
	if len(b) > MaxActivityXMLBytes {
		return nil, errors.New("ibkr flex: response too large")
	}
	return b, nil
}

// NewActivityClient builds a report-capable client without requiring Gateway
// credentials or opening/authenticating a brokerage session.
func NewActivityClient(cfg config.IBKRConfig) (*Client, error) {
	if e := ValidateFlexBaseURL(cfg.FlexBaseURL); e != nil {
		return nil, e
	}
	return New(cfg), nil
}

// FetchActivityHistory is bounded to one requested date window. Callers store
// actual report coverage, not the requested range or min/max transaction dates.
func (c *Client) FetchActivityHistory(ctx context.Context, from, to string) ([]activity.RawRecord, error) {
	body, e := c.FetchActivityReport(ctx, from, to)
	if e != nil {
		return nil, e
	}
	return ParseActivityXML(body)
}
func (c *Client) FetchActivityReport(ctx context.Context, from, to string) ([]byte, error) {
	token := strings.TrimSpace(c.cfg.FlexToken)
	query := strings.TrimSpace(c.cfg.FlexActivityQueryID)
	if token == "" || query == "" {
		return nil, errors.New("source_not_configured: Flex activity token and query ID are required")
	}
	if e := activity.ValidateDate(from); e != nil {
		return nil, e
	}
	if e := activity.ValidateDate(to); e != nil {
		return nil, e
	}
	if (from == "") != (to == "") || from > to {
		return nil, errors.New("invalid activity history range")
	}
	if from != "" {
		start, _ := time.Parse("2006-01-02", from)
		end, _ := time.Parse("2006-01-02", to)
		if end.Sub(start) > 364*24*time.Hour {
			return nil, errors.New("activity history window exceeds 365 days")
		}
	}
	period := flexPeriod{from: from, to: to}
	ref, e := c.flexSendRequest(ctx, token, query, period)
	if e != nil {
		return nil, e
	}
	return c.flexGetStatement(ctx, token, ref, period)
}

// FetchRecentTrades only performs a GET. It neither switches accounts nor
// starts/competes for a brokerage session. The endpoint returns the accounts in
// the Gateway session scope; callers still map them to authorized accounts.
func (c *Client) FetchRecentTrades(ctx context.Context) ([]activity.RawRecord, error) {
	endpoint := strings.TrimRight(c.cfg.GatewayURL, "/") + "/v1/api/iserver/account/trades?days=7"
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if e != nil {
		return nil, errors.New("ibkr: invalid history request")
	}
	hc := *c.httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := hc.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("ibkr: history request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ibkr: trade history HTTP status %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, MaxActivityXMLBytes+1))
	if e != nil {
		return nil, errors.New("ibkr: history response read failed")
	}
	if len(b) > MaxActivityXMLBytes {
		return nil, errors.New("ibkr: history response too large")
	}
	rows, e := ParseGatewayTrades(b)
	if e != nil {
		return nil, e
	}
	return rows, nil
}

type ActivityReportRange struct {
	ProviderAccountID string   `json:"provider_account_id"`
	From              string   `json:"from"`
	To                string   `json:"to"`
	Sections          []string `json:"sections"`
}

// ActivityReportCoverage extracts explicit statement dates including empty
// sections. Absence of a section is never evidence of an empty complete ledger.
func ActivityReportCoverage(body []byte) ([]ActivityReportRange, error) {
	if _, e := ParseActivityXML(body); e != nil {
		return nil, e
	}
	d := xml.NewDecoder(strings.NewReader(string(body)))
	ranges := []ActivityReportRange{}
	current := -1
	depth := 0
	statementDepth := 0
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, errors.New("invalid activity XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "FlexStatement" {
				r := ActivityReportRange{Sections: []string{}}
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "accountId":
						r.ProviderAccountID = a.Value
					case "fromDate":
						r.From = activity.NormalizeDate(a.Value)
					case "toDate":
						r.To = activity.NormalizeDate(a.Value)
					}
				}
				ranges = append(ranges, r)
				current = len(ranges) - 1
				statementDepth = depth
			} else if current >= 0 && depth == statementDepth+1 {
				ranges[current].Sections = append(ranges[current].Sections, t.Name.Local)
			}
		case xml.EndElement:
			if t.Name.Local == "FlexStatement" {
				current = -1
			}
			depth--
		}
	}
	return ranges, nil
}
