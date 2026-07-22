package billingadminhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAdminBillingSettingsPlatformAdminReadWriteAndAudit(t *testing.T) {
	store := newAdminBillingSettingsStore()
	audit := &adminBillingSettingsAuditStore{}
	handler := newAdminBillingSettingsTestRouter(AdminBillingSettingsDeps{
		Auth:         adminBillingAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Store:        store,
		AuditUpdater: newAdminBillingSettingsTestAuditUpdater(store, audit),
	})

	rec := serveAdminBillingJSON(t, handler, http.MethodPut, "/admin/v1/billing/settings", map[string]any{
		"tenant_id":                            7,
		"stream_input_only_interrupted_policy": "no_bill_record",
		"reason":                               "case C rollout",
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	got := decodeAdminBillingResponse(t, rec)
	if got.TenantID != 7 || got.Value != "no_bill_record" || got.Source != "tenant" {
		t.Fatalf("PUT response=%+v", got)
	}
	if got.UpdatedBy == nil || *got.UpdatedBy != "admin_token:11" {
		t.Fatalf("updated_by=%v want admin_token:11", got.UpdatedBy)
	}
	if len(audit.records) != 1 {
		t.Fatalf("audit records=%d want 1", len(audit.records))
	}
	record := audit.records[0]
	if record.ActorID != "admin_token:11" || record.ActorRole != admin.RolePlatformAdmin ||
		record.Action != "update_billing_settings" || record.TenantID == nil || *record.TenantID != 7 {
		t.Fatalf("audit record mismatch: %+v", record)
	}
	var payload map[string]string
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		t.Fatalf("audit payload json: %v", err)
	}
	if payload["previous_value"] != "no_bill" || payload["previous_source"] != "default" ||
		payload["new_value"] != "no_bill_record" || payload["reason"] != "case C rollout" {
		t.Fatalf("audit payload=%v", payload)
	}

	rec = serveAdminBillingJSON(t, handler, http.MethodGet, "/admin/v1/billing/settings?tenant_id=7", nil)
	assertHTTPStatus(t, rec, http.StatusOK)
	got = decodeAdminBillingResponse(t, rec)
	if got.Value != "no_bill_record" || got.Source != "tenant" {
		t.Fatalf("GET response=%+v", got)
	}
}

func TestAdminBillingSettingsAuditFailureRollsBackSetting(t *testing.T) {
	store := newAdminBillingSettingsStore()
	audit := &adminBillingSettingsAuditStore{err: errors.New("audit unavailable")}
	if _, err := store.UpsertStreamInputOnlyInterruptedPolicy(context.Background(), 7, billing.StreamInputOnlyInterruptedPolicyNoBill, "seed"); err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	handler := newAdminBillingSettingsTestRouter(AdminBillingSettingsDeps{
		Auth:         adminBillingAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Store:        store,
		AuditUpdater: newAdminBillingSettingsTestAuditUpdater(store, audit),
	})

	rec := serveAdminBillingJSON(t, handler, http.MethodPut, "/admin/v1/billing/settings", map[string]any{
		"tenant_id":                            7,
		"stream_input_only_interrupted_policy": "no_bill_record",
		"reason":                               "audit outage rollback",
	})

	assertHTTPStatus(t, rec, http.StatusServiceUnavailable)
	assertAdminBillingErrorCode(t, rec, "billing_settings_audit_failed")
	row, found, err := store.Get(context.Background(), 7, billing.StreamInputOnlyInterruptedPolicyKey)
	if err != nil {
		t.Fatalf("read setting after failed PUT: %v", err)
	}
	if !found || row.Value != "no_bill" || row.UpdatedBy != "seed" {
		t.Fatalf("setting after rollback=%+v found=%v, want original no_bill/seed", row, found)
	}
	if len(audit.records) != 0 {
		t.Fatalf("audit records persisted on rollback: %d", len(audit.records))
	}
}

func TestAdminBillingSettingsAuditPreviousValueTracksCommittedSetting(t *testing.T) {
	store := newAdminBillingSettingsStore()
	audit := &adminBillingSettingsAuditStore{}
	handler := newAdminBillingSettingsTestRouter(AdminBillingSettingsDeps{
		Auth:         adminBillingAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Store:        store,
		AuditUpdater: newAdminBillingSettingsTestAuditUpdater(store, audit),
	})

	first := serveAdminBillingJSON(t, handler, http.MethodPut, "/admin/v1/billing/settings", map[string]any{
		"tenant_id":                            7,
		"stream_input_only_interrupted_policy": "no_bill_record",
		"reason":                               "first setting",
	})
	assertHTTPStatus(t, first, http.StatusOK)
	second := serveAdminBillingJSON(t, handler, http.MethodPut, "/admin/v1/billing/settings", map[string]any{
		"tenant_id":                            7,
		"stream_input_only_interrupted_policy": "no_bill",
		"reason":                               "second setting",
	})
	assertHTTPStatus(t, second, http.StatusOK)

	if len(audit.records) != 2 {
		t.Fatalf("audit records=%d want 2", len(audit.records))
	}
	var payload map[string]string
	if err := json.Unmarshal(audit.records[1].Payload, &payload); err != nil {
		t.Fatalf("audit payload json: %v", err)
	}
	if payload["previous_value"] != "no_bill_record" || payload["previous_source"] != "tenant" ||
		payload["new_value"] != "no_bill" || payload["reason"] != "second setting" {
		t.Fatalf("second audit payload=%v", payload)
	}
}

func TestAdminBillingSettingsGetDefaultSource(t *testing.T) {
	handler := newAdminBillingSettingsTestRouter(AdminBillingSettingsDeps{
		Auth:  adminBillingAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Store: newAdminBillingSettingsStore(),
	})

	rec := serveAdminBillingJSON(t, handler, http.MethodGet, "/admin/v1/billing/settings?tenant_id=42", nil)
	assertHTTPStatus(t, rec, http.StatusOK)
	got := decodeAdminBillingResponse(t, rec)
	if got.Value != "no_bill" || got.Source != "default" || got.UpdatedAt != nil || got.UpdatedBy != nil {
		t.Fatalf("default response=%+v", got)
	}
	if !sameStrings(got.AllowedValues, []string{"no_bill", "no_bill_record"}) ||
		!sameStrings(got.RoadmapValues, []string{"bill_input"}) {
		t.Fatalf("value lists allowed=%v roadmap=%v", got.AllowedValues, got.RoadmapValues)
	}
}

func TestAdminBillingSettingsRejectsMissingTenantBeforeSettingsAccess(t *testing.T) {
	store := newAdminBillingSettingsStore()
	checker := &adminBillingTenantCheckerStub{exists: map[int64]bool{99: false}, defaultExists: true}
	updater := &adminBillingSettingsAuditUpdaterSpy{}
	handler := newAdminBillingSettingsTestRouter(AdminBillingSettingsDeps{
		Auth:          adminBillingAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Store:         store,
		TenantChecker: checker,
		AuditUpdater:  updater,
	})

	rec := serveAdminBillingJSON(t, handler, http.MethodGet, "/admin/v1/billing/settings?tenant_id=99", nil)
	assertHTTPStatus(t, rec, http.StatusNotFound)
	assertAdminBillingErrorCode(t, rec, "tenant_not_found")
	if store.getCalls != 0 {
		t.Fatalf("GET touched billing settings store %d times; want 0", store.getCalls)
	}

	rec = serveAdminBillingJSON(t, handler, http.MethodPut, "/admin/v1/billing/settings", map[string]any{
		"tenant_id":                            99,
		"stream_input_only_interrupted_policy": "no_bill_record",
		"reason":                               "unknown tenant must fail before write",
	})
	assertHTTPStatus(t, rec, http.StatusNotFound)
	assertAdminBillingErrorCode(t, rec, "tenant_not_found")
	if updater.called {
		t.Fatalf("PUT called audit updater for nonexistent tenant")
	}
	if store.upsertCalls != 0 || len(store.rows) != 0 {
		t.Fatalf("PUT changed settings store: upsertCalls=%d rows=%v", store.upsertCalls, store.rows)
	}
	if len(checker.calls) != 2 || checker.calls[0] != 99 || checker.calls[1] != 99 {
		t.Fatalf("tenant check calls=%v want [99 99]", checker.calls)
	}
}

func TestAdminBillingSettingsTenantOperatorScope(t *testing.T) {
	store := newAdminBillingSettingsStore()
	audit := &adminBillingSettingsAuditStore{}
	handler := newAdminBillingSettingsTestRouter(AdminBillingSettingsDeps{
		Auth:         adminBillingAuthStub{ident: admin.AdminIdentity{TokenID: 22, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store:        store,
		AuditUpdater: newAdminBillingSettingsTestAuditUpdater(store, audit),
	})

	rec := serveAdminBillingJSON(t, handler, http.MethodPut, "/admin/v1/billing/settings", map[string]any{
		"tenant_id":                            7,
		"stream_input_only_interrupted_policy": "no_bill_record",
		"reason":                               "own tenant update",
	})
	assertHTTPStatus(t, rec, http.StatusOK)

	rec = serveAdminBillingJSON(t, handler, http.MethodPut, "/admin/v1/billing/settings", map[string]any{
		"tenant_id":                            8,
		"stream_input_only_interrupted_policy": "no_bill",
		"reason":                               "cross tenant attempt",
	})
	assertHTTPStatus(t, rec, http.StatusForbidden)
	if _, ok := store.rows[8]; ok {
		t.Fatalf("cross-tenant PUT created row: %+v", store.rows[8])
	}

	rec = serveAdminBillingJSON(t, handler, http.MethodGet, "/admin/v1/billing/settings?tenant_id=8", nil)
	assertHTTPStatus(t, rec, http.StatusForbidden)
}

func TestAdminBillingSettingsValidationFailures(t *testing.T) {
	cases := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "invalid value",
			body:       map[string]any{"tenant_id": 7, "stream_input_only_interrupted_policy": "charge_everything", "reason": "bad value"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "billing_policy_value_invalid",
		},
		{
			name:       "roadmap value",
			body:       map[string]any{"tenant_id": 7, "stream_input_only_interrupted_policy": "bill_input", "reason": "future setting"},
			wantStatus: http.StatusConflict,
			wantCode:   "billing_policy_value_roadmap",
		},
		{
			name:       "missing reason",
			body:       map[string]any{"tenant_id": 7, "stream_input_only_interrupted_policy": "no_bill_record", "reason": "  "},
			wantStatus: http.StatusBadRequest,
			wantCode:   "billing_settings_reason_required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := newAdminBillingSettingsTestRouter(AdminBillingSettingsDeps{
				Auth:  adminBillingAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
				Store: newAdminBillingSettingsStore(),
			})
			rec := serveAdminBillingJSON(t, handler, http.MethodPut, "/admin/v1/billing/settings", tc.body)
			assertHTTPStatus(t, rec, tc.wantStatus)
			assertAdminBillingErrorCode(t, rec, tc.wantCode)
		})
	}
}

