package hermesops

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/adminhttp/accounthealthview"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/quotawindowview"
)

const secretSentinel = "sk-SECRET-LEAK-SENTINEL-9f3a"

// jsonContains 报告哨兵值是否出现在 v 的 JSON 编码中的任何位置。
// 用于证明工具结果 / 持久化行绝不携带被注入的密钥。
func jsonContains(t *testing.T, v any, needle string) bool {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return strings.Contains(string(raw), needle)
}

func req(tenant int64) ToolRequest {
	return ToolRequest{TenantID: tenant, ActorSource: "token", ActorID: 42, Role: RoleTenantOperator, Args: map[string]any{}}
}

// --- credential_diagnose ----------------------------------------------------

type fakeCredTestStore struct{}

func (fakeCredTestStore) LoadForProviderAccountTest(context.Context, int64, int64) (credentialstore.CredentialRecord, error) {
	return credentialstore.CredentialRecord{}, nil
}

func TestCredentialDiagnoseShapeAndPrivacy(t *testing.T) {
	// 回归:工具必须露出 dry-run 的 ok 标志 + error_class,以及续期状态的诊断字段,
	// 且绝不能泄露一条 fake 续期行所携带的密钥。变异:丢掉 error_class 字段,
	// 或投影整条续期行(那会带上哨兵值),都会让此测试失败。
	deps := CredentialDiagnoseDeps{
		DryRun: func(_ context.Context, _ credentialworker.ProviderAccountCredentialTestStore, _ *credentialworker.ModeAdapterRegistry, tenantID, accountID int64, _ time.Time) (credentialworker.ProviderAccountCredentialTestResult, error) {
			return credentialworker.ProviderAccountCredentialTestResult{OK: false, ErrorClass: "invalid_grant", Message: "credential authorization failed; operator re-authentication is required"}, nil
		},
		TestStore: fakeCredTestStore{},
		ListByAccount: func(_ context.Context, tenantID, accountID int64) ([]credentialstore.CredentialMetadata, error) {
			if tenantID != 7 || accountID != 5 {
				t.Fatalf("凭据精确查询范围=%d/%d，期望 7/5", tenantID, accountID)
			}
			fc := "invalid_grant"
			return []credentialstore.CredentialMetadata{{
				ID: 9, TenantID: 7, ProviderAccountID: 5,
				Vendor: "anthropic", AuthMode: "api_key", State: "failing",
				FailureClass: &fc, FailureCount: 3,
			}}, nil
		},
	}
	spec := CredentialDiagnoseSpec(deps)
	r := req(7)
	r.Args["account_id"] = float64(5)
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ErrorClass != "invalid_grant" {
		t.Fatalf("error_class=%q want invalid_grant", res.ErrorClass)
	}
	if res.Summary["credential_ok"] != false {
		t.Fatalf("credential_ok=%v want false", res.Summary["credential_ok"])
	}
	if jsonContains(t, res.Summary, secretSentinel) {
		t.Fatalf("credential_diagnose result leaked the account-name secret sentinel: %v", res.Summary)
	}
}

func TestCredentialDiagnoseFailsClosedOnNilDep(t *testing.T) {
	// 回归:nil 的 dry-run/store 依赖必须产生 ErrDependencyUnwired,而绝不能 panic。
	spec := CredentialDiagnoseSpec(CredentialDiagnoseDeps{})
	r := req(7)
	r.Args["account_id"] = float64(5)
	if _, err := spec.Run(context.Background(), r); !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("err=%v want ErrDependencyUnwired", err)
	}
}

