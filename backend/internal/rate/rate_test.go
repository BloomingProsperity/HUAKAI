// Package rate 的合约保护测试。
//
// 现状（2026-05-17 e961e5c 之上）：rate.go 只声明了 Service interface +
// Decision/StateChange/Reason 三个枚举集合，concrete 实现尚未落地（spec
// §Phase A-G 的多平台 429 解析、handle403 dispatch、cascade 清理、OAuth
// 401 force-refresh 在 Phase 4 才会着陆）。
//
// 在 impl 缺位的窗口里，这套测试做两件事来防回归：
//   1) StateChange / Reason 枚举的稳定性 — 一旦有人改了 iota 顺序或删了
//      Reason 字符串值，下游 audit / dashboard / metrics 的语义就会被静默
//      破坏。
//   2) Service interface 的可实现性 — 用一个最小 fake 实现穿一遍三个
//      method，证明 interface 形状（参数列表、返回值数量、context 位置、
//      error 末位）没被无意改动；新加 method 必须显式更新此测试。
//
// 当 Phase 4 实现到位后，这套测试将被扩展为 token-bucket / sliding-window
// 边界 + 并发安全 + tenant 隔离的功能测试。
package rate

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// =============================================================================
// 枚举稳定性 — StateChange
// =============================================================================

// 防止有人重新排序 iota，导致已经持久化在 PG / audit 的整数 state code
// 含义改变。锁死值与名字。
func TestStateChange_StableIotaValues(t *testing.T) {
	cases := []struct {
		state StateChange
		want  int
		name  string
	}{
		{StateNoChange, 0, "StateNoChange"},
		{StateRateLimited, 1, "StateRateLimited"},
		{StateOverloaded, 2, "StateOverloaded"},
		{StateTempUnsched, 3, "StateTempUnsched"},
		{StateModelRateLimited, 4, "StateModelRateLimited"},
		{StatePermanentDisable, 5, "StatePermanentDisable"},
	}
	for _, tc := range cases {
		if int(tc.state) != tc.want {
			t.Errorf("%s: 期望 iota 值=%d，实际=%d（不要重新排序 StateChange iota）",
				tc.name, tc.want, int(tc.state))
		}
	}
}

// =============================================================================
// 枚举稳定性 — Reason
// =============================================================================

// Reason 是写入 PG account_states.rate_limit_reason 与 audit payload 的
// 字符串字面值。任何拼写漂移都会让历史行无法 join 解释，metrics 也会断。
func TestReason_StableStringValues(t *testing.T) {
	cases := []struct {
		reason Reason
		want   string
	}{
		{ReasonRateLimit5h, "rate_limit_5h_exceeded"},
		{ReasonRateLimit7d, "rate_limit_7d_exceeded"},
		{ReasonRateLimitBoth, "rate_limit_both_windows"},
		{ReasonRateLimitRPM, "rate_limit_rpm"},
		{ReasonRateLimitTPM, "rate_limit_tpm"},
		{ReasonExtraUsageRequired, "extra_usage_required"},
		{ReasonOverloaded, "overloaded"},
		{ReasonTokenRefreshRequired, "token_refresh_required"},
		{ReasonTokenRevoked, "token_permanently_revoked"},
		{ReasonKYCRequired, "kyc_required"},
		{ReasonOrgDisabled, "org_disabled"},
		{ReasonCreditExhausted, "credit_exhausted"},
		{ReasonWorkspaceDeactivated, "workspace_deactivated"},
		{ReasonModelLimitExceeded, "model_limit_exceeded"},
		{ReasonTempUnschedRule, "temp_unsched_rule_matched"},
		{ReasonOpenAI403Counted, "openai_403_counted"},
		{ReasonOpenAI403Disabled, "openai_403_disabled"},
		{ReasonAntigravityValidation, "antigravity_403_validation"},
		{ReasonCustomErrorCode, "custom_error_code"},
	}
	for _, tc := range cases {
		if string(tc.reason) != tc.want {
			t.Errorf("Reason %q 字面值漂移：期望 %q，实际 %q（持久化兼容性！）",
				tc.want, tc.want, string(tc.reason))
		}
	}
}

// Reason 集合不能有重复字面值 — 重复就意味着两个语义共享一个字符串，
// 下游过滤 / 统计会错把它们合并。
func TestReason_NoDuplicateValues(t *testing.T) {
	all := []Reason{
		ReasonRateLimit5h, ReasonRateLimit7d, ReasonRateLimitBoth,
		ReasonRateLimitRPM, ReasonRateLimitTPM, ReasonExtraUsageRequired,
		ReasonOverloaded, ReasonTokenRefreshRequired, ReasonTokenRevoked,
		ReasonKYCRequired, ReasonOrgDisabled, ReasonCreditExhausted,
		ReasonWorkspaceDeactivated, ReasonModelLimitExceeded,
		ReasonTempUnschedRule, ReasonOpenAI403Counted, ReasonOpenAI403Disabled,
		ReasonAntigravityValidation, ReasonCustomErrorCode,
	}
	seen := make(map[string]struct{}, len(all))
	for _, r := range all {
		if _, dup := seen[string(r)]; dup {
			t.Errorf("Reason 字面值重复：%q", string(r))
		}
		seen[string(r)] = struct{}{}
	}
}

// =============================================================================
// Service interface 形状保护
// =============================================================================

