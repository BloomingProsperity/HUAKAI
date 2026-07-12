package hermeschat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesconfirm"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

const toolTestSecret = "internal-tool-secret-32-bytes-minimum-x"

var toolTestClock = time.Unix(1700000000, 0).UTC

// fakeRunner 记录哪个工具以哪个租户运行,并通过它的 mutating 集合把某个名字归类为 mutating。
// 它为 handler 镜像 *hermesops.Registry 的只读 dispatch 契约。
type fakeRunner struct {
	mutating map[string]bool
	ranTool  string
	ranReq   hermesops.ToolRequest
	result   hermesops.ToolResult
	runErr   error
}

func (f *fakeRunner) Get(name string) (hermesops.ToolSpec, bool) {
	// 忠实地模拟真实 registry:非 mutating 工具 ReadOnly=true,mutating 工具 ReadOnly=false。
	// handler 的只读过滤要求 ReadOnly && !Mutating,故此 fake 必须设置 ReadOnly 才能跑到该逻辑。
	mutating := f.mutating[name]
	return hermesops.ToolSpec{Name: name, Mutating: mutating, ReadOnly: !mutating}, true
}

func (f *fakeRunner) Run(_ context.Context, name string, req hermesops.ToolRequest) (hermesops.ToolResult, error) {
	// 镜像 registry 自身的 mutation 守卫,使测试同时跑到两道闸门。
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

// recordingInserter 捕获被持久化的 tool-call 行。
type recordingInserter struct {
	rows []hermestoolsdb.InsertHermesToolCallParams
}

func (r *recordingInserter) InsertHermesToolCall(_ context.Context, arg hermestoolsdb.InsertHermesToolCallParams) (hermestoolsdb.InsertHermesToolCallRow, error) {
	r.rows = append(r.rows, arg)
	return hermestoolsdb.InsertHermesToolCallRow{ID: int64(len(r.rows))}, nil
}

func newToolHandler(t *testing.T, runner ReadOnlyToolRunner, inserter hermesops.ToolCallInserter, bindings *SessionBindings) *InternalToolHandler {
	t.Helper()
	// toolLoopEnabled=true 镜像默认(KNOB B 未设置),故既有 handler 测试跑的是活的工具循环。
	// KNOB B 关的路径有它自己下面的测试。末尾的 nil/nil/false 让 Phase B 提议路径处于未接线状态
	//(无 proposer、无 cache、KNOB 关)——只读测试不受影响。
	return NewInternalToolHandler([]byte(toolTestSecret), bindings, runner, inserter, toolTestClock, true, nil, nil, false)
}

// bindSession 为 (tenant,user,requestID) 铸造一个与 handler 的 secret + 时钟匹配的
// internal_token,并把 operator 绑定到它。
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
	// 回归(安全,头牌守卫):经会话/内部路径提交的 mutating 工具名必须被拒绝——LLM 绝不能
	// 调用 account_pause / dlq_replay / renew_trigger。变异:若去掉只读过滤(以及 Run 的
	// ErrNotMutating 守卫),这会变红,因为 mutating 工具会被 dispatch 并返回 200。我们断言
	// 403 + 一条 denied 审计行 + 工具主体从未运行。
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
	// 回归(安全):会话式工具调用必须以会话的 tenant 运行——runner 无法指定 tenant。变异:
	// 若 handler 从请求体而非已绑定会话读取 tenant,就可能发生跨 tenant 读取。我们绑定 tenant 7,
	// 并断言无论 body 内容如何,被 dispatch 的 ToolRequest.TenantID 都是 7(body 没有 tenant
	// 字段,即便注入一个也会被忽略)。该夹具具有判别性:tenant 7 != 硬编码的 0/其它值。
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{result: hermesops.ToolResult{Summary: map[string]any{"event_count": 2}}}
	ins := &recordingInserter{}
	h := newToolHandler(t, runner, ins, bindings)
	token := bindSession(t, bindings, 7, 42, 99, "tenant_operator", "req-scope")

	// 尝试在 args 里夹带一个外部 tenant——它必须被忽略;工具始终针对已绑定的 tenant 7 运行。
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
	// 回归(安全):会话 A 的 (tenant,user) 的有效 token 不得用于驱动为不同 (tenant,user)
	// 注册的绑定。变异:若去掉 token<->绑定 的身份一致性检查,为 tenant 8 铸造的 token 就能
	// 在同一 request_id 下配上 tenant 7 的绑定。我们绑定 tenant 7,却在同一 request_id 上出示
	// 一个为 tenant 8 签名的 token,并断言 401 + 不 dispatch。
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{}
	ins := &recordingInserter{}
	h := newToolHandler(t, runner, ins, bindings)

	// 在 request id "req-x" 下为 tenant 7 绑定 operator。
	bindings.Bind("req-x", SessionOperator{
		TenantID: 7, ActorUserID: 42, AdminActorTokenID: 99, Role: "platform_admin",
		ExpiresAt: toolTestClock().Add(InternalTokenTTL),
	})
	// 但在同一 request id 上为 tenant 8 / user 50 铸造一个 token。
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
	// 回归:只读工具调用返回脱敏后的 summary,并记录一行携带 operator 归属
	//(admin_actor_token_id)+ 会话 tenant/actor 的 hermes_tool_calls。变异:若 recordCall
	// 丢掉 operator token id,审计轨迹就失去 operator 归属。我们断言该行的 AdminActorTokenID == 99
	// 且 status == ok。
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
	// 回归(FAIL-CLOSED):一个有效的 internal_token,若其 request_id 没有 operator 绑定,
	// 必须被拒绝——工具绝不能在缺少已解析 operator 的情况下运行。变异:若把 Lookup 的未命中
	// 当作"允许",未绑定的会话就能在无 role/范围 的情况下运行工具。我们出示一个正确签名的
	// token 但从不调用 Bind,并断言 401 + 不 dispatch。
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
	// 回归:一个无效/伪造的 internal_token 必须在任何绑定 lookup 或 dispatch 之前被拒绝。
	// 变异:若不检查 VerifyInternalToken 的结果,伪造的 token 就能通过授权。我们篡改签名并
	// 断言 401 + 不 dispatch。
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

// --- KNOB B:HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED 运行时 kill-switch -----------

func TestKnobB_ToolLoopDisabledRefusesEveryCall403(t *testing.T) {
	// 本测试捕获的缺陷:若运行时 tool-loop kill-switch 没有在 runner 回调中被强制执行,那么
	// 在 HUAKAI_HERMES_LLM_TOOLLOOP_ENABLED=false 时,LLM 会话式工具循环仍可触达。在开关关闭
	// 时,一个完全有效的绑定会话 + 有效 token + 只读工具,必须在任何 token 检查或 dispatch
	// 之前就被拒 403 llm_toolloop_disabled。
	//
	// 变异检查(已跑且确认变红):删除 ServeHTTP 里 `if !h.toolLoopEnabled { ... return }`
	// 的早返回,这个调用就会返回 200(工具被 dispatch)——403 + 不 dispatch 的断言随之变红。
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{result: hermesops.ToolResult{Summary: map[string]any{"event_count": 1}}}
	ins := &recordingInserter{}
	// 用 toolLoopEnabled=false 构造 handler(KNOB B 关)。提议路径未接线(nil/nil/false)——此处无关。
	h := NewInternalToolHandler([]byte(toolTestSecret), bindings, runner, ins, toolTestClock, false, nil, nil, false)
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
	// 被 kill-switch 按策略拒绝的调用不写审计行(它在 operator 解析 / dispatch 之前就短路了)。
	if len(ins.rows) != 0 {
		t.Fatalf("disabled tool loop recorded %d rows, want 0", len(ins.rows))
	}
}

func TestKnobB_ToolLoopEnabledByDefaultStillDispatches(t *testing.T) {
	// 阳性对照:在 KNOB B 启用(toolLoopEnabled=true)时,同样的绑定会话 + token + 只读工具
	// 会 dispatch 并返回 200。这证明上面的 403 是由 kill-switch 引起的,而非 token/绑定 损坏,
	// 并且默认值(启用)是零行为变。
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{result: hermesops.ToolResult{Summary: map[string]any{"event_count": 1}}}
	h := NewInternalToolHandler([]byte(toolTestSecret), bindings, runner, &recordingInserter{}, toolTestClock, true, nil, nil, false)
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

// --- Phase B:LLM-提议路径(mode=propose) --------------------------------

// fakeProposer 是 ProposalResolver 的测试替身。它返回一个固定的 MutationPlan(或一个配置好的
// 错误),并记录它收到的(name, role, req),以便测试断言会话作用域确实传到了 ResolveProposal。
// 它只暴露 ResolveProposal——没有 Mutate——镜像"提议路径无法执行 state change"这一真实结构保证。
type fakeProposer struct {
	plan    hermesops.MutationPlan
	err     error
	called  bool
	gotName string
	gotRole string
	gotReq  hermesops.ToolRequest
}

func (f *fakeProposer) ResolveProposal(_ context.Context, name, role string, req hermesops.ToolRequest) (hermesops.MutationPlan, error) {
	f.called = true
	f.gotName = name
	f.gotRole = role
	f.gotReq = req
	if f.err != nil {
		return hermesops.MutationPlan{}, f.err
	}
	return f.plan, nil
}

// newProposeHandler 构造一个接好 Phase B 提议路径的 handler(KNOB B 开,提议 KNOB 按 proposeEnabled)。
func newProposeHandler(t *testing.T, runner ReadOnlyToolRunner, proposer ProposalResolver, cache *hermesconfirm.Cache, ins hermesops.ToolCallInserter, bindings *SessionBindings, proposeEnabled bool) *InternalToolHandler {
	t.Helper()
	return NewInternalToolHandler([]byte(toolTestSecret), bindings, runner, ins, toolTestClock, true, proposer, cache, proposeEnabled)
}

// proposeRequest 构造一个 mode=propose 的 tool-execute 请求。
func proposeRequest(token, toolName string, args map[string]any) *http.Request {
	body, _ := json.Marshal(map[string]any{"tool_name": toolName, "args": args, "mode": "propose"})
	r := httptest.NewRequest(http.MethodPost, "/internal/hermes/tool-execute", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	return r
}

func samplePlan() hermesops.MutationPlan {
	return hermesops.MutationPlan{
		TargetType: "provider_account",
		TargetID:   5,
		Preview:    map[string]any{"from": "active", "to": "paused"},
	}
}

func TestProposeDisabledRefusesBeforeResolution(t *testing.T) {
	// 抓的缺陷(Phase B KNOB、零行为变保证):若 proposeEnabled 门未被强制,一个 mode=propose 调用
	// 会在 Owner 尚未激活该路径时就解析 + 发 correlation_id。KNOB 关时,一个完全合法的绑定会话 +
	// 可提议工具必须在 ResolveProposal 被调用之前就被拒 403 llm_propose_disabled——无 correlation_id、
	// 无审计行。
	// 变异(已验证转红):删掉 serveProposal 里 `if !h.proposeEnabled` 的早返回 → proposer.called
	// 变 true 且返回 200 needs_confirmation,翻掉 403 + 未调用断言。
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{mutating: map[string]bool{"account_pause": true}}
	proposer := &fakeProposer{plan: samplePlan()}
	cache := hermesconfirm.NewCache()
	ins := &recordingInserter{}
	h := newProposeHandler(t, runner, proposer, cache, ins, bindings, false) // KNOB OFF
	token := bindSession(t, bindings, 7, 42, 99, "tenant_operator", "req-prop-off")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proposeRequest(token, "account_pause", map[string]any{"account_id": 5}))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403 (propose disabled)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "llm_propose_disabled") {
		t.Fatalf("body=%s want llm_propose_disabled", rec.Body.String())
	}
	if proposer.called {
		t.Fatalf("ResolveProposal called despite KNOB off — resolution leaked while disabled")
	}
	if len(ins.rows) != 0 {
		t.Fatalf("audit rows=%d want 0 (refused before any record)", len(ins.rows))
	}
}

func TestProposeEnabledIssuesConfirmableCorrelation(t *testing.T) {
	// 抓的缺陷(头牌正向 + operator 绑定安全):KNOB 开时,一个可提议的 mutating 工具被 DRY-RUN
	// 解析并返回 needs_confirmation 句柄而不执行;所发的 correlation_id 绑定到精确的会话 operator
	//(工具+租户+actor+token+target),故只有该 operator 之后才能确认它。
	// 变异(已验证转红):把 serveProposal 里 `TokenID: op.AdminActorTokenID` 改成 `TokenID: 0`
	// → 下面用正确六元组的 Consume 失败;改钉死的 TargetID → entry.TargetID 断言失败。
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{mutating: map[string]bool{"account_pause": true}}
	proposer := &fakeProposer{plan: samplePlan()}
	cache := hermesconfirm.NewCache()
	ins := &recordingInserter{}
	h := newProposeHandler(t, runner, proposer, cache, ins, bindings, true)
	token := bindSession(t, bindings, 7, 42, 99, "tenant_operator", "req-prop-on")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proposeRequest(token, "account_pause", map[string]any{"account_id": 5}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var resp internalToolResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.Status != "needs_confirmation" {
		t.Fatalf("status=%q want needs_confirmation", resp.Status)
	}
	if resp.CorrelationID == "" {
		t.Fatalf("missing correlation_id; resp=%+v", resp)
	}
	if resp.TargetType != "provider_account" || resp.TargetID != 5 {
		t.Fatalf("target=%s/%d want provider_account/5", resp.TargetType, resp.TargetID)
	}
	if resp.Preview["to"] != "paused" {
		t.Fatalf("preview=%v want to=paused", resp.Preview)
	}
	// 提议路径绝不能 dispatch 只读 Run(它不执行)。
	if runner.ranTool != "" {
		t.Fatalf("read-only Run dispatched during a propose (ran=%q) — propose must not execute", runner.ranTool)
	}
	// 会话作用域传到了 resolver:租户钉死为绑定值(7)而非 body;operator 角色透传。
	if !proposer.called || proposer.gotReq.TenantID != 7 || proposer.gotRole != "tenant_operator" || proposer.gotName != "account_pause" {
		t.Fatalf("resolver scope wrong: called=%v name=%q req=%+v role=%q", proposer.called, proposer.gotName, proposer.gotReq, proposer.gotRole)
	}
	// 一行 dry-run 审计(status ok、dry_run=true)——镜像 operator H1 预览。
	if len(ins.rows) != 1 || ins.rows[0].ResultStatus != string(hermesops.ResultOK) || !ins.rows[0].DryRun {
		t.Fatalf("audit rows=%+v want one ok dry_run row", ins.rows)
	}

	// 安全:所发的 correlation_id 只能被绑定钉死的同一个 operator(工具=account_pause、租户=7、
	// actor=42、token=99)消费,并产出钉死的 target。Consume 取走即删,故这一次正确消费即证明
	// handler 把每个字段都绑对了(任一绑错 => !ok 或 TargetID 不符)。
	entry, ok := cache.Consume(resp.CorrelationID, "account_pause", 7, 42, 99)
	if !ok {
		t.Fatalf("session operator could not consume its own proposal — handler bound the wrong tool/tenant/actor/token")
	}
	if entry.TargetID != 5 {
		t.Fatalf("consumed entry TargetID=%d want 5 — handler pinned the wrong target", entry.TargetID)
	}
}

func TestProposeRefusesNonProposableTool(t *testing.T) {
	// 抓的缺陷(提议白名单门):未标记 Proposable 的 mutating 工具(如 renew_trigger 凭证轮换)绝不能
	// 被 LLM 提议。ResolveProposal 返回 ErrNotProposable;handler 必须 403 tool_not_proposable +
	// 一条 denied 审计行,且没有 needs_confirmation。
	// 变异(已验证转红):若 writeProposeError 把 ErrNotProposable 当成功并发了 correlation,rec.Code
	// 会是 200 + needs_confirmation → 翻掉 403 + denied-row 断言。
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{mutating: map[string]bool{"renew_trigger": true}}
	proposer := &fakeProposer{err: hermesops.ErrNotProposable}
	cache := hermesconfirm.NewCache()
	ins := &recordingInserter{}
	h := newProposeHandler(t, runner, proposer, cache, ins, bindings, true)
	token := bindSession(t, bindings, 7, 42, 99, "platform_admin", "req-prop-np")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proposeRequest(token, "renew_trigger", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403 (not proposable)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tool_not_proposable") {
		t.Fatalf("body=%s want tool_not_proposable", rec.Body.String())
	}
	if len(ins.rows) != 1 || ins.rows[0].ResultStatus != string(hermesops.ResultDenied) || ins.rows[0].ToolName != "renew_trigger" {
		t.Fatalf("audit rows=%+v want one denied renew_trigger row", ins.rows)
	}
}

func TestProposeErrorMapping(t *testing.T) {
	// 抓的缺陷(writeProposeError 的全错误映射表):ResolveProposal 的每一类错误必须映射到正确的
	// 状态码 + 错误码 + 审计 status(denied 是授权结果,error 是 args/target/依赖失败)。逐分支覆盖,
	// 防止某条分支错绑状态码或把 denied 误记成 error(反之亦然)。
	// 变异(已验证转红):把任一 case 塌进 default 分支,其状态码(如 404/400 vs 503)即翻;把某条
	// denied 分支的 recordCall status 改成 error,wantAudit 断言即翻。
	cases := []struct {
		name       string
		resolveErr error
		wantStatus int
		wantCode   string
		wantAudit  hermesops.ResultStatus
	}{
		{"unknown_tool", hermesops.ErrToolUnknown, http.StatusNotFound, "unknown_tool", hermesops.ResultDenied},
		{"forbidden_role", hermesops.ErrToolForbidden, http.StatusForbidden, "tool_forbidden", hermesops.ResultDenied},
		{"read_only_tool", hermesops.ErrNotMutating, http.StatusBadRequest, "tool_not_mutating", hermesops.ResultDenied},
		{"invalid_args", hermesops.ErrInvalidArgs, http.StatusBadRequest, "invalid_args", hermesops.ResultError},
		{"target_resolution", hermesops.ErrTargetResolution, http.StatusNotFound, "target_not_found", hermesops.ResultError},
		{"dependency_unwired", hermesops.ErrDependencyUnwired, http.StatusServiceUnavailable, "tool_unavailable", hermesops.ResultError},
		{"default_unknown", errors.New("boom"), http.StatusServiceUnavailable, "tool_failed", hermesops.ResultError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bindings := NewSessionBindings(toolTestClock)
			runner := &fakeRunner{}
			proposer := &fakeProposer{err: tc.resolveErr}
			ins := &recordingInserter{}
			h := newProposeHandler(t, runner, proposer, hermesconfirm.NewCache(), ins, bindings, true)
			token := bindSession(t, bindings, 7, 42, 99, "tenant_operator", "req-prop-"+tc.name)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, proposeRequest(token, "account_pause", map[string]any{"account_id": 5}))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s want %d", rec.Code, rec.Body.String(), tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantCode) {
				t.Fatalf("body=%s want %s", rec.Body.String(), tc.wantCode)
			}
			if len(ins.rows) != 1 || ins.rows[0].ResultStatus != string(tc.wantAudit) {
				t.Fatalf("audit rows=%+v want one %s row", ins.rows, tc.wantAudit)
			}
		})
	}
}

