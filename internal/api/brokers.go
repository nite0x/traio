package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	traioauth "github.com/nite/traio/internal/auth"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/store"
)

// brokerStore is intentionally narrower than store.Repository while retaining
// context.Context in its public contract.
type brokerStore interface {
	store.BrokerCatalogRepository
	store.IBKRGatewayRepository
	ListBrokerAccounts(ctx context.Context) ([]store.BrokerAccount, error)
	ListBrokerAccountsByConnection(ctx context.Context, connectionID int64) ([]store.BrokerAccount, error)
}

type brokerConnectionRuntime interface {
	BeginConnectionLogin(context.Context, int64, string) (broker.LoginAction, error)
	ConnectionLoginStatus(context.Context, int64) (broker.LoginAction, error)
	ExchangeConnectionOAuthCode(context.Context, int64, string) error
	IBKRGatewayTarget(context.Context, int64) (*url.URL, bool, error)
}

// defaultBrokerSessionAcquirer is the provider-neutral bridge used by legacy
// provider-specific endpoints. Each consumer asserts only the operations it
// needs and releases the lease after finishing the operation.
type defaultBrokerSessionAcquirer interface {
	AcquireDefaultSession(providerCode string) (broker.BrokerSession, func())
}

type brokerSessionResolver func() (broker.BrokerSession, func())

func currentBrokerSession(runtime any, providerCode string) brokerSessionResolver {
	return func() (broker.BrokerSession, func()) {
		resolver, ok := runtime.(defaultBrokerSessionAcquirer)
		if !ok {
			return nil, func() {}
		}
		session, release := resolver.AcquireDefaultSession(providerCode)
		if release == nil {
			release = func() {}
		}
		return session, release
	}
}

type brokerConnectionSyncer interface {
	SyncConnection(context.Context, int64) error
}

type providerConfigRequest struct {
	Config  map[string]any    `json:"config"`
	Secrets map[string]string `json:"secrets"`
}

func listBrokerProviders(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		providers, err := st.ListBrokerProviders(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, providers)
	}
}

func updateBrokerProvider(st brokerStore, onChanged func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		var req providerConfigRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		provider, err := st.UpdateBrokerProviderConfig(c.Request.Context(), c.Param("code"), req.Config, req.Secrets)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if !notifyBrokersChanged(c, onChanged) {
			return
		}
		c.JSON(http.StatusOK, provider)
	}
}

func listBrokerConnections(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		connections, err := st.ListBrokerConnections(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		code := strings.ToUpper(strings.TrimSpace(c.Query("provider")))
		if code == "" {
			c.JSON(http.StatusOK, connections)
			return
		}
		filtered := make([]store.BrokerConnection, 0, len(connections))
		for _, connection := range connections {
			if connection.ProviderCode == code {
				filtered = append(filtered, connection)
			}
		}
		c.JSON(http.StatusOK, filtered)
	}
}

func getBrokerConnection(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		connectionID, ok := parseConnectionID(c)
		if !ok {
			return
		}
		connection, err := st.GetBrokerConnection(c.Request.Context(), connectionID)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		c.JSON(http.StatusOK, connection)
	}
}

type brokerConnectionRequest struct {
	ConnectionKey  string            `json:"connection_key"`
	Name           string            `json:"name"`
	ProviderUserID string            `json:"provider_user_id"`
	Username       string            `json:"username"`
	Environment    string            `json:"environment"`
	AuthType       string            `json:"auth_type"`
	Config         map[string]any    `json:"config"`
	Secrets        map[string]string `json:"secrets"`
	Enabled        *bool             `json:"enabled"`
}

func createBrokerConnection(st brokerStore, onChanged func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		var req brokerConnectionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		connection, err := st.UpsertBrokerConnection(c.Request.Context(), store.BrokerConnection{
			ProviderCode: c.Param("code"), ConnectionKey: req.ConnectionKey,
			Name: req.Name, ProviderUserID: req.ProviderUserID, Username: req.Username,
			Environment: req.Environment, AuthType: req.AuthType, Config: req.Config,
			Secrets: req.Secrets, Enabled: enabled,
		})
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if !notifyBrokersChanged(c, onChanged) {
			return
		}
		c.JSON(http.StatusCreated, connection)
	}
}

func updateBrokerConnection(st brokerStore, onChanged func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		connectionID, ok := parseConnectionID(c)
		if !ok {
			return
		}
		existing, err := st.GetBrokerConnection(c.Request.Context(), connectionID)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		var req brokerConnectionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// connection_key is the stable identity within a provider and cannot be
		// changed through an ID-addressed update.
		req.ConnectionKey = existing.ConnectionKey
		enabled := existing.Enabled
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		connection, err := st.UpsertBrokerConnection(c.Request.Context(), store.BrokerConnection{
			ProviderCode: existing.ProviderCode, ConnectionKey: req.ConnectionKey,
			Name: req.Name, ProviderUserID: req.ProviderUserID, Username: req.Username,
			Environment: req.Environment, AuthType: req.AuthType, Config: req.Config,
			Secrets: req.Secrets, Enabled: enabled,
		})
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if !notifyBrokersChanged(c, onChanged) {
			return
		}
		c.JSON(http.StatusOK, connection)
	}
}

func brokerConnectionDeleteImpact(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		connectionID, ok := parseConnectionID(c)
		if !ok {
			return
		}
		impact, err := st.GetBrokerConnectionDeleteImpact(c.Request.Context(), connectionID)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		c.JSON(http.StatusOK, impact)
	}
}

