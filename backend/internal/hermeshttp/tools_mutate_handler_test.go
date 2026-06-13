package hermeshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// fakeMutator is a stand-in MutateOrchestrator that runs the mutate callback
// directly (no real DB) and counts executions, so a handler test can prove the
// confirm flow drives exactly one mutation.
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

// mutatingRegistry registers one mutating tool (account_pause) backed by simple
// resolve/mutate closures + counters, so the handler test can observe whether
// the mutation actually ran and what it saw.
type mutateCounters struct {
	resolves int
	mutates  int
	enabled  bool
}

func mutatingRegistry(c *mutateCounters) *hermesops.Registry {
	reg := hermesops.NewRegistry()
	reg.Register(hermesops.ToolSpec{
		Name: hermesops.ToolAccountPause, Category: hermesops.CategoryMutating,
		Mutating: true, RequiresConfirmation: true, RequiredRole: hermesops.RoleTenantOperator,
		Resolve: func(_ context.Context, req hermesops.ToolRequest) (hermesops.MutationPlan, error) {
			c.resolves++
			id, err := hermesops.ArgInt(req.Args, "account_id")
			if err != nil {
				return hermesops.MutationPlan{}, err
			}
			return hermesops.MutationPlan{
				TargetType: "provider_account", TargetID: id,
				LockKey: "lock:7:" + itoa(id),
				Preview: map[string]any{"current_enabled": true, "next_enabled": false},
			}, nil
		},
		Mutate: func(_ context.Context, _ hermesops.ToolRequest, _ hermesops.MutationPlan) (hermesops.ToolResult, error) {
			c.mutates++
			c.enabled = false
			return hermesops.ToolResult{Summary: map[string]any{"enabled": false}}, nil
		},
	})
	// platform-admin-only mutating tool to exercise the RBAC denial.
	reg.Register(hermesops.ToolSpec{
		Name: hermesops.ToolDLQReplay, Category: hermesops.CategoryMutating,
		Mutating: true, RequiresConfirmation: true, RequiredRole: hermesops.RolePlatformAdmin,
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
		confirmCache: newConfirmCache(),
	}
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

// --- L2 dry-run + confirm ---------------------------------------------------

func TestMutate_DryRunPreviewsWithoutMutating(t *testing.T) {
	// Regression (L2): confirm=false returns a preview + correlation_id and does
	// NOT mutate. Mutation check: route confirm=false to confirmMutation and the
	// mutates counter becomes 1 (RED).
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
	// Regression (L2/L5): confirm=true with the matching correlation_id mutates
	// exactly once. Mutation check: skip consuming the correlation_id and a second
	// confirm would re-execute (executions=2).
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
	// Regression (L5, DISCRIMINATING): a correlation_id is single-use. A second
	// confirm with the same id is 400 and does NOT mutate again. Mutation check:
	// make confirmCache.consume non-deleting and the second confirm executes (RED).
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
	// Regression (L2): a confirm=true with an absent / unknown / wrong-tool
	// correlation_id is 400 and never executes. Mutation check: drop the
	// consume() ok check and the unknown id would proceed to execute.
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident, actor := operator(7)

	// absent correlation_id
	r1 := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5},"confirm":true}`)
	// unknown correlation_id
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

func TestMutate_CorrelationIDBoundToTool(t *testing.T) {
	// Regression (L2): a correlation_id issued for account_pause cannot be used to
	// confirm a DIFFERENT tool. Mutation check: drop the ToolName match in
	// confirmCache.consume and this cross-tool confirm would proceed.
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	platform := func() (sessionauth.Identity, adminActor) {
		return sessionauth.Identity{TenantID: 7, UserID: 42}, adminActor{TokenID: 99, Role: admin.RolePlatformAdmin}
	}
	ident, actor := platform()

	corr := decodeBody(t, mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`))["correlation_id"].(string)
	// Try to confirm dlq_replay (a different tool) with account_pause's id.
	rec := mutateRequest(h, ident, actor, `{"tool_name":"dlq_replay","args":{"id":1},"confirm":true,"correlation_id":"`+corr+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-tool confirm status=%d want 400", rec.Code)
	}
	if mut.executions != 0 {
		t.Fatalf("cross-tool confirm executed: %d want 0", mut.executions)
	}
}

// --- L1 RBAC ----------------------------------------------------------------

func TestMutate_TenantOperatorCannotDLQReplay(t *testing.T) {
	// Regression (L1, DISCRIMINATING): dlq_replay is platform_admin ONLY — a
	// tenant_operator gets 403 + a denied tool-call row, and never previews or
	// mutates. Mutation check: lower DLQReplaySpec's floor to tenant_operator and
	// this returns 200.
	c := &mutateCounters{}
	mut := &fakeMutator{}
	calls := &fakeToolCalls{}
	h := buildMutateHandler(mutatingRegistry(c), calls, mut)
	ident, actor := operator(7) // tenant_operator

	rec := mutateRequest(h, ident, actor, `{"tool_name":"dlq_replay","args":{"id":1}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant_operator dlq_replay status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
	if mut.executions != 0 {
		t.Fatalf("forbidden dlq_replay executed: %d want 0", mut.executions)
	}
	if len(calls.rows) != 1 || calls.rows[0].ResultStatus != string(hermesops.ResultDenied) {
		t.Fatalf("denied row not recorded: %+v", calls.rows)
	}
}

func TestMutate_PlatformAdminCanDLQReplay(t *testing.T) {
	// Regression (L1): platform_admin CAN dry-run dlq_replay (positive control for
	// the RBAC denial test above). Mutation check: this proves the 403 above is
	// about the ROLE, not a broken tool.
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingRegistry(c), &fakeToolCalls{}, mut)
	ident := sessionauth.Identity{TenantID: 7, UserID: 42}
	actor := adminActor{TokenID: 99, Role: admin.RolePlatformAdmin}

	rec := mutateRequest(h, ident, actor, `{"tool_name":"dlq_replay","args":{"id":1}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("platform_admin dlq_replay dry-run status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if decodeBody(t, rec)["dry_run"] != true {
		t.Fatalf("expected dry-run preview")
	}
}

// --- L4 advisory lock (best-effort: assert the lock key reaches the orchestrator)

func TestMutate_AdvisoryLockKeyPassedToOrchestrator(t *testing.T) {
	// Regression (L4): the per-target advisory lock key from the plan reaches the
	// orchestrator's Execute (which acquires/releases the lock around the
	// mutation). Mutation check: pass "" instead of plan.LockKey in
	// confirmMutation and the assertion on the specific key fails.
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

// --- L3 orchestrator abort surfaces as failed, no double-record --------------

func TestMutate_OrchestratorAbortReturnsServiceError(t *testing.T) {
	// Regression: when the orchestrator aborts (audit failure rolled back), the
	// handler surfaces a failure (not 200) so the caller knows the mutation did
	// not happen. Mutation check: ignore Execute's error and return 200.
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
