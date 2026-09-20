package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/market"
	"github.com/nite/traio/internal/market/tiingo"
)

const tiingoTokenPurpose = "tiingo/api_token"

type tiingoConfigResponse struct {
	Scope           string `json:"scope"`
	BaseURL         string `json:"base_url"`
	Enabled         bool   `json:"enabled"`
	TokenConfigured bool   `json:"token_configured"`
	Available       bool   `json:"available"`
	LicenseMode     string `json:"license_mode"`
	LastVerifiedAt  string `json:"last_verified_at,omitempty"`
}

type saveTiingoConfigRequest struct {
	APIToken string `json:"api_token"`
	BaseURL  string `json:"base_url"`
	Enabled  *bool  `json:"enabled"`
	// LicenseMode describes public market-data response handling only. It is
	// independent from TRAIO_PERFORMANCE_RETENTION_MODE.
	LicenseMode string `json:"license_mode"`
}

func tiingoConfiguration(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		config, err := loadTiingoConfig(c.Request.Context(), st)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		c.JSON(http.StatusOK, config)
	}
}

func saveTiingoConfiguration(st brokerStore, cipher *market.SecretCipher, onChanged func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil || cipher == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Tiingo configuration is unavailable"})
			return
		}
		var request saveTiingoConfigRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid Tiingo configuration"})
			return
		}
		current, err := st.GetBrokerProviderRuntimeConfig(c.Request.Context(), "TIINGO")
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		baseURL := strings.TrimSpace(request.BaseURL)
		if baseURL == "" {
			baseURL = stringConfigValue(current.Config, "base_url")
		}
		baseURL, err = tiingo.NormalizeBaseURL(baseURL)
		if err != nil {
			writeMarketDataError(c, err)
			return
		}
		enabled := boolConfigValue(current.Config, "enabled", false)
		licenseMode := strings.ToLower(strings.TrimSpace(request.LicenseMode))
		if licenseMode == "" {
			licenseMode = stringConfigValue(current.Config, "license_mode")
		}
		if licenseMode == "" {
			licenseMode = "transient"
		}
		if licenseMode != "transient" {
			c.JSON(http.StatusConflict, gin.H{"error": "market_data_license_required"})
			return
		}
		if request.Enabled != nil {
			enabled = *request.Enabled
		}
		token := strings.TrimSpace(request.APIToken)
		hasToken := strings.TrimSpace(current.Secrets["api_token"]) != ""
		if request.Enabled == nil && token != "" && !hasToken {
			enabled = true
		}
		if enabled && token == "" && !hasToken {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api_token is required for initial Tiingo configuration"})
			return
		}
		tokenConfigured := hasToken || token != ""
		var secrets map[string]string
		if token != "" {
			encrypted, encryptErr := cipher.Encrypt(token, tiingoTokenPurpose)
			if encryptErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot protect Tiingo credential"})
				return
			}
			secrets = map[string]string{"api_token": encrypted}
		}
		if _, err := st.UpdateBrokerProviderConfig(c.Request.Context(), "TIINGO", map[string]any{
			"base_url": baseURL, "enabled": enabled, "license_mode": licenseMode,
			"last_verified_at": stringConfigValue(current.Config, "last_verified_at"),
		}, secrets); err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if err := st.InvalidatePerformanceSource(c.Request.Context(), "tiingo"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "tiingo_performance_invalidation_failed"})
			return
		}
		if onChanged != nil {
			if err := onChanged(c.Request.Context()); err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "Tiingo configuration was saved but could not be activated"})
				return
			}
		}
		c.JSON(http.StatusOK, tiingoConfigResponse{Scope: "deployment", BaseURL: baseURL, Enabled: enabled, TokenConfigured: tokenConfigured, Available: enabled && tokenConfigured, LicenseMode: licenseMode, LastVerifiedAt: stringConfigValue(current.Config, "last_verified_at")})
	}
}

