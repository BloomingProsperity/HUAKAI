package hermeshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// failCommitPhase replicates the REAL orchestrator's commit-phase fault: the
	// mutation runs (so own-tx tools have persisted) and then the final commit
	// fails. Like the orchestrator, it wraps ErrCommitAfterOwnTxMutation ONLY when
	// rec.OwnTx is set; an in-tx tool gets a bare commit error. This lets a handler
	// test prove the error_class is commit_uncertain vs mutation_failed purely from
	// whether the handler tagged rec.OwnTx for the tool.
	failCommitPhase bool
}

func (f *fakeMutator) Execute(ctx context.Context, lockKey string, rec hermesops.MutationAuditRecord, mutate func(ctx context.Context, tx pgx.Tx) (hermesops.ToolResult, error)) (hermesops.ToolResult, error) {
	f.executions++
	f.lastLock = lockKey
	f.lastRec = rec
	if f.failWith != nil {
		return hermesops.ToolResult{}, f.failWith
	}
	if f.failCommitPhase {
		// Run the mutation first (matches the orchestrator: mutate before commit),
		// then surface the commit fault classified by tx-mode.
		if _, err := mutate(ctx, nil); err != nil {
			return hermesops.ToolResult{}, err
		}
		base := errors.New("commit: connection reset")
		if rec.OwnTx {
			return hermesops.ToolResult{}, fmt.Errorf("hermesops: commit mutation tx: %w: %w", hermesops.ErrCommitAfterOwnTxMutation, base)
		}
		return hermesops.ToolResult{}, fmt.Errorf("hermesops: commit mutation tx: %w", base)
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

// mutatingPlusReadOnlyRegistry registers account_pause (mutating, tenant_operator
// floor) AND a read-only diagnostic (dlq_inspect), so a KNOB-A test can prove the
// kill-switch refuses the mutating tool while the read-only path stays live.
func mutatingPlusReadOnlyRegistry(c *mutateCounters) *hermesops.Registry {
	reg := mutatingRegistry(c)
	reg.Register(hermesops.ToolSpec{
		Name: hermesops.ToolDLQInspect, Category: hermesops.CategoryDiagnostic,
		ReadOnly: true, RequiredRole: hermesops.RoleTenantOperator,
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

// --- H4 S2 commit-phase classification --------------------------------------

// errorClassFor drives a full confirm through the handler under a forced
// commit-phase fault and returns the error_class recorded on the best-effort
// hermes_tool_calls row for the given tool. tool is one of the registered
// mutating tools; args is its arg object; the confirm runs as platform_admin so
// dlq_replay is authorized too.
func errorClassFor(t *testing.T, tool, args string) string {
	t.Helper()
	c := &mutateCounters{}
	mut := &fakeMutator{failCommitPhase: true}
	calls := &fakeToolCalls{}
	h := buildMutateHandler(mutatingRegistry(c), calls, mut)
	ident := sessionauth.Identity{TenantID: 7, UserID: 42}
	actor := adminActor{TokenID: 99, Role: admin.RolePlatformAdmin}

	preview := mutateRequest(h, ident, actor, `{"tool_name":"`+tool+`","args":`+args+`}`)
	corr, ok := decodeBody(t, preview)["correlation_id"].(string)
	if !ok {
		t.Fatalf("%s: no correlation_id in preview body=%s", tool, preview.Body.String())
	}
	mutateRequest(h, ident, actor, `{"tool_name":"`+tool+`","args":`+args+`,"confirm":true,"correlation_id":"`+corr+`"}`)

	// The best-effort error row is the LAST recorded row (a dry-run row precedes it).
	if len(calls.rows) == 0 {
		t.Fatalf("%s: no tool-call row recorded", tool)
	}
	last := calls.rows[len(calls.rows)-1]
	if last.ResultStatus != string(hermesops.ResultError) {
		t.Fatalf("%s: last row status=%q want error", tool, last.ResultStatus)
	}
	if last.ErrorClass == nil {
		t.Fatalf("%s: error row has nil error_class", tool)
	}
	return *last.ErrorClass
}

func TestMutate_CommitPhaseFaultClassifiesByTxMode(t *testing.T) {
	// Regression (H4 S2, DISCRIMINATING): a commit-phase fault (mutation ran, final
	// orchestrator commit carrying the audit rows failed) must classify by tx-mode.
	// dlq_replay commits its mutation in its OWN tx, so the mutation persisted while
	// the audit rolled back -> error_class "commit_uncertain". account_pause flips
	// enabled INSIDE the orchestrator tx, so the same fault rolls the mutation back
	// -> "mutation_failed".
	//
	// Mutation check (self-proving): the two tools hit the SAME forced fault and the
	// SAME classifier; the only thing that differs is hermesops.IsOwnTxMutation(tool)
	// which the handler threads into rec.OwnTx. If the handler stopped setting
	// rec.OwnTx (or IsOwnTxMutation lost dlq_replay), the own-tx class would collapse
	// to "mutation_failed" and the `ownClass == inClass` guard goes RED.
	ownClass := errorClassFor(t, "dlq_replay", `{"id":1}`)
	inClass := errorClassFor(t, "account_pause", `{"account_id":5}`)

	if ownClass != "commit_uncertain" {
		t.Fatalf("own-tx dlq_replay commit fault error_class=%q want commit_uncertain", ownClass)
	}
	if inClass != "mutation_failed" {
		t.Fatalf("in-tx account_pause commit fault error_class=%q want mutation_failed", inClass)
	}
	if ownClass == inClass {
		t.Fatalf("tx-mode did not flip the class (own=%q in=%q) — rec.OwnTx not threaded per tool", ownClass, inClass)
	}
}

// --- KNOB A: HUAKAI_HERMES_MUTATING_ENABLED runtime kill-switch --------------

func TestKnobA_MutatingDisabledRefusesMutatingTool403AndRecordsDenial(t *testing.T) {
	// Defect this catches: if the runtime mutating kill-switch were not enforced
	// (or enforced only on confirm, not on the preview), a mutating tool-execute
	// would still preview/mutate while HUAKAI_HERMES_MUTATING_ENABLED=false. This
	// asserts the choke at the TOP of the mutating branch covers the preview path:
	// 403 hermes_mutating_disabled + a recorded denied row + NO preview/mutation.
	//
	// Mutation check (run + RED confirmed): delete the `if h.mutatingDisabled { ...
	// return }` block in executeTool and this returns 200 with a dry_run preview +
	// correlation_id (no denial) — the status/code/denial assertions all go RED.
	c := &mutateCounters{}
	mut := &fakeMutator{}
	calls := &fakeToolCalls{}
	h := buildMutateHandler(mutatingPlusReadOnlyRegistry(c), calls, mut)
	h.mutatingDisabled = true // KNOB A off (HUAKAI_HERMES_MUTATING_ENABLED=false)
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
	// Defect this catches: a kill-switch that disabled the WHOLE handler (not just
	// the mutating branch) would also break read-only diagnostics. This proves the
	// orthogonality requirement — with KNOB A off, a read-only tool still returns
	// 200 with its result.
	//
	// Mutation check (run + RED confirmed): move the `if h.mutatingDisabled` guard
	// above the mutating-branch test so it gates EVERY tool, and this read-only call
	// returns 403 instead of 200 — the status assertion goes RED.
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
	// Positive control: with KNOB A in its DEFAULT (enabled) state — a handler built
	// without setting mutatingDisabled — the mutating tool still previews. This
	// proves the 403 above is caused by the kill-switch, not a broken tool, and that
	// the default is zero-behavior-change.
	c := &mutateCounters{}
	mut := &fakeMutator{}
	h := buildMutateHandler(mutatingPlusReadOnlyRegistry(c), &fakeToolCalls{}, mut)
	// mutatingDisabled left false (default-enabled).
	ident, actor := operator(7)

	rec := mutateRequest(h, ident, actor, `{"tool_name":"account_pause","args":{"account_id":5}}`)
	if rec.Code != http.StatusOK || decodeBody(t, rec)["dry_run"] != true {
		t.Fatalf("default-enabled mutating tool did not preview: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
