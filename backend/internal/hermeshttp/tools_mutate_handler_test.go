package hermeshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// fakeMutator 是 MutateOrchestrator 的替身,直接运行 mutate 回调(无真实 DB)并
// 对执行次数计数,使 handler 测试能证明 confirm 流程恰好驱动一次变更。
type fakeMutator struct {
	executions int
	lastLock   string
	lastRec    hermesops.MutationAuditRecord
	failWith   error
}

func (f *fakeMutator) Execute(ctx context.Context, lockKey string, rec hermesops.MutationAuditRecord, mutate func(ctx context.Context, tx pgx.Tx) (hermesops.ToolResult, error)) (hermesops.ToolResult, error) {
	f.executions++
	f.lastLock = lockKey
	f.lastRec = rec
	if f.failWith != nil {
		return hermesops.ToolResult{}, f.failWith
	}
	return mutate(ctx, nil)
}

// mutatingRegistry 注册一个由简单的 resolve/mutate 闭包 + 计数器支撑的 mutating
// 工具(account_pause),使 handler 测试能观察变更是否真正运行以及它看到了什么。
type mutateCounters struct {
	resolves int
	mutates  int
	enabled  bool
	state    string
}

func mutatingRegistry(c *mutateCounters) *hermesops.Registry {
	reg := hermesops.NewRegistry()
	mustRegisterTestTool(reg, hermesops.ToolSpec{
		Name: hermesops.ToolAccountPause, Category: hermesops.CategoryMutating,
		Description: "暂停测试账号", Mutating: true, RequiresConfirmation: true, RequiredRole: hermesops.RoleTenantOperator,
		InputSchema: hermesops.ObjectSchema(map[string]any{
			"account_id":    hermesops.PositiveIntegerSchema("账号编号"),
			"operator_note": hermesops.NonEmptyStringSchema("本次操作标记"),
		}, "account_id"),
		Resolve: func(_ context.Context, req hermesops.ToolRequest) (hermesops.MutationPlan, error) {
			c.resolves++
			id, err := hermesops.ArgInt(req.Args, "account_id")
			if err != nil {
				return hermesops.MutationPlan{}, err
			}
			state := c.state
			if state == "" {
				state = "active"
			}
			return hermesops.MutationPlan{
				TargetType: "provider_account", TargetID: id,
				LockKey: "lock:7:" + itoa(id),
				Preview: map[string]any{"current_state": state, "next_enabled": false},
			}, nil
		},
		Mutate: func(_ context.Context, _ hermesops.ToolRequest, _ hermesops.MutationPlan) (hermesops.ToolResult, error) {
			c.mutates++
			c.enabled = false
			return hermesops.ToolResult{Summary: map[string]any{"enabled": false}}, nil
		},
	})
	// 租户管理员可执行的本租户死信恢复工具。
	mustRegisterTestTool(reg, hermesops.ToolSpec{
		Name: hermesops.ToolDLQReplay, Category: hermesops.CategoryMutating,
		Description: "重放测试死信", Mutating: true, RequiresConfirmation: true, RequiredRole: hermesops.RoleTenantOperator,
		InputSchema: hermesops.ObjectSchema(map[string]any{
			"id": hermesops.PositiveIntegerSchema("死信编号"),
		}, "id"),
		Resolve: func(_ context.Context, _ hermesops.ToolRequest) (hermesops.MutationPlan, error) {
			return hermesops.MutationPlan{TargetType: "dlq_event", TargetID: 1}, nil
		},
		Mutate: func(_ context.Context, _ hermesops.ToolRequest, _ hermesops.MutationPlan) (hermesops.ToolResult, error) {
			return hermesops.ToolResult{Summary: map[string]any{"ok": true}}, nil
		},
	})
	return reg
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func buildMutateHandler(reg ToolRegistry, calls *fakeToolCalls, mutator MutateOrchestrator) handler {
	return handler{
		svc:          hermes.NewService(&auditCaptureStore{}),
		tools:        reg,
		toolCalls:    calls,
		mutator:      mutator,
		confirmStore: hermesconfirm.NewCache(),
	}
}

// mutatingPlusReadOnlyRegistry 同时注册 account_pause(mutating、tenant_operator
// 下限)与一个只读诊断工具(dlq_inspect),使 KNOB-A 测试能证明 kill-switch 拒绝
// mutating 工具的同时只读路径仍保持可用。
func mutatingPlusReadOnlyRegistry(c *mutateCounters) *hermesops.Registry {
	reg := mutatingRegistry(c)
	mustRegisterTestTool(reg, hermesops.ToolSpec{
		Name: hermesops.ToolDLQInspect, Category: hermesops.CategoryDiagnostic,
		Description: "读取测试死信", ReadOnly: true, RequiredRole: hermesops.RoleTenantOperator,
		InputSchema: hermesops.ObjectSchema(nil),
		Run: func(_ context.Context, _ hermesops.ToolRequest) (hermesops.ToolResult, error) {
			return hermesops.ToolResult{Summary: map[string]any{"dlq_count": 0}}, nil
		},
	})
	return reg
}

func mutateRequest(h handler, ident sessionauth.Identity, actor adminActor, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/tool-execute", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), authContextKey{}, ident)
	ctx = context.WithValue(ctx, adminActorContextKey{}, actor)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.executeTool(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return m
}

// --- L2 dry-run + confirm（试运行 + 确认）---------------------------------------------------

func TestMutate_DryRunPreviewsWithoutMutating(t *testing.T) {
	// 回归(L2):confirm=false 返回 preview + correlation_id 且不做变更。变异检查:
	// 把 confirm=false 引导到 confirmMutation,mutates 计数会变成 1(变红)。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident, actor := operator(7)

	rec := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["dry_run"] != true || body["correlation_id"] == nil || body["correlation_id"] == "" {
		t.Fatalf("preview body missing dry_run/correlation_id: %v", body)
	}
	if c.mutates != 0 || mut.executions != 0 {
		t.Fatalf("dry-run mutated: mutates=%d orchestrator=%d want 0/0", c.mutates, mut.executions)
	}
}

func TestMutate_ConfirmExecutesExactlyOnce(t *testing.T) {
	// 回归(L2/L5):confirm=true 且 correlation_id 匹配时恰好变更一次。变异检查:
	// 跳过消费 correlation_id,第二次 confirm 就会重新执行(executions=2)。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident, actor := operator(7)

	preview := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
	corr := decodeBody(t, preview)["correlation_id"].(string)

	confirm := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true,"correlation_id":"`+corr+`"}`)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s want 200", confirm.Code, confirm.Body.String())
	}
	if c.mutates != 1 || mut.executions != 1 {
		t.Fatalf("confirm mutates=%d orchestrator=%d want 1/1", c.mutates, mut.executions)
	}
	body := decodeBody(t, confirm)
	if body["dry_run"] != false {
		t.Fatalf("confirm body dry_run=%v want false", body["dry_run"])
	}
	if mut.lastRec.AdminAction != "hermes.tool.account_pause" {
		t.Fatalf("admin action=%q want hermes.tool.account_pause", mut.lastRec.AdminAction)
	}
}

func TestMutate_ReusedCorrelationIDDoesNotDoubleExecute(t *testing.T) {
	// 回归(L5,区分性):correlation_id 一次性。用同一 id 的第二次 confirm 是 400
	// 且不再变更。变异检查:让 confirmCache.consume 不删除条目,则第二次 confirm
	// 会执行(变红)。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident, actor := operator(7)

	corr := decodeBody(t, mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`))["correlation_id"].(string)
	body := `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true,"correlation_id":"` + corr + `"}`

	first := mutateRequest(h, ident, actor, body)
	second := mutateRequest(h, ident, actor, body)

	if first.Code != http.StatusOK {
		t.Fatalf("first confirm=%d want 200", first.Code)
	}
	if second.Code != http.StatusBadRequest {
		t.Fatalf("second confirm=%d want 400 (single-use)", second.Code)
	}
	if c.mutates != 1 || mut.executions != 1 {
		t.Fatalf("reused id double-executed: mutates=%d orchestrator=%d want 1/1", c.mutates, mut.executions)
	}
}

