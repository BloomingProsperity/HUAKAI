package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
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
	riskPeers        []providerAccountRiskPeerForTest
	listArg          *admindb.ListAdminProviderAccountsParams
	list             []admindb.AdminProviderAccountRow
	getArg           *admindb.GetAdminProviderAccountParams
	get              *admindb.AdminProviderAccountRow
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

type providerAccountRiskPeerForTest struct {
	ID       int64
	TenantID int64
	Provider int64
	Channel  int64
	Type     string
	Vendor   string
	AuthMode string
}

type adminPoolCredentialWriterStub struct {
	input *credentialstore.CreateCredentialInput
	id    int64
}

type adminPoolChannelHealthStub struct {
	key *channelhealth.ChannelKey
}

type adminPoolRateLimitRecoveryStub struct {
	input  *provideraccountrecovery.ClearRateLimitInput
	result provideraccountrecovery.ClearRateLimitResult
	err    error
}

func (s *adminPoolRateLimitRecoveryStub) ClearRateLimit(_ context.Context, in provideraccountrecovery.ClearRateLimitInput) (provideraccountrecovery.ClearRateLimitResult, error) {
	s.input = &in
	if s.result.Account.ID == 0 {
		s.result.Account = adminProviderRow(in.AccountID, in.TenantID)
	}
	return s.result, s.err
}

func (s *adminPoolChannelHealthStub) EnsureDefaultActive(_ context.Context, key channelhealth.ChannelKey) (channelhealth.Record, error) {
	s.key = &key
	return channelhealth.Record{Key: key, State: channelhealth.StateActive}, nil
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

func (s *adminPoolStoreStub) InsertProviderAccount(_ context.Context, arg admindb.InsertProviderAccountParams) (int64, error) {
	s.insert = &arg
	if s.insertID == 0 {
		return 101, nil
	}
	return s.insertID, nil
}

func (s *adminPoolStoreStub) ListProviderAccountRiskPeers(_ context.Context, arg admindb.ListProviderAccountRiskPeersParams) ([]admindb.ProviderAccountRiskPeerRow, error) {
	out := make([]admindb.ProviderAccountRiskPeerRow, 0, len(s.riskPeers))
	for _, peer := range s.riskPeers {
		if peer.TenantID != arg.TenantID || peer.Channel != arg.ChannelID {
			continue
		}
		out = append(out, admindb.ProviderAccountRiskPeerRow{
			ID: peer.ID, TenantID: peer.TenantID, ProviderID: peer.Provider, ChannelID: peer.Channel,
			AccountType: peer.Type, CredentialVendor: peer.Vendor, CredentialAuthMode: peer.AuthMode,
		})
	}
	return out, nil
}

func (s *adminPoolStoreStub) InsertProviderAccountWithMixedRiskCheck(ctx context.Context, arg adminPoolAccountCreateWithMixedRiskParams) (adminPoolAccountCreateWithMixedRiskResult, error) {
	if err := validateProviderAccountProtocolCompatibility(arg.ProviderFamily, arg.Candidate.AccountType, arg.Candidate.Vendor, arg.Candidate.AuthMode); err != nil {
		return adminPoolAccountCreateWithMixedRiskResult{}, err
	}
	peers, err := s.ListProviderAccountRiskPeers(ctx, admindb.ListProviderAccountRiskPeersParams{
		TenantID:  arg.Insert.TenantID,
		ChannelID: arg.Insert.ChannelID,
	})
	if err != nil {
		return adminPoolAccountCreateWithMixedRiskResult{}, err
	}
	report := mixedchannelrisk.Evaluate(arg.Candidate, mixedRiskPeerAccounts(peers))
	if report.HighRisk && !arg.Confirmed {
		return adminPoolAccountCreateWithMixedRiskResult{RiskReport: report}, errProviderAccountMixedRiskConfirmationRequired
	}
	id, err := s.InsertProviderAccount(ctx, arg.Insert)
	if err != nil {
		return adminPoolAccountCreateWithMixedRiskResult{RiskReport: report}, err
	}
	return adminPoolAccountCreateWithMixedRiskResult{ID: id, RiskReport: report}, nil
}

func mixedRiskPeerAccounts(rows []admindb.ProviderAccountRiskPeerRow) []mixedchannelrisk.Account {
	out := make([]mixedchannelrisk.Account, 0, len(rows))
	for _, row := range rows {
		out = append(out, mixedchannelrisk.Account{
			ID: row.ID, ProviderID: row.ProviderID, ChannelID: row.ChannelID,
			AccountType: row.AccountType, Vendor: row.CredentialVendor, AuthMode: row.CredentialAuthMode,
		})
	}
	return out
}

func (s *adminPoolStoreStub) ListAdminProviderAccounts(_ context.Context, arg admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
	s.listArg = &arg
	return s.list, nil
}

func (s *adminPoolStoreStub) GetAdminProviderAccount(_ context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	s.getArg = &arg
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
	row := adminProviderRow(arg.ID, arg.TenantID)
	if arg.Enabled != nil {
		row.Enabled = *arg.Enabled
	}
	if arg.Priority != nil {
		row.Priority = *arg.Priority
	}
	if arg.StaticWeight != nil {
		row.StaticWeight = *arg.StaticWeight
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

func TestAdminPoolAccounts_CreateHappyPathInsertsAccount(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":" acct ","account_type":"api_key","credentials":{"api_key":"sk-live"},"cap_concurrency":3,"priority":10,"model_allow_list":[" gpt-4o "],"capability_flags":["chat"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != 7 || store.insert.ProviderID != 8 || store.insert.ChannelID != 9 {
		t.Fatalf("insert params mismatch: %+v", store.insert)
	}
	if store.insert.Name != "acct" || store.insert.AccountType != "api_key" || string(store.insert.Credentials) == "" {
		t.Fatalf("insert account fields mismatch: %+v", store.insert)
	}
	if store.insert.CapConcurrency == nil || *store.insert.CapConcurrency != 3 || store.insert.Priority == nil || *store.insert.Priority != 10 {
		t.Fatalf("insert capacity fields mismatch: %+v", store.insert)
	}
	if got := store.insert.ModelAllowList; len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("model_allow_list=%v", got)
	}
}

func TestAdminPoolAccounts_CreateAdditionalProviderAccountFields(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","credentials":{"api_key":"sk-live"},"static_weight":4,"probe_model":" gpt-4o-mini-probe ","tags":[" prod ",""],"extra":{"azure_api_version":"2024-08-01","claude_beta_query":"true"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil {
		t.Fatal("InsertProviderAccount was not called")
	}
	if store.insert.StaticWeight == nil || *store.insert.StaticWeight != 4 {
		t.Fatalf("static_weight insert=%v want 4", store.insert.StaticWeight)
	}
	if store.insert.ProbeModel == nil || *store.insert.ProbeModel != "gpt-4o-mini-probe" {
		t.Fatalf("probe_model insert=%v want gpt-4o-mini-probe", store.insert.ProbeModel)
	}
	if got := strings.Join(store.insert.Tags, ","); got != "prod" {
		t.Fatalf("tags insert=%v want [prod]", store.insert.Tags)
	}
	if !strings.Contains(string(store.insert.Extra), `"azure_api_version":"2024-08-01"`) ||
		!strings.Contains(string(store.insert.Extra), `"claude_beta_query":"true"`) {
		t.Fatalf("extra insert=%s", string(store.insert.Extra))
	}
}

