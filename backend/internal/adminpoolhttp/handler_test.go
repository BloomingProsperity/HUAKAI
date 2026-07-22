package adminpoolhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/provideraccountrecovery"
)

// adminAuditActionWhitelist 镜像已迁移的 admin_audit_events_action_check CHECK
// 约束(最新 = 迁移 0141)。该 stub 强制执行它,这样一个发出不在实时白名单中
// action 的 handler 会以与对真实 DB 相同的方式失败(SQLSTATE 23514),而不是静默
// 通过 —— 正是这一点把原先不加区分的 stub 变成一道能抓住「action 未列入白名单 ->
// 503 audit_write_failed」潜在缺陷的守卫。
var adminAuditActionWhitelist = map[string]struct{}{
	"issue_api_key": {}, "revoke_api_key": {}, "list_api_keys": {},
	"issue_admin_token": {}, "revoke_admin_token": {}, "admin_login": {},
	"create_provider_account": {}, "disable_provider_account": {},
	"enable_provider_account": {}, "delete_provider_account": {},
	"create_account_credential": {}, "rotate_account_credential": {},
	"disable_account_credential": {}, "delete_account_credential": {},
	"list_account_credentials":       {},
	"credential_acquisition_started": {}, "credential_acquisition_completed": {},
	"credential_acquisition_failed": {}, "credential_acquisition_cancelled": {},
	"update_billing_settings": {},
	"create_pool_group":       {}, "update_pool_group": {},
	"update_platform_settings": {},
	"unlock_user":              {}, "force_disable_2fa": {}, "reset_passkey": {},
	"set_user_group": {}, "set_user_remark": {},
	"set_user_status": {}, "create_user": {}, "delete_user": {},
	"create_quota_policy": {}, "update_quota_policy": {}, "delete_quota_policy": {},
	"clear_provider_account_rate_limit": {}, "update_provider_account": {},
}

type adminPoolAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s adminPoolAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type adminPoolStoreStub struct {
	insertID         int64
	insert           *admindb.InsertProviderAccountParams
	providerFamilies map[int64]string
	listArg          *admindb.ListAdminProviderAccountsParams
	list             []admindb.AdminProviderAccountRow
	getArg           *admindb.GetAdminProviderAccountParams
	get              *admindb.AdminProviderAccountRow
	getErr           error
	updateFull       *admindb.UpdateAdminProviderAccountParams
	update           *admindb.UpdateProviderAccountEnabledParams
	delete           *admindb.SoftDeleteProviderAccountParams
	audits           []admindb.InsertAdminAuditEventParams
}

func (s *adminPoolStoreStub) GetProviderProtocolForAccountCreate(_ context.Context, arg admindb.GetProviderProtocolForAccountCreateParams) (string, error) {
	if family, ok := s.providerFamilies[arg.ProviderID]; ok {
		return family, nil
	}
	return "openai_chat", nil
}

type adminPoolCredentialWriterStub struct {
	input *credentialstore.CreateCredentialInput
	id    int64
}

type adminPoolRateLimitRecoveryStub struct {
	input        *provideraccountrecovery.ClearRateLimitInput
	recoverInput *provideraccountrecovery.RecoverAccountInput
	result       provideraccountrecovery.ClearRateLimitResult
	err          error
}

type allowAdminPoolCapability struct{}

func (allowAdminPoolCapability) Allowed(context.Context, int64, string) (bool, error) {
	return true, nil
}

type adminPoolCapabilityStub struct {
	allowed bool
	err     error
}

func (s adminPoolCapabilityStub) Allowed(context.Context, int64, string) (bool, error) {
	return s.allowed, s.err
}

func (s *adminPoolRateLimitRecoveryStub) ClearRateLimit(_ context.Context, in provideraccountrecovery.ClearRateLimitInput) (provideraccountrecovery.ClearRateLimitResult, error) {
	s.input = &in
	if s.result.Account.ID == 0 {
		s.result.Account = adminProviderRow(in.AccountID, in.TenantID)
	}
	return s.result, s.err
}

func (s *adminPoolRateLimitRecoveryStub) RecoverAccountState(_ context.Context, in provideraccountrecovery.RecoverAccountInput) (provideraccountrecovery.RecoverAccountResult, error) {
	s.recoverInput = &in
	if s.result.Account.ID == 0 {
		s.result.Account = adminProviderRow(in.AccountID, in.TenantID)
	}
	return s.result, s.err
}

func (s *adminPoolCredentialWriterStub) Create(_ context.Context, in credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error) {
	s.input = &in
	id := s.id
	if id == 0 {
		id = 9001
	}
	return credentialstore.CredentialMetadata{
		ID: id, TenantID: in.TenantID, ProviderAccountID: in.ProviderAccountID,
		Vendor: credentialstore.Normalize(in.Vendor), AuthMode: credentialstore.Normalize(in.AuthMode),
		State: credentialstore.StateActive, Version: 1,
	}, nil
}