func TestMutate_StaleOrWrongCorrelationIDIs400NoMutation(t *testing.T) {
	// 回归(L2):带缺失 / 未知 / 错误工具的 correlation_id 的 confirm=true 是 400
	// 且绝不执行。变异检查:去掉 consume() 的 ok 检查,未知 id 就会继续执行。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident, actor := operator(7)

	// 缺失 correlation_id
	r1 := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true}`)
	// 未知 correlation_id
	r2 := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true,"correlation_id":"hmc_does_not_exist"}`)
	for name, r := range map[string]*httptest.ResponseRecorder{"absent": r1, "unknown": r2} {
		if r.Code != http.StatusBadRequest {
			t.Fatalf("%s correlation_id status=%d want 400", name, r.Code)
		}
	}
	if c.mutates != 0 || mut.executions != 0 {
		t.Fatalf("stale/absent id executed: mutates=%d orchestrator=%d want 0/0", c.mutates, mut.executions)
	}
}

func TestMutate_UnknownCorrelationIDAsksOperatorToRePropose(t *testing.T) {
	// HERMES-IP-02:跨副本或过期 token 在本副本未命中时,operator 必须得到可恢复的
	// re-propose/re-dry-run 错误码。变异证伪:把 confirmMutation 改回统一
	// hermes_tool_confirmation_invalid,下面 code 断言会变红。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident, actor := operator(7)

	rec := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true,"correlation_id":"hmc_not_on_this_replica"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	errObj := decodeBody(t, rec)["error"].(map[string]any)
	if got := errObj["code"]; got != "hermes_tool_confirmation_repropose_required" {
		t.Fatalf("error code=%v want hermes_tool_confirmation_repropose_required body=%s", got, rec.Body.String())
	}
	if c.mutates != 0 || mut.executions != 0 {
		t.Fatalf("unknown correlation executed: mutates=%d exec=%d want 0/0", c.mutates, mut.executions)
	}
}

