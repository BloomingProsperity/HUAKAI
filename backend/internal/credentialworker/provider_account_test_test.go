package credentialworker

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
)

func TestDryRunProviderAccountCredentialSuccessDoesNotPersist(t *testing.T) {
	// DRY-RUN 判别: 成功 adapter 只能返回 ok=true,不能进入 SaveRefreshSuccess/Failure。
	// Mutation:把实现改成后台 Refresh 路径或显式 Save*,calls 中会出现 save/failure 而红。
	calls := []string{}
	payload := []byte(`{"refresh_token":"rt-secret-marker"}`)
	store := &dryRunCredentialStoreStub{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 45, TenantID: 7, ProviderAccountID: 99,
			Vendor: "testvendor", AuthMode: "safe_refresh",
			CredentialVersion: 3, PlaintextPayload: payload,
		},
	}
	registry := NewModeAdapterRegistry()
	if err := registry.Register("testvendor", "safe_refresh", dryRunModeAdapter{
		calls: &calls,
		result: ModeRefreshResult{
			Payload:         []byte(`{"access_token":"new-secret"}`),
			AccessExpiresAt: time.Date(2026, 6, 2, 12, 30, 0, 0, time.UTC),
			Outcome:         "refresh_succeeded",
		},
	}); err != nil {
		t.Fatalf("register adapter: %v", err)
	}

	result, err := DryRunProviderAccountCredential(context.Background(), store, registry, 7, 99, time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DryRunProviderAccountCredential: %v", err)
	}
	if !result.OK || result.ErrorClass != "" {
		t.Fatalf("result=%+v, want ok true with no error class", result)
	}
	want := []string{"load:7:99", "adapter:45:7:99"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls=%v want %v", calls, want)
	}
	for i, b := range payload {
		if b != 0 {
			t.Fatalf("payload byte %d not zeroized: %q", i, payload)
		}
	}
}

func TestDryRunProviderAccountCredentialPassesProbeModelToAdapter(t *testing.T) {
	// MUTATION: dry-run 构造 ModeRefreshInput 时漏 ProbeModel, adapter 只能
	// 用默认模型探测, 账号级 probe_model 不生效。
	calls := []string{}
	probeModels := []string{}
	store := &dryRunCredentialStoreStub{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 49, TenantID: 7, ProviderAccountID: 103,
			Vendor: "testvendor", AuthMode: "safe_refresh",
			PlaintextPayload: []byte(`{"api_key":"sk-probe"}`),
		},
	}
	registry := NewModeAdapterRegistry()
	if err := registry.Register("testvendor", "safe_refresh", dryRunModeAdapter{
		calls: &calls, probeModels: &probeModels,
		result: ModeRefreshResult{Payload: []byte(`{"ok":true}`), Outcome: "probe_succeeded"},
	}); err != nil {
		t.Fatalf("register adapter: %v", err)
	}

	result, err := DryRunProviderAccountCredentialWithProbeModel(context.Background(), store, registry, 7, 103, time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC), "gpt-4o-mini-probe")
	if err != nil {
		t.Fatalf("DryRunProviderAccountCredentialWithProbeModel: %v", err)
	}
	if !result.OK {
		t.Fatalf("result=%+v, want ok", result)
	}
	if len(probeModels) != 1 || probeModels[0] != "gpt-4o-mini-probe" {
		t.Fatalf("probeModels=%v want [gpt-4o-mini-probe]", probeModels)
	}
}

func TestDryRunProviderAccountCredentialMapsInvalidGrantWithoutLeakingRawError(t *testing.T) {
	// 判别 secret leak: raw adapter error 带 secret marker;结果 message 只能是泛化文案。
	// Mutation:把 err.Error() 塞进 Message,下方 marker 断言会红。
	calls := []string{}
	secretMarker := "sk-live-secret-marker"
	store := &dryRunCredentialStoreStub{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 46, TenantID: 7, ProviderAccountID: 100,
			Vendor: "testvendor", AuthMode: "safe_refresh",
			PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
		},
	}
	registry := NewModeAdapterRegistry()
	if err := registry.Register("testvendor", "safe_refresh", dryRunModeAdapter{
		calls: &calls,
		err:   errors.New(`upstream body {"error":"invalid_grant","detail":"` + secretMarker + `"}`),
	}); err != nil {
		t.Fatalf("register adapter: %v", err)
	}

	result, err := DryRunProviderAccountCredential(context.Background(), store, registry, 7, 100, time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DryRunProviderAccountCredential returned transport error for adapter failure: %v", err)
	}
	if result.OK || result.ErrorClass != "invalid_grant" {
		t.Fatalf("result=%+v, want invalid_grant failure", result)
	}
	joined := result.Message + result.ErrorClass
	if strings.Contains(joined, secretMarker) || strings.Contains(result.Message, "upstream body") || strings.Contains(result.Message, "invalid_grant") {
		t.Fatalf("dry-run result leaked raw upstream error: %+v", result)
	}
}

