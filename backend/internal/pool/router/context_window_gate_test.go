package router

import (
	"context"
	"errors"
	"testing"
)

// ctxWindowReq 构造一个携带 gate 所读取的三个 context-window
// 输入的 SelectionRequest。
func ctxWindowReq(estimate, window, maxOut int) SelectionRequest {
	return SelectionRequest{
		TenantID:             1,
		EstimatedInputTokens: estimate,
		ModelContextWindow:   window,
		MaxOutputTokens:      maxOut,
	}
}

// TestContextWindowGate_OverBudget_Excluded 证明 gate 在与 window 比较前,
// 会把预留的输出空间加到输入估算值上。
//
// 变异:去掉 "+ MaxOutputTokens" 这一项后,单凭 195000(< 200000)
// 就会错误地放行第二个用例 → 变红。正确与被破坏的差异恰好就在
// 输出预留这一项上。
func TestContextWindowGate_OverBudget_Excluded(t *testing.T) {
	gate := ContextWindowGate{}

	// 190000 输入 + 8192 输出 = 198192 < 200000 → 放行。
	ok, _, err := gate.Allow(context.Background(), nil, ctxWindowReq(190000, 200000, 8192))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("198192 fits in 200000 → must allow")
	}

	// 195000 输入 + 8192 输出 = 203192 > 200000 → 剔除。
	ok, reason, err := gate.Allow(context.Background(), nil, ctxWindowReq(195000, 200000, 8192))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("203192 overflows 200000 → must exclude (output reservation must be added)")
	}
	if reason != GateFailureContextWindow {
		t.Fatalf("reason=%q want %q", reason, GateFailureContextWindow)
	}
}

// TestContextWindowGate_UnknownWindow_FailOpen 证明 per-model window 为零/未设置时,
// 无论估算值多大都不会把账号下架。
//
// 变异:把 window<=0 的护栏改成将 0 当作真实上限处理,巨大的估算值就会
// 溢出 0 → 变红。
func TestContextWindowGate_UnknownWindow_FailOpen(t *testing.T) {
	gate := ContextWindowGate{}
	ok, _, err := gate.Allow(context.Background(), nil, ctxWindowReq(10_000_000, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("unknown window (0) must fail-open (allow) even for a huge estimate")
	}
}

// TestContextWindowGate_NoEstimate_FailOpen 证明估算值为零/未接线时不会把账号下架,
// 即便单是预留的输出就已超过 window。
//
// 变异:移除 estimate<=0 的短路,使 0+输出(输出>上限)
// 溢出 → 变红。
func TestContextWindowGate_NoEstimate_FailOpen(t *testing.T) {
	gate := ContextWindowGate{}
	// estimate=0, window=1000, output=5000:若没有估算值短路,
	// 0+5000 > 1000 就会错误地剔除。
	ok, _, err := gate.Allow(context.Background(), nil, ctxWindowReq(0, 1000, 5000))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no estimate (0) must fail-open (allow)")
	}
}

// TestContextWindowGate_AtExactBoundary_Allowed 钉死严格大于的边界:
// 恰好放得下的请求会被放行。
//
// 变异:把 > 改成 >=,恰好放得下的用例就会被剔除 → 变红。
func TestContextWindowGate_AtExactBoundary_Allowed(t *testing.T) {
	gate := ContextWindowGate{}
	// 191808 + 8192 == 200000,恰好相等。
	ok, _, err := gate.Allow(context.Background(), nil, ctxWindowReq(191808, 200000, 8192))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("estimate+output == window must allow (strict > comparison)")
	}
}