func TestProposeModeExecuteFallsToReadOnlyPath(t *testing.T) {
	// 抓的缺陷(向后兼容:非 "propose" 的 mode 走旧只读路径):mode="execute"(非空、非 propose)的
	// 只读工具必须照常走只读 dispatch 并 200,proposer 绝不被触达。这钉住 internalToolRequest.Mode
	// 注释承诺的 "" / "execute" 同走读路径。
	// 变异(已验证转红):把 serveProposal 的分支判断从 `req.Mode == proposeMode` 改成
	// `req.Mode != ""` → mode="execute" 会错走提议路径,proposer.called 变 true、只读工具不再 dispatch。
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{result: hermesops.ToolResult{Summary: map[string]any{"event_count": 3}}}
	proposer := &fakeProposer{plan: samplePlan()}
	h := newProposeHandler(t, runner, proposer, hermesconfirm.NewCache(), &recordingInserter{}, bindings, true)
	token := bindSession(t, bindings, 7, 42, 99, "platform_admin", "req-mode-exec")

	body, _ := json.Marshal(map[string]any{"tool_name": "log_analyze", "args": nil, "mode": "execute"})
	r := httptest.NewRequest(http.MethodPost, "/internal/hermes/tool-execute", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200 (mode=execute 走只读路径)", rec.Code, rec.Body.String())
	}
	if runner.ranTool != "log_analyze" {
		t.Fatalf("只读工具未 dispatch(ran=%q)——mode=execute 应走只读路径", runner.ranTool)
	}
	if proposer.called {
		t.Fatalf("proposer 被触达——mode=execute 误入提议路径")
	}
}