// WithTransaction 仅供满足接口；单元桩不驱动真事务，原子建号（账号+凭据+审计单事务回滚）
// 由 admin_pool_accounts_create_atomic_integration_test.go 用真 PG 覆盖。
func (s *adminPoolCredentialWriterStub) WithTransaction(context.Context, func(*credentialstore.Store, db.DBTX) error) error {
	return errors.New("adminPoolCredentialWriterStub 不支持事务建号")
}

func (s *adminPoolStoreStub) InsertProviderAccount(_ context.Context, arg admindb.InsertProviderAccountParams) (int64, error) {
	s.insert = &arg
	if s.insertID == 0 {
		return 101, nil
	}
	return s.insertID, nil
}

func (s *adminPoolStoreStub) ListAdminProviderAccounts(_ context.Context, arg admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
	s.listArg = &arg
	return s.list, nil
}

func (s *adminPoolStoreStub) GetAdminProviderAccount(_ context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	s.getArg = &arg
	if s.getErr != nil {
		return admindb.AdminProviderAccountRow{}, s.getErr
	}
	if s.get != nil {
		return *s.get, nil
	}
	id := arg.ID
	if id == 0 {
		id = s.insertID
	}
	if id == 0 {
		id = 101
	}
	if s.insert != nil {
		return adminProviderRowFromInsert(id, *s.insert), nil
	}
	return adminProviderRow(id, arg.TenantID), nil
}

func (s *adminPoolStoreStub) UpdateAdminProviderAccount(_ context.Context, arg admindb.UpdateAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	s.updateFull = &arg
	// 基线优先取 seed(s.get):模拟 UPDATE...RETURNING 在既有行上就地改,
	// 未提交字段保持原值(高级字段"改一保余"语义要求)。
	row := adminProviderRow(arg.ID, arg.TenantID)
	if s.get != nil {
		row = *s.get
		row.ID = arg.ID
		row.TenantID = arg.TenantID
	}
	if arg.Enabled != nil {
		row.Enabled = *arg.Enabled
	}
	if arg.Priority != nil {
		row.Priority = *arg.Priority
	}
	if arg.StaticWeight != nil {
		row.StaticWeight = *arg.StaticWeight
	}
	if arg.SetUpstreamCostRatio {
		row.UpstreamCostRatio = arg.UpstreamCostRatio
	}
	if arg.CapConcurrency != nil {
		row.CapConcurrency = *arg.CapConcurrency
	}
	if arg.SetProbeModel {
		row.ProbeModel = arg.ProbeModel
	}
	if arg.SetTags {
		row.Tags = arg.Tags
	}
	if arg.SetExtra {
		row.Extra = arg.Extra
	}
	if arg.SetModelAllowList {
		row.ModelAllowList = arg.ModelAllowList
	}
	if arg.SetCapabilityFlags {
		row.CapabilityFlags = arg.CapabilityFlags
	}
	// 高级字段:指针型非 nil=已提交则改;可空型按 Set-flag 改(含清空)。
	if arg.RPMLimit != nil {
		row.RPMLimit = *arg.RPMLimit
	}
	if arg.TPMLimit != nil {
		row.TPMLimit = *arg.TPMLimit
	}
	if arg.WindowCostLimitCents != nil {
		row.WindowCostLimitCents = *arg.WindowCostLimitCents
	}
	if arg.MaxSessions != nil {
		row.MaxSessions = *arg.MaxSessions
	}
	if arg.DisableCooling != nil {
		row.DisableCooling = *arg.DisableCooling
	}
	if arg.SetRefreshLeadSeconds {
		row.RefreshLeadSeconds = arg.RefreshLeadSeconds
	}
	if arg.SetExpiresAt {
		row.ExpiresAt = arg.ExpiresAt
	}
	if arg.TLSFingerprintRotate != nil {
		row.TLSFingerprintRotate = *arg.TLSFingerprintRotate
	}
	if arg.CustomErrorCodesEnabled != nil {
		row.CustomErrorCodesEnabled = *arg.CustomErrorCodesEnabled
	}
	if arg.SetCustomErrorCodes {
		row.CustomErrorCodes = arg.CustomErrorCodes
	}
	if arg.PoolMode != nil {
		row.PoolMode = *arg.PoolMode
	}
	if arg.TempUnschedulableEnabled != nil {
		row.TempUnschedulableEnabled = *arg.TempUnschedulableEnabled
	}
	if arg.SetTempUnschedulableRules {
		row.TempUnschedulableRules = arg.TempUnschedulableRulesJSON
	}
	if arg.SetProxyID {
		row.ProxyID = arg.ProxyID
	}
	if arg.SetProxyGroupID {
		row.ProxyGroupID = arg.ProxyGroupID
	}
	s.get = &row
	return row, nil
}

