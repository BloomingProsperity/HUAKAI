package router

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestATBFCSelectorTypedExhaustionFamilies 锁定 selector 的结构化耗尽语义：
// 只有纯容量族可进入 quota；静态不匹配、上下文窗口和混合族必须可区分。
// 变异：删除 family 归约或把 mixed 按最后一个 reason 归类，会让精确 family/
// reason 断言变红。
func TestATBFCSelectorTypedExhaustionFamilies(t *testing.T) {
	now := time.Date(2026, 7, 14, 20, 0, 0, 0, time.UTC)
	accounts := []*AccountSnapshot{
		snap(101, 1, 100, 0.1, now.Add(-time.Hour)),
		snap(102, 1, 100, 0.2, now.Add(-2*time.Hour)),
	}
	tests := []struct {
		name             string
		gates            GateChain
		slots            SlotManager
		req              SelectionRequest
		wantFamily       ExhaustionFamily
		wantReasons      map[GateFailureReason]int
		wantPureCapacity bool
		wantContextOnly  bool
	}{
		{
			name: "纯健康容量",
			gates: gateChainWithHealth(reasonByAccountGate{
				101: GateFailureHealth,
				102: GateFailureHealth,
			}),
			slots:            newMemSlotManager(),
			wantFamily:       ExhaustionFamilyCapacity,
			wantReasons:      map[GateFailureReason]int{GateFailureHealth: 2},
			wantPureCapacity: true,
		},
		{
			name: "纯静态协议不匹配",
			gates: gateChainWithProtocol(reasonByAccountGate{
				101: GateFailureProtocolFamily,
				102: GateFailureProtocolFamily,
			}),
			slots:       newMemSlotManager(),
			wantFamily:  ExhaustionFamilyStaticMismatch,
			wantReasons: map[GateFailureReason]int{GateFailureProtocolFamily: 2},
		},
		{
			name: "容量与静态混合",
			gates: gateChainWithHealth(reasonByAccountGate{
				101: GateFailureHealth,
				102: GateFailureCredential,
			}),
			slots:       newMemSlotManager(),
			wantFamily:  ExhaustionFamilyMixed,
			wantReasons: map[GateFailureReason]int{GateFailureHealth: 1, GateFailureCredential: 1},
		},
		{
			name:  "纯上下文窗口",
			gates: gateChainWithContextWindow(),
			slots: newMemSlotManager(),
			req: SelectionRequest{
				EstimatedInputTokens: 101,
				ModelContextWindow:   100,
			},
			wantFamily:      ExhaustionFamilyContextWindow,
			wantReasons:     map[GateFailureReason]int{GateFailureContextWindow: 2},
			wantContextOnly: true,
		},
		{
			name:             "纯并发槽耗尽",
			gates:            DefaultGateChain(),
			slots:            alwaysNoSlot{},
			wantFamily:       ExhaustionFamilyCapacity,
			wantReasons:      map[GateFailureReason]int{GateFailureSlotCapacity: 2},
			wantPureCapacity: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			req.TenantID = 1
			req.RequestedModel = "model-a"
			sel := NewDefaultSelector(
				&stubAccountSource{accounts: accounts},
				WithGateChain(tc.gates),
				WithSlotManager(tc.slots),
			)

			res, err := sel.Select(context.Background(), req)
			if res != nil || (!errors.Is(err, ErrNoEligibleAccount) && !errors.Is(err, ErrAllChannelsDegraded)) {
				t.Fatalf("res/err=%+v/%v，期望 nil 且保持既有无容量 errors.Is 语义", res, err)
			}
			var noCap *NoCapacityError
			if !errors.As(err, &noCap) {
				t.Fatalf("err=%v，期望结构化 NoCapacityError", err)
			}
			if noCap.Exhaustion.Family != tc.wantFamily {
				t.Fatalf("family=%q，期望 %q；exhaustion=%+v", noCap.Exhaustion.Family, tc.wantFamily, noCap.Exhaustion)
			}
			if !equalFailureCounts(noCap.Exhaustion.Reasons, tc.wantReasons) {
				t.Fatalf("reasons=%v，期望 %v", noCap.Exhaustion.Reasons, tc.wantReasons)
			}
			if noCap.PureCapacity() != tc.wantPureCapacity {
				t.Fatalf("PureCapacity()=%v，期望 %v", noCap.PureCapacity(), tc.wantPureCapacity)
			}
			if noCap.ContextWindowOnly() != tc.wantContextOnly {
				t.Fatalf("ContextWindowOnly()=%v，期望 %v", noCap.ContextWindowOnly(), tc.wantContextOnly)
			}
		})
	}
}