// TestCredentialDiagnoseProjectsExpiryTiming 守护 access_expires_at /
// refresh_before_at / last_refresh_at 的投影(凭证到期根因)。
// 三个时间戳各不相同(因此丢掉某个 key 会读到 nil,字段被错位则会不匹配),
// 且该行仍在一个非诊断字段里携带密钥哨兵值 —— 所以此测试也再次确认这些时间戳
// 没有放宽掩码。变异:丢掉这三个 key 中的任意一个 -> 变红。
func TestCredentialDiagnoseProjectsExpiryTiming(t *testing.T) {
	accessExp := time.Date(2026, 5, 14, 18, 0, 0, 0, time.UTC)
	refreshBy := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	lastRefresh := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	deps := CredentialDiagnoseDeps{
		DryRun: func(_ context.Context, _ credentialworker.ProviderAccountCredentialTestStore, _ *credentialworker.ModeAdapterRegistry, _, _ int64, _ time.Time) (credentialworker.ProviderAccountCredentialTestResult, error) {
			return credentialworker.ProviderAccountCredentialTestResult{OK: true, Message: "credential is valid"}, nil
		},
		TestStore: fakeCredTestStore{},
		ListByAccount: func(_ context.Context, _, _ int64) ([]credentialstore.CredentialMetadata, error) {
			return []credentialstore.CredentialMetadata{{
				ID: 9, TenantID: 7, ProviderAccountID: 5,
				Vendor: "anthropic", AuthMode: "oauth", State: "active",
				AccessExpiresAt: &accessExp, RefreshBeforeAt: &refreshBy, LastRefreshAt: &lastRefresh,
			}}, nil
		},
	}
	spec := CredentialDiagnoseSpec(deps)
	r := req(7)
	r.Args["account_id"] = float64(5)
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	rows, ok := res.Summary["renew_status"].([]map[string]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("renew_status=%v want exactly one row", res.Summary["renew_status"])
	}
	row := rows[0]
	for _, c := range []struct {
		key  string
		want time.Time
	}{
		{"access_expires_at", accessExp},
		{"refresh_before_at", refreshBy},
		{"last_refresh_at", lastRefresh},
	} {
		got, ok := row[c.key].(time.Time)
		if !ok || !got.Equal(c.want) {
			t.Fatalf("%s=%v want %v (projection dropped?)", c.key, row[c.key], c.want)
		}
	}
	// 新增的时间戳绝不能放宽密钥掩码。
	if jsonContains(t, res.Summary, secretSentinel) {
		t.Fatalf("credential_diagnose leaked the secret sentinel after adding timestamps: %v", res.Summary)
	}
}

func TestCredentialDiagnoseRejectsMissingAccountID(t *testing.T) {
	// 回归:缺失/为零的 account_id 必须返回 ErrInvalidArgs(400),
	// 而不是对 account 0 发起读取。
	spec := CredentialDiagnoseSpec(CredentialDiagnoseDeps{
		DryRun: func(_ context.Context, _ credentialworker.ProviderAccountCredentialTestStore, _ *credentialworker.ModeAdapterRegistry, _, _ int64, _ time.Time) (credentialworker.ProviderAccountCredentialTestResult, error) {
			return credentialworker.ProviderAccountCredentialTestResult{OK: true}, nil
		},
		TestStore: fakeCredTestStore{},
	})
	if _, err := spec.Run(context.Background(), req(7)); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("err=%v want ErrInvalidArgs", err)
	}
}

// --- account_health_diagnose ------------------------------------------------

