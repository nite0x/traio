package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/broker/ibkr"
	historysvc "github.com/nite/traio/internal/history"
	"github.com/nite/traio/internal/store"
)

type historyAPI interface {
	ListAccounts(context.Context) ([]store.HistoryAccount, error)
	List(context.Context, store.HistoryQuery) (store.HistoryPage, error)
	Get(context.Context, string) (activity.Activity, error)
	Revisions(context.Context, string) ([]activity.Activity, error)
	Coverage(context.Context) (historysvc.Coverage, error)
	Issues(context.Context) ([]store.HistoryIssue, error)
	Enqueue(context.Context, store.HistoryRequest) (store.HistoryJob, error)
	GetJob(context.Context, string) (store.HistoryJob, error)
	PreviewImport(context.Context, []byte, int64) (store.HistoryImport, error)
	GetImport(context.Context, string, int64) (store.HistoryImport, error)
	CommitImport(context.Context, string, int64) (store.HistoryJob, error)
	ResolveIssue(context.Context, string, string, string, int64) error
}

func requireHistory(c *gin.Context, svc historyAPI) bool {
	if svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "history unavailable"})
		return false
	}
	principal, ok := currentPrincipal(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return false
	}
	if principal.WorkspaceID != store.DefaultWorkspaceID {
		c.JSON(http.StatusForbidden, gin.H{"error": "workspace is not available"})
		return false
	}
	return true
}

func historyUserID(c *gin.Context) int64 {
	principal, _ := currentPrincipal(c)
	return principal.UserID
}

func listHistoryAccounts(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		items, err := svc.ListAccounts(c.Request.Context())
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	}
}

func listTransactions(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		accountIDs, err := store.ParseHistoryIDs(strings.TrimSpace(c.Query("account_ids")))
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		limit := 50
		if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil || limit <= 0 || limit > 200 {
				writeHistoryError(c, historysvc.ErrInvalidRequest)
				return
			}
		}
		if raw := strings.TrimSpace(c.Query("instrument_id")); raw != "" {
			if id, parseErr := strconv.ParseInt(raw, 10, 64); parseErr != nil || id <= 0 {
				writeHistoryError(c, historysvc.ErrInvalidRequest)
				return
			}
		}
		q := store.HistoryQuery{AccountIDs: accountIDs, Provider: strings.TrimSpace(c.Query("provider")), InstrumentID: strings.TrimSpace(c.Query("instrument_id")), Types: strings.TrimSpace(c.Query("types")), From: strings.TrimSpace(c.Query("from")), To: strings.TrimSpace(c.Query("to")), Status: strings.TrimSpace(c.Query("status")), Currency: strings.TrimSpace(c.Query("currency")), Cursor: strings.TrimSpace(c.Query("cursor")), Limit: limit}
		q.Symbol = store.NormalizeInstrumentSymbol(c.Query("symbol"))
		page, err := svc.List(c.Request.Context(), q)
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, page)
	}
}

func getTransaction(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		if !validHistoryID(c.Param("id")) {
			writeHistoryError(c, historysvc.ErrInvalidRequest)
			return
		}
		item, err := svc.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, item)
	}
}

func getTransactionRevisions(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		if !validHistoryID(c.Param("id")) {
			writeHistoryError(c, historysvc.ErrInvalidRequest)
			return
		}
		items, err := svc.Revisions(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"items": items})
	}
}

func getHistoryCoverage(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		coverage, err := svc.Coverage(c.Request.Context())
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, coverage)
	}
}

func listHistoryIssues(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		items, err := svc.Issues(c.Request.Context())
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"items": items})
	}
}

func syncHistory(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		var req store.HistoryRequest
		if err := decodeHistoryJSON(c, &req); err != nil {
			writeHistoryError(c, historysvc.ErrInvalidRequest)
			return
		}
		job, err := svc.Enqueue(c.Request.Context(), req)
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"job_id": job.ID, "status": job.Status})
	}
}

func getHistoryJob(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		if !validHistoryID(c.Param("id")) {
			writeHistoryError(c, historysvc.ErrInvalidRequest)
			return
		}
		job, err := svc.GetJob(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, job)
	}
}

