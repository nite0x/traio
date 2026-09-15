package ibkr

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Invalidation warms page zero. We still fetch every page afterwards and only
// return a complete snapshot; the invalidation response is not a full portfolio.
func (c *Client) invalidatePositions(ctx context.Context, accountID string) error {
	path := "/portfolio/" + url.PathEscape(accountID) + "/positions/invalidate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL()+"/v1/api"+path, strings.NewReader("{}"))
	if err != nil {
		return errors.New("ibkr: invalid positions refresh request")
	}
	req.Header.Set("Content-Type", "application/json")
	hc := *c.httpClient
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return errors.New("ibkr: positions refresh failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return gatewayUnauthorizedError(resp, path)
	}
	if resp.StatusCode != http.StatusOK {
		return errors.New("ibkr: positions cache invalidation failed")
	}
	return nil
}