func TestAccountHealthDiagnoseShape(t *testing.T) {
	// 回归:工具必须露出 account health 行的 state + 失败分类/计数,以及 channel summary
	// 的逐状态计数。变异:丢掉 failure_class 会破坏下面断言的 error_class 推导。
	fc := "rate_limit_exceeded"
	subscriptionVendor := "openai"
	subscriptionPlan := "plus"
	subscriptionScope := "personal"
	subscriptionSource := "oauth_token_response"
	subscriptionTrust := "issuer_response"
	subscriptionVerification := "issuer_response"
	subscriptionStatus := "observed"
	subscriptionMappingVersion := int32(1)
	probeAt := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	observedAt := time.Date(2026, 7, 16, 10, 7, 0, 0, time.UTC)
	deps := AccountHealthDeps{
		ProviderAccountHealth: func(_ context.Context, p admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error) {
			if p.TenantID != 7 || p.ID != 5 {
				t.Fatalf("scope leaked: params=%+v want tenant=7 id=5", p)
			}
			return admindb.GetAdminProviderAccountHealthRow{
				ID: 5, TenantID: 7, HealthState: "degraded", Enabled: true,
				FailureClass: &fc, FailureCount: 2,
				SubscriptionVendor: &subscriptionVendor, SubscriptionPlan: &subscriptionPlan,
				SubscriptionScope: &subscriptionScope, SubscriptionSource: &subscriptionSource,
				SubscriptionTrust: &subscriptionTrust, SubscriptionVerification: &subscriptionVerification,
				SubscriptionStatus: &subscriptionStatus, SubscriptionMappingVersion: &subscriptionMappingVersion,
				LastProbeAt:           pgtype.Timestamptz{Time: probeAt, Valid: true},
				LastRequestObservedAt: pgtype.Timestamptz{Time: observedAt, Valid: true},
			}, nil
		},
		ChannelSummary: func(_ context.Context, tenantID int64) (channelhealth.ChannelHealthSummary, error) {
			return channelhealth.ChannelHealthSummary{Total: 4, ByState: map[channelhealth.HealthState]int64{"active": 3, "cooldown": 1}}, nil
		},
	}
	spec := AccountHealthDiagnoseSpec(deps)
	r := req(7)
	r.Args["account_id"] = float64(5)
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["health_state"] != "degraded" || res.ErrorClass != "rate_limit_exceeded" {
		t.Fatalf("summary=%v error_class=%q want degraded/rate_limit_exceeded", res.Summary, res.ErrorClass)
	}
	if got, ok := res.Summary["last_probe_at"].(time.Time); !ok || !got.Equal(probeAt) {
		t.Fatalf("last_probe_at=%v want %v", res.Summary["last_probe_at"], probeAt)
	}
	if got, ok := res.Summary["last_request_observed_at"].(time.Time); !ok || !got.Equal(observedAt) {
		t.Fatalf("last_request_observed_at=%v want %v", res.Summary["last_request_observed_at"], observedAt)
	}
	if res.Summary["last_request_observation_source"] != "request_completion_event" {
		t.Fatalf("last_request_observation_source=%v want request_completion_event", res.Summary["last_request_observation_source"])
	}
	subscription, ok := res.Summary["subscription"].(*accounthealthview.SubscriptionAxis)
	if !ok || subscription.Label != "openai:plus" || subscription.Status != "observed" {
		t.Fatalf("Hermes 套餐投影=%v，期望复用账号健康投影", res.Summary["subscription"])
	}
	labels, ok := res.Summary["system_labels"].([]string)
	if !ok || len(labels) != 1 || labels[0] != "openai:plus" {
		t.Fatalf("Hermes 系统标签=%v", res.Summary["system_labels"])
	}
	cs, ok := res.Summary["channel_summary"].(map[string]any)
	if !ok || cs["total"].(int64) != 4 {
		t.Fatalf("channel_summary=%v want total=4", res.Summary["channel_summary"])
	}
}

func TestAccountHealthDiagnoseFailsClosedOnNilDep(t *testing.T) {
	spec := AccountHealthDiagnoseSpec(AccountHealthDeps{})
	r := req(7)
	r.Args["account_id"] = float64(5)
	if _, err := spec.Run(context.Background(), r); !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("err=%v want ErrDependencyUnwired", err)
	}
}