func (s *adminPoolStoreStub) UpdateProviderAccountEnabled(_ context.Context, arg admindb.UpdateProviderAccountEnabledParams) error {
	s.update = &arg
	return nil
}

func (s *adminPoolStoreStub) SoftDeleteProviderAccount(_ context.Context, arg admindb.SoftDeleteProviderAccountParams) error {
	s.delete = &arg
	return nil
}

func (s *adminPoolStoreStub) InsertAdminAuditEvent(_ context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	if _, ok := adminAuditActionWhitelist[arg.Action]; !ok {
		// 复现一个不在白名单中的 action 对 Postgres 触发的真实 CHECK 违反,
		// 使 handler 测试不再掩盖 audit-write 缺陷。
		return admindb.InsertAdminAuditEventRow{}, &pgconn.PgError{
			Code:           "23514",
			ConstraintName: "admin_audit_events_action_check",
			Message:        "new row for relation \"admin_audit_events\" violates check constraint \"admin_audit_events_action_check\"",
		}
	}
	s.audits = append(s.audits, arg)
	return admindb.InsertAdminAuditEventRow{ID: int64(len(s.audits))}, nil
}

func TestAdminPoolAccounts_CreateRejectsLegacyInlineCredential(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"legacy","account_type":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "vendor and auth_mode are required") {
		t.Fatalf("status=%d body=%s，期望在落库前拒绝 legacy 内联凭据", rec.Code, rec.Body.String())
	}
	if store.insert != nil {
		t.Fatalf("legacy 内联凭据不得写 provider_accounts：%+v", store.insert)
	}
}

func TestAdminPoolAccounts_CreateRejectsOAuthOnlyModeBeforeTransaction(t *testing.T) {
	req := createProviderAccountRequest{
		ProviderID: 8, ChannelID: 9, Name: "claude-oauth", AccountType: "oauth",
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		Credentials: json.RawMessage(`{"access_token":"forged","refresh_token":"forged"}`),
	}
	if err := validateCreateProviderAccount(req); err == nil || !strings.Contains(err.Error(), "dedicated acquisition flow") {
		t.Fatalf("纯 OAuth 模式必须拒绝通用直建,err=%v", err)
	}
	req.AccountType = "api_key"
	req.Vendor = credentialstore.VendorOpenAI
	req.AuthMode = credentialstore.AuthModeAPIKey
	req.Credentials = json.RawMessage(`{"api_key":"sk-test"}`)
	if err := validateCreateProviderAccount(req); err != nil {
		t.Fatalf("可证明为粘贴来源的官钥应通过直建校验: %v", err)
	}
}

func TestAdminPoolAccounts_CreateRequiresTenantGrantAndPlatformOwnership(t *testing.T) {
	body := `{"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`
	store := &adminPoolStoreStub{}
	rec := invokeAdminPoolWithDeps(t, AdminPoolAccountDeps{
		Auth: providerAccountAdmin(), Store: store, Credentials: &adminPoolCredentialWriterStub{},
		Capabilities: adminPoolCapabilityStub{allowed: false},
	}, http.MethodPost, "/admin/v1/provider-accounts", body)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "tenant_capability_not_granted") {
		t.Fatalf("未授权租户 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil {
		t.Fatalf("未授权租户不得开始建号:%+v", store.insert)
	}

	rec = invokeAdminPoolWithDeps(t, AdminPoolAccountDeps{
		Auth: adminPoolAdmin(), Store: store, Credentials: &adminPoolCredentialWriterStub{},
		PlatformTenantID: 7,
	}, http.MethodPost, "/admin/v1/provider-accounts?tenant_id=8", body)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "cross_tenant_account_admin_forbidden") {
		t.Fatalf("部署者代下级租户建号 status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPoolAccounts_CreateFailsClosedWithoutCredentialStore(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	rec := invokeAdminPoolWithCredentialStore(t, store, nil, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "gateway_not_configured") {
		t.Fatalf("status=%d body=%s，期望凭据存储未接线时 fail closed", rec.Code, rec.Body.String())
	}
	if store.insert != nil {
		t.Fatalf("凭据存储未接线不得先建账号：%+v", store.insert)
	}
}

