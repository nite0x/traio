package api

import (
	"bytes"
	"context"
	"encoding/json"
	traioauth "github.com/nite/traio/internal/auth"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nite/traio/internal/activity"
	"github.com/nite/traio/internal/broker"
	historysvc "github.com/nite/traio/internal/history"
	"github.com/nite/traio/internal/store"
)

const testActivityID = "11111111-1111-4111-8111-111111111111"
const testImportID = "22222222-2222-4222-8222-222222222222"

type fakeHistoryAPI struct {
	listErr     error
	preview     store.HistoryImport
	previewBody []byte
	commit      store.HistoryJob
	revisions   []activity.Activity
}

func (f *fakeHistoryAPI) ListAccounts(context.Context) ([]store.HistoryAccount, error) {
	return []store.HistoryAccount{{ID: 7, Provider: "IBKR", ProviderAccountID: "U123"}}, nil
}
func (f *fakeHistoryAPI) List(context.Context, store.HistoryQuery) (store.HistoryPage, error) {
	return store.HistoryPage{Items: []activity.Activity{}}, f.listErr
}
func (f *fakeHistoryAPI) Get(context.Context, string) (activity.Activity, error) {
	return activity.Activity{ID: testActivityID}, nil
}
func (f *fakeHistoryAPI) Revisions(context.Context, string) ([]activity.Activity, error) {
	return f.revisions, nil
}
func (f *fakeHistoryAPI) Coverage(context.Context) (historysvc.Coverage, error) {
	return historysvc.Coverage{Items: []store.HistoryCoverage{}, Capabilities: []historysvc.Capability{}, Accounts: []store.HistoryAccount{}}, nil
}
func (f *fakeHistoryAPI) Issues(context.Context) ([]store.HistoryIssue, error) {
	return []store.HistoryIssue{}, nil
}
func (f *fakeHistoryAPI) Enqueue(context.Context, store.HistoryRequest) (store.HistoryJob, error) {
	return store.HistoryJob{ID: "job-1", Status: "queued"}, nil
}
func (f *fakeHistoryAPI) GetJob(context.Context, string) (store.HistoryJob, error) {
	return store.HistoryJob{ID: "job-1", Status: "queued"}, nil
}
func (f *fakeHistoryAPI) PreviewImport(_ context.Context, body []byte, _ int64) (store.HistoryImport, error) {
	f.previewBody = append([]byte(nil), body...)
	return f.preview, nil
}
func (f *fakeHistoryAPI) GetImport(context.Context, string, int64) (store.HistoryImport, error) {
	return f.preview, nil
}
func (f *fakeHistoryAPI) CommitImport(context.Context, string, int64) (store.HistoryJob, error) {
	return f.commit, nil
}
func (f *fakeHistoryAPI) ResolveIssue(context.Context, string, string, string, int64) error {
	return nil
}

func historyRequest(t *testing.T, router http.Handler, method, path, token, contentType string, body *bytes.Buffer) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		requestBody = bytes.NewReader(body.Bytes())
	}
	req := httptest.NewRequest(method, path, requestBody)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestHistoryRoutesRequireAuthenticationAndWrapCollections(t *testing.T) {
	token := "history-test-token"
	fake := &fakeHistoryAPI{revisions: []activity.Activity{{ID: testActivityID, Revision: 2}}}
	router := NewRouter(Deps{APIToken: token, AllowedAPIHosts: []string{"example.com"}, History: fake}, ServerControl{})

	unauthorized := historyRequest(t, router, http.MethodGet, "/api/v1/transaction-history/accounts", "", "", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", unauthorized.Code)
	}

	accounts := historyRequest(t, router, http.MethodGet, "/api/v1/transaction-history/accounts", token, "", nil)
	if accounts.Code != http.StatusOK {
		t.Fatalf("accounts status = %d: %s", accounts.Code, accounts.Body.String())
	}
	var accountBody struct {
		Items []store.HistoryAccount `json:"items"`
	}
	if err := json.Unmarshal(accounts.Body.Bytes(), &accountBody); err != nil || len(accountBody.Items) != 1 {
		t.Fatalf("accounts response = %s, error = %v", accounts.Body.String(), err)
	}

	revisions := historyRequest(t, router, http.MethodGet, "/api/v1/transactions/"+testActivityID+"/revisions", token, "", nil)
	var revisionBody map[string]json.RawMessage
	if revisions.Code != http.StatusOK {
		t.Fatalf("revisions status = %d: %s", revisions.Code, revisions.Body.String())
	}
	if err := json.Unmarshal(revisions.Body.Bytes(), &revisionBody); err != nil || revisionBody["items"] == nil {
		t.Fatalf("revisions must be wrapped in items: %s", revisions.Body.String())
	}
}

