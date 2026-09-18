// Package history coordinates durable account activity imports and broker fetches.
// Reads are always served from the repository; broker traffic only happens in a
// claimed background job.
package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/ibkr"
	"github.com/nite/traio/internal/config"
	"github.com/nite/traio/internal/store"
)

const (
	SourceGateway = "gateway"
	SourceFlex    = "flex"
	SourceXML     = "xml"
)

var (
	ErrInvalidRequest      = errors.New("invalid_history_request")
	ErrUnknownAccount      = errors.New("history_account_not_found")
	ErrAccountNotConnected = errors.New("history_account_not_connected")
	ErrSourceNotConfigured = errors.New("source_not_configured")
	ErrConnectionRequired  = errors.New("history_connection_required")
)

type Repository interface {
	store.ActivityRepository
	ReplaceBrokerConnectionAccounts(context.Context, int64, []broker.Account) error
	GetBrokerConnection(context.Context, int64) (store.BrokerConnection, error)
	GetBrokerConnectionRuntimeConfig(context.Context, int64) (store.BrokerConnection, error)
	ListBrokerConnections(context.Context) ([]store.BrokerConnection, error)
	ListBrokerAccounts(context.Context) ([]store.BrokerAccount, error)
	ListBrokerAccountsByConnection(context.Context, int64) ([]store.BrokerAccount, error)
}

type Capability struct {
	Provider       string                 `json:"provider"`
	SupportedTypes []string               `json:"supported_types"`
	Sources        []string               `json:"sources"`
	Granularity    string                 `json:"granularity"`
	Backfill       string                 `json:"backfill_support"`
	Connections    []ConnectionCapability `json:"connections"`
}

type ConnectionCapability struct {
	ConnectionID      int64    `json:"connection_id"`
	Enabled           bool     `json:"enabled"`
	ConfiguredSources []string `json:"configured_sources"`
}

type Coverage struct {
	Items        []store.HistoryCoverage `json:"items"`
	Capabilities []Capability            `json:"capabilities"`
	Accounts     []store.HistoryAccount  `json:"accounts"`
}

type Service struct {
	repo  Repository
	now   func() time.Time
	owner string

	mu              sync.Mutex
	lastRun         map[string]time.Time
	wake            chan struct{}
	autoSyncEnabled bool
}

func New(repo Repository) *Service {
	return &Service{
		repo: repo, now: time.Now, owner: "history-" + uuid.NewString(),
		lastRun: map[string]time.Time{}, wake: make(chan struct{}, 1), autoSyncEnabled: true,
	}
}

// SetSyncConfig only controls new scheduled jobs. Explicit jobs and in-flight work can finish.
func (s *Service) SetSyncConfig(cfg config.BrokerSyncConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoSyncEnabled = cfg.AutomaticEnabled("IBKR")
}

func (s *Service) Capabilities() []Capability {
	return []Capability{{
		Provider:       "IBKR",
		SupportedTypes: []string{"trade", "deposit", "withdrawal", "dividend", "interest", "lending_income", "fee", "tax", "refund", "fx_conversion", "cash_transfer", "security_transfer"},
		Sources:        []string{SourceGateway, SourceFlex, SourceXML}, Granularity: "activity", Backfill: "configured_range", Connections: []ConnectionCapability{},
	}}
}

func (s *Service) ListAccounts(ctx context.Context) ([]store.HistoryAccount, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("history unavailable")
	}
	return s.repo.ListHistoryAccounts(ctx)
}

func (s *Service) List(ctx context.Context, q store.HistoryQuery) (store.HistoryPage, error) {
	if err := store.ValidateHistoryDates(q.From, q.To); err != nil {
		return store.HistoryPage{}, err
	}
	if _, err := s.validateAccountIDs(ctx, q.AccountIDs, 0, false); err != nil {
		return store.HistoryPage{}, err
	}
	return s.repo.ListActivities(ctx, q)
}

func (s *Service) Get(ctx context.Context, id string) (activity.Activity, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return activity.Activity{}, ErrInvalidRequest
	}
	return s.repo.GetActivity(ctx, id)
}

