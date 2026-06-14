package hermeschat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

const toolTestSecret = "internal-tool-secret-32-bytes-minimum-x"

var toolTestClock = time.Unix(1700000000, 0).UTC

// fakeRunner records which tool ran with which tenant, and classifies a name as
// mutating via its mutating set. It mirrors *hermesops.Registry's read-only
// dispatch contract for the handler.
type fakeRunner struct {
	mutating map[string]bool
	ranTool  string
	ranReq   hermesops.ToolRequest
	result   hermesops.ToolResult
	runErr   error
}

func (f *fakeRunner) Get(name string) (hermesops.ToolSpec, bool) {
	// Model the real registry faithfully: a non-mutating tool is ReadOnly=true,
	// a mutating tool is ReadOnly=false. The handler's read-only filter requires
	// ReadOnly && !Mutating, so the fake must set ReadOnly to exercise it.
	mutating := f.mutating[name]
	return hermesops.ToolSpec{Name: name, Mutating: mutating, ReadOnly: !mutating}, true
}

func (f *fakeRunner) Run(_ context.Context, name string, req hermesops.ToolRequest) (hermesops.ToolResult, error) {
	// Mirror the registry's own mutation guard so the test exercises BOTH gates.
	if f.mutating[name] {
		return hermesops.ToolResult{}, hermesops.ErrNotMutating
	}
	f.ranTool = name
	f.ranReq = req
	if f.runErr != nil {
		return hermesops.ToolResult{}, f.runErr
	}
	return f.result, nil
}

func (f *fakeRunner) ReadOnlyCatalog() []hermesops.CatalogTool { return nil }

// recordingInserter captures the persisted tool-call row.
type recordingInserter struct {
	rows []hermestoolsdb.InsertHermesToolCallParams
}

func (r *recordingInserter) InsertHermesToolCall(_ context.Context, arg hermestoolsdb.InsertHermesToolCallParams) (hermestoolsdb.InsertHermesToolCallRow, error) {
	r.rows = append(r.rows, arg)
	return hermestoolsdb.InsertHermesToolCallRow{ID: int64(len(r.rows))}, nil
}

func newToolHandler(t *testing.T, runner ReadOnlyToolRunner, inserter hermesops.ToolCallInserter, bindings *SessionBindings) *InternalToolHandler {
	t.Helper()
	// toolLoopEnabled=true mirrors the default (KNOB B unset) so the existing
	// handler tests exercise the live tool loop. The KNOB B off-path has its own
	// test below.
	return NewInternalToolHandler([]byte(toolTestSecret), bindings, runner, inserter, toolTestClock, true)
}

// signSessionToken mints an internal_token for (tenant,user,requestID) matching
// the handler's secret + clock, and binds the operator to it.
func bindSession(t *testing.T, b *SessionBindings, tenantID, userID, adminTokenID int64, role, requestID string) string {
	t.Helper()
	exp := toolTestClock().Add(InternalTokenTTL)
	token, err := SignInternalToken([]byte(toolTestSecret), InternalTokenClaims{
		TenantID: tenantID, UserID: userID, RequestID: requestID, ExpiresAt: exp,
	})
	if err != nil {
		t.Fatalf("SignInternalToken: %v", err)
	}
	b.Bind(requestID, SessionOperator{
		TenantID: tenantID, ActorUserID: userID,
		AdminActorTokenID: adminTokenID, Role: role, ExpiresAt: exp,
	})
	return token
}

func toolRequest(token, toolName string, args map[string]any) *http.Request {
	body, _ := json.Marshal(map[string]any{"tool_name": toolName, "args": args})
	r := httptest.NewRequest(http.MethodPost, "/internal/hermes/tool-execute", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestInternalToolHandlerRejectsMutatingTool(t *testing.T) {
	// Regression (SAFETY, the headline guard): a mutating tool name submitted
	// through the conversational/internal path MUST be rejected — the LLM can never
	// invoke account_pause / dlq_replay / renew_trigger. Mutation: if the read-only
	// filter (and the Run ErrNotMutating guard) are dropped, this goes RED because
	// the mutating tool would dispatch and return 200. We assert 403 + a denied
	// audit row + that the tool body never ran.
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{mutating: map[string]bool{"account_pause": true}}
	ins := &recordingInserter{}
	h := newToolHandler(t, runner, ins, bindings)
	token := bindSession(t, bindings, 7, 42, 99, "platform_admin", "req-mut")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, toolRequest(token, "account_pause", map[string]any{"account_id": 5}))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403 for mutating tool", rec.Code, rec.Body.String())
	}
	if runner.ranTool != "" {
		t.Fatalf("mutating tool dispatched (ran=%q) — read-only filter bypassed", runner.ranTool)
	}
	if len(ins.rows) != 1 || ins.rows[0].ResultStatus != string(hermesops.ResultDenied) {
		t.Fatalf("audit rows=%+v want one denied row", ins.rows)
	}
	if ins.rows[0].ToolName != "account_pause" {
		t.Fatalf("denied row tool=%q want account_pause", ins.rows[0].ToolName)
	}
}