func createHistoryImport(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, ibkr.MaxActivityXMLBytes+(1<<20))
		if err := c.Request.ParseMultipartForm(ibkr.MaxActivityXMLBytes); err != nil {
			writeHistoryError(c, errors.New("import_invalid"))
			return
		}
		if c.Request.MultipartForm != nil {
			defer c.Request.MultipartForm.RemoveAll()
		}
		file, _, err := c.Request.FormFile("file")
		if err != nil {
			writeHistoryError(c, errors.New("import_invalid"))
			return
		}
		defer file.Close()
		body, err := io.ReadAll(io.LimitReader(file, ibkr.MaxActivityXMLBytes+1))
		if err != nil || len(body) > ibkr.MaxActivityXMLBytes {
			writeHistoryError(c, errors.New("import_invalid"))
			return
		}
		preview, err := svc.PreviewImport(c.Request.Context(), body, historyUserID(c))
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusCreated, preview)
	}
}

func getHistoryImport(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		if !validHistoryID(c.Param("id")) {
			writeHistoryError(c, historysvc.ErrInvalidRequest)
			return
		}
		preview, err := svc.GetImport(c.Request.Context(), c.Param("id"), historyUserID(c))
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, preview)
	}
}

func commitHistoryImport(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		if !validHistoryID(c.Param("id")) {
			writeHistoryError(c, historysvc.ErrInvalidRequest)
			return
		}
		job, err := svc.CommitImport(c.Request.Context(), c.Param("id"), historyUserID(c))
		if err != nil {
			writeHistoryError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"job_id": job.ID, "status": job.Status})
	}
}

type resolveHistoryIssueRequest struct {
	Action           string `json:"action"`
	TargetActivityID string `json:"target_activity_id"`
}

func resolveHistoryIssue(svc historyAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireHistory(c, svc) {
			return
		}
		if !validHistoryID(c.Param("id")) {
			writeHistoryError(c, historysvc.ErrInvalidRequest)
			return
		}
		var req resolveHistoryIssueRequest
		if err := decodeHistoryJSON(c, &req); err != nil {
			writeHistoryError(c, historysvc.ErrInvalidRequest)
			return
		}
		if err := svc.ResolveIssue(c.Request.Context(), c.Param("id"), strings.TrimSpace(req.Action), strings.TrimSpace(req.TargetActivityID), historyUserID(c)); err != nil {
			writeHistoryError(c, err)
			return
		}
		status := "resolved"
		if req.Action == "keep_pending" {
			status = "open"
		}
		c.JSON(http.StatusOK, gin.H{"status": status})
	}
}

func validHistoryID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

func decodeHistoryJSON(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid JSON body")
	}
	return nil
}

func writeHistoryError(c *gin.Context, err error) {
	status, code := http.StatusInternalServerError, "history_request_failed"
	switch {
	case errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "history_not_found"
	case errors.Is(err, store.ErrForbidden):
		status, code = http.StatusForbidden, "history_forbidden"
	case errors.Is(err, store.ErrHistoryCursorStale):
		status, code = http.StatusConflict, "history_cursor_stale"
	case errors.Is(err, historysvc.ErrUnknownAccount):
		status, code = http.StatusBadRequest, "history_account_not_found"
	case errors.Is(err, historysvc.ErrAccountNotConnected):
		status, code = http.StatusBadRequest, "history_account_not_connected"
	case errors.Is(err, historysvc.ErrInvalidRequest):
		status, code = http.StatusBadRequest, "invalid_history_request"
	case errors.Is(err, historysvc.ErrConnectionRequired):
		status, code = http.StatusBadRequest, "history_connection_required"
	case errors.Is(err, historysvc.ErrSourceNotConfigured):
		status, code = http.StatusConflict, "source_not_configured"
	default:
		message := strings.ToLower(err.Error())
		switch {
		case strings.Contains(message, "history_not_supported"):
			status, code = http.StatusConflict, "history_not_supported"
		case strings.Contains(message, "source_not_configured"):
			status, code = http.StatusConflict, "source_not_configured"
		case strings.Contains(message, "import_invalid"):
			status, code = http.StatusBadRequest, "import_invalid"
		case strings.Contains(message, "import_expired"):
			status, code = http.StatusGone, "import_expired"
		case strings.Contains(message, "import_needs_action"):
			status, code = http.StatusConflict, "import_needs_action"
		case strings.Contains(message, "mapping_conflict"):
			status, code = http.StatusConflict, "mapping_conflict"
		case strings.Contains(message, "resolution_requires_evidence"):
			status, code = http.StatusBadRequest, "resolution_requires_evidence"
		case strings.Contains(message, "invalid_transfer_pair"):
			status, code = http.StatusBadRequest, "invalid_transfer_pair"
		case strings.Contains(message, "transfer_effect_mismatch"):
			status, code = http.StatusConflict, "transfer_effect_mismatch"
		case strings.Contains(message, "issue_already_resolved"), strings.Contains(message, "issue_revision_stale"), strings.Contains(message, "resolution_stale"):
			status, code = http.StatusConflict, "issue_revision_stale"
		case strings.Contains(message, "duplicate_effect_mismatch"):
			status, code = http.StatusConflict, "duplicate_effect_mismatch"
		case strings.Contains(message, "invalid_duplicate_target"):
			status, code = http.StatusBadRequest, "invalid_duplicate_target"
		case strings.Contains(message, "invalid_history"):
			status, code = http.StatusBadRequest, "invalid_history_request"
		}
	}
	c.JSON(status, gin.H{"error": code})
}