func (s *Service) Revisions(ctx context.Context, id string) ([]activity.Activity, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return nil, ErrInvalidRequest
	}
	return s.repo.ActivityRevisions(ctx, id)
}

func (s *Service) Coverage(ctx context.Context) (Coverage, error) {
	items, err := s.repo.ListHistoryCoverage(ctx)
	if err != nil {
		return Coverage{}, err
	}
	accounts, err := s.repo.ListHistoryAccounts(ctx)
	if err != nil {
		return Coverage{}, err
	}
	capabilities := s.Capabilities()
	connections, err := s.repo.ListBrokerConnections(ctx)
	if err != nil {
		return Coverage{}, err
	}
	for _, connection := range connections {
		if !strings.EqualFold(connection.ProviderCode, "IBKR") {
			continue
		}
		runtimeConnection, err := s.repo.GetBrokerConnectionRuntimeConfig(ctx, connection.ID)
		if err != nil {
			return Coverage{}, err
		}
		configured := []string{}
		if validateConnectionSource(runtimeConnection, SourceGateway) == nil {
			configured = append(configured, SourceGateway)
		}
		if validateConnectionSource(runtimeConnection, SourceFlex) == nil {
			configured = append(configured, SourceFlex)
		}
		capabilities[0].Connections = append(capabilities[0].Connections, ConnectionCapability{ConnectionID: connection.ID, Enabled: connection.Enabled, ConfiguredSources: configured})
	}
	return Coverage{Items: items, Capabilities: capabilities, Accounts: accounts}, nil
}

func (s *Service) Issues(ctx context.Context) ([]store.HistoryIssue, error) {
	return s.repo.ListHistoryIssues(ctx)
}

func (s *Service) Enqueue(ctx context.Context, req store.HistoryRequest) (store.HistoryJob, error) {
	req.Source = strings.ToLower(strings.TrimSpace(req.Source))
	if req.Source != "" && req.Source != SourceGateway && req.Source != SourceFlex {
		return store.HistoryJob{}, ErrInvalidRequest
	}
	if err := store.ValidateHistoryDates(req.From, req.To); err != nil {
		return store.HistoryJob{}, err
	}
	if len(req.AccountIDs) > 0 {
		if _, err := s.validateAccountIDs(ctx, req.AccountIDs, 0, true); err != nil {
			return store.HistoryJob{}, err
		}
	}
	connection, accounts, source, err := s.resolveRequestScope(ctx, req)
	if err != nil {
		return store.HistoryJob{}, err
	}
	req.UseQueryPeriod = source == SourceFlex && req.From == "" && req.To == ""
	req.ConnectionID, req.Source = connection.ID, source
	if len(req.AccountIDs) == 0 && source != SourceFlex {
		for _, account := range accounts {
			req.AccountIDs = append(req.AccountIDs, account.ID)
		}
	}
	if len(req.AccountIDs) == 0 && source != SourceFlex {
		return store.HistoryJob{}, ErrUnknownAccount
	}
	if err := normalizeRequestDates(&req, s.now().UTC()); err != nil {
		return store.HistoryJob{}, err
	}
	job, err := s.repo.CreateHistoryJob(ctx, req)
	if err == nil {
		s.signalWorker()
	}
	return job, err
}

func (s *Service) GetJob(ctx context.Context, id string) (store.HistoryJob, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return store.HistoryJob{}, ErrInvalidRequest
	}
	job, err := s.repo.GetHistoryJob(ctx, id)
	if err != nil {
		return job, err
	}
	if len(job.Request.AccountIDs) > 0 {
		if _, err := s.validateAccountIDs(ctx, job.Request.AccountIDs, job.Request.ConnectionID, true); err != nil {
			return store.HistoryJob{}, store.ErrForbidden
		}
	}
	return job, nil
}