// TestAccountHealthDiagnoseProjectsRecoveryAndSessionWindow 守护恢复 ETA 与配额矩阵投影。
func TestAccountHealthDiagnoseProjectsRecoveryAndSessionWindow(t *testing.T) {
	until := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	winStart := time.Date(2099, 5, 14, 8, 0, 0, 0, time.UTC)
	winEnd := time.Date(2099, 5, 14, 13, 0, 0, 0, time.UTC)
	weekStart := time.Date(2099, 5, 8, 13, 0, 0, 0, time.UTC)
	weekEnd := weekStart.Add(7 * 24 * time.Hour)
	deps := AccountHealthDeps{
		ProviderAccountHealth: func(_ context.Context, _ admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error) {
			return admindb.GetAdminProviderAccountHealthRow{
				ID: 5, TenantID: 7, HealthState: "degraded",
				HealthStateUntil:           pgtype.Timestamptz{Time: until, Valid: true},
				SessionWindow5hStart:       pgtype.Timestamptz{Time: winStart, Valid: true},
				SessionWindow5hEnd:         pgtype.Timestamptz{Time: winEnd, Valid: true},
				SessionWindow5hUtilization: hermesNumeric(t, 37.5),
				SessionWindow7dStart:       pgtype.Timestamptz{Time: weekStart, Valid: true},
				SessionWindow7dEnd:         pgtype.Timestamptz{Time: weekEnd, Valid: true},
				SessionWindow7dUtilization: hermesNumeric(t, 62.25),
			}, nil
		},
	}
	spec := AccountHealthDiagnoseSpec(deps)
	r := req(7)
	r.Args["account_id"] = float64(5)
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, c := range []struct {
		key  string
		want time.Time
	}{
		{"health_state_until", until},
		{"session_window_5h_start", winStart},
		{"session_window_5h_end", winEnd},
	} {
		got, ok := res.Summary[c.key].(time.Time)
		if !ok || !got.Equal(c.want) {
			t.Fatalf("%s=%v want %v (projection dropped?)", c.key, res.Summary[c.key], c.want)
		}
	}
	matrix, ok := res.Summary["quota_windows"].(quotawindowview.Matrix)
	if !ok || matrix.FiveHour.RemainingPercent == nil || *matrix.FiveHour.RemainingPercent != 62.5 ||
		matrix.SevenDay.RemainingPercent == nil || *matrix.SevenDay.RemainingPercent != 37.75 {
		t.Fatalf("quota_windows=%+v，期望 Hermes 与管理列表使用同一剩余比例合同", res.Summary["quota_windows"])
	}
}

func hermesNumeric(t *testing.T, value float64) pgtype.Numeric {
	t.Helper()
	var result pgtype.Numeric
	if err := result.Scan(strconv.FormatFloat(value, 'f', -1, 64)); err != nil {
		t.Fatalf("构造 Hermes numeric: %v", err)
	}
	return result
}

// --- request_diagnose -------------------------------------------------------

func TestRequestDiagnoseCorrelatesAndDropsCost(t *testing.T) {
	// 回归:工具必须只关联匹配的 request_id,且必须丢弃 actual_cost(钱)。
	// 变异:输出 actual_cost,或不按 request_id 过滤,都会让此测试失败。
	deps := ObservabilityDeps{
		ListUsage: func(_ context.Context, p dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
			if p.TenantID == nil || *p.TenantID != 7 || p.RequestID == nil || *p.RequestID != "req-A" || p.ClaimID != nil {
				t.Fatalf("用量精确查询参数=%+v，期望 tenant=7 request=req-A", p)
			}
			return []dbbilling.ListUsageRecordsRow{
				{ID: 1, ClaimID: 11, RequestID: "req-A", EndClass: "ok", TokensInput: 10},
				{ID: 2, ClaimID: 22, RequestID: "req-B", EndClass: "error"},
			}, nil
		},
		ListClaims: func(_ context.Context, p dbbilling.ListBillingClaimsParams) ([]dbbilling.ListBillingClaimsRow, error) {
			if p.TenantID == nil || *p.TenantID != 7 || p.RequestID == nil || *p.RequestID != "req-A" || p.ClaimID != nil {
				t.Fatalf("计费精确查询参数=%+v，期望 tenant=7 request=req-A", p)
			}
			return []dbbilling.ListBillingClaimsRow{
				{ID: 11, LogicalRequestID: "req-A", Status: "settled", EndpointFamily: "chat"},
			}, nil
		},
	}
	spec := RequestDiagnoseSpec(deps)
	r := req(7)
	r.Args["request_id"] = "req-A"
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["usage_count"].(int) != 1 || res.Summary["claim_count"].(int) != 1 {
		t.Fatalf("counts=%v/%v want 1/1 (request_id filter not applied?)", res.Summary["usage_count"], res.Summary["claim_count"])
	}
	if jsonContains(t, res.Summary, "actual_cost") {
		t.Fatalf("request_diagnose leaked the money field actual_cost: %v", res.Summary)
	}
}

