package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/broker/ibkr"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

const (
	ibkrProviderCode    = "IBKR"
	ibkrManagerURLKey   = "manager_url"
	ibkrManagerTokenKey = "manager_api_token"
	ibkrGatewayIDKey    = "gateway_id"
	ibkrGatewayURLKey   = "gateway_url"
	ibkrGatewayTokenKey = "gateway_token"
)

func validateIBKRProviderUpdate(ctx context.Context, st brokerStore, providerCode string, req providerConfigRequest) (int, error) {
	if !strings.EqualFold(strings.TrimSpace(providerCode), ibkrProviderCode) {
		return 0, nil
	}
	managerURL := mapString(req.Config, ibkrManagerURLKey)
	token := ""
	if req.Secrets == nil {
		current, err := st.GetBrokerProviderRuntimeConfig(ctx, ibkrProviderCode)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return http.StatusInternalServerError, err
		}
		token = current.Secrets[ibkrManagerTokenKey]
	} else {
		token = req.Secrets[ibkrManagerTokenKey]
	}
	client, err := ibkr.NewManagerClient(managerURL, token)
	if err != nil {
		return http.StatusBadRequest, err
	}
	if _, err := client.Health(ctx); err != nil {
		return http.StatusBadGateway, err
	}
	if _, err := client.Gateways(ctx); err != nil {
		return http.StatusBadGateway, err
	}
	return 0, nil
}

func prepareIBKRConnection(ctx context.Context, st brokerStore, providerCode string, connectionID int64, req *brokerConnectionRequest) (int, error) {
	if !strings.EqualFold(strings.TrimSpace(providerCode), ibkrProviderCode) {
		return 0, nil
	}
	gatewayID := mapString(req.Config, ibkrGatewayIDKey)
	if gatewayID == "" {
		return http.StatusBadRequest, fmt.Errorf("gateway_id is required")
	}
	client, err := ibkrManagerClient(ctx, st)
	if err != nil {
		return http.StatusBadRequest, err
	}
	gateways, err := client.Gateways(ctx)
	if err != nil {
		return http.StatusBadGateway, err
	}
	var selected *ibkr.ManagerGateway
	for i := range gateways {
		if gateways[i].ID == gatewayID {
			selected = &gateways[i]
			break
		}
	}
	if selected == nil {
		return http.StatusBadRequest, fmt.Errorf("IBKR Gateway Manager has no instance %q", gatewayID)
	}
	if strings.TrimSpace(selected.ProxyURL) == "" {
		return http.StatusBadRequest, fmt.Errorf("IBKR Gateway %q has no proxy_public_url", gatewayID)
	}
	gatewayURL, err := normalizeGatewayOrigin(selected.ProxyURL)
	if err != nil {
		return http.StatusBadGateway, fmt.Errorf("IBKR Gateway %q proxy URL: %w", gatewayID, err)
	}
	gatewayToken := strings.TrimSpace(req.Secrets[ibkrGatewayTokenKey])
	if gatewayToken == "" && connectionID > 0 {
		current, loadErr := st.GetBrokerConnectionRuntimeConfig(ctx, connectionID)
		if loadErr == nil {
			gatewayToken = strings.TrimSpace(current.Secrets[ibkrGatewayTokenKey])
		}
	}
	if selected.ProxyTokenConfigured && gatewayToken == "" {
		return http.StatusBadRequest, fmt.Errorf("gateway_token is required for IBKR Gateway %q", gatewayID)
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	req.Config[ibkrGatewayIDKey] = gatewayID
	req.Config[ibkrGatewayURLKey] = gatewayURL
	if selected.Status.Running && selected.ProxyListening {
		probe := ibkr.New(config.IBKRConfig{GatewayURL: gatewayURL, GatewayToken: gatewayToken})
		action, probeErr := probe.LoginStatus(ctx)
		if probeErr != nil {
			return http.StatusBadGateway, fmt.Errorf("validate IBKR Gateway %q: %w", gatewayID, probeErr)
		}
		if selected.Status.Authenticated && !action.Authenticated {
			return http.StatusBadGateway, fmt.Errorf("IBKR Gateway %q is authenticated in Manager, but its public proxy session is unavailable", gatewayID)
		}
		if action.Authenticated {
			req.Status = store.BrokerConnectionStatusConnected
			if strings.TrimSpace(req.ProviderUserID) == "" {
				req.ProviderUserID = strings.TrimSpace(action.AccountID)
			}
		}
	}
	return 0, nil
}

func ibkrManagerHealth(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		client, ok := requireIBKRManagerClient(c, st)
		if !ok {
			return
		}
		health, err := client.Health(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, health)
	}
}

func listIBKRManagerGateways(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		client, ok := requireIBKRManagerClient(c, st)
		if !ok {
			return
		}
		gateways, err := client.Gateways(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gateways)
	}
}

func getIBKRManagerGatewayStatus(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		client, ok := requireIBKRManagerClient(c, st)
		if !ok {
			return
		}
		status, err := client.GatewayStatus(c.Request.Context(), c.Param("gateway_id"))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, status)
	}
}

func requireIBKRManagerClient(c *gin.Context, st brokerStore) (*ibkr.ManagerClient, bool) {
	if st == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
		return nil, false
	}
	client, err := ibkrManagerClient(c.Request.Context(), st)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	return client, true
}

func ibkrManagerClient(ctx context.Context, st brokerStore) (*ibkr.ManagerClient, error) {
	provider, err := st.GetBrokerProviderRuntimeConfig(ctx, ibkrProviderCode)
	if err != nil {
		return nil, fmt.Errorf("load IBKR provider: %w", err)
	}
	return ibkr.NewManagerClient(mapString(provider.Config, ibkrManagerURLKey), provider.Secrets[ibkrManagerTokenKey])
}

func mapString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func normalizeGatewayOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("must be an HTTP(S) origin")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("must not contain credentials, path, query, or fragment")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