func TestHistoryUnknownAccountIsRejected(t *testing.T) {
	fake := &fakeHistoryAPI{listErr: historysvc.ErrUnknownAccount}
	router := NewRouter(Deps{History: fake}, ServerControl{})
	response := historyRequest(t, router, http.MethodGet, "/api/v1/transactions?account_ids=999", "", "", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Body.String() != "{\"error\":\"history_account_not_found\"}" {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestHistoryXMLImportPreviewAndCommit(t *testing.T) {
	fixture := []byte(`<?xml version="1.0"?><FlexQueryResponse queryName="Activity"><FlexStatements><FlexStatement accountId="U123"></FlexStatement></FlexStatements></FlexQueryResponse>`)
	fake := &fakeHistoryAPI{preview: store.HistoryImport{ID: testImportID, Status: "preview", RecordCount: 1, Accounts: []string{"U123"}, Records: []activity.RawRecord{{Payload: []byte(`{"private":"raw-secret"}`)}}, Coverage: []store.HistoryCoverage{{AccountID: 7}}, Snapshots: []activity.ReconciliationSnapshot{{ProviderAccountID: "U123", SourceRef: "private-snapshot"}}}, commit: store.HistoryJob{ID: "job-import-1", Status: "queued"}}
	router := NewRouter(Deps{History: fake}, ServerControl{})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "activity.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(fixture); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	preview := historyRequest(t, router, http.MethodPost, "/api/v1/transaction-history/imports", "", writer.FormDataContentType(), &body)
	if preview.Code != http.StatusCreated {
		t.Fatalf("preview status = %d: %s", preview.Code, preview.Body.String())
	}
	if !bytes.Equal(fake.previewBody, fixture) {
		t.Fatalf("preview did not receive exact XML fixture")
	}
	if bytes.Contains(preview.Body.Bytes(), []byte("raw-secret")) || bytes.Contains(preview.Body.Bytes(), []byte("private-snapshot")) || bytes.Contains(preview.Body.Bytes(), []byte("coverage")) {
		t.Fatalf("preview leaked raw evidence: %s", preview.Body.String())
	}

	commit := historyRequest(t, router, http.MethodPost, "/api/v1/transaction-history/imports/"+testImportID+"/commit", "", "application/json", bytes.NewBufferString("{}"))
	if commit.Code != http.StatusAccepted {
		t.Fatalf("commit status = %d: %s", commit.Code, commit.Body.String())
	}
	if commit.Body.String() != "{\"job_id\":\"job-import-1\",\"status\":\"queued\"}" {
		t.Fatalf("commit body = %s", commit.Body.String())
	}
}

func TestHistoryImportRealServiceRejectsUnknownAndCreatesDurableJob(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{ProviderCode: "IBKR", ConnectionKey: "history", Name: "History", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U123", DisplayName: "Brokerage", BaseCurrency: "USD"}}); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Deps{History: historysvc.New(st)}, ServerControl{})

	upload := func(account string) *httptest.ResponseRecorder {
		fixture := []byte(`<?xml version="1.0"?><FlexQueryResponse><FlexStatements><FlexStatement accountId="` + account + `" fromDate="20260901" toDate="20260901"><CashTransactions><CashTransaction transactionID="C1" type="Dividend" currency="USD" amount="1" dateTime="20260901;120000"/></CashTransactions></FlexStatement></FlexStatements></FlexQueryResponse>`)
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, createErr := writer.CreateFormFile("file", "activity.xml")
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := part.Write(fixture); writeErr != nil {
			t.Fatal(writeErr)
		}
		if closeErr := writer.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		return historyRequest(t, router, http.MethodPost, "/api/v1/transaction-history/imports", "", writer.FormDataContentType(), &body)
	}

	unknown := upload("U999")
	if unknown.Code != http.StatusBadRequest || unknown.Body.String() != "{\"error\":\"history_account_not_found\"}" {
		t.Fatalf("unknown import = %d %s", unknown.Code, unknown.Body.String())
	}
	preview := upload("U123")
	if preview.Code != http.StatusCreated {
		t.Fatalf("preview = %d %s", preview.Code, preview.Body.String())
	}
	var imported store.HistoryImport
	if err := json.Unmarshal(preview.Body.Bytes(), &imported); err != nil || imported.ID == "" {
		t.Fatalf("preview body = %s, err = %v", preview.Body.String(), err)
	}
	commit := historyRequest(t, router, http.MethodPost, "/api/v1/transaction-history/imports/"+imported.ID+"/commit", "", "application/json", bytes.NewBufferString("{}"))
	if commit.Code != http.StatusAccepted {
		t.Fatalf("commit = %d %s", commit.Code, commit.Body.String())
	}
	var accepted struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(commit.Body.Bytes(), &accepted); err != nil || accepted.JobID == "" || accepted.Status != "queued" {
		t.Fatalf("commit body = %s, err = %v", commit.Body.String(), err)
	}
	job, err := st.GetHistoryJob(t.Context(), accepted.JobID)
	if err != nil || job.Request.ImportID != imported.ID || job.Request.Source != "xml" {
		t.Fatalf("durable job = %#v, err = %v", job, err)
	}
}

func TestHistoryConfigPreservesUnrelatedRuntimeConfiguration(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "history-config.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	connection, err := st.UpsertBrokerConnection(t.Context(), store.BrokerConnection{
		ProviderCode: "IBKR", ConnectionKey: "configured", Name: "Configured", Enabled: true,
		Config:  map[string]any{"gateway_url": "https://gateway.example", "flex_query_id": "nav-query", "unrelated": "keep"},
		Secrets: map[string]string{"gateway_token": "gateway-secret", "flex_token": "old-flex-secret", "other_secret": "keep-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reloads := 0
	router := NewRouter(Deps{Brokers: st, History: historysvc.New(st), OnBrokersChanged: func(context.Context) error { reloads++; return nil }}, ServerControl{})
	body := bytes.NewBufferString(`{"activity_history_enabled":true,"activity_history_from":"2026-01-01","flex_activity_query_id":"12345","flex_token":"new-flex-secret"}`)
	response := historyRequest(t, router, http.MethodPut, "/api/v1/broker-connections/"+strconv.FormatInt(connection.ID, 10)+"/history-config", "", "application/json", body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("new-flex-secret")) || bytes.Contains(response.Body.Bytes(), []byte("gateway-secret")) {
		t.Fatalf("response leaked a secret: %s", response.Body.String())
	}
	if reloads != 1 {
		t.Fatalf("reloads = %d", reloads)
	}
	updated, err := st.GetBrokerConnectionRuntimeConfig(t.Context(), connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Config["flex_query_id"] != "nav-query" || updated.Config["unrelated"] != "keep" || updated.Config["flex_activity_query_id"] != "12345" || updated.Config["activity_history_enabled"] != true {
		t.Fatalf("config = %#v", updated.Config)
	}
	if updated.Secrets["gateway_token"] != "gateway-secret" || updated.Secrets["other_secret"] != "keep-secret" || updated.Secrets["flex_token"] != "new-flex-secret" {
		t.Fatalf("secrets were not preserved")
	}
	if err := st.ReplaceBrokerConnectionAccounts(t.Context(), connection.ID, []broker.Account{{ID: "U1", DisplayName: "One", BaseCurrency: "USD"}}); err != nil {
		t.Fatal(err)
	}
	syncResponse := historyRequest(t, router, http.MethodPost, "/api/v1/transaction-history/sync", "", "application/json", bytes.NewBufferString("{}"))
	if syncResponse.Code != http.StatusAccepted {
		t.Fatalf("default sync = %d %s", syncResponse.Code, syncResponse.Body.String())
	}
}

func TestHistorySessionPermissionsAndCSRF(t *testing.T) {
	st, e := store.Open(filepath.Join(t.TempDir(), "history-auth.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	service, e := traioauth.NewService(t.Context(), st, traioauth.Config{Mode: traioauth.ModePassword, BootstrapUsername: "owner", BootstrapPassword: "synthetic-test-password-long"})
	if e != nil {
		t.Fatal(e)
	}
	if e = st.InviteWorkspaceMember(t.Context(), store.WorkspaceInvite{WorkspaceID: store.DefaultWorkspaceID, InvitedBy: 1, Email: "viewer@example.invalid", Role: "viewer"}); e != nil {
		t.Fatal(e)
	}
	viewer, e := st.UpsertOIDCIdentity(t.Context(), "fixture", "viewer", "viewer@example.invalid", "Viewer")
	if e != nil {
		t.Fatal(e)
	}
	if e = st.UpdateWorkspaceMemberRole(t.Context(), store.DefaultWorkspaceID, viewer.User.ID, "viewer"); e != nil {
		t.Fatal(e)
	}
	session, csrf := "history-session-fixture", "history-csrf-fixture"
	now := time.Now().UTC()
	if e = st.CreateAuthSession(t.Context(), store.AuthSession{TokenHash: testTokenHash(session), CSRFTokenHash: testTokenHash(csrf), UserID: viewer.User.ID, WorkspaceID: store.DefaultWorkspaceID, ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), LastSeenAt: now.Format(time.RFC3339Nano)}); e != nil {
		t.Fatal(e)
	}
	router := NewRouter(Deps{Auth: service, History: &fakeHistoryAPI{}}, ServerControl{})
	request := func(method, path string, withCSRF bool) int {
		req := httptest.NewRequest(method, "http://127.0.0.1"+path, bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: service.CookieName(), Value: session})
		if withCSRF {
			req.AddCookie(&http.Cookie{Name: service.CSRFCookieName(), Value: csrf})
			req.Header.Set("X-CSRF-Token", csrf)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res.Code
	}
	if got := request("GET", "/api/v1/transactions", false); got != 200 {
		t.Fatalf("viewer read=%d", got)
	}
	for _, path := range []string{"/api/v1/transaction-history/sync", "/api/v1/transaction-history/imports", "/api/v1/transaction-history/imports/" + testImportID + "/commit", "/api/v1/transaction-history/issues/" + testActivityID + "/resolve"} {
		if got := request("POST", path, true); got != 403 {
			t.Fatalf("viewer mutation %s=%d", path, got)
		}
	}
	if e = st.UpdateWorkspaceMemberRole(t.Context(), store.DefaultWorkspaceID, viewer.User.ID, "member"); e != nil {
		t.Fatal(e)
	}
	if got := request("POST", "/api/v1/transaction-history/sync", false); got != 403 {
		t.Fatalf("missing CSRF=%d", got)
	}
	if got := request("POST", "/api/v1/transaction-history/sync", true); got != 202 {
		t.Fatalf("member sync=%d", got)
	}
	if got := request("POST", "/api/v1/transaction-history/imports", true); got != 403 {
		t.Fatalf("member import=%d", got)
	}
}