func TestRequestDiagnose将ClaimID下推数据库(t *testing.T) {
	deps := ObservabilityDeps{
		ListUsage: func(_ context.Context, p dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
			if p.ClaimID == nil || *p.ClaimID != 11 {
				t.Fatalf("用量 claim 过滤=%v，期望 11", p.ClaimID)
			}
			return []dbbilling.ListUsageRecordsRow{{ID: 1, ClaimID: 11, RequestID: "req-A"}}, nil
		},
		ListClaims: func(_ context.Context, p dbbilling.ListBillingClaimsParams) ([]dbbilling.ListBillingClaimsRow, error) {
			if p.ClaimID == nil || *p.ClaimID != 11 {
				t.Fatalf("计费 claim 过滤=%v，期望 11", p.ClaimID)
			}
			return []dbbilling.ListBillingClaimsRow{{ID: 11, LogicalRequestID: "req-A"}}, nil
		},
	}
	r := req(7)
	r.Args = map[string]any{"request_id": "req-A", "claim_id": float64(11)}
	res, err := RequestDiagnoseSpec(deps).Run(context.Background(), r)
	if err != nil {
		t.Fatalf("运行请求诊断：%v", err)
	}
	if res.Summary["usage_count"] != 1 || res.Summary["claim_count"] != 1 {
		t.Fatalf("诊断计数=%v/%v，期望 1/1", res.Summary["usage_count"], res.Summary["claim_count"])
	}
}

func TestRequestDiagnoseFailsClosedOnNilDep(t *testing.T) {
	spec := RequestDiagnoseSpec(ObservabilityDeps{})
	r := req(7)
	r.Args["request_id"] = "x"
	if _, err := spec.Run(context.Background(), r); !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("err=%v want ErrDependencyUnwired", err)
	}
}

// TestRequestDiagnoseProjectsModelRewriteFields 守护 requested_model /
// upstream_model 的投影,它让运营者能看到某 request_id 的模型重写 / 回退
// (requested != upstream)。变异:从 usageDiagnosticShape 丢掉其中任一 key ->
// 该字段会从 usage_records[0] 中消失 -> 变红。
func TestRequestDiagnoseProjectsModelRewriteFields(t *testing.T) {
	upstream := "claude-opus-4-20260514"
	deps := ObservabilityDeps{
		ListUsage: func(_ context.Context, _ dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
			return []dbbilling.ListUsageRecordsRow{
				{ID: 1, ClaimID: 11, RequestID: "req-A", RequestedModel: "claude-opus-4", UpstreamModel: &upstream, EndClass: "ok"},
			}, nil
		},
		ListClaims: func(_ context.Context, _ dbbilling.ListBillingClaimsParams) ([]dbbilling.ListBillingClaimsRow, error) {
			return nil, nil
		},
	}
	spec := RequestDiagnoseSpec(deps)
	r := req(7)
	r.Args["request_id"] = "req-A"
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	records, ok := res.Summary["usage_records"].([]map[string]any)
	if !ok || len(records) != 1 {
		t.Fatalf("usage_records=%v want exactly one shape", res.Summary["usage_records"])
	}
	rec := records[0]
	// 有区分度:requested != upstream 是一次真实的重写;若投影被丢掉,
	// 该 key 会变成 nil,与两个预置值都不相同。
	if rec["requested_model"] != "claude-opus-4" {
		t.Fatalf("requested_model=%v want claude-opus-4 (projection dropped?)", rec["requested_model"])
	}
	if rec["upstream_model"] != "claude-opus-4-20260514" {
		t.Fatalf("upstream_model=%v want claude-opus-4-20260514 (projection dropped?)", rec["upstream_model"])
	}
}

// --- audit_lookup -----------------------------------------------------------

