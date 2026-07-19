package moderation

import (
	"context"
	"fmt"
	"testing"
)

// TestScreener_B4_AutoBanNotEvadableByNonUserTailRole 判别测试 (bug B4 [S2]).
//
// 缺陷:recordAutoBan() 只要 repeatAgentTurn(req) 为真(尾消息 role != "user",
// 而 TailRole 完全由客户端请求体最后一条消息的 role 决定)就短路跳过 auto-ban
// 计数。滥用者对每一条违规请求都追加一条 role="assistant"/"tool"/"system" 的尾
// 消息,即可让 CountBlocksInWindow 永不递增、DisableAPIKey 永不触发——API key
// 永远不会被自动封禁,尽管每条请求仍被逐条拦截。
//
// 正确行为:持续发送违规内容的 key,即便每条尾消息都非 user,也必须持续喂给
// auto-ban 计数器,达到阈值后被封禁。逐条拦截照旧、clean 审计降噪照旧。
//
// 修复前:非 user 尾的违规请求 ban.calls 恒为 0 → RED。
func TestScreener_B4_AutoBanNotEvadableByNonUserTailRole(t *testing.T) {
	const threshold = 3
	mk := func() (Screener, *banCounterSpy) {
		ban := &banCounterSpy{}
		s := NewScreener(ScreenerDeps{
			Config: configStub{cfg: ModerationConfig{
				Enabled: true, FailClosed: true, SampleRatePct: 100,
				BanThreshold: threshold, BanWindowSeconds: 3600,
			}},
			Keywords: &keywordStoreStub{rules: []KeywordRule{{ID: 17, Keyword: "forbidden", ReasonCode: "policy_keyword"}}},
			Hashes:   hashStoreStub{},
			Audit:    &auditSpy{},
			Ban:      ban,
		})
		return s, ban
	}

	// 滥用向量:每条请求都携带违规关键词,但尾消息 role="assistant",企图规避 auto-ban。
	for _, tail := range []string{"assistant", "tool", "system"} {
		s, ban := mk()
		for i := 0; i < threshold; i++ {
			// 每轮内容各不相同(不同的追加尾文本),排除任何按 payload 去重的可能:
			// 这是真正的持续滥用,而非同一条消息在 agent 循环里的重发。
			body := []byte(fmt.Sprintf(
				`{"messages":[{"role":"user","content":"contains a forbidden phrase %d"},{"role":%q,"content":"evade-%d"}]}`,
				i, tail, i))
			res, err := s.Screen(context.Background(), ScreenRequest{
				TenantID: 7, APIKeyID: 11, RequestID: fmt.Sprintf("r-%s-%d", tail, i),
				Body: body, TailRole: tail,
			})
			if err != nil {
				t.Fatalf("tail=%s i=%d screen err: %v", tail, i, err)
			}
			// 逐条拦截必须照旧(尾 role 不影响拦截判定)。
			if res.Decision != DecisionBlockKeyword {
				t.Fatalf("tail=%s i=%d 拦截判定被放水: %q", tail, i, res.Decision)
			}
		}
		// 正确行为:threshold 条违规请求即使尾非 user,也应逐条喂给 auto-ban 计数器,
		// 否则 DisableAPIKey 永远无法触发,构成完整的自动封禁绕过。
		if ban.calls != threshold {
			t.Fatalf("tail=%s: 非 user 尾的持续违规必须喂 auto-ban 计数器: ban.calls=%d want %d (自动封禁被绕过)", tail, ban.calls, threshold)
		}
	}
}