func (s *Service) resolveRequestScope(ctx context.Context, req store.HistoryRequest) (store.BrokerConnection, []store.BrokerAccount, string, error) {
	connections, err := s.repo.ListBrokerConnections(ctx)
	if err != nil {
		return store.BrokerConnection{}, nil, "", err
	}
	candidates := []store.BrokerConnection{}
	requestedConnectionFound := false
	requestedConnectionAccountMismatch := false
	configuredCandidates := 0
	candidateAccounts := map[int64][]store.BrokerAccount{}
	candidateSources := map[int64]string{}
	for _, public := range connections {
		if req.ConnectionID > 0 && public.ID != req.ConnectionID {
			continue
		}
		if req.ConnectionID > 0 {
			requestedConnectionFound = true
		}
		if !public.Enabled || !strings.EqualFold(public.ProviderCode, "IBKR") {
			continue
		}
		connection, err := s.repo.GetBrokerConnectionRuntimeConfig(ctx, public.ID)
		if err != nil {
			return store.BrokerConnection{}, nil, "", err
		}
		source := req.Source
		if source == "" {
			source = preferredSource(connection, req.From, s.now().UTC())
		}
		if validateConnectionSource(connection, source) != nil {
			continue
		}
		configuredCandidates++
		accounts, err := s.repo.ListBrokerAccountsByConnection(ctx, public.ID)
		if err != nil {
			return store.BrokerConnection{}, nil, "", err
		}
		available := map[int64]bool{}
		for _, account := range accounts {
			available[account.ID] = true
		}
		containsAll := true
		for _, id := range req.AccountIDs {
			if !available[id] {
				containsAll = false
				break
			}
		}
		if !containsAll {
			if req.ConnectionID > 0 {
				requestedConnectionAccountMismatch = true
			}
			continue
		}
		candidates = append(candidates, connection)
		candidateAccounts[connection.ID] = accounts
		candidateSources[connection.ID] = source
	}
	if len(candidates) == 0 {
		if requestedConnectionAccountMismatch {
			return store.BrokerConnection{}, nil, "", ErrAccountNotConnected
		}
		if req.ConnectionID > 0 {
			if !requestedConnectionFound {
				return store.BrokerConnection{}, nil, "", store.ErrNotFound
			}
			connection, err := s.repo.GetBrokerConnectionRuntimeConfig(ctx, req.ConnectionID)
			if err != nil {
				return store.BrokerConnection{}, nil, "", err
			}
			if !strings.EqualFold(connection.ProviderCode, "IBKR") {
				return store.BrokerConnection{}, nil, "", fmt.Errorf("history_not_supported")
			}
		}
		if configuredCandidates > 0 && len(req.AccountIDs) > 0 {
			return store.BrokerConnection{}, nil, "", ErrConnectionRequired
		}
		return store.BrokerConnection{}, nil, "", ErrSourceNotConfigured
	}
	if len(candidates) > 1 {
		return store.BrokerConnection{}, nil, "", ErrConnectionRequired
	}
	selected := candidates[0]
	return selected, candidateAccounts[selected.ID], candidateSources[selected.ID], nil
}

func preferredSource(connection store.BrokerConnection, from string, now time.Time) string {
	flexReady := configString(connection.Config, "flex_activity_query_id") != "" && strings.TrimSpace(connection.Secrets["flex_token"]) != ""
	gatewayReady := configString(connection.Config, "gateway_url") != "" && strings.TrimSpace(connection.Secrets["gateway_token"]) != ""
	if flexReady && from != "" && from < now.AddDate(0, 0, -7).Format("2006-01-02") {
		return SourceFlex
	}
	if gatewayReady {
		return SourceGateway
	}
	if flexReady {
		return SourceFlex
	}
	return ""
}

func normalizeRequestDates(req *store.HistoryRequest, now time.Time) error {
	today := now.Format("2006-01-02")
	if req.To == "" {
		req.To = today
	}
	if req.From == "" {
		req.From = now.AddDate(0, 0, -7).Format("2006-01-02")
	}
	if err := store.ValidateHistoryDates(req.From, req.To); err != nil {
		return err
	}
	start, _ := time.Parse("2006-01-02", req.From)
	end, _ := time.Parse("2006-01-02", req.To)
	if end.After(now) {
		return ErrInvalidRequest
	}
	if req.Source == SourceGateway && start.Before(now.AddDate(0, 0, -7).Truncate(24*time.Hour)) {
		return fmt.Errorf("history_not_supported")
	}
	if req.Source == SourceFlex && end.Sub(start) > 364*24*time.Hour {
		return ErrInvalidRequest
	}
	return nil
}