// TestAdminPoolAccounts_ClaudeSessionRejectsAPIKeyBeforeInsert 咬住配置面防线：
// API-key 账号不能挂到 session provider，且拒绝发生在账号行与凭据写入之前。
func TestAdminPoolAccounts_ClaudeSessionRejectsAPIKeyBeforeInsert(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77, providerFamilies: map[int64]string{8: "anthropic_claude_session"}}
	credentials := &adminPoolCredentialWriterStub{id: 88}
	rec := invokeAdminPoolWithCredentialStore(t, store, credentials, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"wrong-key","account_type":"api_key","vendor":"anthropic","auth_mode":"api_key","credentials":{"api_key":"sk-ant"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil || credentials.input != nil {
		t.Fatalf("不兼容账号不得落库或写凭据: insert=%+v credential=%+v", store.insert, credentials.input)
	}
}

func TestAdminPoolAccounts_Unauthorized(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, adminPoolAuthStub{err: admin.ErrAdminUnauthorized}, http.MethodPost,
		"/admin/v1/provider-accounts", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil || len(store.audits) != 0 {
		t.Fatalf("unauthorized request touched store: %+v audits=%d", store.insert, len(store.audits))
	}
}

func TestAdminPoolAccounts_NonAdminForbidden(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 12, Role: admin.RoleTenantOperator}}, http.MethodPost,
		"/admin/v1/provider-accounts", `{}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil || len(store.audits) != 0 {
		t.Fatalf("non-admin request touched store: %+v audits=%d", store.insert, len(store.audits))
	}
}

func TestAdminPoolAccounts_DisableUpdatesAndAudits(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77/enabled",
		`{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.update == nil || store.update.ID != 77 || store.update.TenantID != 7 || store.update.Enabled {
		t.Fatalf("update params mismatch: %+v", store.update)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "disable_provider_account" ||
		store.audits[0].Reason == nil || *store.audits[0].Reason != "禁用 provider account" {
		t.Fatalf("disable audit mismatch: %+v", store.audits)
	}
}

func TestAdminPoolAccounts_DeleteSoftDeletesAndAudits(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodDelete, "/admin/v1/provider-accounts/77",
		`{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.delete == nil || store.delete.ID != 77 || store.delete.TenantID != 7 {
		t.Fatalf("delete params mismatch: %+v", store.delete)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "delete_provider_account" ||
		store.audits[0].Reason == nil || *store.audits[0].Reason != "删除 provider account" {
		t.Fatalf("delete audit mismatch: %+v", store.audits)
	}
}

func TestAdminPoolAccounts_ListProviderAccountsPaginated(t *testing.T) {
	probeAt := time.Date(2026, 7, 16, 7, 0, 0, 0, time.UTC)
	observedAt := time.Date(2026, 7, 16, 7, 4, 0, 0, time.UTC)
	window5hStart := time.Date(2099, 7, 19, 3, 0, 0, 0, time.UTC)
	window5hEnd := window5hStart.Add(5 * time.Hour)
	window7dStart := time.Date(2099, 7, 13, 8, 0, 0, 0, time.UTC)
	window7dEnd := window7dStart.Add(7 * 24 * time.Hour)
	first := adminProviderRow(77, 7)
	first.LastProbeAt = pgtype.Timestamptz{Time: probeAt, Valid: true}
	first.LastRequestObservedAt = pgtype.Timestamptz{Time: observedAt, Valid: true}
	first.SessionWindow5hStart = pgtype.Timestamptz{Time: window5hStart, Valid: true}
	first.SessionWindow5hEnd = pgtype.Timestamptz{Time: window5hEnd, Valid: true}
	first.SessionWindow5hUtilization = adminProviderNumeric(t, 37.5)
	first.SessionWindow7dStart = pgtype.Timestamptz{Time: window7dStart, Valid: true}
	first.SessionWindow7dEnd = pgtype.Timestamptz{Time: window7dEnd, Valid: true}
	first.SessionWindow7dUtilization = adminProviderNumeric(t, 62.25)
	first.SubscriptionVendor = testStringPtr("openai")
	first.SubscriptionPlan = testStringPtr("pro")
	first.SubscriptionRawPlan = testStringPtr("pro")
	first.SubscriptionScope = testStringPtr("personal")
	first.SubscriptionSource = testStringPtr("id_token_claim")
	first.SubscriptionTrust = testStringPtr("unverified_token")
	first.SubscriptionVerification = testStringPtr("unverified")
	first.SubscriptionStatus = testStringPtr("observed")
	first.SubscriptionMappingVersion = testInt32Ptr(1)
	first.SubscriptionFirstObservedAt = pgtype.Timestamptz{Time: observedAt.Add(-time.Hour), Valid: true}
	first.SubscriptionObservedAt = pgtype.Timestamptz{Time: observedAt, Valid: true}
	first.SubscriptionChangedAt = pgtype.Timestamptz{Time: observedAt, Valid: true}
	store := &adminPoolStoreStub{list: []admindb.AdminProviderAccountRow{
		first,
		adminProviderRow(78, 7),
	}}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodGet, "/admin/v1/provider-accounts?limit=1&state_filter=active&pool_group_id=9&tag=prod&system_label=OpenAI:Pro&subscription_scope=personal&subscription_status=observed&subscription_source=id_token_claim", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.listArg == nil || store.listArg.TenantID != 7 || store.listArg.LimitCount != 2 ||
		store.listArg.StateFilter != "active" || store.listArg.PoolGroupID != 9 || store.listArg.TagFilter != "prod" ||
		store.listArg.SubscriptionVendorFilter != "openai" || store.listArg.SubscriptionPlanFilter != "pro" ||
		store.listArg.SubscriptionScopeFilter != "personal" || store.listArg.SubscriptionStatusFilter != "observed" ||
		store.listArg.SubscriptionSourceFilter != "id_token_claim" {
		t.Fatalf("list arg mismatch: %+v", store.listArg)
	}
	var response struct {
		Items []providerAccountResponse `json:"items"`
		Page  struct {
			HasMore    bool    `json:"has_more"`
			NextCursor *string `json:"next_cursor"`
		} `json:"page"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].ID != 77 || !response.Page.HasMore || response.Page.NextCursor == nil {
		t.Fatalf("unexpected list response: %+v", response)
	}
	if response.Items[0].LastProbeAt == nil || !response.Items[0].LastProbeAt.Equal(probeAt) {
		t.Fatalf("列表 last_probe_at=%v want %v", response.Items[0].LastProbeAt, probeAt)
	}
	if response.Items[0].LastRequestObservedAt == nil || !response.Items[0].LastRequestObservedAt.Equal(observedAt) {
		t.Fatalf("列表 last_request_observed_at=%v want %v", response.Items[0].LastRequestObservedAt, observedAt)
	}
	if response.Items[0].ObservationSource != "request_completion_event" {
		t.Fatalf("列表 last_request_observation_source=%q want request_completion_event", response.Items[0].ObservationSource)
	}
	if response.Items[0].Subscription == nil || response.Items[0].Subscription.Label != "openai:pro" ||
		response.Items[0].Subscription.Plan != "pro" || response.Items[0].Subscription.Status != "observed" ||
		len(response.Items[0].SystemLabels) != 1 || response.Items[0].SystemLabels[0] != "openai:pro" {
		t.Fatalf("列表套餐投影不完整：%+v", response.Items[0])
	}
	if strings.Join(response.Items[0].Tags, ",") == "openai:pro" {
		t.Fatalf("系统标签不应写入人工 tags：%+v", response.Items[0])
	}
	if got := response.Items[0].QuotaWindows.FiveHour; got.State != "active" ||
		got.RemainingPercent == nil || *got.RemainingPercent != 62.5 ||
		got.ResetsAt == nil || !got.ResetsAt.Equal(window5hEnd) {
		t.Fatalf("列表 5h 配额进度=%+v，期望一次列表请求即可得到剩余比例和重置时间", got)
	}
	if got := response.Items[0].QuotaWindows.SevenDay; got.State != "active" ||
		got.RemainingPercent == nil || *got.RemainingPercent != 37.75 ||
		got.ResetsAt == nil || !got.ResetsAt.Equal(window7dEnd) {
		t.Fatalf("列表 7d 配额进度=%+v，期望一次列表请求即可得到剩余比例和重置时间", got)
	}
	if strings.Contains(rec.Body.String(), "temp_unschedulable_rules") {
		t.Fatalf("列表响应不应携带详情规则：%s", rec.Body.String())
	}
}