// TestATBFCPASRPreservesTypedExhaustion 锁定 PASR 与默认 selector 的耗尽契约一致，
// 避免切换调度模式后静态不匹配被误当成 quota 容量。
func TestATBFCPASRPreservesTypedExhaustion(t *testing.T) {
	now := time.Date(2026, 7, 14, 20, 0, 0, 0, time.UTC)
	accounts := []*AccountSnapshot{
		snap(101, 1, 100, 0.1, now.Add(-time.Hour)),
		snap(102, 1, 100, 0.2, now.Add(-2*time.Hour)),
	}
	tests := []struct {
		name       string
		gates      GateChain
		wantFamily ExhaustionFamily
	}{
		{name: "纯容量", gates: gateChainWithHealth(reasonByAccountGate{
			101: GateFailureHealth, 102: GateFailureModelCooldown,
		}), wantFamily: ExhaustionFamilyCapacity},
		{name: "静态与容量混合", gates: gateChainWithHealth(reasonByAccountGate{
			101: GateFailureHealth, 102: GateFailureCredential,
		}), wantFamily: ExhaustionFamilyMixed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := NewPASRSelector(PASRSelectorConfig{
				Accounts: &stubAccountSource{accounts: accounts},
				Segments: NewSegmentTable(SegmentTableConfig{}),
				Gates:    tc.gates,
			})
			if err != nil {
				t.Fatalf("NewPASRSelector: %v", err)
			}
			res, err := sel.Select(context.Background(), SelectionRequest{
				TenantID: 1, RequestedModel: "model-a",
			})
			var noCap *NoCapacityError
			if res != nil || !errors.As(err, &noCap) || noCap.Exhaustion.Family != tc.wantFamily {
				t.Fatalf("res/err/exhaustion=%+v/%v/%+v，期望 family=%q", res, err, noCap, tc.wantFamily)
			}
		})
	}
}

func TestATBFCExhaustionFamilyMatrixIsComplete(t *testing.T) {
	tests := []struct {
		family  ExhaustionFamily
		reasons []GateFailureReason
	}{
		{family: ExhaustionFamilyCapacity, reasons: []GateFailureReason{
			GateFailureHealth, GateFailureAuthCooldown, GateFailureModelCooldown,
			GateFailureWindowCost, GateFailureSessionCount, GateFailureRatePrecheck,
			GateFailureSlotCapacity, GateFailureScoredBand,
		}},
		{family: ExhaustionFamilyStaticMismatch, reasons: []GateFailureReason{
			GateFailureTenantFilter, GateFailureLifecycle, GateFailureChannel,
			GateFailureProtocolFamily, GateFailureModel, GateFailureCapability,
			GateFailureCredential, GateFailureGroupPolicy, GateFailurePerRequestExclusion,
			GateFailurePinnedAccount, GateFailureSlotManager,
		}},
		{family: ExhaustionFamilyContextWindow, reasons: []GateFailureReason{
			GateFailureContextWindow,
		}},
	}
	for _, tc := range tests {
		for _, reason := range tc.reasons {
			t.Run(string(reason), func(t *testing.T) {
				if got := exhaustionFamily(map[GateFailureReason]int{reason: 1}); got != tc.family {
					t.Fatalf("reason=%q family=%q，期望 %q", reason, got, tc.family)
				}
			})
		}
	}
}

func gateChainWithHealth(g Gate) GateChain {
	chain := DefaultGateChain()
	chain.Health = g
	return chain
}

func gateChainWithProtocol(g Gate) GateChain {
	chain := DefaultGateChain()
	chain.Protocol = g
	return chain
}

func gateChainWithContextWindow() GateChain {
	chain := DefaultGateChain()
	chain.ContextWindow = ContextWindowGate{}
	return chain
}

type reasonByAccountGate map[int64]GateFailureReason

func (g reasonByAccountGate) Allow(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (bool, GateFailureReason, error) {
	if account == nil {
		return false, GateFailureLifecycle, nil
	}
	reason := g[account.ID]
	if reason == "" {
		return true, "", nil
	}
	return false, reason, nil
}

type alwaysNoSlot struct{}

func (alwaysNoSlot) Acquire(context.Context, *AccountSnapshot, SelectionRequest) (*AcquireResult, error) {
	return nil, ErrNoSlotAvailable
}

func equalFailureCounts(got, want map[GateFailureReason]int) bool {
	if len(got) != len(want) {
		return false
	}
	for reason, count := range want {
		if got[reason] != count {
			return false
		}
	}
	return true
}
