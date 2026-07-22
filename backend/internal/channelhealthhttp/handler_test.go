package channelhealthhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
)

type adminAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s adminAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

func platformAdmin() adminAuthStub {
	return adminAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}}
}

type channelHealthControllerStub struct {
	key             channelhealth.ChannelKey
	actorID         string
	reason          string
	called          string
	response        channelhealth.Record
	summary         channelhealth.ChannelHealthSummary
	summaryTenantID int64
	err             error
}

func (s *channelHealthControllerStub) ListChannelHealth(context.Context, int64, int, int) ([]channelhealth.ChannelHealthState, error) {
	return nil, nil
}

func (s *channelHealthControllerStub) GetChannelHealth(context.Context, int64, string) (channelhealth.ChannelHealthState, []channelhealth.AuditEvent, error) {
	return channelhealth.ChannelHealthState{}, nil, channelhealth.ErrNotFound
}

func (s *channelHealthControllerStub) SummarizeChannelHealth(_ context.Context, tenantID int64) (channelhealth.ChannelHealthSummary, error) {
	s.called, s.summaryTenantID = "summary", tenantID
	return s.summary, s.err
}

func (s *channelHealthControllerStub) ManualPause(_ context.Context, key channelhealth.ChannelKey, actorID, reason string) (channelhealth.Record, error) {
	s.called, s.key, s.actorID, s.reason = "pause", key, actorID, reason
	if s.err != nil {
		return channelhealth.Record{}, s.err
	}
	if s.response.State == "" {
		s.response = channelhealth.Record{Key: key, State: channelhealth.StateManualPaused, ReasonClass: channelhealth.SignalManualOverride}
	}
	return s.response, nil
}

func (s *channelHealthControllerStub) ManualResume(_ context.Context, key channelhealth.ChannelKey, actorID, reason string) (channelhealth.Record, error) {
	s.called, s.key, s.actorID, s.reason = "resume", key, actorID, reason
	return channelhealth.Record{Key: key, State: channelhealth.StateRamping, RampStagePct: 1, ReasonClass: channelhealth.SignalManualOverride}, s.err
}

func (s *channelHealthControllerStub) ForceActive(_ context.Context, key channelhealth.ChannelKey, actorID, reason string) (channelhealth.Record, error) {
	s.called, s.key, s.actorID, s.reason = "force-active", key, actorID, reason
	return channelhealth.Record{Key: key, State: channelhealth.StateActive, ReasonClass: channelhealth.SignalManualOverride}, s.err
}

