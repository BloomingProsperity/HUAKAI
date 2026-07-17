package provideraccountrecoveryhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type authStub struct {
	identity admin.AdminIdentity
	err      error
}

func (s authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.identity, s.err
}

type accountStoreStub struct {
	row admindb.AdminProviderAccountRow
	err error
	arg admindb.GetAdminProviderAccountParams
}

func (s *accountStoreStub) GetAdminProviderAccount(_ context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	s.arg = arg
	return s.row, s.err
}

type credentialStoreStub struct {
	rows      []credentialstore.CredentialMetadata
	err       error
	tenantID  int64
	accountID int64
}

func (s *credentialStoreStub) ListByAccount(_ context.Context, tenantID, accountID int64) ([]credentialstore.CredentialMetadata, error) {
	s.tenantID, s.accountID = tenantID, accountID
	return s.rows, s.err
}

type channelHealthStub struct {
	records map[string]channelhealth.Record
	err     error
	keys    []channelhealth.ChannelKey
}

func (s *channelHealthStub) GetRecord(_ context.Context, key channelhealth.ChannelKey) (channelhealth.Record, error) {
	s.keys = append(s.keys, key)
	if s.err != nil {
		return channelhealth.Record{}, s.err
	}
	rec, ok := s.records[key.StableChannelID()]
	if !ok {
		return channelhealth.Record{}, channelhealth.ErrNotFound
	}
	return rec, nil
}

func TestRecoveryActionsPlatformAdminMapsProblemStates(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	resetAt := now.Add(20 * time.Minute)
	failed := "refresh_failed"
	accountStore := &accountStoreStub{row: admindb.AdminProviderAccountRow{
		ID: 91, TenantID: 7, Enabled: false, HealthState: "degraded", CredentialState: "needs_rotation",
		RateLimitResetAt: pgtype.Timestamptz{Time: resetAt, Valid: true},
	}}
	credentialStore := &credentialStoreStub{rows: []credentialstore.CredentialMetadata{
		{
			ID: 501, TenantID: 7, ProviderAccountID: 91, Vendor: credentialstore.VendorAnthropic,
			AuthMode: credentialstore.AuthModeClaudeCode, State: credentialstore.StateNeedsRotation, Version: 4,
		},
		{
			ID: 502, TenantID: 7, ProviderAccountID: 91, Vendor: credentialstore.VendorOpenAI,
			AuthMode: credentialstore.AuthModeCodexCLIOAuth, State: credentialstore.StateActive, Version: 2,
			LastRefreshOutcome: &failed, FailureCount: 1,
		},
	}}
	channelStore := channelStoreWith(
		channelhealth.Record{
			Key: channelhealth.ChannelKey{
				TenantID: 7, ProviderAccountID: 91, Vendor: credentialstore.VendorAnthropic,
				AccountCredentialID: 501, CredentialVersion: 4,
			},
			State: channelhealth.StateManualPaused, ReasonClass: channelhealth.SignalManualOverride,
		},
		channelhealth.Record{
			Key: channelhealth.ChannelKey{
				TenantID: 7, ProviderAccountID: 91, Vendor: credentialstore.VendorOpenAI,
				AccountCredentialID: 502, CredentialVersion: 2,
			},
			State: channelhealth.StateDisabled, ReasonClass: channelhealth.SignalCredentialRevoked,
		},
	)
	deps := Deps{
		Auth:          authStub{identity: admin.AdminIdentity{Role: admin.RolePlatformAdmin}},
		Accounts:      accountStore,
		Credentials:   credentialStore,
		ChannelHealth: channelStore,
		Now:           func() time.Time { return now },
	}

	rec := serveRecovery(t, deps, "/admin/v1/provider-accounts/91/recovery-actions?tenant_id=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if !resp.RequiresAction {
		t.Fatal("问题账号必须标记 requires_action=true")
	}
	if accountStore.arg.ID != 91 || accountStore.arg.TenantID != 7 {
		t.Fatalf("账号查询作用域=%+v，期望 tenant/account=7/91", accountStore.arg)
	}
	if credentialStore.tenantID != 7 || credentialStore.accountID != 91 {
		t.Fatalf("凭据查询作用域=%d/%d，期望 7/91", credentialStore.tenantID, credentialStore.accountID)
	}
	if len(channelStore.keys) != 2 {
		t.Fatalf("渠道健康精确查询次数=%d，期望每个当前凭据各一次", len(channelStore.keys))
	}
	for _, key := range channelStore.keys {
		if key.TenantID != 7 || key.ProviderAccountID != 91 {
			t.Fatalf("渠道健康查询作用域=%+v，期望 tenant/account=7/91", key)
		}
	}
	if len(resp.Credentials) != 2 {
		t.Fatalf("credentials=%d，期望 2", len(resp.Credentials))
	}
	if len(resp.Channels) != 2 {
		t.Fatalf("channels=%+v，期望两个当前凭据各有一条", resp.Channels)
	}

	enable := assertAction(t, resp.Actions, "enable_account", 91, true, true, "account_disabled", "low")
	assertRequiredFields(t, enable, "enabled")
	clear := assertAction(t, resp.Actions, "clear_account_backoff", 91, true, true, "account_backoff_active", "medium")
	assertRequiredFields(t, clear)
	rotateState := assertAction(t, resp.Actions, "rotate_credential", 501, true, true, "credential_needs_rotation", "high")
	assertRequiredFields(t, rotateState, "tenant_id", "credentials")
	rotateRefresh := assertAction(t, resp.Actions, "rotate_credential", 502, true, true, "credential_refresh_failed", "high")
	assertRequiredFields(t, rotateRefresh, "tenant_id", "credentials")
	resume := assertAction(t, resp.Actions, "resume_channel", 501, true, true, "channel_manual_paused", "medium")
	assertRequiredFields(t, resume, "tenant_id", "vendor", "account_credential_id", "credential_version", "reason")
	force := assertAction(t, resp.Actions, "force_channel_active", 501, true, false, "channel_manual_paused", "high")
	assertRequiredFields(t, force, "tenant_id", "vendor", "account_credential_id", "credential_version", "reason")
	if force.Path != "/admin/v1/provider-accounts/91/channel-health/force-active" {
		t.Fatalf("force path=%q", force.Path)
	}
	assertAction(t, resp.Actions, "force_channel_active", 502, true, false, "channel_disabled", "high")
	if got := len(resp.Actions); got != 7 {
		t.Fatalf("actions=%d，期望 7，实际=%+v", got, resp.Actions)
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"encrypted_payload", "plaintext", "manual pause secret", "refresh-token"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("响应泄露禁止字段/文本 %q：%s", forbidden, body)
		}
	}
}