func testTiingoConfiguration(st brokerStore, cipher *market.SecretCipher) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil || cipher == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "tiingo_unavailable"})
			return
		}
		var request saveTiingoConfigRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_tiingo_configuration"})
			return
		}
		current, err := st.GetBrokerProviderRuntimeConfig(c.Request.Context(), "TIINGO")
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		token := strings.TrimSpace(request.APIToken)
		usingStored := token == ""
		if usingStored {
			encrypted := strings.TrimSpace(current.Secrets["api_token"])
			if encrypted == "" {
				c.JSON(http.StatusBadRequest, gin.H{"connected": false, "status": "not_configured", "error_code": "tiingo_not_configured"})
				return
			}
			token, err = cipher.Decrypt(encrypted, tiingoTokenPurpose)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"connected": false, "status": "unavailable", "error_code": "tiingo_credential_unavailable"})
				return
			}
		}
		baseURL := strings.TrimSpace(request.BaseURL)
		if baseURL == "" {
			baseURL = stringConfigValue(current.Config, "base_url")
		}
		client, err := tiingo.New(tiingo.Config{APIToken: token, BaseURL: baseURL})
		if err == nil {
			_, err = client.GetQuote(c.Request.Context(), "AAPL")
		}
		if err != nil {
			code, _ := market.ErrorDetails(err)
			status := "unavailable"
			errorCode := "upstream_unavailable"
			httpStatus := http.StatusBadGateway
			switch code {
			case market.ErrorAuthentication:
				status, errorCode, httpStatus = "unauthorized", "authentication_failed", http.StatusUnauthorized
			case market.ErrorPermission:
				status, errorCode, httpStatus = "unauthorized", "market_data_permission_denied", http.StatusForbidden
			case market.ErrorRateLimited:
				status, errorCode, httpStatus = "rate_limited", "rate_limited", http.StatusTooManyRequests
			case market.ErrorInvalidRequest:
				status, errorCode, httpStatus = "unavailable", "invalid_configuration", http.StatusBadRequest
			}
			c.JSON(httpStatus, gin.H{"connected": false, "status": status, "error_code": errorCode})
			return
		}
		verifiedAt := time.Now().UTC().Format(time.RFC3339)
		if usingStored {
			current.Config["last_verified_at"] = verifiedAt
			if _, err = st.UpdateBrokerProviderConfig(c.Request.Context(), "TIINGO", current.Config, nil); err != nil {
				writeBrokerStoreError(c, err)
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"connected": true, "status": "connected", "checked_at": verifiedAt})
	}
}

func deleteTiingoConfiguration(st brokerStore, onChanged func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "tiingo_unavailable"})
			return
		}
		if _, err := st.UpdateBrokerProviderConfig(c.Request.Context(), "TIINGO", map[string]any{
			"base_url": tiingo.DefaultBaseURL, "enabled": false, "license_mode": "transient",
		}, map[string]string{}); err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if err := st.InvalidatePerformanceSource(c.Request.Context(), "tiingo"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "tiingo_performance_invalidation_failed"})
			return
		}
		if onChanged != nil {
			if err := onChanged(c.Request.Context()); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "tiingo_deactivation_failed"})
				return
			}
		}
		c.Status(http.StatusNoContent)
	}
}

func loadTiingoConfig(ctx context.Context, st brokerStore) (tiingoConfigResponse, error) {
	if st == nil {
		return tiingoConfigResponse{}, errors.New("broker store unavailable")
	}
	config, err := st.GetBrokerProviderRuntimeConfig(ctx, "TIINGO")
	if err != nil {
		return tiingoConfigResponse{}, err
	}
	baseURL, err := tiingo.NormalizeBaseURL(stringConfigValue(config.Config, "base_url"))
	if err != nil {
		return tiingoConfigResponse{}, err
	}
	configured := strings.TrimSpace(config.Secrets["api_token"]) != ""
	enabled := boolConfigValue(config.Config, "enabled", false)
	licenseMode := stringConfigValue(config.Config, "license_mode")
	if licenseMode == "" {
		licenseMode = "transient"
	}
	return tiingoConfigResponse{Scope: "deployment", BaseURL: baseURL, Enabled: enabled, TokenConfigured: configured, Available: enabled && configured, LicenseMode: licenseMode, LastVerifiedAt: stringConfigValue(config.Config, "last_verified_at")}, nil
}

func boolConfigValue(config map[string]any, key string, fallback bool) bool {
	value, ok := config[key].(bool)
	if !ok {
		return fallback
	}
	return value
}