func TestAuditLookupDropsPayloadAndReason(t *testing.T) {
	// 回归(PRIVACY):审计投影必须丢弃自由文本的 Payload blob 和 Reason 字符串,
	// 它们在这里携带密钥哨兵值。变异:露出 payload/reason 会重新引入泄露。
	reason := secretSentinel
	deps := ObservabilityDeps{
		ListAudit: func(_ context.Context, p dbbilling.ListAuditEventsParams) ([]dbbilling.ListAuditEventsRow, error) {
			return []dbbilling.ListAuditEventsRow{{
				ID: 1, TenantID: 7, EventClass: "billing", EventType: "settle", Severity: "info",
				Reason:  &reason,
				Payload: []byte(`{"leak":"` + secretSentinel + `"}`),
			}}, nil
		},
	}
	spec := AuditLookupSpec(deps)
	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["event_count"].(int) != 1 {
		t.Fatalf("event_count=%v want 1", res.Summary["event_count"])
	}
	if jsonContains(t, res.Summary, secretSentinel) {
		t.Fatalf("audit_lookup leaked payload/reason sentinel: %v", res.Summary)
	}
}

// --- log_analyze ------------------------------------------------------------

func TestLogAnalyzeAggregatesEnums(t *testing.T) {
	// 回归:工具必须统计 end_class 枚举(这里是 2 个 ok + 1 个 error),
	// 且绝不读取原始报文。变异:统计错误(例如只统计第一行)会让有区分度的预期 map 失败。
	deps := ObservabilityDeps{
		ListUsage: func(_ context.Context, p dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
			return []dbbilling.ListUsageRecordsRow{
				{ID: 1, EndClass: "ok", SettlementSource: "live"},
				{ID: 2, EndClass: "ok", SettlementSource: "live"},
				{ID: 3, EndClass: "error", SettlementSource: "reconcile", PendingReconciliation: true},
			}, nil
		},
	}
	spec := LogAnalyzeSpec(deps)
	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["sample_size"].(int) != 3 {
		t.Fatalf("sample_size=%v want 3", res.Summary["sample_size"])
	}
	byEnd := res.Summary["by_end_class"].(map[string]any)
	if byEnd["ok"].(int) != 2 || byEnd["error"].(int) != 1 {
		t.Fatalf("by_end_class=%v want ok=2 error=1", byEnd)
	}
	if res.Summary["pending_reconcile"].(int) != 1 {
		t.Fatalf("pending_reconcile=%v want 1", res.Summary["pending_reconcile"])
	}
}

// --- dlq_inspect ------------------------------------------------------------

func TestDLQInspectDropsPayloadAndAggregates(t *testing.T) {
	// 回归(PRIVACY + 结构):dlq_inspect 必须丢弃原始事件 Payload(携带哨兵值),
	// 并按 status/kind 聚合。变异:露出 payload 会再次泄露;统计错误会让有区分度的 map 失败。
	deps := DLQInspectDeps{
		List: func(_ context.Context, f dlq.ListFilter) ([]dlq.Record, error) {
			if f.TenantID == nil || *f.TenantID != 7 {
				t.Fatalf("tenant filter not applied: %+v", f)
			}
			return []dlq.Record{
				{ID: 1, EventKind: "usage_record", Status: "pending", Payload: []byte(`{"body":"` + secretSentinel + `"}`), FailureReason: "timeout", FailureAt: time.Now(), NextRetryAt: time.Now()},
				{ID: 2, EventKind: "usage_record", Status: "failed", Payload: []byte(`{}`), FailureReason: "5xx", FailureAt: time.Now(), NextRetryAt: time.Now()},
			}, nil
		},
	}
	spec := DLQInspectSpec(deps)
	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["dlq_count"].(int) != 2 {
		t.Fatalf("dlq_count=%v want 2", res.Summary["dlq_count"])
	}
	byKind := res.Summary["by_kind"].(map[string]any)
	if byKind["usage_record"].(int) != 2 {
		t.Fatalf("by_kind=%v want usage_record=2", byKind)
	}
	if jsonContains(t, res.Summary, secretSentinel) {
		t.Fatalf("dlq_inspect leaked the raw event payload sentinel: %v", res.Summary)
	}
}

func TestDLQInspectFailsClosedOnNilDep(t *testing.T) {
	spec := DLQInspectSpec(DLQInspectDeps{})
	if _, err := spec.Run(context.Background(), req(7)); !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("err=%v want ErrDependencyUnwired", err)
	}
}