func TestAdminPoolAccounts_CreateSessionTypeInsertsAccount(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 88}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"cursor-sub","account_type":"session","credentials":{"session_token":"sess-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil {
		t.Fatal("InsertProviderAccount was not called")
	}
	if store.insert.AccountType != "session" {
		t.Fatalf("AccountType=%q want session", store.insert.AccountType)
	}
}

func TestAdminPoolAccounts_CreateWritesAuditEventWithoutCredentialBytes(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.audits) != 1 {
		t.Fatalf("audit count=%d want 1", len(store.audits))
	}
	a := store.audits[0]
	if a.Action != "create_provider_account" || a.TargetType != "provider_account" || *a.TargetID != 77 {
		t.Fatalf("audit target mismatch: %+v", a)
	}
	if a.Reason == nil || *a.Reason != "创建 provider account" {
		t.Fatalf("audit reason=%v", a.Reason)
	}
	if strings.Contains(string(a.Payload), "sk-live") {
		t.Fatalf("audit payload leaked credential: %s", string(a.Payload))
	}
}

func TestAdminPoolAccounts_CreateWithCredentialV2StoresEmptyLegacyJSON(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	credentials := &adminPoolCredentialWriterStub{id: 88}
	rec := invokeAdminPoolWithCredentialStore(t, store, credentials, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if string(store.insert.Credentials) != "{}" {
		t.Fatalf("legacy credentials=%s want empty object", string(store.insert.Credentials))
	}
	if credentials.input == nil || credentials.input.ProviderAccountID != 77 || credentials.input.AuthMode != "api_key" {
		t.Fatalf("credential input mismatch: %+v", credentials.input)
	}
	if strings.Contains(string(store.audits[0].Payload), "sk-live") {
		t.Fatalf("audit leaked credential: %s", string(store.audits[0].Payload))
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if response.ID != 77 {
		t.Fatalf("id=%d want 77", response.ID)
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

// TestAdminPoolAccounts_ClaudeSessionAcceptsOAuth 证明正向模式仍可创建，
// 并精确把 claude_ai_oauth 凭据交给 credential store。
func TestAdminPoolAccounts_ClaudeSessionAcceptsOAuth(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77, providerFamilies: map[int64]string{8: "anthropic_claude_session"}}
	credentials := &adminPoolCredentialWriterStub{id: 88}
	rec := invokeAdminPoolWithCredentialStore(t, store, credentials, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"claude-oauth","account_type":"oauth","vendor":"anthropic","auth_mode":"claude_ai_oauth","credentials":{"access_token":"oauth-access","refresh_token":"oauth-refresh"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d want 201 body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || credentials.input == nil || credentials.input.AuthMode != credentialstore.AuthModeClaudeAIOAuth {
		t.Fatalf("session OAuth 正向创建未闭合: insert=%+v credential=%+v", store.insert, credentials.input)
	}
}

func TestAdminPoolAccounts_CreateWithCredentialInitializesChannelHealth(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77}
	credentials := &adminPoolCredentialWriterStub{id: 88}
	health := &adminPoolChannelHealthStub{}
	rec := invokeAdminPoolWithDeps(t, AdminPoolAccountDeps{
		Auth: providerAccountAdmin(), Store: store, Credentials: credentials, ChannelHealth: health,
	}, http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if health.key == nil || health.key.TenantID != 7 || health.key.ProviderAccountID != 77 ||
		health.key.AccountCredentialID != 88 || health.key.CredentialVersion != 1 || health.key.Vendor != "openai" {
		t.Fatalf("channel health init key mismatch: %+v", health.key)
	}
	if strings.Contains(string(store.audits[0].Payload), "sk-live") {
		t.Fatalf("audit leaked credential: %s", string(store.audits[0].Payload))
	}
}

func TestAdminPoolAccounts_CreateMixedChannelRiskRequiresConfirm(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77, riskPeers: []providerAccountRiskPeerForTest{{
		ID: 61, TenantID: 7, Provider: 8, Channel: 9, Type: "oauth", Vendor: "anthropic", AuthMode: "claude_ai_oauth",
	}}}
	credentials := &adminPoolCredentialWriterStub{id: 88}
	rec := invokeAdminPoolWithCredentialStore(t, store, credentials, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":11,"channel_id":9,"name":"acct","account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert != nil {
		t.Fatalf("InsertProviderAccount called despite unconfirmed mixed risk: %+v", store.insert)
	}
	var response struct {
		Error           string `json:"error"`
		ConfirmRequired bool   `json:"confirm_required"`
		Risks           []struct {
			Dimension         string `json:"dimension"`
			ExistingAccountID int64  `json:"existing_account_id"`
		} `json:"risks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if response.Error != "mixed_channel_risk_confirmation_required" || !response.ConfirmRequired {
		t.Fatalf("response risk gate mismatch: %+v body=%s", response, rec.Body.String())
	}
	if len(response.Risks) < 3 {
		t.Fatalf("risks=%+v want source/vendor/credential_type items", response.Risks)
	}
	seen := map[string]bool{}
	for _, item := range response.Risks {
		seen[item.Dimension] = true
		if item.ExistingAccountID != 61 {
			t.Fatalf("risk item existing account=%d want 61", item.ExistingAccountID)
		}
	}
	for _, dim := range []string{"source", "vendor", "credential_type"} {
		if !seen[dim] {
			t.Fatalf("missing risk dimension %s in %+v", dim, response.Risks)
		}
	}
}

func TestAdminPoolAccounts_CreateMixedChannelRiskConfirmAllowsAndAudits(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77, riskPeers: []providerAccountRiskPeerForTest{{
		ID: 61, TenantID: 7, Provider: 8, Channel: 9, Type: "oauth", Vendor: "anthropic", AuthMode: "claude_ai_oauth",
	}}}
	credentials := &adminPoolCredentialWriterStub{id: 88}
	rec := invokeAdminPoolWithCredentialStore(t, store, credentials, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts?confirm=true",
		`{"provider_id":11,"channel_id":9,"name":"acct","account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || store.insert.ProviderID != 11 || store.insert.ChannelID != 9 {
		t.Fatalf("insert params mismatch: %+v", store.insert)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "create_provider_account" {
		t.Fatalf("audit mismatch: %+v", store.audits)
	}
	if !strings.Contains(string(store.audits[0].Payload), `"mixed_channel_risk_confirmed":true`) ||
		!strings.Contains(string(store.audits[0].Payload), `"dimension":"vendor"`) {
		t.Fatalf("audit payload missing mixed-risk confirmation: %s", string(store.audits[0].Payload))
	}
	if strings.Contains(string(store.audits[0].Payload), "sk-live") {
		t.Fatalf("audit leaked credential: %s", string(store.audits[0].Payload))
	}
}

func TestAdminPoolAccounts_CreateSameSourceNoMixedChannelRisk(t *testing.T) {
	store := &adminPoolStoreStub{insertID: 77, riskPeers: []providerAccountRiskPeerForTest{{
		ID: 61, TenantID: 7, Provider: 11, Channel: 9, Type: "api_key", Vendor: "openai", AuthMode: "api_key",
	}}}
	credentials := &adminPoolCredentialWriterStub{id: 88}
	rec := invokeAdminPoolWithCredentialStore(t, store, credentials, providerAccountAdmin(), http.MethodPost, "/admin/v1/provider-accounts",
		`{"provider_id":11,"channel_id":9,"name":"acct","account_type":"api_key","vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil {
		t.Fatal("InsertProviderAccount was not called for same-source account")
	}
	if strings.Contains(string(store.audits[0].Payload), `"mixed_channel_risk_confirmed":true`) {
		t.Fatalf("same-source audit should not mark risk confirmation: %s", string(store.audits[0].Payload))
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
	store := &adminPoolStoreStub{list: []admindb.AdminProviderAccountRow{
		adminProviderRow(77, 7),
		adminProviderRow(78, 7),
	}}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodGet, "/admin/v1/provider-accounts?limit=1&state_filter=active&pool_group_id=9&tag=prod", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.listArg == nil || store.listArg.TenantID != 7 || store.listArg.LimitCount != 2 ||
		store.listArg.StateFilter != "active" || store.listArg.PoolGroupID != 9 || store.listArg.TagFilter != "prod" {
		t.Fatalf("list arg mismatch: %+v", store.listArg)
	}
	var response struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Page struct {
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
	if strings.Contains(rec.Body.String(), "temp_unschedulable_rules") {
		t.Fatalf("列表响应不应携带详情规则：%s", rec.Body.String())
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

func TestAdminPoolAccounts_UpdateProviderAccountFull(t *testing.T) {
	store := &adminPoolStoreStub{}
	rec := invokeAdminPool(t, store, providerAccountAdmin(), http.MethodPatch, "/admin/v1/provider-accounts/77",
		`{"enabled":true,"priority":5,"cap_concurrency":9,"static_weight":4,"probe_model":"claude-probe","tags":["prod"],"extra":{"claude_beta_query":"true"},"model_allow_list":[" claude "],"capability_flags":["tool"],"custom_error_codes_enabled":true,"custom_error_codes":[429],"pool_mode":true,"temp_unschedulable_enabled":true,"temp_unschedulable_rules":[{"error_code":529,"keywords":["busy"],"duration_minutes":5}]}`)
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
		`{"tenant_id":8,"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","credentials":{"api_key":"sk-live"}}`)
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

func TestAdminPoolAccounts_GlobalAdminCreateScopesToQueryTenant(t *testing.T) {
	// 全局 platform_admin 创建到 ?tenant_id=9 指名的 tenant(body 的 tenant_id
	// 必须一致);insert 必须落在 tenant 9 而非 1。
	store := &adminPoolStoreStub{insertID: 77}
	rec := invokeAdminPool(t, store, adminPoolAdmin(), http.MethodPost,
		"/admin/v1/provider-accounts?tenant_id=9",
		`{"tenant_id":9,"provider_id":8,"channel_id":9,"name":"acct","account_type":"api_key","credentials":{"api_key":"sk-live"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.insert == nil || store.insert.TenantID != 9 {
		t.Fatalf("global admin create must insert into tenant 9, got %+v", store.insert)
	}
}

func adminPoolAdmin() adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}}
}

func providerAccountAdmin() adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RoleTenantOperator, ScopeTenantID: 7}}
}

func invokeAdminPool(t *testing.T, store *adminPoolStoreStub, auth AdminPoolAccountAuth, method, target, body string) *httptest.ResponseRecorder {
	return invokeAdminPoolWithCredentialStore(t, store, nil, auth, method, target, body)
}

func invokeAdminPoolWithCredentialStore(t *testing.T, store *adminPoolStoreStub, credentials AdminPoolAccountCredentialWriter, auth AdminPoolAccountAuth, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return invokeAdminPoolWithDeps(t, AdminPoolAccountDeps{
		Auth: auth, Store: store, Credentials: credentials,
		RateLimitRecovery: &adminPoolRateLimitRecoveryStub{},
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
	row.ProbeModel = in.ProbeModel
	row.Tags = in.Tags
	row.Extra = in.Extra
	row.ModelAllowList = in.ModelAllowList
	row.CapabilityFlags = in.CapabilityFlags
	return row
}