func TestMutate_CorrelationIDBoundToTool(t *testing.T) {
	// 回归(L2):为 account_pause 签发的 correlation_id 不能用来确认另一个工具。
	// 变异检查:去掉 confirmCache.consume 里的 ToolName 匹配,这次跨工具 confirm
	// 就会继续执行。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	platform := func() (sessionauth.Identity, adminActor) {
		return sessionauth.Identity{TenantID: 7, UserID: 42}, adminActor{Source: admin.AdminSourceToken, ID: 99, Role: admin.RolePlatformAdmin}
	}
	ident, actor := platform()

	corr := decodeBody(t, mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`))["correlation_id"].(string)
	// 尝试用 account_pause 的 id 去确认 dlq_replay(另一个工具)。
	rec := mutateRequest(h, ident, actor, `{"tool_name":"dlq_replay","args":{"id":1},"confirm":true,"correlation_id":"`+corr+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-tool confirm status=%d want 400", rec.Code)
	}
	if mut.executions != 0 {
		t.Fatalf("cross-tool confirm executed: %d want 0", mut.executions)
	}
}

func TestMutate_确认时换参会被拒绝且确认被销毁(t *testing.T) {
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident, actor := operator(7)

	preview := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5,"operator_note":"first"}}`)
	corr := decodeBody(t, preview)["correlation_id"].(string)
	changed := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5,"operator_note":"second"},"confirm":true,"correlation_id":"`+corr+`"}`)
	if changed.Code != http.StatusBadRequest {
		t.Fatalf("换参确认 status=%d body=%s，期望 400", changed.Code, changed.Body.String())
	}
	original := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5,"operator_note":"first"},"confirm":true,"correlation_id":"`+corr+`"}`)
	if original.Code != http.StatusBadRequest {
		t.Fatalf("换参冲突后的原请求 status=%d，期望确认已销毁", original.Code)
	}
	if c.mutates != 0 || mut.executions != 0 {
		t.Fatalf("换参确认执行了改动: mutates=%d executions=%d", c.mutates, mut.executions)
	}
}

func TestMutate_确认前目标状态变化必须重新预览(t *testing.T) {
	c := &mutateCounters{state: "active"}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident, actor := operator(7)

	preview := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
	corr := decodeBody(t, preview)["correlation_id"].(string)
	c.state = "rate_limited"
	confirm := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true,"correlation_id":"`+corr+`"}`)
	if confirm.Code != http.StatusBadRequest {
		t.Fatalf("陈旧预览确认 status=%d body=%s，期望 400", confirm.Code, confirm.Body.String())
	}
	if c.mutates != 0 || mut.executions != 0 {
		t.Fatalf("状态漂移后仍执行改动: mutates=%d executions=%d", c.mutates, mut.executions)
	}
}

