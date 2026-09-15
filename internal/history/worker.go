package history

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/broker"
	"github.com/nite/traio/internal/broker/ibkr"
	"github.com/nite/traio/internal/store"
)

const renewInterval = 30 * time.Second

// Start runs one durable worker and the opt-in scheduler until ctx is canceled.
func (s *Service) Start(ctx context.Context) {
	if s == nil || s.repo == nil {
		return
	}
	go s.workerLoop(ctx)
	go s.schedulerLoop(ctx)
}

func (s *Service) workerLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.claimAndRun(ctx); err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

func (s *Service) claimAndRun(ctx context.Context) error {
	job, err := s.repo.ClaimHistoryJob(ctx, s.owner)
	if err != nil {
		return err
	}
	s.runJob(ctx, job)
	return nil
}

func (s *Service) runJob(parent context.Context, job store.HistoryJob) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	renewErr := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(renewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.repo.RenewHistoryJob(ctx, job); err != nil {
					select {
					case renewErr <- err:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	defer close(done)

	if job.Phase == "fetch" {
		fetched, err := s.fetchJob(ctx, &job)
		if err == nil {
			err = s.repo.SaveHistorySnapshots(ctx, job, fetched.snapshots)
		}
		if err == nil {
			err = s.repo.SaveHistoryRaw(ctx, job, fetched.records)
		}
		if err != nil {
			s.finishFailure(parent, job, err)
			return
		}
		job.Phase = "normalize"
	}
	counts, err := s.repo.NormalizeHistoryJob(ctx, job)
	if err == nil {
		var statuses map[int64]string
		statuses, err = s.repo.ReconcileHistoryJob(ctx, job)
		if err == nil {
			applyReconciliationStatus(&job, statuses)
		}
	}
	if err == nil {
		err = s.repo.SaveHistoryCoverage(ctx, job, counts)
	}
	if err == nil {
		status := "succeeded"
		if counts.Unsupported > 0 || counts.Conflicts > 0 {
			status = "partial"
		}
		err = s.repo.FinishHistoryJob(ctx, job, status, counts, "")
	}
	if err != nil {
		select {
		case leaseErr := <-renewErr:
			err = leaseErr
		default:
		}
		if !errors.Is(err, store.ErrHistoryLeaseLost) {
			s.finishFailure(parent, job, err)
		}
	}
}

type fetchResult struct {
	records   []activity.RawRecord
	snapshots []activity.ReconciliationSnapshot
}

func (s *Service) fetchJob(ctx context.Context, job *store.HistoryJob) (fetchResult, error) {
	if job.Request.ImportID != "" {
		preview, err := s.repo.GetHistoryImport(ctx, job.Request.ImportID)
		if err == nil {
			job.Coverage = append([]store.HistoryCoverage(nil), preview.Coverage...)
		}
		return fetchResult{records: preview.Records, snapshots: preview.Snapshots}, err
	}
	accounts, err := s.validateAccountIDs(ctx, job.Request.AccountIDs, job.Request.ConnectionID, job.Request.Source != SourceFlex || len(job.Request.AccountIDs) > 0)
	if err != nil {
		return fetchResult{}, err
	}
	allowed := allowedProviderIDs(accounts, job.Request.AccountIDs)
	connection, err := s.repo.GetBrokerConnectionRuntimeConfig(ctx, job.Request.ConnectionID)
	if err != nil {
		return fetchResult{}, err
	}
	if err := validateConnectionSource(connection, job.Request.Source); err != nil {
		return fetchResult{}, err
	}
	client, err := ibkr.NewActivityClient(connectionIBKRConfig(connection))
	if err != nil {
		return fetchResult{}, err
	}
	var records []activity.RawRecord
	var snapshots []activity.ReconciliationSnapshot
	switch job.Request.Source {
	case SourceGateway:
		records, err = client.FetchRecentTrades(ctx)
	case SourceFlex:
		var body []byte
		from, to := job.Request.From, job.Request.To
		if job.Request.UseQueryPeriod {
			from, to = "", ""
		}
		body, err = client.FetchActivityReport(ctx, from, to)
		if err == nil {
			records, err = ibkr.ParseActivityXML(body)
		}
		if err == nil && len(job.Request.AccountIDs) == 0 {
			err = s.discoverFlexAccounts(ctx, connection.ID, body)
			if err == nil {
				accounts, err = s.validateAccountIDs(ctx, nil, connection.ID, false)
				for id := range accounts {
					job.Request.AccountIDs = append(job.Request.AccountIDs, id)
				}
				allowed = allowedProviderIDs(accounts, job.Request.AccountIDs)
			}
		}
		if err == nil {
			job.Coverage, err = verifiedReportCoverage(body, accounts, allowed, SourceFlex)
		}
		if err == nil {
			snapshots, err = ibkr.ParseReconciliationSnapshots(body)
		}
	default:
		err = ErrInvalidRequest
	}
	if err != nil {
		return fetchResult{}, err
	}
	return fetchResult{records: filterRecords(records, allowed), snapshots: filterSnapshots(snapshots, allowed)}, nil
}

func filterSnapshots(snapshots []activity.ReconciliationSnapshot, allowed map[string]bool) []activity.ReconciliationSnapshot {
	out := make([]activity.ReconciliationSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if allowed[snapshot.ProviderAccountID] {
			out = append(out, snapshot)
		}
	}
	return out
}

func applyReconciliationStatus(job *store.HistoryJob, statuses map[int64]string) {
	if len(job.Coverage) == 0 {
		dataType := "activities"
		if job.Request.Source == SourceGateway {
			dataType = "trade"
		}
		for accountID, status := range statuses {
			job.Coverage = append(job.Coverage, store.HistoryCoverage{AccountID: accountID, Source: job.Request.Source, DataType: dataType, ScopeKey: "account", From: job.Request.From, To: job.Request.To, FetchStatus: "observed", ReconcileStatus: status})
		}
		return
	}
	for i := range job.Coverage {
		job.Coverage[i].ReconcileStatus = statuses[job.Coverage[i].AccountID]
		if job.Coverage[i].ReconcileStatus == "" {
			job.Coverage[i].ReconcileStatus = "unknown"
		}
	}
}

func verifiedFlexCoverage(body []byte, accounts map[int64]store.BrokerAccount, allowed map[string]bool) ([]store.HistoryCoverage, error) {
	return verifiedReportCoverage(body, accounts, allowed, SourceFlex)
}

func verifiedReportCoverage(body []byte, accounts map[int64]store.BrokerAccount, allowed map[string]bool, source string) ([]store.HistoryCoverage, error) {
	ranges, err := ibkr.ActivityReportCoverage(body)
	if err != nil {
		return nil, err
	}
	accountIDs := map[string]int64{}
	for _, account := range accounts {
		accountIDs[account.ProviderAccountID] = account.ID
	}
	sectionTypes := map[string]string{"Trades": "trade", "CashTransactions": "cash", "Transfers": "transfer", "CorporateActions": "corporate_action"}
	required := []string{"Trades", "CashTransactions", "Transfers", "CorporateActions"}
	coverage := []store.HistoryCoverage{}
	for _, report := range ranges {
		if !allowed[report.ProviderAccountID] || report.From == "" || report.To == "" {
			continue
		}
		accountID := accountIDs[report.ProviderAccountID]
		seen := map[string]bool{}
		for _, section := range report.Sections {
			seen[section] = true
			if dataType, ok := sectionTypes[section]; ok {
				coverage = append(coverage, store.HistoryCoverage{AccountID: accountID, Source: source, DataType: dataType, ScopeKey: "account", From: report.From, To: report.To, FetchStatus: "complete"})
			}
		}
		complete := true
		for _, section := range required {
			if !seen[section] {
				complete = false
				break
			}
		}
		status := "observed"
		if complete {
			status = "complete"
		}
		coverage = append(coverage, store.HistoryCoverage{AccountID: accountID, Source: source, DataType: "activities", ScopeKey: "account", From: report.From, To: report.To, FetchStatus: status})
	}
	return coverage, nil
}

func (s *Service) finishFailure(ctx context.Context, job store.HistoryJob, cause error) {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, store.ErrHistoryLeaseLost) {
		return
	}
	s.logFailure(job, cause)
	status := "retry_wait"
	code := sanitizeError(cause)
	if job.Attempt >= 5 {
		status = "failed"
	}
	if code == "source_not_configured" || code == "source_authentication_required" || code == "history_not_supported" || code == "history_account_not_found" || code == "history_account_not_connected" || code == "import_invalid" || code == "import_expired" {
		status = "needs_action"
	}
	if err := s.repo.FinishHistoryJob(ctx, job, status, job.Counts, code); err != nil && !errors.Is(err, store.ErrHistoryLeaseLost) {
		log.Printf("history job finish failed id=%s error=history_state_update_failed", job.ID)
	}
}

// Only a report fetched with a saved Flex credential may discover its accounts.
// Old report windows do not revoke account links omitted from that window.
func (s *Service) discoverFlexAccounts(ctx context.Context, connectionID int64, body []byte) error {
	reports, err := ibkr.ActivityReportCoverage(body)
	if err != nil {
		return err
	}
	existing, err := s.repo.ListBrokerAccountsByConnection(ctx, connectionID)
	if err != nil {
		return err
	}
	discovered := []broker.Account{}
	seen := map[string]bool{}
	for _, account := range existing {
		seen[account.ProviderAccountID] = true
		discovered = append(discovered, broker.Account{ID: account.ProviderAccountID, Broker: "IBKR"})
	}
	for _, report := range reports {
		if report.ProviderAccountID != "" && !seen[report.ProviderAccountID] {
			seen[report.ProviderAccountID] = true
			discovered = append(discovered, broker.Account{ID: report.ProviderAccountID, Broker: "IBKR"})
		}
	}
	if len(reports) == 0 || len(discovered) == 0 {
		return ErrUnknownAccount
	}
	return s.repo.ReplaceBrokerConnectionAccounts(ctx, connectionID, discovered)
}
