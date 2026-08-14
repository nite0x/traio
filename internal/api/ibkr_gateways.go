package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

type ibkrGatewayRuntime interface {
	IBKRGatewayStatus(int64) (any, error)
	IBKRGatewayLoginURL(int64) (string, error)
	StartIBKRGateway(context.Context, int64) error
	StopIBKRGateway(int64, bool) error
	ReconnectIBKRGateway(int64) error
	UpgradeIBKRGateway(context.Context, int64) error
	RollbackIBKRGateway(context.Context, int64) error
}

type ibkrGatewayRequest struct {
	GatewayKey  string `json:"gateway_key"`
	Name        string `json:"name"`
	GatewayURL  string `json:"gateway_url"`
	GatewayDir  string `json:"gateway_dir"`
	GatewayPort int    `json:"gateway_port"`
	Lifecycle   string `json:"lifecycle"`
	Enabled     *bool  `json:"enabled"`
}

func listIBKRGateways(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		gateways, err := st.ListIBKRGateways(c.Request.Context())
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		c.JSON(http.StatusOK, gateways)
	}
}

func ibkrGatewayDefaults(runtimeDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		gatewayKey := strings.TrimSpace(c.DefaultQuery("gateway_key", "local"))
		gatewayDir := config.DefaultIBKRGatewayDir(runtimeDir, gatewayKey)
		if gatewayDir == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "gateway_key must be a path-safe name"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"runtime_dir":  runtimeDir,
			"gateway_root": config.DefaultIBKRGatewayRoot(runtimeDir),
			"gateway_dir":  gatewayDir,
			"lifecycle":    config.ResolveIBKRGatewayLifecycle(),
		})
	}
}

func getIBKRGateway(st brokerStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		gatewayID, ok := parseGatewayID(c)
		if !ok {
			return
		}
		gateway, err := st.GetIBKRGateway(c.Request.Context(), gatewayID)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		c.JSON(http.StatusOK, gateway)
	}
}

func createIBKRGateway(st brokerStore, onChanged func(context.Context) error, runtimeDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		var req ibkrGatewayRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		if strings.TrimSpace(req.GatewayDir) == "" {
			req.GatewayDir = config.DefaultIBKRGatewayDir(runtimeDir, req.GatewayKey)
			if req.GatewayDir == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "gateway_key must be a path-safe name when gateway_dir is omitted"})
				return
			}
		}
		if strings.TrimSpace(req.Lifecycle) == "" {
			req.Lifecycle = config.ResolveIBKRGatewayLifecycle()
		}
		gateway, err := st.UpsertIBKRGateway(c.Request.Context(), store.IBKRGateway{
			GatewayKey: req.GatewayKey, Name: req.Name, GatewayURL: req.GatewayURL,
			GatewayDir: req.GatewayDir, GatewayPort: req.GatewayPort,
			Lifecycle: req.Lifecycle, Enabled: enabled,
		})
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if !notifyBrokersChanged(c, onChanged) {
			return
		}
		c.JSON(http.StatusCreated, gateway)
	}
}

func updateIBKRGateway(st brokerStore, onChanged func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		gatewayID, ok := parseGatewayID(c)
		if !ok {
			return
		}
		existing, err := st.GetIBKRGateway(c.Request.Context(), gatewayID)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		var req ibkrGatewayRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		enabled := existing.Enabled
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		if strings.TrimSpace(req.GatewayDir) == "" {
			req.GatewayDir = existing.GatewayDir
		}
		if strings.TrimSpace(req.Lifecycle) == "" {
			req.Lifecycle = existing.Lifecycle
		}
		gateway, err := st.UpsertIBKRGateway(c.Request.Context(), store.IBKRGateway{
			GatewayKey: existing.GatewayKey, Name: req.Name, GatewayURL: req.GatewayURL,
			GatewayDir: req.GatewayDir, GatewayPort: req.GatewayPort,
			Lifecycle: req.Lifecycle, Enabled: enabled,
		})
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if !notifyBrokersChanged(c, onChanged) {
			return
		}
		c.JSON(http.StatusOK, gateway)
	}
}