func TestAdminBillingSettingsRejectsCustomerAPIKeySurface(t *testing.T) {
	handler := newAdminBillingSettingsTestRouter(AdminBillingSettingsDeps{
		Auth:  adminBillingAuthStub{err: admin.ErrAdminUnauthorized},
		Store: newAdminBillingSettingsStore(),
	})

	rec := serveAdminBillingJSON(t, handler, http.MethodGet, "/admin/v1/billing/settings?tenant_id=7", nil)
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
	assertAdminBillingErrorCode(t, rec, "admin_unauthorized")
}

func TestAdminBillingSettingsAuditStoreMirrorsAuditChecks(t *testing.T) {
	store := &adminBillingSettingsAuditStore{}
	if _, err := store.InsertAdminAuditEvent(context.Background(), admindb.InsertAdminAuditEventParams{
		Action:     "unknown_action",
		TargetType: "billing_setting",
	}); err == nil {
		t.Fatalf("unknown action was accepted")
	}
	if _, err := store.InsertAdminAuditEvent(context.Background(), admindb.InsertAdminAuditEventParams{
		Action:     "update_billing_settings",
		TargetType: "unknown_target",
	}); err == nil {
		t.Fatalf("unknown target_type was accepted")
	}
	if len(store.records) != 0 {
		t.Fatalf("invalid audit records persisted: %d", len(store.records))
	}
}