func TestAdminPoolAccounts_ListRejectsInvalidQuotaProjection(t *testing.T) {
	row := adminProviderRow(77, 7)
	row.QuotaFacts = []byte(`{"state":`)
	store := &adminPoolStoreStub{list: []admindb.AdminProviderAccountRow{row}}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodGet, "/admin/v1/provider-accounts", "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "provider_account_quota_projection_invalid") {
		t.Fatalf("损坏额度投影必须明确失败：status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPoolAccounts_RejectsConflictingSubscriptionFilters(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodGet,
		"/admin/v1/provider-accounts?system_label=openai:pro&subscription_plan=plus", "")
	if rec.Code != http.StatusBadRequest || store.listArg != nil {
		t.Fatalf("冲突筛选必须在查库前拒绝：status=%d body=%s arg=%+v", rec.Code, rec.Body.String(), store.listArg)
	}
}

func TestAdminPoolAccounts_GetProviderAccount(t *testing.T) {
	row := adminProviderRow(77, 7)
	probeModel := "gpt-4o-mini-probe"
	row.StaticWeight = 4
	row.ProbeModel = &probeModel
	row.Tags = []string{"prod", "blue"}
	row.Extra = []byte(`{"azure_api_version":"2024-08-01"}`)
	row.TempUnschedulableRules = []byte(`[{"error_code":529,"keywords":["busy"],"duration_minutes":5,"description":"拥塞"}]`)
	probeAt := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	observedAt := time.Date(2026, 7, 16, 8, 5, 0, 0, time.UTC)
	row.LastProbeAt = pgtype.Timestamptz{Time: probeAt, Valid: true}
	row.LastRequestObservedAt = pgtype.Timestamptz{Time: observedAt, Valid: true}
	store := &adminPoolStoreStub{get: &row}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodGet, "/admin/v1/provider-accounts/77", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.getArg == nil || store.getArg.ID != 77 || store.getArg.TenantID != 7 {
		t.Fatalf("get arg mismatch: %+v", store.getArg)
	}
	var body providerAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if body.StaticWeight != 4 || body.ProbeModel == nil || *body.ProbeModel != probeModel {
		t.Fatalf("additional fields static_weight=%d probe_model=%v", body.StaticWeight, body.ProbeModel)
	}
	if strings.Join(body.Tags, ",") != "prod,blue" {
		t.Fatalf("tags response=%v want [prod blue]", body.Tags)
	}
	if !strings.Contains(string(body.Extra), `"azure_api_version":"2024-08-01"`) {
		t.Fatalf("extra response=%s", string(body.Extra))
	}
	if body.LastProbeAt == nil || !body.LastProbeAt.Equal(probeAt) {
		t.Fatalf("last_probe_at=%v want %v", body.LastProbeAt, probeAt)
	}
	if body.LastRequestObservedAt == nil || !body.LastRequestObservedAt.Equal(observedAt) {
		t.Fatalf("last_request_observed_at=%v want %v", body.LastRequestObservedAt, observedAt)
	}
	if body.ObservationSource != "request_completion_event" {
		t.Fatalf("last_request_observation_source=%q want request_completion_event", body.ObservationSource)
	}
	var rules []struct {
		ErrorCode       int      `json:"error_code"`
		Keywords        []string `json:"keywords"`
		DurationMinutes int      `json:"duration_minutes"`
		Description     string   `json:"description"`
	}
	if err := json.Unmarshal(body.TempUnschedulableRules, &rules); err != nil {
		t.Fatalf("temp_unschedulable_rules 响应无效: %v raw=%s", err, body.TempUnschedulableRules)
	}
	if len(rules) != 1 || rules[0].ErrorCode != 529 || rules[0].DurationMinutes != 5 || rules[0].Description != "拥塞" {
		t.Fatalf("temp_unschedulable_rules=%+v", rules)
	}
}