// fakeService 是 testing-only 最小实现：记录每个 method 被调用时的
// 关键参数，便于断言 interface 形状没被改动。
type fakeService struct {
	handleCalls       int
	clearCalls        int
	updateCalls       int
	lastAccountID     int64
	lastStatusCode    int
	lastActorID       string
	lastHeader        http.Header
	returnedDecision  Decision
	returnedHandleErr error
	returnedClearErr  error
	returnedUpdateErr error
}

func (f *fakeService) HandleUpstreamError(_ context.Context, accountID int64, status int,
	respHeaders http.Header, _ []byte) (Decision, error) {
	f.handleCalls++
	f.lastAccountID = accountID
	f.lastStatusCode = status
	f.lastHeader = respHeaders
	return f.returnedDecision, f.returnedHandleErr
}

func (f *fakeService) ClearCascade(_ context.Context, accountID int64, actorID string) error {
	f.clearCalls++
	f.lastAccountID = accountID
	f.lastActorID = actorID
	return f.returnedClearErr
}

func (f *fakeService) UpdateSessionWindow(_ context.Context, accountID int64, headers http.Header) error {
	f.updateCalls++
	f.lastAccountID = accountID
	f.lastHeader = headers
	return f.returnedUpdateErr
}

// 编译期断言：fakeService 满足 Service interface。任何 interface 形状改动
// 都会让本文件编译失败。
var _ Service = (*fakeService)(nil)

// 运行期断言：三个 method 都能正常 invoke、参数透传、返回值原样回传，
// 防 interface 形状默默扩字段或漂移参数顺序。
func TestService_InterfaceContract(t *testing.T) {
	ctx := context.Background()
	fake := &fakeService{
		returnedDecision: Decision{
			StateChange:       StateRateLimited,
			CooldownUntil:     time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
			Reason:            ReasonRateLimit5h,
			ShouldFailover:    true,
			RetryAfterSeconds: 42,
		},
	}

	headers := http.Header{}
	headers.Set("Retry-After", "60")
	headers.Set("X-RateLimit-Reset", "1731000000")

	const accountID int64 = 9001
	dec, err := fake.HandleUpstreamError(ctx, accountID, http.StatusTooManyRequests, headers, []byte("{}"))
	if err != nil {
		t.Fatalf("HandleUpstreamError: 不期望错误: %v", err)
	}
	if fake.handleCalls != 1 {
		t.Fatalf("HandleUpstreamError 调用次数=%d，期望 1", fake.handleCalls)
	}
	if fake.lastAccountID != accountID {
		t.Errorf("accountID 透传错误：期望 %d 实际 %d", accountID, fake.lastAccountID)
	}
	if fake.lastStatusCode != http.StatusTooManyRequests {
		t.Errorf("statusCode 透传错误：期望 429 实际 %d", fake.lastStatusCode)
	}
	if dec.StateChange != StateRateLimited {
		t.Errorf("Decision.StateChange 回传错误：期望 StateRateLimited，实际 %d", dec.StateChange)
	}
	if dec.Reason != ReasonRateLimit5h {
		t.Errorf("Decision.Reason 回传错误：期望 ReasonRateLimit5h，实际 %s", dec.Reason)
	}
	if dec.RetryAfterSeconds != 42 {
		t.Errorf("Decision.RetryAfterSeconds 回传错误：期望 42，实际 %d", dec.RetryAfterSeconds)
	}
	if !dec.ShouldFailover {
		t.Errorf("Decision.ShouldFailover 回传错误：期望 true")
	}

	const actor = "admin:operator-7"
	if err := fake.ClearCascade(ctx, accountID, actor); err != nil {
		t.Fatalf("ClearCascade: 不期望错误: %v", err)
	}
	if fake.clearCalls != 1 || fake.lastActorID != actor {
		t.Errorf("ClearCascade 参数透传错误：calls=%d actor=%q", fake.clearCalls, fake.lastActorID)
	}

	if err := fake.UpdateSessionWindow(ctx, accountID, headers); err != nil {
		t.Fatalf("UpdateSessionWindow: 不期望错误: %v", err)
	}
	if fake.updateCalls != 1 {
		t.Errorf("UpdateSessionWindow 调用次数=%d，期望 1", fake.updateCalls)
	}
}

// =============================================================================
// Decision 零值合理性
// =============================================================================

// 零值 Decision 必须能被 caller 安全处理：StateChange=StateNoChange、
// ShouldFailover=false、CooldownUntil=零时间。否则一个误初始化的
// Decision 就会让 Selector 错误地把 account 标 RateLimited。
func TestDecision_ZeroValueIsSafe(t *testing.T) {
	var d Decision
	if d.StateChange != StateNoChange {
		t.Errorf("Decision 零值 StateChange 应是 StateNoChange(0)，实际 %d", d.StateChange)
	}
	if d.ShouldFailover {
		t.Errorf("Decision 零值 ShouldFailover 应为 false")
	}
	if !d.CooldownUntil.IsZero() {
		t.Errorf("Decision 零值 CooldownUntil 应为零时间，实际 %v", d.CooldownUntil)
	}
	if d.RetryAfterSeconds != 0 {
		t.Errorf("Decision 零值 RetryAfterSeconds 应为 0，实际 %d", d.RetryAfterSeconds)
	}
	if string(d.Reason) != "" {
		t.Errorf("Decision 零值 Reason 应为空字符串，实际 %q", string(d.Reason))
	}
}