func TestAdminBillingSettingsAuditUpdaterLocksBeforeReadingPreviousValue(t *testing.T) {
	store := newAdminBillingSettingsStore()
	audit := &adminBillingSettingsAuditStore{}
	var callOrder []string
	var lockCalls []dbbilling.AcquireBillingSettingLockParams
	updater := &adminBillingSettingsTxUpdater{
		runner: &adminBillingSettingsTestTxRunner{
			store:     store,
			audit:     audit,
			callOrder: &callOrder,
			lockCalls: &lockCalls,
		},
	}

	_, err := updater.UpsertStreamInputOnlyInterruptedPolicyWithAudit(context.Background(), AdminBillingSettingsAuditUpdate{
		TenantID:  7,
		Policy:    billing.StreamInputOnlyInterruptedPolicyNoBillRecord,
		UpdatedBy: "11",
		ActorID:   "11",
		ActorRole: admin.RolePlatformAdmin,
		Reason:    "first write must serialize",
	})
	if err != nil {
		t.Fatalf("upsert with audit: %v", err)
	}
	if len(callOrder) < 2 || callOrder[0] != "lock" || callOrder[1] != "get_for_update" {
		t.Fatalf("call order=%v want lock before get_for_update", callOrder)
	}
	if len(lockCalls) != 1 {
		t.Fatalf("lock calls=%d want 1", len(lockCalls))
	}
	if lockCalls[0].TenantID != 7 || lockCalls[0].SettingKey != billing.StreamInputOnlyInterruptedPolicyKey {
		t.Fatalf("lock params=%+v", lockCalls[0])
	}
}

type adminBillingAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s adminBillingAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type adminBillingTenantCheckerStub struct {
	exists        map[int64]bool
	defaultExists bool
	err           error
	calls         []int64
}

func (s *adminBillingTenantCheckerStub) AdminCheckTenantExists(_ context.Context, tenantID int64) (bool, error) {
	s.calls = append(s.calls, tenantID)
	if s.err != nil {
		return false, s.err
	}
	if s.exists != nil {
		if exists, ok := s.exists[tenantID]; ok {
			return exists, nil
		}
	}
	return s.defaultExists, nil
}

type adminBillingSettingsStore struct {
	rows        map[int64]billing.StoredBillingSetting
	nextID      int64
	now         time.Time
	getCalls    int
	upsertCalls int
}

func newAdminBillingSettingsStore() *adminBillingSettingsStore {
	return &adminBillingSettingsStore{
		rows: make(map[int64]billing.StoredBillingSetting),
		now:  time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC),
	}
}

func (s *adminBillingSettingsStore) Get(_ context.Context, tenantID int64, key string) (billing.StoredBillingSetting, bool, error) {
	s.getCalls++
	row, ok := s.rows[tenantID]
	if !ok || row.Key != key {
		return billing.StoredBillingSetting{}, false, nil
	}
	return row, true, nil
}

func (s *adminBillingSettingsStore) UpsertStreamInputOnlyInterruptedPolicy(_ context.Context, tenantID int64, policy billing.StreamInputOnlyInterruptedPolicy, updatedBy string) (billing.StoredBillingSetting, error) {
	s.upsertCalls++
	parsed, err := billing.ParseStreamInputOnlyInterruptedPolicy(policy.String())
	if err != nil {
		return billing.StoredBillingSetting{}, err
	}
	row, ok := s.rows[tenantID]
	if !ok {
		s.nextID++
		row.ID = s.nextID
	}
	row.TenantID = tenantID
	row.Key = billing.StreamInputOnlyInterruptedPolicyKey
	row.Value = parsed.String()
	row.UpdatedAt = s.now
	row.UpdatedBy = updatedBy
	s.rows[tenantID] = row
	return row, nil
}

type adminBillingSettingsAuditStore struct {
	records []admindb.InsertAdminAuditEventParams
	err     error
}

type adminBillingSettingsAuditUpdaterSpy struct {
	called bool
	result AdminBillingSettingsAuditUpdateResult
	err    error
}

func (s *adminBillingSettingsAuditUpdaterSpy) UpsertStreamInputOnlyInterruptedPolicyWithAudit(context.Context, AdminBillingSettingsAuditUpdate) (AdminBillingSettingsAuditUpdateResult, error) {
	s.called = true
	return s.result, s.err
}