func TestInternalToolHandlerPinsSessionTenantScope(t *testing.T) {
	// Regression (SAFETY): a conversational tool call MUST run with the SESSION's
	// tenant — the runner cannot name a tenant. Mutation: if the handler read a
	// tenant from the request body instead of the bound session, a cross-tenant
	// read would be possible. We bind tenant 7 and assert the dispatched
	// ToolRequest.TenantID is 7 regardless of any body content (the body has no
	// tenant field, and even an injected one is ignored). The fixture is
	// discriminating: tenant 7 != a hardcoded 0/other.
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{result: hermesops.ToolResult{Summary: map[string]any{"event_count": 2}}}
	ins := &recordingInserter{}
	h := newToolHandler(t, runner, ins, bindings)
	token := bindSession(t, bindings, 7, 42, 99, "tenant_operator", "req-scope")

	// Attempt to smuggle a foreign tenant in args — it must be ignored; the tool
	// always runs against the bound tenant 7.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, toolRequest(token, "audit_lookup", map[string]any{"tenant_id": 999}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if runner.ranReq.TenantID != 7 {
		t.Fatalf("dispatched tenant=%d want 7 (session scope), foreign tenant honored", runner.ranReq.TenantID)
	}
	if runner.ranReq.ActorUserID != 42 || runner.ranReq.Role != "tenant_operator" {
		t.Fatalf("dispatched actor=%d role=%q want 42/tenant_operator", runner.ranReq.ActorUserID, runner.ranReq.Role)
	}
}

