package schwab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const refreshBeforeExpiry = 30 * time.Second

// Token is the response returned by Schwab's OAuth token endpoint.
// ExpiresAt is calculated locally from ExpiresIn.
type Token struct {
	ExpiresIn    int       `json:"expires_in"`
	TokenType    string    `json:"token_type"`
	Scope        string    `json:"scope"`
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token"`
	IDToken      string    `json:"id_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// AuthorizationResponse contains the values returned to the application's
// callback URL after the user completes Schwab authorization.
type AuthorizationResponse struct {
	Code    string
	Session string
	State   string
}

// APIError describes a non-2xx response from Schwab.
type APIError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("schwab: %s", e.Status)
	}
	return fmt.Sprintf("schwab: %s: %s", e.Status, e.Body)
}

// AuthURL returns the URL the user must visit to authorize the application.
func (c *Client) AuthURL(state string) string {
	c.mu.RLock()
	cfg := c.cfg
	authorizeURL := c.authorizeURL
	c.mu.RUnlock()

	values := url.Values{
		"client_id":    {cfg.ClientID},
		"redirect_uri": {cfg.RedirectURI},
	}
	if state != "" {
		values.Set("state", state)
	}
	return authorizeURL + "?" + values.Encode()
}

// ParseCallbackURL extracts the authorization response from Schwab's landing
// URL. URL parsing decodes the code before it is sent to the token endpoint.
func ParseCallbackURL(callbackURL string) (AuthorizationResponse, error) {
	parsed, err := url.Parse(strings.TrimSpace(callbackURL))
	if err != nil {
		return AuthorizationResponse{}, fmt.Errorf("schwab: parse callback URL: %w", err)
	}
	query := parsed.Query()
	if oauthErr := query.Get("error"); oauthErr != "" {
		description := query.Get("error_description")
		if description == "" {
			description = oauthErr
		}
		return AuthorizationResponse{}, fmt.Errorf("schwab: authorization failed: %s", description)
	}
	code := query.Get("code")
	if code == "" {
		return AuthorizationResponse{}, errors.New("schwab: callback URL does not include an authorization code")
	}
	return AuthorizationResponse{
		Code:    code,
		Session: query.Get("session"),
		State:   query.Get("state"),
	}, nil
}

// ExchangeCode exchanges an authorization code and stores the returned token.
func (c *Client) ExchangeCode(ctx context.Context, code string) error {
	_, err := c.ExchangeCodeForToken(ctx, code)
	return err
}

// ExchangeCodeForToken exchanges a URL-encoded authorization code and returns
// the initial access and refresh tokens.
func (c *Client) ExchangeCodeForToken(ctx context.Context, code string) (Token, error) {
	decodedCode, err := url.QueryUnescape(strings.TrimSpace(code))
	if err != nil {
		return Token{}, fmt.Errorf("schwab: decode authorization code: %w", err)
	}
	if decodedCode == "" {
		return Token{}, errors.New("schwab: authorization code is required")
	}

	c.mu.RLock()
	redirectURI := c.cfg.RedirectURI
	c.mu.RUnlock()
	if redirectURI == "" {
		return Token{}, errors.New("schwab: redirect URI is required")
	}

	return c.exchangeToken(ctx, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {decodedCode},
		"redirect_uri": {redirectURI},
	}, "")
}

// RefreshAccessToken exchanges the client's current refresh token for a new
// access token and stores the response.
func (c *Client) RefreshAccessToken(ctx context.Context) (Token, error) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	c.mu.RLock()
	token := cloneToken(c.token)
	c.mu.RUnlock()
	if token == nil || token.RefreshToken == "" {
		return Token{}, errors.New("schwab: refresh token is required")
	}
	return c.refreshToken(ctx, token.RefreshToken)
}

// RefreshToken exchanges a supplied refresh token and stores the response.
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (Token, error) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	return c.refreshToken(ctx, refreshToken)
}

func (c *Client) refreshToken(ctx context.Context, refreshToken string) (Token, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Token{}, errors.New("schwab: refresh token is required")
	}
	return c.exchangeToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}, refreshToken)
}

func (c *Client) exchangeToken(ctx context.Context, form url.Values, previousRefreshToken string) (Token, error) {
	c.mu.RLock()
	cfg := c.cfg
	tokenURL := c.tokenURL
	httpClient := c.httpClient
	c.mu.RUnlock()
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return Token{}, errors.New("schwab: client ID and client secret are required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("schwab: create token request: %w", err)
	}
	req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("schwab: token request: %w", err)
	}
	defer resp.Body.Close()
	if err := responseError(resp); err != nil {
		return Token{}, err
	}

	var token Token
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return Token{}, fmt.Errorf("schwab: decode token response: %w", err)
	}
	if token.AccessToken == "" {
		return Token{}, errors.New("schwab: token response did not include an access token")
	}
	if token.RefreshToken == "" {
		token.RefreshToken = previousRefreshToken
	}
	token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)

	c.mu.Lock()
	c.token = cloneToken(&token)
	onToken := c.onToken
	c.mu.Unlock()
	if onToken != nil {
		onToken(token)
	}
	c.stream.wake()
	return token, nil
}

// NewAuthenticatedRequest creates a request with a valid Bearer access token.
// It refreshes the access token when it is within 30 seconds of expiration.
func (c *Client) NewAuthenticatedRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	accessToken, err := c.validAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("schwab: create API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// Do sends an authenticated request and returns non-2xx responses as APIError.
func (c *Client) Do(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, error) {
	req, err := c.NewAuthenticatedRequest(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	httpClient := c.httpClient
	c.mu.RUnlock()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("schwab: API request: %w", err)
	}
	if err := responseError(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

func (c *Client) validAccessToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	token := cloneToken(c.token)
	c.mu.RUnlock()
	if token == nil || token.AccessToken == "" {
		return "", errors.New("schwab: access token is required")
	}
	if token.ExpiresAt.IsZero() || time.Now().Add(refreshBeforeExpiry).Before(token.ExpiresAt) {
		return token.AccessToken, nil
	}
	refreshed, err := c.RefreshAccessToken(ctx)
	if err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

func responseError(resp *http.Response) error {
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return &APIError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       strings.TrimSpace(string(body)),
	}
}

func cloneToken(token *Token) *Token {
	if token == nil {
		return nil
	}
	copy := *token
	return &copy
}