func TestProposeFailsClosedWhenUnwired(t *testing.T) {
	// 抓的缺陷(fail-closed 依赖):proposeEnabled=true 但提议依赖为 nil(无 proposer / 无共享 cache)
	// —— handler 必须 503 propose_unavailable,绝不 panic、绝不静默跳过确认要求。
	// 变异(已验证转红):删掉 `if h.proposer == nil || h.confirmCache == nil` 守卫 → serveProposal
	// 在 h.proposer.ResolveProposal 处 nil 解引用并 panic。
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{mutating: map[string]bool{"account_pause": true}}
	// KNOB 开,但 proposer + cache 为 nil。
	h := NewInternalToolHandler([]byte(toolTestSecret), bindings, runner, &recordingInserter{}, toolTestClock, true, nil, nil, true)
	token := bindSession(t, bindings, 7, 42, 99, "tenant_operator", "req-prop-nil")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proposeRequest(token, "account_pause", map[string]any{"account_id": 5}))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 (propose deps unwired)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "propose_unavailable") {
		t.Fatalf("body=%s want propose_unavailable", rec.Body.String())
	}
}

func TestProposeStillGatedByToolLoopKnob(t *testing.T) {
	// 抓的缺陷(防御纵深):提议路径活在对话工具循环内部,故 KNOB B(toolLoopEnabled=false)也必须
	// 拒绝它——在提议 KNOB 被查询之前。工具循环关时,一个 mode=propose 调用(即便 proposeEnabled=true)
	// 被拒 403 llm_toolloop_disabled,且 resolver 从不被调用。
	// 变异(已验证转红):若提议分支被放到 ServeHTTP 里 KNOB B 早返回之前,此处会改返回
	// llm_propose_disabled/200。
	bindings := NewSessionBindings(toolTestClock)
	runner := &fakeRunner{mutating: map[string]bool{"account_pause": true}}
	proposer := &fakeProposer{plan: samplePlan()}
	// toolLoopEnabled=false(KNOB B 关),但 proposeEnabled=true。
	h := NewInternalToolHandler([]byte(toolTestSecret), bindings, runner, &recordingInserter{}, toolTestClock, false, proposer, hermesconfirm.NewCache(), true)
	token := bindSession(t, bindings, 7, 42, 99, "tenant_operator", "req-prop-loopoff")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proposeRequest(token, "account_pause", map[string]any{"account_id": 5}))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403 (tool loop disabled)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "llm_toolloop_disabled") {
		t.Fatalf("body=%s want llm_toolloop_disabled (KNOB B precedes the propose branch)", rec.Body.String())
	}
	if proposer.called {
		t.Fatalf("ResolveProposal called despite tool loop disabled")
	}
}