func TestInternalToolHandlerRejectsTokenSessionTenantMismatch(t *testing.T) {
	// Regression (SAFETY): a valid token for session A's (tenant,user) must not be
	// usable to drive a binding registered for a DIFFERENT (tenant,user). Mutation:
	// if the token<->binding identity-consistency check is removed, a token minted
	// for tenant 8 could pair with a tenant-7 binding under the same request_id. We
	// bind tenant 7 but present a token signed for tenant 8 on the same request_id
	// and assert 401 + no dispatch.
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{}
	ins := &recordingInserter{}
	h := newToolHandler(t, runner, ins, bindings)

	// Bind operator for tenant 7 under request id "req-x".
	bindings.Bind("req-x", SessionOperator{
		TenantID: 7, ActorUserID: 42, AdminActorTokenID: 99, Role: "platform_admin",
		ExpiresAt: toolTestClock().Add(InternalTokenTTL),
	})
	// But mint a token for tenant 8 / user 50 on the SAME request id.
	mismatchToken, err := SignInternalToken([]byte(toolTestSecret), InternalTokenClaims{
		TenantID: 8, UserID: 50, RequestID: "req-x", ExpiresAt: toolTestClock().Add(InternalTokenTTL),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, toolRequest(mismatchToken, "audit_lookup", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 for token/binding identity mismatch", rec.Code)
	}
	if runner.ranTool != "" {
		t.Fatalf("tool ran despite identity mismatch (ran=%q)", runner.ranTool)
	}
}

func TestInternalToolHandlerReadOnlySuccessRecordsOperatorAttribution(t *testing.T) {
	// Regression: a read-only tool call returns the sanitized summary AND records a
	// hermes_tool_calls row carrying the operator attribution (admin_actor_token_id)
	// + the session tenant/actor. Mutation: if recordCall drops the operator token
	// id, the audit trail loses operator attribution. We assert the row's
	// AdminActorTokenID == 99 and status == ok.
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{result: hermesops.ToolResult{Summary: map[string]any{"event_count": 4}, ErrorClass: ""}}
	ins := &recordingInserter{}
	h := newToolHandler(t, runner, ins, bindings)
	token := bindSession(t, bindings, 7, 42, 99, "platform_admin", "req-ok")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, toolRequest(token, "log_analyze", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var resp internalToolResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v body=%s", err, rec.Body.String())
	}
	if resp.Status != string(hermesops.ResultOK) || resp.Result["event_count"] != float64(4) {
		t.Fatalf("resp=%+v want ok with event_count 4", resp)
	}
	if len(ins.rows) != 1 {
		t.Fatalf("audit rows=%d want 1", len(ins.rows))
	}
	row := ins.rows[0]
	if row.ResultStatus != string(hermesops.ResultOK) {
		t.Fatalf("row status=%q want ok", row.ResultStatus)
	}
	if row.AdminActorTokenID == nil || *row.AdminActorTokenID != 99 {
		t.Fatalf("row admin_actor_token_id=%v want 99 (operator attribution dropped)", row.AdminActorTokenID)
	}
	if row.TenantID != 7 || row.ActorUserID != 42 {
		t.Fatalf("row tenant/actor=%d/%d want 7/42", row.TenantID, row.ActorUserID)
	}
}

func TestInternalToolHandlerRejectsUnboundSession(t *testing.T) {
	// Regression (FAIL-CLOSED): a valid internal_token whose request_id has NO
	// operator binding must be rejected — a tool can never run without a resolved
	// operator. Mutation: if Lookup's miss were treated as "allow", an unbound
	// session could run tools with no role/scope. We present a correctly-signed
	// token but never call Bind, and assert 401 + no dispatch.
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{}
	h := newToolHandler(t, runner, &recordingInserter{}, bindings)
	token, err := SignInternalToken([]byte(toolTestSecret), InternalTokenClaims{
		TenantID: 7, UserID: 42, RequestID: "req-unbound", ExpiresAt: toolTestClock().Add(InternalTokenTTL),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, toolRequest(token, "audit_lookup", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 for unbound session", rec.Code)
	}
	if runner.ranTool != "" {
		t.Fatalf("tool ran for unbound session (ran=%q)", runner.ranTool)
	}
}

func TestInternalToolHandlerRejectsInvalidToken(t *testing.T) {
	// Regression: an invalid/forged internal_token must be rejected before any
	// binding lookup or dispatch. Mutation: if VerifyInternalToken's result is not
	// checked, a forged token would authorize. We tamper the signature and assert
	// 401 + no dispatch.
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{}
	h := newToolHandler(t, runner, &recordingInserter{}, bindings)
	good := bindSession(t, bindings, 7, 42, 99, "platform_admin", "req-forge")
	forged := good[:len(good)-1] + flipLast(good)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, toolRequest(forged, "audit_lookup", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 for forged token", rec.Code)
	}
	if runner.ranTool != "" {
		t.Fatalf("tool ran for forged token (ran=%q)", runner.ranTool)
	}
}

func flipLast(s string) string {
	if s == "" {
		return "0"
	}
	if s[len(s)-1] == '0' {
		return "1"
	}
	return "0"
}

// --- KNOB B: HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED runtime kill-switch ----------

func TestKnobB_ToolLoopDisabledRefusesEveryCall403(t *testing.T) {
	// Defect this catches: if the runtime tool-loop kill-switch were not enforced
	// in the runner callback, the LLM conversational tool loop would stay reachable
	// while HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED=false. With the switch off, a fully
	// valid bound session + valid token + READ-ONLY tool must be refused 403
	// llm_toolloop_disabled, before any token inspection or dispatch.
	//
	// Mutation check (run + RED confirmed): delete the `if !h.toolLoopEnabled { ...
	// return }` early-return in ServeHTTP and this call returns 200 (the tool
	// dispatches) — the 403 + no-dispatch assertions go RED.
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{result: hermesops.ToolResult{Summary: map[string]any{"event_count": 1}}}
	ins := &recordingInserter{}
	// Construct the handler with toolLoopEnabled=false (KNOB B off).
	h := NewInternalToolHandler([]byte(toolTestSecret), bindings, runner, ins, toolTestClock, false)
	token := bindSession(t, bindings, 7, 42, 99, "platform_admin", "req-toolloop-off")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, toolRequest(token, "log_analyze", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403 (tool loop disabled)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "llm_toolloop_disabled") {
		t.Fatalf("body=%s want llm_toolloop_disabled error code", rec.Body.String())
	}
	if runner.ranTool != "" {
		t.Fatalf("tool ran despite tool loop disabled (ran=%q)", runner.ranTool)
	}
	// No audit row for a policy-refused-by-kill-switch call (it short-circuits
	// before operator resolution / dispatch).
	if len(ins.rows) != 0 {
		t.Fatalf("disabled tool loop recorded %d rows, want 0", len(ins.rows))
	}
}

func TestKnobB_ToolLoopEnabledByDefaultStillDispatches(t *testing.T) {
	// Positive control: with KNOB B enabled (toolLoopEnabled=true) the SAME bound
	// session + token + read-only tool dispatches 200. This proves the 403 above is
	// caused by the kill-switch, not a broken token/binding, and that the default
	// (enabled) is zero behavior change.
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{result: hermesops.ToolResult{Summary: map[string]any{"event_count": 1}}}
	h := NewInternalToolHandler([]byte(toolTestSecret), bindings, runner, &recordingInserter{}, toolTestClock, true)
	token := bindSession(t, bindings, 7, 42, 99, "platform_admin", "req-toolloop-on")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, toolRequest(token, "log_analyze", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 (tool loop enabled)", rec.Code, rec.Body.String())
	}
	if runner.ranTool != "log_analyze" {
		t.Fatalf("tool did not dispatch with loop enabled (ran=%q)", runner.ranTool)
	}
}