var adminBillingSettingsAllowedAuditActions = map[string]struct{}{
	"issue_api_key":                    {},
	"revoke_api_key":                   {},
	"list_api_keys":                    {},
	"issue_admin_token":                {},
	"revoke_admin_token":               {},
	"admin_login":                      {},
	"create_provider_account":          {},
	"disable_provider_account":         {},
	"enable_provider_account":          {},
	"delete_provider_account":          {},
	"create_account_credential":        {},
	"rotate_account_credential":        {},
	"disable_account_credential":       {},
	"delete_account_credential":        {},
	"list_account_credentials":         {},
	"credential_acquisition_started":   {},
	"credential_acquisition_completed": {},
	"credential_acquisition_failed":    {},
	"credential_acquisition_cancelled": {},
	"update_billing_settings":          {},
}

var adminBillingSettingsAllowedAuditTargetTypes = map[string]struct{}{
	"api_key":            {},
	"admin_token":        {},
	"tenant":             {},
	"user":               {},
	"provider_account":   {},
	"account_credential": {},
	"billing_setting":    {},
}

func (s *adminBillingSettingsAuditStore) InsertAdminAuditEvent(_ context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	if s.err != nil {
		return admindb.InsertAdminAuditEventRow{}, s.err
	}
	if _, ok := adminBillingSettingsAllowedAuditActions[arg.Action]; !ok {
		return admindb.InsertAdminAuditEventRow{}, fmt.Errorf("admin_audit_events_action_check violation: %q", arg.Action)
	}
	if _, ok := adminBillingSettingsAllowedAuditTargetTypes[arg.TargetType]; !ok {
		return admindb.InsertAdminAuditEventRow{}, fmt.Errorf("admin_audit_events_target_type_check violation: %q", arg.TargetType)
	}
	s.records = append(s.records, arg)
	return admindb.InsertAdminAuditEventRow{ID: int64(len(s.records))}, nil
}

func newAdminBillingSettingsTestAuditUpdater(store *adminBillingSettingsStore, audit *adminBillingSettingsAuditStore) AdminBillingSettingsAuditUpdater {
	return &adminBillingSettingsTxUpdater{
		runner: &adminBillingSettingsTestTxRunner{
			store: store,
			audit: audit,
		},
	}
}

type adminBillingSettingsTestTxRunner struct {
	store     *adminBillingSettingsStore
	audit     *adminBillingSettingsAuditStore
	callOrder *[]string
	lockCalls *[]dbbilling.AcquireBillingSettingLockParams
}

func (r *adminBillingSettingsTestTxRunner) RunAdminBillingSettingsTx(ctx context.Context, fn func(context.Context, adminBillingSettingsBillingQueries, adminBillingSettingsAuditQueries) error) error {
	if r.store == nil || r.audit == nil {
		return errors.New("test billing settings tx runner dependency unset")
	}
	rows := make(map[int64]billing.StoredBillingSetting, len(r.store.rows))
	for tenantID, row := range r.store.rows {
		rows[tenantID] = row
	}
	billingQueries := &adminBillingSettingsTestBillingQueries{
		rows:      rows,
		nextID:    r.store.nextID,
		now:       r.store.now,
		callOrder: r.callOrder,
		lockCalls: r.lockCalls,
	}
	auditQueries := &adminBillingSettingsTestAuditQueries{
		records: append([]admindb.InsertAdminAuditEventParams(nil), r.audit.records...),
		err:     r.audit.err,
	}
	if err := fn(ctx, billingQueries, auditQueries); err != nil {
		return err
	}
	r.store.rows = billingQueries.rows
	r.store.nextID = billingQueries.nextID
	r.audit.records = auditQueries.records
	return nil
}

type adminBillingSettingsTestBillingQueries struct {
	rows      map[int64]billing.StoredBillingSetting
	nextID    int64
	now       time.Time
	callOrder *[]string
	lockCalls *[]dbbilling.AcquireBillingSettingLockParams
}