func TestChannelHealthAdminPause(t *testing.T) {
	ctrl := &channelHealthControllerStub{}
	rec := invokeChannelHealthAdmin(t, ctrl, platformAdmin(), http.MethodPost,
		"/v1/admin/pool-accounts/101/channel-health/pause",
		`{"tenant_id":7,"vendor":"openai","account_credential_id":9001,"credential_version":1,"reason":"ops pause"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ctrl.called != "pause" || ctrl.key.ProviderAccountID != 101 || ctrl.key.TenantID != 7 ||
		ctrl.key.AccountCredentialID != 9001 || ctrl.reason != "ops pause" || ctrl.actorID != "admin_token:11" {
		t.Fatalf("controller call mismatch: %+v", ctrl)
	}
	if !strings.Contains(rec.Body.String(), `"state":"manual_paused"`) {
		t.Fatalf("response body=%s", rec.Body.String())
	}
}

func TestChannelHealthAdminRejectsUnauthorizedAndMissingReason(t *testing.T) {
	ctrl := &channelHealthControllerStub{}
	rec := invokeChannelHealthAdmin(t, ctrl, adminAuthStub{err: admin.ErrAdminUnauthorized}, http.MethodPost,
		"/v1/admin/pool-accounts/101/channel-health/force-active",
		`{"tenant_id":7,"vendor":"openai","account_credential_id":9001,"credential_version":1,"reason":"break glass"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ctrl.called != "" {
		t.Fatalf("unauthorized touched controller: %+v", ctrl)
	}
	rec = invokeChannelHealthAdmin(t, ctrl, platformAdmin(), http.MethodPost,
		"/v1/admin/pool-accounts/101/channel-health/force-active",
		`{"tenant_id":7,"vendor":"openai","account_credential_id":9001,"credential_version":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing reason status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAT_CH_002_014_ChannelHealthListPaginationTenantScope(t *testing.T) {
	store := channelhealth.NewMemoryStore()
	svc := channelhealth.NewService(store, channelhealth.DefaultPolicy(), nil)
	now := time.Now().UTC()
	upsertChannelHealthRecord(t, store, 7, "openai", 101, 9001, 1, now.Add(-time.Minute))
	wantSecond := upsertChannelHealthRecord(t, store, 7, "anthropic", 102, 9002, 1, now.Add(-2*time.Minute))
	upsertChannelHealthRecord(t, store, 8, "openai", 201, 9901, 1, now)

	rec := invokeChannelHealthReadAdmin(t, svc, platformAdmin(), http.MethodGet,
		"/v1/admin/channel-health?tenant_id=7&limit=1&offset=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			TenantID  int64  `json:"tenant_id"`
			ChannelID string `json:"channel_id"`
		} `json:"items"`
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if body.Limit != 1 || body.Offset != 1 || len(body.Items) != 1 {
		t.Fatalf("pagination mismatch: %+v", body)
	}
	if body.Items[0].TenantID != 7 || body.Items[0].ChannelID != wantSecond.Key.StableChannelID() {
		t.Fatalf("tenant-scoped list mismatch: %+v", body.Items)
	}
}

func TestChannelHealthSummaryHandler_CountsByState(t *testing.T) {
	// 变异:把 /summary 注册到 /{channel_id} 之后、按错误的字段分组、或把所有行塌缩成同一个 state;精确的不均计数与总数会变红。
	store := channelhealth.NewMemoryStore()
	svc := channelhealth.NewService(store, channelhealth.DefaultPolicy(), nil)
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	olderCooldown := now.Add(30 * time.Minute)
	newerCooldown := now.Add(90 * time.Minute)
	upsertChannelHealthRecordWithState(t, store, 7, "openai", 101, 9001, 1, channelhealth.StateActive, now.Add(-time.Minute), nil)
	upsertChannelHealthRecordWithState(t, store, 7, "anthropic", 102, 9002, 1, channelhealth.StateActive, now.Add(-2*time.Minute), nil)
	upsertChannelHealthRecordWithState(t, store, 7, "gemini", 103, 9003, 1, channelhealth.StateCoolingDown, now.Add(-3*time.Minute), &olderCooldown)
	upsertChannelHealthRecordWithState(t, store, 7, "openai", 104, 9004, 1, channelhealth.StateDisabled, now.Add(-4*time.Minute), &newerCooldown)
	upsertChannelHealthRecordWithState(t, store, 7, "anthropic", 105, 9005, 1, channelhealth.StateManualPaused, now.Add(-5*time.Minute), nil)

	rec := invokeChannelHealthReadAdmin(t, svc, platformAdmin(), http.MethodGet,
		"/v1/admin/channel-health/summary?tenant_id=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		ByState          map[string]int64 `json:"by_state"`
		Total            int64            `json:"total"`
		OldestCooldownAt *time.Time       `json:"oldest_cooldown_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode summary response: %v", err)
	}
	want := map[string]int64{
		"active":        2,
		"degraded":      0,
		"cooling_down":  1,
		"ramping":       0,
		"disabled":      1,
		"manual_paused": 1,
	}
	if body.Total != 5 {
		t.Fatalf("total=%d want 5; body=%+v", body.Total, body)
	}
	for state, count := range want {
		if body.ByState[state] != count {
			t.Fatalf("by_state[%s]=%d want %d; all=%+v", state, body.ByState[state], count, body.ByState)
		}
	}
	if body.OldestCooldownAt == nil || !body.OldestCooldownAt.Equal(olderCooldown) {
		t.Fatalf("oldest_cooldown_at=%v want %s", body.OldestCooldownAt, olderCooldown.Format(time.RFC3339))
	}
}

func TestChannelHealthSummary_AdminAuthRequired(t *testing.T) {
	// 变异:放行 tenant_operator/非 admin 角色,或在鉴权之前就调用 summary 控制器;这会返回 200 或记录一次控制器调用。
	ctrl := &channelHealthControllerStub{summary: channelhealth.ChannelHealthSummary{
		ByState: map[channelhealth.HealthState]int64{channelhealth.StateActive: 1},
		Total:   1,
	}}
	rec := invokeChannelHealthReadAdmin(t, ctrl, adminAuthStub{ident: admin.AdminIdentity{TokenID: 12, Role: admin.RoleTenantOperator}}, http.MethodGet,
		"/v1/admin/channel-health/summary?tenant_id=7")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ctrl.called != "" {
		t.Fatalf("non-admin reached controller: %+v", ctrl)
	}
}

func TestAT_CH_002_015_ChannelHealthDetailWithRedactedAuditEvents(t *testing.T) {
	store := channelhealth.NewMemoryStore()
	svc := channelhealth.NewService(store, channelhealth.DefaultPolicy(), nil)
	now := time.Now().UTC()
	rec := upsertChannelHealthRecord(t, store, 7, "openai", 101, 9001, 1, now)
	if err := store.AppendAudit(context.Background(), channelhealth.AuditEvent{
		Type:          channelhealth.EventManualOverride,
		Key:           rec.Key,
		PreviousState: channelhealth.StateActive,
		NewState:      channelhealth.StateManualPaused,
		ReasonClass:   channelhealth.SignalManualOverride,
		PolicyVersion: "channel-health-v1",
		ActorID:       "11",
		Payload:       map[string]any{"tenant_id": int64(7), "api_key": "secret", "request_body": "raw"},
		OccurredAt:    now,
	}); err != nil {
		t.Fatalf("append audit: %v", err)
	}

	detail := invokeChannelHealthReadAdmin(t, svc, platformAdmin(), http.MethodGet,
		"/v1/admin/channel-health/"+rec.Key.StableChannelID()+"?tenant_id=7")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var body struct {
		State struct {
			TenantID  int64  `json:"tenant_id"`
			ChannelID string `json:"channel_id"`
		} `json:"state"`
		AuditEvents []struct {
			Payload map[string]any `json:"payload"`
		} `json:"audit_events"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if body.State.TenantID != 7 || body.State.ChannelID != rec.Key.StableChannelID() || len(body.AuditEvents) != 1 {
		t.Fatalf("detail mismatch: %+v", body)
	}
	if _, ok := body.AuditEvents[0].Payload["api_key"]; ok {
		t.Fatalf("audit payload leaked api_key: %+v", body.AuditEvents[0].Payload)
	}
	if _, ok := body.AuditEvents[0].Payload["request_body"]; ok {
		t.Fatalf("audit payload leaked request_body: %+v", body.AuditEvents[0].Payload)
	}
	if _, ok := body.AuditEvents[0].Payload["tenant_id"]; !ok {
		t.Fatalf("audit payload lost safe tenant_id: %+v", body.AuditEvents[0].Payload)
	}

	crossTenant := invokeChannelHealthReadAdmin(t, svc, platformAdmin(), http.MethodGet,
		"/v1/admin/channel-health/"+rec.Key.StableChannelID()+"?tenant_id=8")
	if crossTenant.Code != http.StatusNotFound {
		t.Fatalf("cross tenant status=%d body=%s", crossTenant.Code, crossTenant.Body.String())
	}
}

func invokeChannelHealthAdmin(t *testing.T, ctrl ChannelHealthController, auth ChannelHealthAdminAuth, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/v1/admin/pool-accounts", func(r chi.Router) {
		MountChannelHealthAdminRoutes(r, ChannelHealthAdminDeps{Auth: auth, Controller: ctrl})
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func invokeChannelHealthReadAdmin(t *testing.T, ctrl ChannelHealthController, auth ChannelHealthAdminAuth, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/v1/admin/channel-health", func(r chi.Router) {
		MountChannelHealthReadAdminRoutes(r, ChannelHealthAdminDeps{Auth: auth, Controller: ctrl})
	})
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func upsertChannelHealthRecord(t *testing.T, store *channelhealth.MemoryStore, tenantID int64, vendor string, providerAccountID, credentialID int64, credentialVersion int, updatedAt time.Time) channelhealth.Record {
	t.Helper()
	return upsertChannelHealthRecordWithState(t, store, tenantID, vendor, providerAccountID, credentialID, credentialVersion, channelhealth.StateActive, updatedAt, nil)
}

func upsertChannelHealthRecordWithState(t *testing.T, store *channelhealth.MemoryStore, tenantID int64, vendor string, providerAccountID, credentialID int64, credentialVersion int, state channelhealth.HealthState, updatedAt time.Time, cooldownUntil *time.Time) channelhealth.Record {
	t.Helper()
	key := channelhealth.ChannelKey{
		TenantID:            tenantID,
		Vendor:              vendor,
		ProviderAccountID:   providerAccountID,
		AccountCredentialID: credentialID,
		CredentialVersion:   credentialVersion,
	}
	rec, err := store.UpsertRecord(context.Background(), channelhealth.Record{
		Key:              key,
		State:            state,
		Score:            100,
		ReasonClass:      channelhealth.SignalNone,
		Confidence:       channelhealth.ConfidenceObserved,
		CooldownUntil:    cooldownUntil,
		PolicyVersion:    "channel-health-v1",
		StateEnteredAt:   updatedAt.Add(-time.Minute),
		LastTransitionAt: updatedAt.Add(-time.Minute),
		UpdatedAt:        updatedAt,
		CreatedAt:        updatedAt.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("upsert channel health record: %v", err)
	}
	return rec
}
