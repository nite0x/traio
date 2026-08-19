package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/store"
)

type alpacaStatusSession interface {
	Configured() bool
	LoginStatus(context.Context) (broker.LoginAction, error)
	AccountSummary(context.Context) (broker.AccountSummary, error)
}

type alpacaConfigResponse struct {
	BaseURL             string `json:"base_url"`
	APIKeyConfigured    bool   `json:"api_key_configured"`
	APISecretConfigured bool   `json:"api_secret_configured"`
	ConnectionID        int64  `json:"connection_id,omitempty"`
	ConnectionEnabled   bool   `json:"connection_enabled"`
	Available           bool   `json:"available"`
}

type saveAlpacaConfigRequest struct {
	APIKey    string `json:"api_key"`
	APISecret string `json:"api_secret"`
	BaseURL   string `json:"base_url"`
}

func alpacaConfiguration(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		connection, err := loadPrimaryAlpacaConnection(c, st)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return
		}
		c.JSON(http.StatusOK, newAlpacaConfigResponse(connection))
	}
}

func saveAlpacaConfiguration(st brokerStore, onChanged func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		var req saveAlpacaConfigRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		req.APIKey = strings.TrimSpace(req.APIKey)
		req.APISecret = strings.TrimSpace(req.APISecret)
		req.BaseURL = strings.TrimSpace(req.BaseURL)
		if req.BaseURL == "" {
			req.BaseURL = "https://paper-api.alpaca.markets"
		}
		if err := validateBrokerAPIBaseURL(req.BaseURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		connection, err := loadPrimaryAlpacaConnection(c, st)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return
		}
		hasAPIKey := configuredSecret(connection.ConfiguredSecretKeys, "api_key")
		hasAPISecret := configuredSecret(connection.ConfiguredSecretKeys, "api_secret")
		providedAPIKey := req.APIKey != ""
		providedAPISecret := req.APISecret != ""
		if providedAPIKey != providedAPISecret {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api_key and api_secret must be provided together"})
			return
		}
		if !providedAPIKey && (!hasAPIKey || !hasAPISecret) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api_key and api_secret are required for initial Alpaca configuration"})
			return
		}

		if errors.Is(err, store.ErrNotFound) {
			connection = store.BrokerConnection{
				ProviderCode:  "ALPACA",
				ConnectionKey: "primary",
				Name:          "Alpaca Paper",
				Environment:   "paper",
				AuthType:      "api_key",
				Enabled:       true,
				Status:        store.BrokerConnectionStatusDisconnected,
			}
		}
		connection.Config = map[string]any{"base_url": req.BaseURL}
		if providedAPIKey {
			connection.Secrets = map[string]string{"api_key": req.APIKey, "api_secret": req.APISecret}
		} else {
			connection.Secrets = nil
		}
		connection, err = st.UpsertBrokerConnection(c.Request.Context(), connection)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if !notifyBrokersChanged(c, onChanged) {
			return
		}
		c.JSON(http.StatusOK, newAlpacaConfigResponse(connection))
	}
}

func loadPrimaryAlpacaConnection(c *gin.Context, st brokerStore) (store.BrokerConnection, error) {
	connections, err := st.ListBrokerConnections(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return store.BrokerConnection{}, err
	}
	var fallback store.BrokerConnection
	for _, connection := range connections {
		if connection.ProviderCode != "ALPACA" {
			continue
		}
		if fallback.ID == 0 {
			fallback = connection
		}
		if connection.Enabled {
			return connection, nil
		}
	}
	if fallback.ID != 0 {
		return fallback, nil
	}
	return store.BrokerConnection{}, store.ErrNotFound
}

func newAlpacaConfigResponse(connection store.BrokerConnection) alpacaConfigResponse {
	response := alpacaConfigResponse{
		BaseURL:             strings.TrimSpace(stringConfigValue(connection.Config, "base_url")),
		APIKeyConfigured:    configuredSecret(connection.ConfiguredSecretKeys, "api_key"),
		APISecretConfigured: configuredSecret(connection.ConfiguredSecretKeys, "api_secret"),
		ConnectionID:        connection.ID,
		ConnectionEnabled:   connection.Enabled,
	}
	if response.BaseURL == "" {
		response.BaseURL = "https://paper-api.alpaca.markets"
	}
	response.Available = response.APIKeyConfigured && response.APISecretConfigured && response.ConnectionID != 0 && response.ConnectionEnabled
	return response
}

func validateBrokerAPIBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("base_url must be an HTTP(S) URL without credentials, query, or fragment")
	}
	return nil
}

func alpacaStatus(resolve brokerSessionResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		session, release := resolve()
		defer release()
		client, ok := session.(alpacaStatusSession)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"available": false, "configured": false})
			return
		}
		configured := client.Configured()
		status := gin.H{
			"available":  true,
			"configured": configured,
		}
		if !configured {
			c.JSON(http.StatusOK, status)
			return
		}
		login, err := client.LoginStatus(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "configured": true})
			return
		}
		summary, err := client.AccountSummary(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "configured": true})
			return
		}
		status["authenticated"] = login.Authenticated
		status["account_id"] = summary.AccountID
		status["equity"] = summary.NetLiquidation
		status["currency"] = summary.Currency
		c.JSON(http.StatusOK, status)
	}
}
