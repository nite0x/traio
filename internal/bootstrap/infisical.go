package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	sdkauth "github.com/infisical/go-sdk/packages/api/auth"
	sdksecrets "github.com/infisical/go-sdk/packages/api/secrets"
	sdkerrors "github.com/infisical/go-sdk/packages/errors"
)

// infisicalProvider uses the official SDK's exported request modules. The SDK's
// top-level client does not expose request context/timeout control in v0.8.0.
// Owning the transport lets us enforce the entire startup deadline without
// global HTTP changes, detached goroutines, or a background token lifecycle.
type infisicalProvider struct {
	config    ProviderConfig
	transport http.RoundTripper
	mu        sync.Mutex
	loaded    bool
	values    map[string]string
	err       error
}

func NewInfisical(config ProviderConfig) (Provider, error) {
	return &infisicalProvider{config: config}, nil
}

func (p *infisicalProvider) Fetch(ctx context.Context, keys []string) (FetchResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return FetchResult{}, providerDiagnostic("configuration retrieval canceled or timed out")
	}
	if !p.loaded {
		p.loaded = true
		p.values, p.err = p.load(ctx)
		// Only selected application values survive authentication; release bootstrap credentials.
		p.config.ClientSecret = ""
	}
	if p.err != nil {
		return FetchResult{}, p.err
	}
	result := FetchResult{Values: map[string]string{}}
	for _, key := range keys {
		if value, ok := p.values[key]; ok {
			result.Values[key] = value
		}
	}
	return result, nil
}

func (p *infisicalProvider) load(ctx context.Context) (map[string]string, error) {
	transport := p.transport
	if transport == nil {
		own := http.DefaultTransport.(*http.Transport).Clone()
		defer own.CloseIdleConnections()
		transport = own
	}
	hc := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirects are disabled") },
	}
	client := resty.NewWithClient(hc).SetBaseURL(p.config.SiteURL + "/api").
		SetRetryCount(2).SetRetryWaitTime(200 * time.Millisecond).SetRetryMaxWaitTime(time.Second).
		SetLogger(discardLogger{}).SetDebug(false)
	client.OnBeforeRequest(func(_ *resty.Client, req *resty.Request) error {
		req.SetContext(ctx)
		return ctx.Err()
	})
	client.OnAfterResponse(func(_ *resty.Client, res *resty.Response) error {
		if res.StatusCode() >= 300 && res.StatusCode() < 400 {
			return fmt.Errorf("unexpected redirect")
		}
		return nil
	})
	client.AddRetryCondition(func(res *resty.Response, err error) bool {
		if ctx.Err() != nil {
			return false
		}
		if res != nil && res.StatusCode() != 0 {
			status := res.StatusCode()
			return status == 429 || status == 500 || status == 502 || status == 503 || status == 504
		}
		return err != nil
	})
	token, err := sdkauth.CallUniversalAuthLogin(client, sdkauth.UniversalAuthLoginRequest{
		ClientID: p.config.ClientID, ClientSecret: p.config.ClientSecret,
	})
	if err != nil {
		return nil, safeProviderError(ctx, "authentication", err)
	}
	if token.AccessToken == "" {
		return nil, providerDiagnostic("Infisical authentication returned an invalid response")
	}
	client.SetAuthToken(token.AccessToken)
	defer client.SetAuthToken("")
	// A successful scoped list distinguishes an absent key from a wrong project,
	// environment or path (404). One snapshot also keeps selectors and values coherent.
	response, err := sdksecrets.CallListSecretsV3(nil, client, sdksecrets.ListSecretsV3RawRequest{
		ProjectID: p.config.ProjectID, Environment: p.config.Environment, SecretPath: p.config.SecretPath,
		ExpandSecretReferences: true, IncludeImports: false, Recursive: false,
	})
	if err != nil {
		return nil, safeProviderError(ctx, "secret retrieval", err)
	}
	if response.Secrets == nil {
		return nil, providerDiagnostic("Infisical secret retrieval returned an invalid response")
	}
	allowed := map[string]bool{}
	for _, f := range allFields() {
		allowed[f.name] = true
	}
	values := map[string]string{}
	for _, secret := range response.Secrets {
		if !allowed[secret.SecretKey] {
			continue
		}
		if _, exists := values[secret.SecretKey]; exists {
			return nil, providerDiagnostic("Infisical returned duplicate configuration keys")
		}
		values[secret.SecretKey] = secret.SecretValue
	}
	return values, nil
}

func safeProviderError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return providerDiagnostic(fmt.Sprintf("Infisical %s canceled or timed out", operation))
	}
	var apiErr *sdkerrors.APIError
	if errors.As(err, &apiErr) {
		return providerDiagnostic(fmt.Sprintf("Infisical %s failed (HTTP %d)", operation, apiErr.StatusCode))
	}
	return providerDiagnostic(fmt.Sprintf("Infisical %s failed (transport or response error)", operation))
}

type discardLogger struct{}

func (discardLogger) Errorf(string, ...interface{}) {}
func (discardLogger) Warnf(string, ...interface{})  {}
func (discardLogger) Debugf(string, ...interface{}) {}