func TestRecoveryActionsTenantOperatorSeesRoleBlockedActions(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	accountStore := &accountStoreStub{row: admindb.AdminProviderAccountRow{
		ID: 91, TenantID: 7, Enabled: false, HealthState: "degraded", CredentialState: "revoked",
	}}
	credentialStore := &credentialStoreStub{rows: []credentialstore.CredentialMetadata{{
		ID: 501, TenantID: 7, ProviderAccountID: 91, Vendor: credentialstore.VendorAnthropic,
		AuthMode: credentialstore.AuthModeClaudeCode, State: credentialstore.StateRevoked, Version: 4,
	}}}
	channelStore := channelStoreWith(channelhealth.Record{
		Key: channelhealth.ChannelKey{
			TenantID: 7, ProviderAccountID: 91, Vendor: credentialstore.VendorAnthropic,
			AccountCredentialID: 501, CredentialVersion: 4,
		},
		State: channelhealth.StateManualPaused,
	})
	deps := Deps{
		Auth: authStub{identity: admin.AdminIdentity{
			Role: admin.RoleTenantOperator, ScopeTenantID: 7,
		}},
		Accounts: accountStore, Credentials: credentialStore, ChannelHealth: channelStore,
		Now: func() time.Time { return now },
	}

	rec := serveRecovery(t, deps, "/admin/v1/provider-accounts/91/recovery-actions")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	assertAction(t, resp.Actions, "enable_account", 91, true, true, "account_disabled", "low")
	rotate := assertAction(t, resp.Actions, "rotate_credential", 501, false, true, "credential_revoked", "high")
	if rotate.RequiredRole != admin.RolePlatformAdmin || rotate.Available {
		t.Fatalf("tenant operator 的 rotate 权限必须明确被挡：%+v", rotate)
	}
	resume := assertAction(t, resp.Actions, "resume_channel", 501, false, true, "channel_manual_paused", "medium")
	if resume.Available {
		t.Fatalf("tenant operator 不得被告知 channel resume 可直接执行：%+v", resume)
	}
}