// --- L1 RBAC ----------------------------------------------------------------

func TestMutate_TenantOperatorCanPreviewOwnTenantDLQReplay(t *testing.T) {
	c := &mutateCounters{}
	mut := &fakeMutator{}
	calls := &fakeToolCalls{}
	h := buildMutateHandler(mutatingRegistry(c), calls, mut)
	ident, actor := operator(7) // tenant_operator(租户运营者)

	rec := mutateRequest(h, ident, actor, `{"tool_name":"dlq_replay","args":{"id":1}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant_operator dlq_replay status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if mut.executions != 0 {
		t.Fatalf("dry-run dlq_replay executed: %d want 0", mut.executions)
	}
	if len(calls.rows) != 1 || !calls.rows[0].DryRun || calls.rows[0].ResultStatus != string(hermesops.ResultOK) {
		t.Fatalf("dry-run row not recorded: %+v", calls.rows)
	}
}

func TestMutate_PlatformAdminCanDLQReplay(t *testing.T) {
	// 回归(L1):platform_admin 可以 dry-run dlq_replay(对上面 RBAC 拒绝测试的
	// 正向对照)。变异检查:它证明上面的 403 是关于 ROLE 的,而非工具坏了。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident := sessionauth.Identity{TenantID: 7, UserID: 42}
	actor := adminActor{Source: admin.AdminSourceToken, ID: 99, Role: admin.RolePlatformAdmin}

	rec := mutateRequest(h, ident, actor, `{"tool_name":"dlq_replay","args":{"id":1}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("platform_admin dlq_replay dry-run status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if decodeBody(t, rec)["dry_run"] != true {
		t.Fatalf("expected dry-run preview")
	}
}

// --- L4 advisory lock(尽力而为:断言 lock key 到达 orchestrator)

func TestMutate_AdvisoryLockKeyPassedToOrchestrator(t *testing.T) {
	// 回归(L4):来自 plan 的 per-target advisory lock key 会到达 orchestrator 的
	// Execute(它在变更前后获取/释放该锁)。变异检查:在 confirmMutation 里传 ""
	// 而非 plan.LockKey,针对具体 key 的断言会失败。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident, actor := operator(7)

	corr := decodeBody(t, mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`))["correlation_id"].(string)
	mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true,"correlation_id":"`+corr+`"}`)
	if mut.lastLock != "lock:7:5" {
		t.Fatalf("orchestrator lock key=%q want lock:7:5 (per-target)", mut.lastLock)
	}
}

// --- L3 orchestrator 中止表现为失败,不重复记录 --------------

func TestMutate_OrchestratorAbortReturnsServiceError(t *testing.T) {
	// 回归:当 orchestrator 中止(审计失败已回滚)时,handler 应表现为失败
	// (而非 200),使调用方知道变更并未发生。变异检查:忽略 Execute 的错误并返回 200。
	c := &mutateCounters{}
	mut := &fakeMutator{failWith: context.DeadlineExceeded}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident, actor := operator(7)

	corr := decodeBody(t, mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`))["correlation_id"].(string)
	rec := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true,"correlation_id":"`+corr+`"}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("orchestrator abort returned 200 — caller would think the mutation succeeded")
	}
}

func TestMutate_RecoveryPendingDoesNotWriteDuplicateErrorRow(t *testing.T) {
	counters := &mutateCounters{}
	calls := &fakeToolCalls{}
	mutator := &fakeMutator{failWith: hermesops.ErrMutationRecoveryPending}
	h := buildMutateHandler(mutatingRegistry(counters), calls, mutator)
	ident := sessionauth.Identity{TenantID: 7, UserID: 42}
	actor := adminActor{Source: admin.AdminSourceToken, ID: 99, Role: admin.RolePlatformAdmin}
	preview := mutateRequest(h, ident, actor, `{"tool_name":"dlq_replay","args":{"id":1}}`)
	corr := decodeBody(t, preview)["correlation_id"].(string)
	confirm := mutateRequest(h, ident, actor, `{"tool_name":"dlq_replay","args":{"id":1},"confirm":true,"correlation_id":"`+corr+`"}`)
	if confirm.Code != http.StatusServiceUnavailable || !bytes.Contains(confirm.Body.Bytes(), []byte("hermes_tool_recovery_pending")) {
		t.Fatalf("恢复中响应=%d %s", confirm.Code, confirm.Body.String())
	}
	if len(calls.rows) != 1 || !calls.rows[0].DryRun {
		t.Fatalf("恢复日志负责最终结果时不应补写重复错误行：%+v", calls.rows)
	}
}

func TestMutate_InTransactionFailureStillWritesAttemptError(t *testing.T) {
	counters := &mutateCounters{}
	calls := &fakeToolCalls{}
	h := buildMutateHandler(mutatingRegistry(counters), calls, &fakeMutator{failWith: errors.New("事务失败")})
	ident, actor := operator(7)
	preview := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
	corr := decodeBody(t, preview)["correlation_id"].(string)
	mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true,"correlation_id":"`+corr+`"}`)
	if len(calls.rows) != 2 || calls.rows[1].ResultStatus != string(hermesops.ResultError) {
		t.Fatalf("同事务失败应保留一次失败尝试日志：%+v", calls.rows)
	}
}

func TestMutate_DLQReplayAlreadyDeliveredIsIdempotentSuccess(t *testing.T) {
	// HERMES-IP-03:已投递/已处理的 DLQ 记录在 confirm 阶段命中底层幂等保护时,
	// operator 应看到 200 + 幂等提示,而不是泛化 503 "rolled back"。变异证伪:
	// 把 DLQReplaySpec.Mutate 改回直接返回 dlq.ErrNotFound,本测试会收到 503。
	reg := hermesops.NewRegistry()
	mustRegisterTestTool(reg, hermesops.DLQReplaySpec(hermesops.DLQReplayDeps{
		Lookup: func(_ context.Context, id, tenant int64) (dlq.Record, error) {
			return dlq.Record{ID: id, TenantID: tenant, Status: dlq.StatusDelivered}, nil
		},
		Replay: func(context.Context, int64, int64, string) (*dlq.Record, error) {
			return nil, dlq.ErrNotFound
		},
	}))
	h := buildMutateHandler(reg, &fakeToolCalls{}, &fakeMutator{})
	ident := sessionauth.Identity{TenantID: 7, UserID: 42}
	actor := adminActor{Source: admin.AdminSourceToken, ID: 99, Role: admin.RolePlatformAdmin}

	preview := mutateRequest(h, ident, actor, `{"tool_name":"dlq_replay","args":{"id":11}}`)
	corr := decodeBody(t, preview)["correlation_id"].(string)
	confirm := mutateRequest(h, ident, actor, `{"tool_name":"dlq_replay","args":{"id":11},"confirm":true,"correlation_id":"`+corr+`"}`)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s want 200", confirm.Code, confirm.Body.String())
	}
	result := decodeBody(t, confirm)["result"].(map[string]any)
	if result["status"] != "already_processed" || result["idempotent"] != true {
		t.Fatalf("result=%v want already_processed/idempotent=true", result)
	}
}

// --- HUAKAI_HERMES_MUTATING_ENABLED 运行时总开关 -------------------------

func TestKnobA_MutatingDisabledRefusesMutatingTool403AndRecordsDenial(t *testing.T) {
	// 它捕获的缺陷:若运行时 mutating kill-switch 没有被执行(或只在 confirm 上执行、
	// 不在 preview 上执行),那么在 HUAKAI_HERMES_MUTATING_ENABLED=false 时,mutating
	// 的 tool-execute 仍会 preview/变更。本测试断言位于 mutating 分支顶端的节流点覆盖了
	// preview 路径:403 hermes_mutating_disabled + 记录一条 denied 行 + 不做 preview/变更。
	//
	// 变异检查(已运行 + 确认变红):删掉 executeTool 里的
	// `if h.mutatingDisabled { ... return }` 块,这里就会返回 200 并带 dry_run preview
	// + correlation_id(无 denial)——status/code/denial 断言全部变红。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	calls := &fakeToolCalls{}
	h := buildMutateHandler(mutatingPlusReadOnlyRegistry(c), calls, mut)
	h.mutatingDisabled = true // 模拟 HUAKAI_HERMES_MUTATING_ENABLED=false。
	ident, actor := operator(7)

	rec := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403 (mutating disabled)", rec.Code, rec.Body.String())
	}
	if got := decodeBody(t, rec)["error"]; got == nil {
		t.Fatalf("no error object in body=%s", rec.Body.String())
	} else if code, _ := got.(map[string]any)["code"].(string); code != "hermes_mutating_disabled" {
		t.Fatalf("error code=%q want hermes_mutating_disabled (body=%s)", code, rec.Body.String())
	}
	if c.resolves != 0 || c.mutates != 0 || mut.executions != 0 {
		t.Fatalf("disabled mutating tool still touched the tool: resolves=%d mutates=%d exec=%d want 0/0/0", c.resolves, c.mutates, mut.executions)
	}
	if len(calls.rows) != 1 || calls.rows[0].ResultStatus != string(hermesops.ResultDenied) {
		t.Fatalf("want exactly one denied row, got %+v", calls.rows)
	}
	if calls.rows[0].ToolName != hermesops.ToolAccountPause {
		t.Fatalf("denied row tool=%q want account_pause", calls.rows[0].ToolName)
	}
}