func TestDryRunProviderAccountCredentialDecryptFailureReturnsPayloadInvalid(t *testing.T) {
	// 判别 corrupt stored credential:解密/AAD 错误是凭据 payload 问题,不能冒充 503。
	// Mutation:直接 return store error,该测试会收到 err 而红。
	calls := []string{}
	store := &dryRunCredentialStoreStub{
		calls: &calls,
		err:   credentialstore.ErrDecryptFailed,
	}

	result, err := DryRunProviderAccountCredential(context.Background(), store, NewModeAdapterRegistry(), 7, 100, time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DryRunProviderAccountCredential returned transport error for decrypt failure: %v", err)
	}
	if result.OK || result.ErrorClass != "payload_invalid" {
		t.Fatalf("result=%+v, want payload_invalid failure", result)
	}
}

func TestDryRunProviderAccountCredentialBlocksRotatingRefreshGrantBeforeAdapterCall(t *testing.T) {
	// OAuth refresh-token grant 可能上游轮换 refresh_token；dry-run 不能调用后丢弃结果。
	// Mutation:删除 modeRequiresPersistingRefresh guard,adapter 会被调用并让 calls 出现 adapter。
	calls := []string{}
	store := &dryRunCredentialStoreStub{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 47, TenantID: 7, ProviderAccountID: 101,
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeRefreshToken,
			PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
		},
	}
	registry := NewModeAdapterRegistry()
	if err := registry.Register(credentialstore.VendorOpenAI, credentialstore.AuthModeRefreshToken, dryRunModeAdapter{
		calls: &calls,
		result: ModeRefreshResult{
			Payload: []byte(`{"access_token":"new","refresh_token":"rotated"}`),
		},
	}); err != nil {
		t.Fatalf("register adapter: %v", err)
	}

	result, err := DryRunProviderAccountCredential(context.Background(), store, registry, 7, 101, time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DryRunProviderAccountCredential: %v", err)
	}
	if result.OK || result.ErrorClass != "operator_config_required" {
		t.Fatalf("result=%+v, want fail-closed operator_config_required", result)
	}
	if got := strings.Join(calls, ","); got != "load:7:101" {
		t.Fatalf("calls=%v, rotating mode must not reach adapter", calls)
	}
}

func TestDryRunProviderAccountCredentialNoRefreshRequiredFailsClosed(t *testing.T) {
	// 判别 false-positive: static/no-op adapter 没有上游探测时不能返回 ok=true。
	// Mutation:把 ErrNoRefreshRequired 当成功,该测试会红。
	calls := []string{}
	store := &dryRunCredentialStoreStub{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 48, TenantID: 7, ProviderAccountID: 102,
			Vendor: "testvendor", AuthMode: "safe_refresh",
			PlaintextPayload: []byte(`{"api_key":"sk-should-not-leak"}`),
		},
	}
	registry := NewModeAdapterRegistry()
	if err := registry.Register("testvendor", "safe_refresh", dryRunModeAdapter{
		calls: &calls,
		err:   ErrNoRefreshRequired,
	}); err != nil {
		t.Fatalf("register adapter: %v", err)
	}

	result, err := DryRunProviderAccountCredential(context.Background(), store, registry, 7, 102, time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DryRunProviderAccountCredential: %v", err)
	}
	if result.OK || result.ErrorClass != "operator_config_required" {
		t.Fatalf("result=%+v, want fail-closed operator_config_required", result)
	}
	if strings.Contains(result.Message, "sk-should-not-leak") {
		t.Fatalf("result leaked credential material: %+v", result)
	}
}