func TestAdminPoolAccounts_GetRejectsInvalidQuotaProjection(t *testing.T) {
	row := adminProviderRow(77, 7)
	row.QuotaFacts = []byte(`[{"metric_key":"model_quota"}`)
	store := &adminPoolStoreStub{get: &row}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodGet, "/admin/v1/provider-accounts/77", "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "provider_account_quota_projection_invalid") {
		t.Fatalf("损坏额度投影必须明确失败：status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPoolAccounts_UpdateProviderAccountFull(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"enabled":true,"priority":5,"cap_concurrency":9,"static_weight":4,"probe_model":"claude-probe","tags":["prod"],"extra":{"claude_beta_query":"true"},"model_allow_list":[" claude "],"capability_flags":["tool"],"custom_error_codes_enabled":true,"custom_error_codes":[429],"pool_mode":true,"temp_unschedulable_enabled":true,"temp_unschedulable_rules":[{"rule_id":"busy-529","error_code":529,"keywords":["busy"],"duration_minutes":5}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.updateFull == nil || store.updateFull.ID != 77 || store.updateFull.TenantID != 7 ||
		store.updateFull.Priority == nil || *store.updateFull.Priority != 5 ||
		store.updateFull.CapConcurrency == nil || *store.updateFull.CapConcurrency != 9 ||
		store.updateFull.StaticWeight == nil || *store.updateFull.StaticWeight != 4 ||
		!store.updateFull.SetProbeModel || store.updateFull.ProbeModel == nil || *store.updateFull.ProbeModel != "claude-probe" ||
		!store.updateFull.SetTags || store.updateFull.Tags[0] != "prod" ||
		!store.updateFull.SetExtra || !strings.Contains(string(store.updateFull.Extra), `"claude_beta_query":"true"`) ||
		!store.updateFull.SetModelAllowList || store.updateFull.ModelAllowList[0] != "claude" ||
		!store.updateFull.SetCustomErrorCodes || store.updateFull.CustomErrorCodes[0] != 429 ||
		!store.updateFull.SetTempUnschedulableRules {
		t.Fatalf("update arg mismatch: %+v", store.updateFull)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "update_provider_account" {
		t.Fatalf("update audit mismatch: %+v", store.audits)
	}
}

func TestAdminPoolAccounts_ClearRateLimit(t *testing.T) {
	store := &adminPoolStoreStub{}
	recovery := &adminPoolRateLimitRecoveryStub{
		result: provideraccountrecovery.ClearRateLimitResult{
			Account:        adminProviderRow(77, 7),
			Channel:        &channelhealth.Record{State: channelhealth.StateRamping},
			ChannelChanged: true,
		},
	}
	rec := invokeAdminPoolWithDeps(t, AdminPoolAccountDeps{
		Auth: providerAccountAdmin(), Store: store, RateLimitRecovery: recovery,
	}, http.MethodPost, "/admin/v1/provider-accounts/77/clear-rate-limit", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if recovery.input == nil || recovery.input.AccountID != 77 || recovery.input.TenantID != 7 ||
		recovery.input.ActorID != "admin_token:11" || recovery.input.ActorRole != admin.RoleTenantOperator {
		t.Fatalf("recovery input mismatch: %+v", recovery.input)
	}
	var body providerAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode clear-rate-limit response: %v body=%s", err, rec.Body.String())
	}
	if body.ID != 77 || body.RateLimitRecovery == nil ||
		!body.RateLimitRecovery.AccountBackoffCleared ||
		!body.RateLimitRecovery.ChannelRecordFound ||
		!body.RateLimitRecovery.ChannelChanged ||
		body.RateLimitRecovery.ChannelState != channelhealth.StateRamping {
		t.Fatalf("clear-rate-limit response mismatch: %+v", body)
	}
}

func TestAdminPoolAccounts_ClearRateLimitPartialRecoveryIsExplicit(t *testing.T) {
	store := &adminPoolStoreStub{}
	recovery := &adminPoolRateLimitRecoveryStub{err: provideraccountrecovery.ErrPartialRecovery}
	rec := invokeAdminPoolWithDeps(t, AdminPoolAccountDeps{
		Auth: providerAccountAdmin(), Store: store, RateLimitRecovery: recovery,
	}, http.MethodPost, "/admin/v1/provider-accounts/77/clear-rate-limit", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("partial recovery status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "provider_account_recovery_partial") {
		t.Fatalf("partial recovery code missing: %s", rec.Body.String())
	}
}

func TestAdminPoolAccounts_ClearRateLimitFailsClosedWhenRecoveryUnset(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPoolWithDeps(t, AdminPoolAccountDeps{
		Auth: providerAccountAdmin(), Store: store,
	}, http.MethodPost, "/admin/v1/provider-accounts/77/clear-rate-limit", "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "gateway_not_configured") {
		t.Fatalf("unset recovery status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminPoolAccounts_CrossTenantBodyRejected(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"tenant_id":8,"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil || len(store.audits) != 0 {
		t.Fatalf("cross-tenant request touched store: %+v audits=%d", store.insert, len(store.audits))
	}
}

func TestAdminPoolAccounts_GlobalAdminWithTenantQueryOperatesTargetTenant(t *testing.T) {
	// 全局 platform_admin(无隐式 scope)通过 ?tenant_id=9 指名 tenant 9,
	// 列表必须针对 tenant 9 运行 —— 「不」能静默回退到 tenant 1。
	store := &adminPoolStoreStub{list: []admindb.AdminProviderAccountRow{adminProviderRow(77, 9)}}
	rec := invokeAdminPool(t, store, adminPoolAdmin(), http.MethodGet,
		"/admin/v1/provider-accounts?tenant_id=9", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.listArg == nil || store.listArg.TenantID != 9 {
		t.Fatalf("global admin tenant_id=9 must scope list to tenant 9, got %+v", store.listArg)
	}
}

func TestAdminPoolAccounts_GlobalAdminWithoutTenantQueryRejected(t *testing.T) {
	// 没有显式 tenant_id 时,全局 platform_admin 必须被拒绝(不静默默认为
	// tenant 1),且 store 必须保持未被触碰。
	store := &adminPoolStoreStub{list: []admindb.AdminProviderAccountRow{adminProviderRow(77, 1)}}
	rec := invokeAdminPool(t, store, adminPoolAdmin(), http.MethodGet,
		"/admin/v1/provider-accounts", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing tenant_id must be 400, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.listArg != nil {
		t.Fatalf("rejected request must not query the store, got %+v", store.listArg)
	}
}

func adminPoolAdmin() adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}}
}

func providerAccountAdmin() adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RoleTenantOperator, ScopeTenantID: 7}}
}

func invokeAdminPool(t *testing.T, store *adminPoolStoreStub, auth AdminPoolAccountAuth, method, target, body string) *httptest.ResponseRecorder {
	return invokeAdminPoolWithCredentialStore(t, store, &adminPoolCredentialWriterStub{}, auth, method, target, body)
}

func invokeAdminPoolWithCredentialStore(t *testing.T, store *adminPoolStoreStub, credentials AdminPoolAccountCredentialWriter, auth AdminPoolAccountAuth, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return invokeAdminPoolWithDeps(t, AdminPoolAccountDeps{
		Auth: auth, Store: store, Credentials: credentials,
		RateLimitRecovery: &adminPoolRateLimitRecoveryStub{}, Capabilities: allowAdminPoolCapability{},
		PlatformTenantID: 7,
	}, method, target, body)
}

func invokeAdminPoolWithDeps(t *testing.T, deps AdminPoolAccountDeps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountAdminPoolAccountRoutes(r, deps)
	})
	r.Route("/v1/admin/pool-accounts", func(r chi.Router) {
		MountAdminPoolAccountRoutes(r, deps)
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func adminProviderRow(id, tenantID int64) admindb.AdminProviderAccountRow {
	return admindb.AdminProviderAccountRow{
		ID: id, TenantID: tenantID, ProviderID: 8, ChannelID: 9, Name: "acct",
		AccountType: "api_key", Enabled: true, HealthState: "operational", CredentialState: "valid",
		CapConcurrency: 4, Priority: 100, StaticWeight: 1, TokenVersion: 1, OAuthEndpointHealth: "operational",
		ModelAllowList: []string{}, CapabilityFlags: []string{}, Tags: []string{}, Extra: []byte(`{}`), CustomErrorCodes: []int32{},
	}
}

func adminProviderNumeric(t *testing.T, value float64) pgtype.Numeric {
	t.Helper()
	var result pgtype.Numeric
	if err := result.Scan(strconv.FormatFloat(value, 'f', -1, 64)); err != nil {
		t.Fatalf("构造 provider account numeric: %v", err)
	}
	return result
}

func testStringPtr(value string) *string {
	return &value
}

func testInt32Ptr(value int32) *int32 {
	return &value
}

func adminProviderRowFromInsert(id int64, in admindb.InsertProviderAccountParams) admindb.AdminProviderAccountRow {
	row := adminProviderRow(id, in.TenantID)
	row.ProviderID = in.ProviderID
	row.ChannelID = in.ChannelID
	row.Name = in.Name
	row.AccountType = in.AccountType
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if in.CapConcurrency != nil {
		row.CapConcurrency = *in.CapConcurrency
	}
	if in.Priority != nil {
		row.Priority = *in.Priority
	}
	if in.StaticWeight != nil {
		row.StaticWeight = *in.StaticWeight
	}
	row.UpstreamCostRatio = in.UpstreamCostRatio
	row.ProbeModel = in.ProbeModel
	row.Tags = in.Tags
	row.Extra = in.Extra
	row.ModelAllowList = in.ModelAllowList
	row.CapabilityFlags = in.CapabilityFlags
	// 高级字段 arg→row 往返(模拟真实 INSERT...RETURNING),使 create 回显生效。
	if in.RPMLimit != nil {
		row.RPMLimit = *in.RPMLimit
	}
	if in.TPMLimit != nil {
		row.TPMLimit = *in.TPMLimit
	}
	if in.WindowCostLimitCents != nil {
		row.WindowCostLimitCents = *in.WindowCostLimitCents
	}
	if in.MaxSessions != nil {
		row.MaxSessions = *in.MaxSessions
	}
	if in.DisableCooling != nil {
		row.DisableCooling = *in.DisableCooling
	}
	if in.RefreshLeadSeconds != nil {
		row.RefreshLeadSeconds = in.RefreshLeadSeconds
	}
	if in.ExpiresAt.Valid {
		row.ExpiresAt = in.ExpiresAt
	}
	if in.TLSFingerprintRotate != nil {
		row.TLSFingerprintRotate = *in.TLSFingerprintRotate
	}
	if in.CustomErrorCodesEnabled != nil {
		row.CustomErrorCodesEnabled = *in.CustomErrorCodesEnabled
	}
	if in.CustomErrorCodes != nil {
		row.CustomErrorCodes = in.CustomErrorCodes
	}
	if in.PoolMode != nil {
		row.PoolMode = *in.PoolMode
	}
	if in.TempUnschedulableEnabled != nil {
		row.TempUnschedulableEnabled = *in.TempUnschedulableEnabled
	}
	if len(in.TempUnschedulableRulesJSON) > 0 {
		row.TempUnschedulableRules = in.TempUnschedulableRulesJSON
	}
	row.ProxyID = in.ProxyID
	row.ProxyGroupID = in.ProxyGroupID
	return row
}