func (q *adminBillingSettingsTestBillingQueries) AcquireBillingSettingLock(_ context.Context, arg dbbilling.AcquireBillingSettingLockParams) error {
	q.appendCall("lock")
	if q.lockCalls != nil {
		*q.lockCalls = append(*q.lockCalls, arg)
	}
	return nil
}

func (q *adminBillingSettingsTestBillingQueries) GetBillingSettingForUpdate(_ context.Context, arg dbbilling.GetBillingSettingForUpdateParams) (dbbilling.BillingSetting, error) {
	q.appendCall("get_for_update")
	row, ok := q.rows[arg.TenantID]
	if !ok || row.Key != arg.SettingKey {
		return dbbilling.BillingSetting{}, pgx.ErrNoRows
	}
	return adminBillingSettingsDBRowFromStored(row), nil
}

func (q *adminBillingSettingsTestBillingQueries) UpsertBillingSetting(_ context.Context, arg dbbilling.UpsertBillingSettingParams) (dbbilling.BillingSetting, error) {
	q.appendCall("upsert")
	if arg.SettingKey == billing.StreamInputOnlyInterruptedPolicyKey && !validAdminBillingSettingsValue(arg.SettingValue) {
		return dbbilling.BillingSetting{}, errors.New("billing_settings setting_value check violation")
	}
	row, ok := q.rows[arg.TenantID]
	if !ok || row.Key != arg.SettingKey {
		q.nextID++
		row.ID = q.nextID
	}
	row.TenantID = arg.TenantID
	row.Key = arg.SettingKey
	row.Value = arg.SettingValue
	row.UpdatedAt = q.now
	row.UpdatedBy = arg.UpdatedBy
	q.rows[arg.TenantID] = row
	return adminBillingSettingsDBRowFromStored(row), nil
}

func (q *adminBillingSettingsTestBillingQueries) appendCall(name string) {
	if q.callOrder != nil {
		*q.callOrder = append(*q.callOrder, name)
	}
}

type adminBillingSettingsTestAuditQueries struct {
	records []admindb.InsertAdminAuditEventParams
	err     error
}

func (q *adminBillingSettingsTestAuditQueries) InsertAdminAuditEvent(ctx context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	store := &adminBillingSettingsAuditStore{
		records: q.records,
		err:     q.err,
	}
	row, err := store.InsertAdminAuditEvent(ctx, arg)
	if err != nil {
		return admindb.InsertAdminAuditEventRow{}, err
	}
	q.records = store.records
	return row, nil
}

func adminBillingSettingsDBRowFromStored(row billing.StoredBillingSetting) dbbilling.BillingSetting {
	return dbbilling.BillingSetting{
		ID:           row.ID,
		TenantID:     row.TenantID,
		SettingKey:   row.Key,
		SettingValue: row.Value,
		UpdatedAt: pgtype.Timestamptz{
			Time:  row.UpdatedAt,
			Valid: !row.UpdatedAt.IsZero(),
		},
		UpdatedBy: row.UpdatedBy,
	}
}

func validAdminBillingSettingsValue(value string) bool {
	switch billing.StreamInputOnlyInterruptedPolicy(value) {
	case billing.StreamInputOnlyInterruptedPolicyNoBill, billing.StreamInputOnlyInterruptedPolicyNoBillRecord:
		return true
	default:
		return false
	}
}

func newAdminBillingSettingsTestRouter(deps AdminBillingSettingsDeps) http.Handler {
	if deps.TenantChecker == nil {
		deps.TenantChecker = &adminBillingTenantCheckerStub{defaultExists: true}
	}
	r := chi.NewRouter()
	r.Route("/admin/v1/billing", func(r chi.Router) {
		MountAdminBillingSettingsRoutes(r, deps)
	})
	return r
}

func serveAdminBillingJSON(t *testing.T, h http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeAdminBillingResponse(t *testing.T, rec *httptest.ResponseRecorder) adminBillingSettingsResponse {
	t.Helper()
	var got adminBillingSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	return got
}

func assertAdminBillingErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if !strings.Contains(rec.Body.String(), `"code":"`+want+`"`) {
		t.Fatalf("body=%s want error code %q", rec.Body.String(), want)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertHTTPStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("状态=%d，期望=%d，响应=%s", rec.Code, want, rec.Body.String())
	}
}