// 确保 pgtype 在需要有效时间戳的 fixture 中保持被引用。
var _ = pgtype.Timestamptz{Valid: true}

func TestAccountHealthDiagnoseUsesServingCredentialSelection(t *testing.T) {
	tests := []struct {
		name       string
		candidates int32
		wantState  string
		wantMeta   bool
	}{
		{name: "唯一候选", candidates: 1, wantState: "resolved", wantMeta: true},
		{name: "多个候选", candidates: 2, wantState: "ambiguous", wantMeta: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := AccountHealthDeps{
				ProviderAccountHealth: func(_ context.Context, _ admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error) {
					return admindb.GetAdminProviderAccountHealthRow{
						ID: 5, TenantID: 7, ProviderCode: "gemini", AccountType: "oauth",
						CredentialVendor: "gemini", CredentialAuthMode: "code_assist",
						ServingCredentialCandidates: tc.candidates, CredentialState: "valid",
					}, nil
				},
			}
			r := req(7)
			r.Args["account_id"] = float64(5)
			res, err := AccountHealthDiagnoseSpec(deps).Run(context.Background(), r)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := res.Summary["credential_selection_state"]; got != tc.wantState {
				t.Fatalf("credential_selection_state=%v，期望 %q", got, tc.wantState)
			}
			_, hasVendor := res.Summary["credential_vendor"]
			_, hasAuthMode := res.Summary["credential_auth_mode"]
			if hasVendor != tc.wantMeta || hasAuthMode != tc.wantMeta {
				t.Fatalf("候选数=%d 时元数据可见性不一致：vendor=%v auth_mode=%v", tc.candidates, hasVendor, hasAuthMode)
			}
		})
	}
}

// TestAccountHealthDiagnoseFoldsAccountChannels 守增强:account_health_diagnose 折叠**本账号**
// (account_id 匹配 ProviderAccountID)的逐通道明细 + 只露安全投影字段;别账号通道绝不混入。
// Mutation:删 channelHealthShape 折叠里的 account_id 过滤 → 别账号通道也进 channels → count 转红。
func TestAccountHealthDiagnoseFoldsAccountChannels(t *testing.T) {
	deps := AccountHealthDeps{
		ProviderAccountHealth: func(_ context.Context, p admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error) {
			return admindb.GetAdminProviderAccountHealthRow{ID: 5, TenantID: 7, HealthState: "active", Enabled: true}, nil
		},
		ChannelListByAccount: func(_ context.Context, tenantID, accountID int64, limit, offset int) ([]channelhealth.Record, error) {
			if tenantID != 7 || accountID != 5 {
				t.Fatalf("健康精确查询范围=%d/%d，期望 7/5", tenantID, accountID)
			}
			return []channelhealth.Record{
				// ManualPauseReason 设哨兵:operator 自填自由文本,**绝不能进 LLM 可见输出**。
				{Key: channelhealth.ChannelKey{ChannelID: "ch-a", ProviderAccountID: 5, Vendor: "openai"}, State: "cooling_down", ReasonClass: "rate_limit", ManualPauseReason: "SENTINEL-free-text-must-not-leak"},
				{Key: channelhealth.ChannelKey{ChannelID: "ch-b", ProviderAccountID: 5}, State: "active"},
			}, nil
		},
	}
	spec := AccountHealthDiagnoseSpec(deps)
	r := req(7)
	r.Args["account_id"] = float64(5)
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	chans, ok := res.Summary["channels"].([]map[string]any)
	if !ok {
		t.Fatalf("channels 缺失或类型不对: %T", res.Summary["channels"])
	}
	if len(chans) != 2 {
		t.Fatalf("应只折叠本账号(id=5)的 2 条通道,got %d(account_id 过滤被破坏会带进别账号)", len(chans))
	}
	for _, c := range chans {
		if c["provider_account_id"].(int64) != 5 {
			t.Fatalf("混入别账号通道: %v", c)
		}
		if _, has := c["state"]; !has {
			t.Fatalf("投影缺 state: %v", c)
		}
		// safe-by-construction:绝不投影自由文本字段(防 operator 笔记泄露进 LLM)。
		if _, has := c["manual_pause_reason"]; has {
			t.Fatalf("投影泄露了自由文本 manual_pause_reason(应只露枚举/时间戳/ids/计数): %v", c)
		}
		if _, has := c["recovery_blocked_reason"]; has {
			t.Fatalf("投影泄露了自由文本 recovery_blocked_reason: %v", c)
		}
	}
}