func TestKnobA_MutatingDisabledKeepsReadOnlyPathLive(t *testing.T) {
	// 它捕获的缺陷:一个会禁用整个 handler(而不只是 mutating 分支)的 kill-switch
	// 会连只读诊断一起搞坏。本测试证明总开关关闭时，只读工具仍返回
	// 200 并带其结果。
	//
	// 变异检查(已运行 + 确认变红):把 `if h.mutatingDisabled` 防护移到 mutating
	// 分支判断之上,使它对每个工具都生效,这次只读调用就会返回 403 而非 200——
	// status 断言变红。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	calls := &fakeToolCalls{}
	h := buildMutateHandler(mutatingPlusReadOnlyRegistry(c), calls, mut)
	h.mutatingDisabled = true
	ident, actor := operator(7)

	rec := mutateRequest(h, ident, actor, `{"tool_name":"dlq_inspect","args":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("read-only status=%d body=%s want 200 even with mutating disabled", rec.Code, rec.Body.String())
	}
	if calls.rows[len(calls.rows)-1].ResultStatus != string(hermesops.ResultOK) {
		t.Fatalf("read-only row status=%q want ok", calls.rows[len(calls.rows)-1].ResultStatus)
	}
}

func TestKnobA_MutatingEnabledByDefaultStillPreviews(t *testing.T) {
	// 正向对照：当总开关处于默认启用状态时，构造处理器时未设置
	// mutatingDisabled——mutating 工具仍会 preview。这证明上面的 403 是由 kill-switch
	// 引起,而非工具坏了,并且默认状态是零行为变更。
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingPlusReadOnlyRegistry(c), &fakeToolCalls{}, mut)
	// mutatingDisabled 保持 false(默认启用)。
	ident, actor := operator(7)

	rec := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
	if rec.Code != http.StatusOK || decodeBody(t, rec)["dry_run"] != true {
		t.Fatalf("default-enabled mutating tool did not preview: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