func TestClassifyRefreshErrorClassUsesSafeSevenClasses(t *testing.T) {
	// 分类只能暴露契约内安全类,不能冒出 raw/decrypt_failed 等内部实现细节。
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "invalid_grant", err: errors.New(`{"error":"invalid_grant"}`), want: "invalid_grant"},
		{name: "invalid_grant_phrase", err: errors.New("invalid grant from provider"), want: "invalid_grant"},
		{name: "rate_limit", err: errors.New("provider says rate_limit_exceeded"), want: "rate_limit_exceeded"},
		{name: "status_429", err: errors.New("token endpoint returned status 429"), want: "rate_limit_exceeded"},
		{name: "too_many_requests", err: errors.New("too many requests from provider"), want: "rate_limit_exceeded"},
		{name: "risk_control", err: errors.New("risk_control_triggered by upstream"), want: "risk_control_triggered"},
		{name: "risk_control_phrase", err: errors.New("risk control triggered by upstream"), want: "risk_control_triggered"},
		{name: "account_disabled", err: errors.New("account_disabled by upstream"), want: "account_disabled"},
		{name: "disabled_account_phrase", err: errors.New("disabled account by upstream"), want: "account_disabled"},
		{name: "payload_invalid", err: adapters.ErrInvalidCredentialMaterial, want: "payload_invalid"},
		{name: "operator_config", err: ErrOperatorOAuthConfigMissing, want: "operator_config_required"},
		{name: "temporary", err: errors.New("temporary token service outage"), want: "temporary"},
		{name: "decrypt_collapses_to_payload_invalid", err: errors.New("decrypt failed"), want: "payload_invalid"},
		{name: "decrypt_substring_not_payload_invalid", err: errors.New("decryptology marker only"), want: "temporary"},
		{name: "json_substring_not_payload_invalid", err: errors.New("jsonify marker only"), want: "temporary"},
		{name: "disabled_account_substring_not_account_disabled", err: errors.New("disabled accountant marker"), want: "temporary"},
		{name: "typed_auth_expired_without_keyword", err: typedRefreshOutcomeErr{outcome: string(OutcomeAuthExpired)}, want: "invalid_grant"},
		{name: "typed_rate_limit_without_keyword", err: typedRefreshOutcomeErr{outcome: string(OutcomeRateLimit)}, want: "rate_limit_exceeded"},
		{name: "typed_risk_control_without_keyword", err: typedRefreshOutcomeErr{outcome: string(OutcomeRiskControl)}, want: "risk_control_triggered"},
		{name: "typed_account_disabled_without_keyword", err: typedRefreshOutcomeErr{outcome: string(OutcomeAccountDisabled)}, want: "account_disabled"},
		{name: "typed_transient_without_keyword", err: typedRefreshOutcomeErr{outcome: string(OutcomeTransientError)}, want: "temporary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyRefreshErrorClass(tc.err); got != tc.want {
				t.Fatalf("ClassifyRefreshErrorClass(%v)=%q want %q", tc.err, got, tc.want)
			}
		})
	}
}

type typedRefreshOutcomeErr struct {
	outcome string
}

func (e typedRefreshOutcomeErr) Error() string {
	return "provider refresh failed without public classifier keyword"
}

func (e typedRefreshOutcomeErr) RefreshFailureOutcome() string {
	return e.outcome
}

type dryRunCredentialStoreStub struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
	err   error
}

func (s *dryRunCredentialStoreStub) LoadForProviderAccountTest(_ context.Context, tenantID, accountID int64) (credentialstore.CredentialRecord, error) {
	*s.calls = append(*s.calls, "load:"+strconvInt64(tenantID)+":"+strconvInt64(accountID))
	if s.err != nil {
		return credentialstore.CredentialRecord{}, s.err
	}
	return s.rec, nil
}

type dryRunModeAdapter struct {
	calls       *[]string
	probeModels *[]string
	result      ModeRefreshResult
	err         error
}

func (a dryRunModeAdapter) RefreshCredential(_ context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	if a.calls != nil {
		*a.calls = append(*a.calls, "adapter:"+strconvInt64(in.CredentialID)+":"+strconvInt64(in.TenantID)+":"+strconvInt64(in.ProviderAccountID))
	}
	if a.probeModels != nil {
		*a.probeModels = append(*a.probeModels, in.ProbeModel)
	}
	if a.err != nil {
		return ModeRefreshResult{}, a.err
	}
	return a.result, nil
}

func strconvInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}