func (s *Service) PreviewImport(ctx context.Context, body []byte, userID int64) (store.HistoryImport, error) {
	if len(body) == 0 {
		return store.HistoryImport{}, fmt.Errorf("import_invalid")
	}
	records, err := ibkr.ParseActivityXML(body)
	if err != nil {
		return store.HistoryImport{}, fmt.Errorf("import_invalid")
	}
	if len(records) == 0 {
		return store.HistoryImport{}, fmt.Errorf("import_invalid")
	}
	if err := s.validateImportedAccounts(ctx, records); err != nil {
		return store.HistoryImport{}, err
	}
	accounts, err := s.repo.ListBrokerAccounts(ctx)
	if err != nil {
		return store.HistoryImport{}, err
	}
	accountMap := make(map[int64]store.BrokerAccount, len(accounts))
	allowed := make(map[string]bool, len(accounts))
	for _, account := range accounts {
		if strings.EqualFold(account.Broker, "IBKR") {
			accountMap[account.ID] = account
			allowed[account.ProviderAccountID] = true
		}
	}
	ranges, err := ibkr.ActivityReportCoverage(body)
	if err != nil {
		return store.HistoryImport{}, fmt.Errorf("import_invalid")
	}
	for _, report := range ranges {
		if report.ProviderAccountID == "" || !allowed[report.ProviderAccountID] {
			return store.HistoryImport{}, ErrUnknownAccount
		}
	}
	coverage, err := verifiedReportCoverage(body, accountMap, allowed, SourceXML)
	if err != nil {
		return store.HistoryImport{}, fmt.Errorf("import_invalid")
	}
	snapshots, err := ibkr.ParseReconciliationSnapshots(body)
	if err != nil {
		return store.HistoryImport{}, fmt.Errorf("import_invalid")
	}
	for _, snapshot := range snapshots {
		if !allowed[snapshot.ProviderAccountID] {
			return store.HistoryImport{}, ErrUnknownAccount
		}
	}
	hash := sha256.Sum256(body)
	preview, err := s.repo.CreateHistoryImport(ctx, records, hex.EncodeToString(hash[:]), userID)
	if err != nil {
		return store.HistoryImport{}, err
	}
	if err := s.repo.SaveHistoryImportEvidence(ctx, preview.ID, coverage, snapshots); err != nil {
		return store.HistoryImport{}, err
	}
	preview.Coverage = coverage
	preview.Snapshots = snapshots
	return preview, nil
}

func (s *Service) GetImport(ctx context.Context, id string, userID int64) (store.HistoryImport, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return store.HistoryImport{}, ErrInvalidRequest
	}
	item, err := s.repo.GetHistoryImport(ctx, id)
	if err != nil {
		return item, err
	}
	if item.CreatedBy > 0 && item.CreatedBy != userID {
		return store.HistoryImport{}, store.ErrForbidden
	}
	return item, nil
}

func (s *Service) CommitImport(ctx context.Context, id string, userID int64) (store.HistoryJob, error) {
	if _, err := s.GetImport(ctx, id, userID); err != nil {
		return store.HistoryJob{}, err
	}
	job, err := s.repo.CommitHistoryImport(ctx, id)
	if err == nil {
		s.signalWorker()
	}
	return job, err
}

