package hermeshttp

import (
	"net/http"
	"sync"
	"testing"
	"time"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
)

// 本文件检验处理器侧按运营者令牌限流：它只对真正已确认的执行计数，预览和拒绝不计，
// 并按运营者令牌而非租户划分限流键。未配置限流参数时不改变改动路径行为。
// 固定时钟使窗口确定性。测试替身
// (fakeMutator / mutatingRegistry / mutateCounters / buildMutateHandler /
// mutateRequest / operator / decodeBody)复用自本包内的 tools_mutate_handler_test.go
// + tools_handler_test.go。

// fixedClock 返回一个 Now 函数,只有当测试推动它时才前进,使滑动窗口在测试期间
// 不会漂移。
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// operatorTok 构造一个带指定管理员令牌编号的操作者身份，使测试能在同一租户内
// 驱动两个不同的管理员令牌。
func operatorTok(tenant, tokenID int64) (sessionauth.Identity, adminActor) {
	ident, actor := operator(tenant)
	actor.ID = tokenID
	return ident, actor
}

// confirmOnce 为 account_pause 跑一整套 preview+confirm,并返回 confirm 的响应
// recorder。
func confirmOnce(t *testing.T, h handler, ident sessionauth.Identity, actor adminActor) (status int) {
	t.Helper()
	preview := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
	corr, ok := decodeBody(t, preview)["correlation_id"].(string)
	if !ok {
		t.Fatalf("no correlation_id in preview body=%s", preview.Body.String())
	}
	confirm := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true,"correlation_id":"`+corr+`"}`)
	return confirm.Code
}

// --- 测试 6:per-token 限流,按 token 而非按 tenant ------------------

func TestS2_RateLimitPerTokenNotPerTenant(t *testing.T) {
	// 回归(S2 c,区分性):注入时钟下 PER_TOKEN=2。Token A 得到两次 confirm
	// (均通过),第三次 confirm -> 429。Token B(同一 tenant、不同 token)的首次
	// confirm -> 通过,证明配额按 operator TOKEN 划分,而非按 tenant。
	//
	// 变异检查(已运行 + 确认变红,随后恢复):
	//   - 把限流键改用租户编号而非 actor.ID -> 令牌 B 的首次确认
	//     会被 token A 的配额限流 -> `bFirst==200` 防护变红;
	//   - 删掉 confirmMutation 里的限流检查 -> token A 的第三次 confirm 通过(200)
	//     -> `aThird==429` 防护变红。
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	c := &mutateCounters{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, &fakeMutator{})
	h.mutateRateLimiter = mutateguard.NewRateLimiter(2, time.Minute, 0, clock.Now)

	identA, actorA := operatorTok(7, 1001)
	identB, actorB := operatorTok(7, 2002) // 同一 tenant 7,不同 token

	if s := confirmOnce(t, h, identA, actorA); s != http.StatusOK {
		t.Fatalf("token A confirm #1 status=%d want 200", s)
	}
	if s := confirmOnce(t, h, identA, actorA); s != http.StatusOK {
		t.Fatalf("token A confirm #2 status=%d want 200", s)
	}
	aThird := confirmOnce(t, h, identA, actorA)
	if aThird != http.StatusTooManyRequests {
		t.Fatalf("token A confirm #3 status=%d want 429 (PER_TOKEN=2 exhausted)", aThird)
	}
	bFirst := confirmOnce(t, h, identB, actorB)
	if bFirst != http.StatusOK {
		t.Fatalf("token B confirm #1 status=%d want 200 — budget is per-token, not per-tenant", bFirst)
	}
}

// --- 测试 7:限流只对真实执行计数,不对 preview 计数 -------------

func TestS2_RateLimitCountsOnlyConfirmedNotPreviews(t *testing.T) {
	// 回归(S2 c,区分性):PER_TOKEN=2 时,五次 dry-run PREVIEW 不得消耗配额;
	// 随后两次 CONFIRM 均通过。限流器在 confirm-id 消费之后才检查,因此 preview 永远
	// 到不了它。
	//
	// 变异检查(已运行 + 确认变红,随后恢复):把限流检查上移到
	// `if !req.Confirm { previewMutation }` 分支之上(例如移到 executeMutatingTool
	// 里 confirm 分流之前,或消费之前),使 preview 也消耗配额;于是 5 次 preview
	// 耗尽 2 的配额,首次 confirm 返回 429 -> `c1==200` 防护变红。
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	c := &mutateCounters{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, &fakeMutator{})
	h.mutateRateLimiter = mutateguard.NewRateLimiter(2, time.Minute, 0, clock.Now)

	ident, actor := operatorTok(7, 3003)

	// 5 次 preview(dry-run、confirm=false)——这些不得计数。
	for i := 0; i < 5; i++ {
		preview := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
		if preview.Code != http.StatusOK || decodeBody(t, preview)["dry_run"] != true {
			t.Fatalf("preview #%d did not return a dry-run: status=%d body=%s", i, preview.Code, preview.Body.String())
		}
	}

	if c1 := confirmOnce(t, h, ident, actor); c1 != http.StatusOK {
		t.Fatalf("confirm #1 after 5 previews status=%d want 200 — previews must not burn budget", c1)
	}
	if c2 := confirmOnce(t, h, ident, actor); c2 != http.StatusOK {
		t.Fatalf("confirm #2 after 5 previews status=%d want 200 (budget=2)", c2)
	}
}

// --- 测试 8:所有 knob 未设置 == 与旧行为逐字节一致 ----------------

func TestS2_AllKnobsUnsetIsLegacyBehavior(t *testing.T) {
	// 回归(S2,默认保守 / 区分性):在未接入任何 guard 时——由 buildMutateHandler
	// 构造的 handler(无限流器)、其下的 orchestrator 无并发上限、无 tx deadline——
	// mutating 路径表现与旧路径完全一致:接连多次 confirm 全部成功(没有 429 busy /
	// 没有限流拒绝),也不发出任何 SET LOCAL statement_timeout。
	//
	// 变异检查(已运行 + 确认变红,随后恢复):让任何 guard 在其哨兵未设置时默认变成
	// 「非禁用」——例如让 NewMutateOrchestrator 即便没有 WithTxDeadline 选项也默认把
	// txDeadline 设为 90s,或让 NewRateLimiter 把 limit<=0 当作「默认 30」而非禁用。
	// 那样一来,旧部署就会开始发出 statement_timeout / 429,这些「全成功」防护即变红。

	// 无限流器的 handler -> 多次 confirm 都不会出现 429;orchestrator 自身的旧行为
	// (无选项时无 statement_timeout、无 ErrMutateBusy)在
	// internal/hermesops/mutate_guard_test.go 中证明。
	c := &mutateCounters{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, &fakeMutator{})
	// h.mutateRateLimiter 保持 nil(禁用哨兵)。
	ident, actor := operator(7)
	for i := 0; i < 20; i++ {
		if s := confirmOnce(t, h, ident, actor); s != http.StatusOK {
			t.Fatalf("confirm #%d status=%d want 200 — unset rate knob must be unbounded (legacy)", i, s)
		}
	}
	if c.mutates != 20 {
		t.Fatalf("mutates=%d want 20 — every confirm should have mutated with no guard", c.mutates)
	}
}