type historyConfigRequest struct {
	ActivityHistoryEnabled *bool   `json:"activity_history_enabled"`
	ActivityHistoryFrom    *string `json:"activity_history_from"`
	FlexQueryID            *string `json:"flex_query_id"`
	FlexActivityQueryID    *string `json:"flex_activity_query_id"`
	FlexToken              *string `json:"flex_token"`
}

func updateHistoryConfig(st brokerStore, onChanged func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if st == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker store unavailable"})
			return
		}
		if principal, ok := currentPrincipal(c); !ok || principal.WorkspaceID != store.DefaultWorkspaceID {
			c.JSON(http.StatusForbidden, gin.H{"error": "workspace is not available"})
			return
		}
		connectionID, ok := parseConnectionID(c)
		if !ok {
			return
		}
		connection, err := st.GetBrokerConnectionRuntimeConfig(c.Request.Context(), connectionID)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if !strings.EqualFold(connection.ProviderCode, "IBKR") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "history_not_supported"})
			return
		}
		var req historyConfigRequest
		if err := decodeHistoryJSON(c, &req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_history_config"})
			return
		}
		if req.ActivityHistoryEnabled == nil && req.ActivityHistoryFrom == nil && req.FlexActivityQueryID == nil && req.FlexQueryID == nil && req.FlexToken == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_history_config"})
			return
		}
		if req.ActivityHistoryFrom != nil {
			value := strings.TrimSpace(*req.ActivityHistoryFrom)
			if err := store.ValidateHistoryDates(value, ""); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_history_date"})
				return
			}
			connection.Config["activity_history_from"] = value
		}
		if req.ActivityHistoryEnabled != nil {
			connection.Config["activity_history_enabled"] = *req.ActivityHistoryEnabled
		}
		if req.FlexActivityQueryID != nil {
			value := strings.TrimSpace(*req.FlexActivityQueryID)
			if value != "" {
				if err := ibkr.ValidateFlexQueryID(value); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_flex_activity_query_id"})
					return
				}
			}
			connection.Config["flex_activity_query_id"] = value
		}
		if req.FlexQueryID != nil {
			value := strings.TrimSpace(*req.FlexQueryID)
			if value != "" {
				if err := ibkr.ValidateFlexQueryID(value); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_flex_query_id"})
					return
				}
			}
			connection.Config["flex_query_id"] = value
		}
		if req.FlexToken != nil {
			connection.Secrets["flex_token"] = strings.TrimSpace(*req.FlexToken)
		}
		for _, key := range []string{"flex_activity_query_id", "flex_query_id"} {
			value, _ := connection.Config[key].(string)
			if value != "" && value == strings.TrimSpace(connection.Secrets["flex_token"]) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_" + key})
				return
			}
		}
		updated, err := st.UpsertBrokerConnection(c.Request.Context(), connection)
		if err != nil {
			writeBrokerStoreError(c, err)
			return
		}
		if !notifyBrokersChanged(c, onChanged) {
			return
		}
		if retry, ok := st.(interface {
			RetryHistoryConnectionFetches(context.Context, int64) error
		}); ok {
			if err := retry.RetryHistoryConnectionFetches(c.Request.Context(), connectionID); err != nil {
				writeBrokerStoreError(c, err)
				return
			}
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, updated)
	}
}