func (s *Service) ResolveIssue(ctx context.Context, id, action, target string, userID int64) error {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return ErrInvalidRequest
	}
	switch action {
	case "confirm_same", "confirm_distinct", "link_transfer":
		if _, err := uuid.Parse(strings.TrimSpace(target)); err != nil {
			return ErrInvalidRequest
		}
	case "keep_pending", "accept_revision":
		if target != "" {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return s.repo.ResolveHistoryIssue(ctx, id, action, target, userID)
}

func (s *Service) validateImportedAccounts(ctx context.Context, records []activity.RawRecord) error {
	accounts, err := s.repo.ListHistoryAccounts(ctx)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, account := range accounts {
		if strings.EqualFold(account.Provider, "IBKR") {
			known[account.ProviderAccountID] = true
		}
	}
	for _, record := range records {
		if record.ProviderAccountID == "" || !known[record.ProviderAccountID] {
			return ErrUnknownAccount
		}
	}
	return nil
}

func (s *Service) validateAccountIDs(ctx context.Context, ids []int64, connectionID int64, require bool) (map[int64]store.BrokerAccount, error) {
	if require && len(ids) == 0 {
		return nil, ErrInvalidRequest
	}
	accounts, err := s.repo.ListBrokerAccounts(ctx)
	if connectionID > 0 {
		accounts, err = s.repo.ListBrokerAccountsByConnection(ctx, connectionID)
	}
	if err != nil {
		return nil, err
	}
	known := make(map[int64]store.BrokerAccount, len(accounts))
	for _, account := range accounts {
		known[account.ID] = account
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrInvalidRequest
		}
		if _, ok := known[id]; !ok {
			if connectionID > 0 {
				return nil, ErrAccountNotConnected
			}
			return nil, ErrUnknownAccount
		}
	}
	return known, nil
}

func validateConnectionSource(connection store.BrokerConnection, source string) error {
	if !strings.EqualFold(connection.ProviderCode, "IBKR") {
		return fmt.Errorf("history_not_supported")
	}
	if !connection.Enabled {
		return ErrSourceNotConfigured
	}
	if source == SourceFlex {
		if configString(connection.Config, "flex_activity_query_id") == "" || strings.TrimSpace(connection.Secrets["flex_token"]) == "" {
			return ErrSourceNotConfigured
		}
	} else if configString(connection.Config, "gateway_url") == "" || strings.TrimSpace(connection.Secrets["gateway_token"]) == "" {
		return ErrSourceNotConfigured
	}
	return nil
}

func (s *Service) signalWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func connectionIBKRConfig(connection store.BrokerConnection) config.IBKRConfig {
	return config.IBKRConfig{
		GatewayURL: configString(connection.Config, "gateway_url"), GatewayToken: connection.Secrets["gateway_token"],
		FlexToken: connection.Secrets["flex_token"], FlexQueryID: configString(connection.Config, "flex_query_id"),
		FlexActivityQueryID: configString(connection.Config, "flex_activity_query_id"), FlexBaseURL: configString(connection.Config, "flex_base_url"),
		ActivityHistoryEnabled: configBool(connection.Config, "activity_history_enabled"), ActivityHistoryFrom: configString(connection.Config, "activity_history_from"),
	}
}

func configString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
func configBool(values map[string]any, key string) bool { value, _ := values[key].(bool); return value }

func filterRecords(records []activity.RawRecord, allowed map[string]bool) []activity.RawRecord {
	out := make([]activity.RawRecord, 0, len(records))
	for _, record := range records {
		if allowed[record.ProviderAccountID] {
			out = append(out, record)
		}
	}
	return out
}

func allowedProviderIDs(accounts map[int64]store.BrokerAccount, ids []int64) map[string]bool {
	allowed := map[string]bool{}
	for _, id := range ids {
		if account, ok := accounts[id]; ok {
			allowed[account.ProviderAccountID] = true
		}
	}
	return allowed
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	if code := ibkr.FlexErrorCode(err); code != "" {
		return "ibkr_flex_" + code
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "status 401") || strings.Contains(message, "status 403") || strings.Contains(message, "not authenticated") || strings.Contains(message, "unauthorized") {
		return "source_authentication_required"
	}
	for _, code := range []string{"source_not_configured", "history_not_supported", "history_account_not_found", "history_account_not_connected", "import_invalid", "import_expired", "mapping_conflict"} {
		if strings.Contains(message, code) {
			return code
		}
	}
	return "history_sync_failed"
}

func (s *Service) logFailure(job store.HistoryJob, err error) {
	log.Printf("history job failed id=%s source=%s phase=%s error=%s", job.ID, job.Request.Source, job.Phase, sanitizeError(err))
}
