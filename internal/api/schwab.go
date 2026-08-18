package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/broker/schwab"
	"github.com/nite/traio/internal/store"
)

type schwabClientResolver func() *schwab.Client

type schwabClientRuntime interface {
	SchwabClient() *schwab.Client
}

func currentSchwabClient(runtime brokerConnectionRuntime, fallback *schwab.Client) schwabClientResolver {
	if dynamic, ok := runtime.(schwabClientRuntime); ok {
		return dynamic.SchwabClient
	}
	return func() *schwab.Client { return fallback }
}

type schwabConfigResponse struct {
	RedirectURI            string `json:"redirect_uri"`
	ClientIDConfigured     bool   `json:"client_id_configured"`
	ClientSecretConfigured bool   `json:"client_secret_configured"`
	ConnectionID           int64  `json:"connection_id,omitempty"`
	ConnectionEnabled      bool   `json:"connection_enabled"`
	Available              bool   `json:"available"`
}

type saveSchwabConfigRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURI  string `json:"redirect_uri"`
}

func schwabConfig(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		provider, connections, err := loadSchwabConfiguration(c, st)
		if err != nil {
			return
		}
		c.JSON(http.StatusOK, newSchwabConfigResponse(provider, connections))
	}
}

func saveSchwabConfig(st brokerStore, onChanged func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		var req saveSchwabConfigRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		req.ClientID = strings.TrimSpace(req.ClientID)
		req.ClientSecret = strings.TrimSpace(req.ClientSecret)
		req.RedirectURI = strings.TrimSpace(req.RedirectURI)
		if err := validateSchwabRedirectURI(req.RedirectURI); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		provider, connections, err := loadSchwabConfiguration(c, st)
		if err != nil {
			return
		}
		hasClientID := configuredSecret(provider.ConfiguredSecretKeys, "client_id")
		hasClientSecret := configuredSecret(provider.ConfiguredSecretKeys, "client_secret")
		providedClientID := req.ClientID != ""
		providedClientSecret := req.ClientSecret != ""
		if providedClientID != providedClientSecret {
			c.JSON(http.StatusBadRequest, gin.H{"error": "client_id and client_secret must be provided together"})
			return
		}
		if !providedClientID && (!hasClientID || !hasClientSecret) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "client_id and client_secret are required for initial Schwab configuration"})
			return
		}

		var secrets map[string]string
		if providedClientID {
			secrets = map[string]string{
				"client_id":     req.ClientID,
				"client_secret": req.ClientSecret,
			}
		}
		provider, err = st.UpdateBrokerProviderConfig(c.Request.Context(), "SCHWAB",
			map[string]any{"redirect_uri": req.RedirectURI}, secrets)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}

		if len(connections) == 0 {
			connection, err := st.UpsertBrokerConnection(c.Request.Context(), store.BrokerConnection{
				ProviderCode:  "SCHWAB",
				ConnectionKey: "primary",
				Name:          "Charles Schwab",
				AuthType:      "oauth",
				Enabled:       true,
				Status:        store.BrokerConnectionStatusDisconnected,
			})
			if err != nil {
				writeBrokerStoreError(c, err)
				return
			}
			connections = append(connections, connection)
		}
		if !notifyBrokersChanged(c, onChanged) {
			return
		}
		c.JSON(http.StatusOK, newSchwabConfigResponse(provider, connections))
	}
}

func loadSchwabConfiguration(c *gin.Context, st brokerStore) (store.BrokerProvider, []store.BrokerConnection, error) {
	providers, err := st.ListBrokerProviders(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return store.BrokerProvider{}, nil, err
	}
	var provider store.BrokerProvider
	found := false
	for _, candidate := range providers {
		if candidate.Code == "SCHWAB" {
			provider = candidate
			found = true
			break
		}
	}
	if !found {
		err := store.ErrNotFound
		writeBrokerStoreError(c, err)
		return store.BrokerProvider{}, nil, err
	}
	allConnections, err := st.ListBrokerConnections(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return store.BrokerProvider{}, nil, err
	}
	connections := make([]store.BrokerConnection, 0)
	for _, connection := range allConnections {
		if connection.ProviderCode == "SCHWAB" {
			connections = append(connections, connection)
		}
	}
	return provider, connections, nil
}

func newSchwabConfigResponse(provider store.BrokerProvider, connections []store.BrokerConnection) schwabConfigResponse {
	response := schwabConfigResponse{
		RedirectURI:            strings.TrimSpace(stringConfigValue(provider.Config, "redirect_uri")),
		ClientIDConfigured:     configuredSecret(provider.ConfiguredSecretKeys, "client_id"),
		ClientSecretConfigured: configuredSecret(provider.ConfiguredSecretKeys, "client_secret"),
		ConnectionID:           0,
		ConnectionEnabled:      false,
	}
	for _, connection := range connections {
		if response.ConnectionID == 0 || connection.Enabled {
			response.ConnectionID = connection.ID
			response.ConnectionEnabled = connection.Enabled
		}
		if connection.Enabled {
			break
		}
	}
	response.Available = response.ClientIDConfigured && response.ClientSecretConfigured &&
		response.RedirectURI != "" && response.ConnectionID != 0 && response.ConnectionEnabled
	return response
}

func configuredSecret(keys []string, target string) bool {
	for _, key := range keys {
		if key == target {
			return true
		}
	}
	return false
}

func stringConfigValue(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return value
}

func validateSchwabRedirectURI(raw string) error {
	if raw == "" {
		return errors.New("redirect_uri is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("redirect_uri must be an HTTPS URL without credentials or fragment")
	}
	return nil
}

func schwabStatus(resolve schwabClientResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		client := resolve()
		if client == nil {
			c.JSON(http.StatusOK, gin.H{
				"available":     false,
				"authenticated": false,
				"stream":        schwab.StreamStatus{},
			})
			return
		}
		_, authenticated := client.Token()
		c.JSON(http.StatusOK, gin.H{
			"available":     true,
			"authenticated": authenticated,
			"stream":        client.StreamStatus(),
		})
	}
}

func schwabOAuthURL(resolve schwabClientResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		client := resolve()
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "schwab is not available"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"url": client.AuthURL(c.Query("state"))})
	}
}

type schwabExchangeRequest struct {
	Code        string `json:"code"`
	CallbackURL string `json:"callback_url"`
}

func schwabOAuthExchange(resolve schwabClientResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		client := resolve()
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "schwab is not available"})
			return
		}
		var req schwabExchangeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		code := strings.TrimSpace(req.Code)
		if code == "" && strings.TrimSpace(req.CallbackURL) != "" {
			callback, err := schwab.ParseCallbackURL(req.CallbackURL)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			code = callback.Code
		}
		if code == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "code or callback_url is required"})
			return
		}
		if _, err := client.ExchangeCodeForToken(c.Request.Context(), code); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "authenticated"})
	}
}