func deleteBrokerConnection(st brokerStore, onChanged func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		connectionID, ok := parseConnectionID(c)
		if !ok {
			return
		}
		impact, err := st.GetBrokerConnectionDeleteImpact(c.Request.Context(), connectionID)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		confirmed, _ := strconv.ParseBool(c.Query("confirm"))
		if !confirmed && (len(impact.Shared) > 0 || len(impact.Orphaned) > 0) {
			c.JSON(http.StatusConflict, gin.H{
				"error":  "connection_has_accounts",
				"impact": impact,
			})
			return
		}
		if err := st.DeleteBrokerConnection(c.Request.Context(), connectionID); err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if !notifyBrokersChanged(c, onChanged) {
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func listBrokerAccounts(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		accounts, err := st.ListBrokerAccounts(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, accounts)
	}
}

func listBrokerConnectionAccounts(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		connectionID, ok := parseConnectionID(c)
		if !ok {
			return
		}
		if _, err := st.GetBrokerConnection(c.Request.Context(), connectionID); err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		accounts, err := st.ListBrokerAccountsByConnection(c.Request.Context(), connectionID)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		c.JSON(http.StatusOK, accounts)
	}
}

func syncBrokerConnection(st brokerStore, syncer brokerConnectionSyncer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil || syncer == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker sync unavailable"})
			return
		}
		connectionID, ok := parseConnectionID(c)
		if !ok {
			return
		}
		if _, err := st.GetBrokerConnection(c.Request.Context(), connectionID); err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if err := syncer.SyncConnection(c.Request.Context(), connectionID); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "synced", "connection_id": connectionID})
	}
}

type connectionLoginRequest struct {
	State string `json:"state"`
}

func beginBrokerConnectionLogin(catalog brokerStore, runtime brokerConnectionRuntime, proxy *IBKRLoginProxy, authService *traioauth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker runtime unavailable"})
			return
		}
		connectionID, ok := parseConnectionID(c)
		if !ok {
			return
		}
		var req connectionLoginRequest
		if c.Request.ContentLength > 0 {
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		if authService != nil && authService.UsesSessions() && catalog != nil {
			connection, err := catalog.GetBrokerConnection(c.Request.Context(), connectionID)
			if err != nil {
				writeBrokerStoreError(c, err)
				return
			}
			if connection.ProviderCode == "SCHWAB" {
				principal, _ := currentPrincipal(c)
				req.State, err = authService.BeginBrokerOAuth(c.Request.Context(), principal, connectionID)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "create broker OAuth state"})
					return
				}
			}
		}
		action, err := runtime.BeginConnectionLogin(c.Request.Context(), connectionID, req.State)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if proxy != nil && action.URL != "" {
			target, isIBKR, targetErr := runtime.IBKRGatewayTarget(c.Request.Context(), connectionID)
			if targetErr != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": targetErr.Error()})
				return
			}
			// The local login proxy is intentionally restricted to loopback
			// Gateways. Remote endpoints keep their own login URL.
			if isIBKR && validateIBKRGatewayTarget(target) == nil {
				principal, _ := currentPrincipal(c)
				action.URL, err = proxy.IssueLoginURLForPrincipal(connectionID, principal.WorkspaceID, principal.UserID)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
			}
		}
		c.JSON(http.StatusOK, action)
	}
}

func brokerConnectionLoginStatus(runtime brokerConnectionRuntime, proxy *IBKRLoginProxy) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker runtime unavailable"})
			return
		}
		connectionID, ok := parseConnectionID(c)
		if !ok {
			return
		}
		action, err := runtime.ConnectionLoginStatus(c.Request.Context(), connectionID)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		// Polling status must never disclose the private Gateway URL.
		action.URL = ""
		if action.Authenticated && proxy != nil {
			proxy.RevokeConnection(connectionID)
		}
		c.JSON(http.StatusOK, action)
	}
}

type connectionOAuthExchangeRequest struct {
	Code        string `json:"code"`
	CallbackURL string `json:"callback_url"`
}

func exchangeBrokerConnectionOAuthCode(runtime brokerConnectionRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker runtime unavailable"})
			return
		}
		connectionID, ok := parseConnectionID(c)
		if !ok {
			return
		}
		var req connectionOAuthExchangeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if strings.TrimSpace(req.Code) == "" && strings.TrimSpace(req.CallbackURL) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "code or callback_url is required"})
			return
		}
		code, err := (broker.AuthenticationCallback{Code: req.Code, CallbackURL: req.CallbackURL}).AuthorizationCode()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := runtime.ExchangeConnectionOAuthCode(c.Request.Context(), connectionID, code); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "authenticated"})
	}
}

func parseConnectionID(c *gin.Context) (int64, bool) {
	connectionID, err := strconv.ParseInt(c.Param("connection_id"), 10, 64)
	if err != nil || connectionID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid connection_id"})
		return 0, false
	}
	return connectionID, true
}

func writeBrokerStoreError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "broker resource not found"})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

func notifyBrokersChanged(c *gin.Context, onChanged func(context.Context) error) bool {
	if onChanged == nil {
		return true
	}
	if err := onChanged(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "broker configuration saved but runtime reload failed",
			"details": err.Error(),
		})
		return false
	}
	return true
}