// TestAccountHealthDiagnoseChannelsBackwardCompat:ChannelList nil 时退化为不返 channels(向后兼容)。
func TestAccountHealthDiagnoseChannelsBackwardCompat(t *testing.T) {
	deps := AccountHealthDeps{
		ProviderAccountHealth: func(_ context.Context, p admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error) {
			return admindb.GetAdminProviderAccountHealthRow{ID: 5, TenantID: 7, HealthState: "active", Enabled: true}, nil
		},
		// ChannelList 故意 nil
	}
	r := req(7)
	r.Args["account_id"] = float64(5)
	res, err := AccountHealthDiagnoseSpec(deps).Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, has := res.Summary["channels"]; has {
		t.Fatalf("ChannelList nil 时不应出现 channels(向后兼容): %v", res.Summary["channels"])
	}
}

// TestChannelHealthListSpec 守新只读工具:整租户逐通道列表 + state 过滤 + by_state 聚合 + no-leak。
// Mutation:删 state 过滤 → cooling_down 查询返全部 3 条 → count 转红;删投影 no-leak → 哨兵泄露转红。
func TestChannelHealthListSpec(t *testing.T) {
	deps := ChannelHealthListDeps{
		List: func(_ context.Context, tenantID int64, limit, offset int) ([]channelhealth.Record, error) {
			if tenantID != 7 {
				t.Fatalf("scope leaked: tenantID=%d want 7", tenantID)
			}
			return []channelhealth.Record{
				{Key: channelhealth.ChannelKey{ChannelID: "ch-a", ProviderAccountID: 5}, State: "cooling_down", ManualPauseReason: "SENTINEL-must-not-leak"},
				{Key: channelhealth.ChannelKey{ChannelID: "ch-b", ProviderAccountID: 6}, State: "active"},
				{Key: channelhealth.ChannelKey{ChannelID: "ch-c", ProviderAccountID: 7}, State: "cooling_down"},
			}, nil
		},
	}
	spec := ChannelHealthListSpec(deps)

	// 无过滤:全部 3 条。
	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["channel_count"].(int) != 3 {
		t.Fatalf("channel_count want 3, got %v", res.Summary["channel_count"])
	}
	items := res.Summary["items"].([]map[string]any)
	if len(items) != 3 {
		t.Fatalf("无过滤应 3 条, got %d", len(items))
	}
	// no-leak:自由文本字段不投影。
	for _, c := range items {
		if _, has := c["manual_pause_reason"]; has {
			t.Fatalf("泄露自由文本 manual_pause_reason: %v", c)
		}
	}

	// state 过滤:只 cooling_down(2 条);by_state 仍统计全部 3。
	r2 := req(7)
	r2.Args["state"] = "cooling_down"
	res2, err := spec.Run(context.Background(), r2)
	if err != nil {
		t.Fatalf("run filtered: %v", err)
	}
	items2 := res2.Summary["items"].([]map[string]any)
	if len(items2) != 2 {
		t.Fatalf("cooling_down 过滤应 2 条, got %d(过滤被破坏会返全部)", len(items2))
	}
	if res2.Summary["channel_count"].(int) != 3 {
		t.Fatalf("channel_count 应仍是全部 3(by_state 过滤前统计), got %v", res2.Summary["channel_count"])
	}
}

func TestChannelHealthListNilDep(t *testing.T) {
	_, err := ChannelHealthListSpec(ChannelHealthListDeps{}).Run(context.Background(), req(7))
	if !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("nil dep 应 ErrDependencyUnwired, got %v", err)
	}
}