func TestRecoveryActionsHealthyAccountReturnsNoActions(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	deps := Deps{
		Auth: authStub{identity: admin.AdminIdentity{
			Role: admin.RoleTenantOperator, ScopeTenantID: 7,
		}},
		Accounts: &accountStoreStub{row: admindb.AdminProviderAccountRow{
			ID: 91, TenantID: 7, Enabled: true, HealthState: "healthy", CredentialState: "active",
		}},
		Credentials: &credentialStoreStub{rows: []credentialstore.CredentialMetadata{{
			ID: 501, TenantID: 7, ProviderAccountID: 91, Vendor: credentialstore.VendorAnthropic,
			AuthMode: credentialstore.AuthModeAPIKey, State: credentialstore.StateActive, Version: 1, FailureCount: 1,
		}}},
		ChannelHealth: channelStoreWith(channelhealth.Record{
			Key: channelhealth.ChannelKey{
				TenantID: 7, ProviderAccountID: 91, Vendor: credentialstore.VendorAnthropic,
				AccountCredentialID: 501, CredentialVersion: 1,
			},
			State: channelhealth.StateActive,
		}),
		Now: func() time.Time { return now },
	}

	rec := serveRecovery(t, deps, "/admin/v1/provider-accounts/91/recovery-actions")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if resp.RequiresAction || len(resp.Actions) != 0 {
		t.Fatalf("只有历史失败计数的健康账号不得产生恢复动作：requires=%v actions=%+v", resp.RequiresAction, resp.Actions)
	}
}

func TestRecoveryActionsAllowsMissingChannelRecord(t *testing.T) {
	deps := Deps{
		Auth: authStub{identity: admin.AdminIdentity{
			Role: admin.RoleTenantOperator, ScopeTenantID: 7,
		}},
		Accounts: &accountStoreStub{row: admindb.AdminProviderAccountRow{
			ID: 91, TenantID: 7, Enabled: true,
		}},
		Credentials:   &credentialStoreStub{},
		ChannelHealth: &channelHealthStub{},
	}
	rec := serveRecovery(t, deps, "/admin/v1/provider-accounts/91/recovery-actions")
	if rec.Code != http.StatusOK {
		t.Fatalf("无 channel record 应正常返回，status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if len(resp.Channels) != 0 {
		t.Fatalf("channels=%+v，期望空数组", resp.Channels)
	}
}

func TestRecoveryActionsIgnoresHistoricalChannelAndRefreshableAccessExpiry(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Minute)
	current := credentialstore.CredentialMetadata{
		ID: 501, TenantID: 7, ProviderAccountID: 91, Vendor: credentialstore.VendorOpenAI,
		AuthMode: credentialstore.AuthModeChatGPTOAuth, State: credentialstore.StateActive, Version: 4,
		AccessExpiresAt: &expiredAt,
	}
	historical := channelhealth.Record{
		Key: channelhealth.ChannelKey{
			TenantID: 7, ProviderAccountID: 91, Vendor: credentialstore.VendorOpenAI,
			AccountCredentialID: 501, CredentialVersion: 3,
		},
		State: channelhealth.StateManualPaused,
	}
	deps := Deps{
		Auth: authStub{identity: admin.AdminIdentity{
			Role: admin.RolePlatformAdmin, ScopeTenantID: 7,
		}},
		Accounts: &accountStoreStub{row: admindb.AdminProviderAccountRow{
			ID: 91, TenantID: 7, Enabled: true, HealthState: "healthy", CredentialState: "active",
		}},
		Credentials:   &credentialStoreStub{rows: []credentialstore.CredentialMetadata{current}},
		ChannelHealth: channelStoreWith(historical),
		Now:           func() time.Time { return now },
	}

	rec := serveRecovery(t, deps, "/admin/v1/provider-accounts/91/recovery-actions")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if resp.RequiresAction || len(resp.Actions) != 0 || len(resp.Channels) != 0 {
		t.Fatalf("历史 channel 和可刷新 access token 到期不得生成恢复动作：%+v", resp)
	}
}