// TestContextWindowGate_IgnoresAccount 证明 gate 的判定与候选账号无关
//(context window 是 per-model 而非 per-account):同一个溢出请求
// 无论传入哪个账号都会被剔除。
func TestContextWindowGate_IgnoresAccount(t *testing.T) {
	gate := ContextWindowGate{}
	req := ctxWindowReq(250000, 200000, 0)
	for _, acct := range []*AccountSnapshot{nil, {ID: 7}, {ID: 9, MaxSessions: 3}} {
		ok, reason, err := gate.Allow(context.Background(), acct, req)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatalf("overflow must exclude for account %+v", acct)
		}
		if reason != GateFailureContextWindow {
			t.Fatalf("reason=%q want %q", reason, GateFailureContextWindow)
		}
	}
}

// TestContextWindowGate_ChainOrdering_RecordsReason 证明该 gate 已接入
// DefaultGateChain 的 ordered() 槽位,并带上 GateFailureContextWindow
// 兜底 reason —— 即对一个溢出请求跑完整条 chain 时,
// 暴露的是 context-window reason,而非空的/错误的 reason。
//
// 变异:漏掉 ordered() 条目后,chain 永远不会运行该 gate
// → 溢出请求被放行 → 在 !ok 断言处变红;若兜底 reason 写错,
// reason 断言会变红。
func TestContextWindowGate_ChainOrdering_RecordsReason(t *testing.T) {
	chain := DefaultGateChain()
	prepared := chain.ForSelection(context.Background(), ctxWindowReq(300000, 200000, 0))
	ok, reason, err := prepared.Allow(context.Background(), &AccountSnapshot{ID: 1, TenantID: 1}, ctxWindowReq(300000, 200000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("DefaultGateChain must exclude an overflowing request (gate not in ordered()?)")
	}
	if reason != GateFailureContextWindow {
		t.Fatalf("chain reason=%q want %q", reason, GateFailureContextWindow)
	}
}

// TestSelect_AllCandidatesOverflow_TriggersNoEligible 证明端到端的
// 优雅降级契约:当唯一候选的 per-model window 放不下该请求时,
// Select 返回 ErrNoEligibleAccount(驱动 dispatch 层 model-fallback 循环的
// 无容量信号)—— 而非裸 error。
//
// 变异:若 gate 硬返回一个 error 而非 (false,reason),
// Select 会把该裸 error 冒泡上来,errors.Is(err, ErrNoEligibleAccount)
// 就会为 false → 变红。若该 gate 不在 chain 中,账号会被选中、
// err 为 nil → 变红。
func TestSelect_AllCandidatesOverflow_TriggersNoEligible(t *testing.T) {
	accounts := []*AccountSnapshot{
		{
			ID:             101,
			TenantID:       7,
			Priority:       1,
			LoadRate:       0.01,
			MaxConcurrency: 4,
			HealthState:    "healthy",
			ProtocolFamily: "anthropic_messages",
		},
	}
	gates := DefaultGateChain()
	gates.ContextWindow = ContextWindowGate{}
	sel := NewDefaultSelector(
		&stubAccountSource{accounts: accounts},
		WithSlotManager(newMemSlotManager()),
		WithGateChain(gates),
	)

	_, err := sel.Select(context.Background(), SelectionRequest{
		TenantID:             7,
		RequestedModel:       "claude-3-5-sonnet",
		ProtocolFamily:       "anthropic_messages",
		EstimatedInputTokens: 250000,
		ModelContextWindow:   200000,
	})
	if !errors.Is(err, ErrNoEligibleAccount) {
		t.Fatalf("all candidates overflow must yield ErrNoEligibleAccount, got %v", err)
	}

	// 对照:在完全相同的配置下,一个放得下的请求必须选中该账号,
	// 以证明上面的 no-eligible 是由溢出导致,而非 fixture 本身。
	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID:             7,
		RequestedModel:       "claude-3-5-sonnet",
		ProtocolFamily:       "anthropic_messages",
		EstimatedInputTokens: 1000,
		ModelContextWindow:   200000,
	})
	if err != nil {
		t.Fatalf("fitting request must select an account, got err %v", err)
	}
	if res == nil || res.AccountID != 101 {
		t.Fatalf("fitting request should select account 101, got %+v", res)
	}
}
