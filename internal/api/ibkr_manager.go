package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/broker"
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

func discoverIBKRConnections(ctx context.Context, st brokerStore, req *providerConfigRequest) ([]store.BrokerConnection, int, error) {
	managerURL := mapString(req.Config, ibkrManagerURLKey)
	current, err := st.GetBrokerProviderRuntimeConfig(ctx, ibkrProviderCode)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, http.StatusInternalServerError, err
	}
	token := current.Secrets[ibkrManagerTokenKey]
	if req.Secrets != nil {
		token = req.Secrets[ibkrManagerTokenKey]
	}
	client, err := ibkr.NewManagerClient(managerURL, token)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	oldOrigin := strings.TrimRight(mapString(current.Config, ibkrManagerURLKey), "/")
	if req.Secrets == nil && oldOrigin != "" && oldOrigin != client.Origin() {
		return nil, http.StatusBadRequest, fmt.Errorf("更换 Gateway Manager 地址时请重新填写 Manager API Token")
	}
	if strings.TrimSpace(token) == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("Manager API Token is required to import instance credentials")
	}
	if _, err := client.Health(ctx); err != nil {
		return nil, http.StatusBadGateway, err
	}
	gateways, err := client.Connections(ctx)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	connections := make([]store.BrokerConnection, 0, len(gateways))
	seen := map[string]bool{}
	for _, gateway := range gateways {
		id := strings.TrimSpace(gateway.ID)
		if id == "" || seen[id] {
			return nil, http.StatusBadGateway, fmt.Errorf("Manager returned an empty or duplicate instance ID")
		}
		seen[id] = true
		gatewayURL, err := normalizeGatewayOrigin(gateway.ProxyURL)
		if err != nil {
			return nil, http.StatusBadGateway, fmt.Errorf("IBKR Gateway %q proxy URL: %w", id, err)
		}
		if strings.TrimSpace(gateway.ProxyToken) == "" {
			return nil, http.StatusBadGateway, fmt.Errorf("IBKR Gateway %q has no proxy token", id)
		}
		connections = append(connections, store.BrokerConnection{
			ProviderCode: ibkrProviderCode, Name: "IBKR · " + id,
			Environment: "default", AuthType: "interactive", Enabled: true,
			Status: store.BrokerConnectionStatusDisconnected,
			Config: map[string]any{ibkrGatewayIDKey: id, ibkrGatewayURLKey: gatewayURL,
				ibkrManagerURLKey: client.Origin(), "gateway_auto_start": gateway.AutoStart},
			Secrets: map[string]string{ibkrGatewayTokenKey: strings.TrimSpace(gateway.ProxyToken)},
		})
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	req.Config[ibkrManagerURLKey] = client.Origin()
	return connections, 0, nil
}

func prepareIBKRConnection(ctx context.Context, st brokerStore, providerCode string, connectionID int64, req *brokerConnectionRequest) (int, error) {
	if !strings.EqualFold(strings.TrimSpace(providerCode), ibkrProviderCode) {
		return 0, nil
	}
	if ibkr.IsFlexConnection(req.Config) {
		if connectionID > 0 {
			current, err := st.GetBrokerConnectionRuntimeConfig(ctx, connectionID)
			if err != nil {
				return http.StatusBadRequest, err
			}
			merged := current.Secrets
			if merged == nil {
				merged = map[string]string{}
			}
			for key, value := range req.Secrets {
				merged[key] = value
			}
			req.Secrets = merged
		}
		session, err := ibkr.NewFactory().Open(ctx, broker.ConnectionConfig{Config: req.Config, Secrets: req.Secrets})
		if err != nil {
			return http.StatusBadRequest, err
		}
		_ = session.Close(ctx)
		req.AuthType = "api_key"
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