func TestCredentialRotationStateDistinguishesRefreshableAccessExpiry(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Minute)
	refreshable := credentialstore.CredentialMetadata{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		State: credentialstore.StateActive, AccessExpiresAt: &expiredAt,
	}
	if applicable, reason := credentialRotationState(refreshable, now); applicable {
		t.Fatalf("可刷新 OAuth access token 到期不得建议人工 rotate，reason=%s", reason)
	}
	static := credentialstore.CredentialMetadata{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey,
		State: credentialstore.StateActive, AccessExpiresAt: &expiredAt,
	}
	applicable, reason := credentialRotationState(static, now)
	if !applicable || reason != "credential_access_expired" {
		t.Fatalf("不可刷新静态凭据到期必须建议 rotate：applicable=%v reason=%s", applicable, reason)
	}
}

func TestRecoveryActionsScopesTenantAndSanitizesFailures(t *testing.T) {
	base := Deps{
		Accounts:      &accountStoreStub{row: admindb.AdminProviderAccountRow{ID: 91, TenantID: 7}},
		Credentials:   &credentialStoreStub{},
		ChannelHealth: &channelHealthStub{},
	}
	cases := []struct {
		name   string
		auth   admin.AdminIdentity
		target string
		status int
		code   string
	}{
		{
			name:   "全局平台管理员必须显式租户",
			auth:   admin.AdminIdentity{Role: admin.RolePlatformAdmin},
			target: "/admin/v1/provider-accounts/91/recovery-actions",
			status: http.StatusBadRequest, code: "tenant_id_required",
		},
		{
			name:   "租户管理员拒绝跨租户查询",
			auth:   admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7},
			target: "/admin/v1/provider-accounts/91/recovery-actions?tenant_id=8",
			status: http.StatusForbidden, code: "admin_forbidden",
		},
		{
			name:   "固定作用域平台管理员拒绝跨租户查询",
			auth:   admin.AdminIdentity{Role: admin.RolePlatformAdmin, ScopeTenantID: 7},
			target: "/admin/v1/provider-accounts/91/recovery-actions?tenant_id=8",
			status: http.StatusForbidden, code: "admin_forbidden",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := base
			deps.Auth = authStub{identity: tc.auth}
			rec := serveRecovery(t, deps, tc.target)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("status=%d body=%s，期望 %d/%s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
		})
	}

	t.Run("账号不存在", func(t *testing.T) {
		deps := base
		deps.Auth = authStub{identity: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}}
		deps.Accounts = &accountStoreStub{err: pgx.ErrNoRows}
		rec := serveRecovery(t, deps, "/admin/v1/provider-accounts/91/recovery-actions")
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "provider_account_not_found") {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("依赖错误脱敏", func(t *testing.T) {
		deps := base
		deps.Auth = authStub{identity: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}}
		deps.Credentials = &credentialStoreStub{err: errors.New("refresh-token=secret-value")}
		rec := serveRecovery(t, deps, "/admin/v1/provider-accounts/91/recovery-actions")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "secret-value") || !strings.Contains(rec.Body.String(), "provider_account_recovery_unavailable") {
			t.Fatalf("错误响应未脱敏：%s", rec.Body.String())
		}
	})
}

func serveRecovery(t *testing.T, deps Deps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func channelStoreWith(records ...channelhealth.Record) *channelHealthStub {
	store := &channelHealthStub{records: make(map[string]channelhealth.Record, len(records))}
	for _, rec := range records {
		store.records[rec.Key.StableChannelID()] = rec
	}
	return store
}

func assertAction(t *testing.T, actions []Action, action string, subjectID int64, authorized, recommended bool, reason, risk string) Action {
	t.Helper()
	for _, item := range actions {
		if item.Action != action || item.SubjectID != subjectID {
			continue
		}
		if !item.StateApplicable || item.Authorized != authorized || item.Available != authorized ||
			item.Recommended != recommended || item.ReasonCode != reason || item.RiskLevel != risk {
			t.Fatalf("action %s/%d=%+v，期望 authorized=%v recommended=%v reason=%s risk=%s",
				action, subjectID, item, authorized, recommended, reason, risk)
		}
		return item
	}
	t.Fatalf("缺少 action %s/%d，实际=%+v", action, subjectID, actions)
	return Action{}
}

func assertRequiredFields(t *testing.T, action Action, want ...string) {
	t.Helper()
	if len(action.RequiredFields) != len(want) {
		t.Fatalf("action %s required_fields=%v，期望 %v", action.Action, action.RequiredFields, want)
	}
	for i := range want {
		if action.RequiredFields[i] != want[i] {
			t.Fatalf("action %s required_fields=%v，期望 %v", action.Action, action.RequiredFields, want)
		}
	}
}