func deleteIBKRGateway(st brokerStore, runtime ibkrGatewayRuntime, onChanged func(context.Context) error, runtimeDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		gatewayID, ok := parseGatewayID(c)
		if !ok {
			return
		}
		gateway, err := st.GetIBKRGateway(c.Request.Context(), gatewayID)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		deleteFiles, _ := strconv.ParseBool(c.Query("delete_files"))
		if deleteFiles {
			if err := validateManagedGatewayDeletePath(runtimeDir, gateway); err != nil {
				c.JSON(http.StatusConflict, gin.H{
					"error":   "gateway_directory_is_not_managed_default",
					"details": err.Error(),
				})
				return
			}
		}
		if gateway.Enabled && runtime != nil {
			if err := runtime.StopIBKRGateway(gatewayID, false); err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
				return
			}
		}
		if deleteFiles {
			if err := removeManagedGatewayFiles(runtimeDir, gateway); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "remove Gateway files", "details": err.Error()})
				return
			}
		}
		if err := st.DeleteIBKRGateway(c.Request.Context(), gatewayID); err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if !notifyBrokersChanged(c, onChanged) {
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func validateManagedGatewayDeletePath(runtimeDir string, gateway store.IBKRGateway) error {
	runtimeDir = strings.TrimSpace(runtimeDir)
	if runtimeDir == "" {
		return fmt.Errorf("runtime directory is unavailable")
	}
	expected := config.DefaultIBKRGatewayDir(runtimeDir, gateway.GatewayKey)
	if expected == "" {
		return fmt.Errorf("gateway key %q cannot produce a managed directory", gateway.GatewayKey)
	}
	expected, err := filepath.Abs(expected)
	if err != nil {
		return fmt.Errorf("resolve managed directory: %w", err)
	}
	actual, err := filepath.Abs(strings.TrimSpace(gateway.GatewayDir))
	if err != nil {
		return fmt.Errorf("resolve configured directory: %w", err)
	}
	if filepath.Clean(actual) != filepath.Clean(expected) {
		return fmt.Errorf("custom directory %q must be removed manually", gateway.GatewayDir)
	}
	return nil
}

func removeManagedGatewayFiles(runtimeDir string, gateway store.IBKRGateway) error {
	if err := validateManagedGatewayDeletePath(runtimeDir, gateway); err != nil {
		return err
	}
	dir, err := filepath.Abs(config.DefaultIBKRGatewayDir(runtimeDir, gateway.GatewayKey))
	if err != nil {
		return err
	}
	for _, target := range []string{
		dir,
		dir + ".rollback",
		dir + ".manager.lock",
		dir + ".audit.jsonl",
		dir + ".audit.jsonl.1",
	} {
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("remove %s: %w", target, err)
		}
	}
	return nil
}

func ibkrManagedGatewayStatus(runtime ibkrGatewayRuntime) gin.HandlerFunc {
	return gatewayRuntimeHandler(runtime, func(c *gin.Context, id int64) (any, error) {
		return runtime.IBKRGatewayStatus(id)
	})
}

func ibkrManagedGatewayLogin(runtime ibkrGatewayRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "IBKR gateway runtime unavailable"})
			return
		}
		id, ok := parseGatewayID(c)
		if !ok {
			return
		}
		loginURL, err := runtime.IBKRGatewayLoginURL(id)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.Redirect(http.StatusFound, loginURL)
	}
}

func ibkrManagedGatewayStart(runtime ibkrGatewayRuntime) gin.HandlerFunc {
	return gatewayRuntimeHandler(runtime, func(c *gin.Context, id int64) (any, error) {
		return gin.H{"status": "started"}, runtime.StartIBKRGateway(c.Request.Context(), id)
	})
}

func ibkrManagedGatewayStop(runtime ibkrGatewayRuntime) gin.HandlerFunc {
	return gatewayRuntimeHandler(runtime, func(c *gin.Context, id int64) (any, error) {
		keepSession, _ := strconv.ParseBool(c.Query("keep_session"))
		return gin.H{"status": "stopped", "session_preserved": keepSession}, runtime.StopIBKRGateway(id, keepSession)
	})
}

func ibkrManagedGatewayReconnect(runtime ibkrGatewayRuntime) gin.HandlerFunc {
	return gatewayRuntimeHandler(runtime, func(_ *gin.Context, id int64) (any, error) {
		return gin.H{"status": "reconnected"}, runtime.ReconnectIBKRGateway(id)
	})
}

func ibkrManagedGatewayUpgrade(runtime ibkrGatewayRuntime) gin.HandlerFunc {
	return gatewayRuntimeHandler(runtime, func(c *gin.Context, id int64) (any, error) {
		return gin.H{"status": "upgraded"}, runtime.UpgradeIBKRGateway(c.Request.Context(), id)
	})
}

func ibkrManagedGatewayRollback(runtime ibkrGatewayRuntime) gin.HandlerFunc {
	return gatewayRuntimeHandler(runtime, func(c *gin.Context, id int64) (any, error) {
		return gin.H{"status": "rolled_back"}, runtime.RollbackIBKRGateway(c.Request.Context(), id)
	})
}

func gatewayRuntimeHandler(runtime ibkrGatewayRuntime, operation func(*gin.Context, int64) (any, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "IBKR gateway runtime unavailable"})
			return
		}
		id, ok := parseGatewayID(c)
		if !ok {
			return
		}
		result, err := operation(c, id)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func parseGatewayID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("gateway_id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid gateway_id"})
		return 0, false
	}
	return id, true
}
