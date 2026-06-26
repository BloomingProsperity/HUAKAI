package hermeshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

const handlerSecretSentinel = "sk-HANDLER-LEAK-7c1d"

// --- fakes ------------------------------------------------------------------

// fakeToolCalls 捕获每一次 hermes_tool_calls 插入(权威的 tool-call 账本),
// 使测试能断言每条路径上记录了什么。
type fakeToolCalls struct {
	rows []hermestoolsdb.InsertHermesToolCallParams
}

func (f *fakeToolCalls) InsertHermesToolCall(_ context.Context, arg hermestoolsdb.InsertHermesToolCallParams) (hermestoolsdb.InsertHermesToolCallRow, error) {
	f.rows = append(f.rows, arg)
	return hermestoolsdb.InsertHermesToolCallRow{ID: int64(len(f.rows))}, nil
}

// auditCaptureStore 是一个 hermes.Store,其唯一有意义的方法是 InsertAuditEvent
// (镜像目标);其余方法在这些测试中未使用。
type auditCaptureStore struct {
	auditCalls int
}

func (s *auditCaptureStore) InsertAuditEvent(_ context.Context, _ dbhermes.InsertAuditEventParams) (dbhermes.HermesAuditEvent, error) {
	s.auditCalls++
	return dbhermes.HermesAuditEvent{}, nil
}

// 其余 hermes.Store 方法(此处未使用)——最小桩实现。
func (s *auditCaptureStore) AppendMessage(context.Context, dbhermes.AppendMessageParams) (int64, error) {
	return 0, nil
}
func (s *auditCaptureStore) CreateConversation(context.Context, dbhermes.CreateConversationParams) (int64, error) {
	return 0, nil
}
func (s *auditCaptureStore) CreateProfile(context.Context, dbhermes.CreateProfileParams) (dbhermes.HermesApiProfile, error) {
	return dbhermes.HermesApiProfile{}, nil
}
func (s *auditCaptureStore) DeleteProfile(context.Context, dbhermes.DeleteProfileParams) (int64, error) {
	return 0, nil
}
func (s *auditCaptureStore) DisableHermes(context.Context, dbhermes.DisableHermesParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}
func (s *auditCaptureStore) GetAPIKeyOwner(context.Context, dbhermes.GetAPIKeyOwnerParams) (int64, error) {
	return 0, nil
}
func (s *auditCaptureStore) GetConversation(context.Context, dbhermes.GetConversationParams) (dbhermes.HermesConversation, error) {
	return dbhermes.HermesConversation{}, nil
}
func (s *auditCaptureStore) GetProfile(context.Context, dbhermes.GetProfileParams) (dbhermes.HermesApiProfile, error) {
	return dbhermes.HermesApiProfile{}, nil
}
func (s *auditCaptureStore) GetSettings(context.Context, dbhermes.GetSettingsParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}
func (s *auditCaptureStore) ListConversationsByOwner(context.Context, dbhermes.ListConversationsByOwnerParams) ([]dbhermes.HermesConversation, error) {
	return nil, nil
}
func (s *auditCaptureStore) ListMessagesByConversation(context.Context, dbhermes.ListMessagesByConversationParams) ([]dbhermes.ListMessagesByConversationRow, error) {
	return nil, nil
}
func (s *auditCaptureStore) ListProfilesByOwner(context.Context, dbhermes.ListProfilesByOwnerParams) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}
func (s *auditCaptureStore) ListProfilesByTenant(context.Context, int64) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}
func (s *auditCaptureStore) ProfileInUse(context.Context, dbhermes.ProfileInUseParams) (bool, error) {
	return false, nil
}
func (s *auditCaptureStore) SoftDeleteConversation(context.Context, dbhermes.SoftDeleteConversationParams) (int64, error) {
	return 0, nil
}
func (s *auditCaptureStore) UpdateConversationLastMessageAt(context.Context, dbhermes.UpdateConversationLastMessageAtParams) (int64, error) {
	return 0, nil
}
func (s *auditCaptureStore) UpsertSettings(context.Context, dbhermes.UpsertSettingsParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

// buildToolHandler 接出一个白盒 handler,并在 context 中注入身份 + admin actor
// (与 AdminAuthMiddleware 注入的内容一致),使 tool-execute 的 RBAC + 审计可在无 DB
// 的情况下测试。
func buildToolHandler(t *testing.T, reg ToolRegistry, calls *fakeToolCalls) (handler, *auditCaptureStore) {
	t.Helper()
	store := &auditCaptureStore{}
	h := handler{
		svc:       hermes.NewService(store),
		tools:     reg,
		toolCalls: calls,
	}
	return h, store
}

func execute(h handler, ident sessionauth.Identity, actor adminActor, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/tool-execute", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), authContextKey{}, ident)
	ctx = context.WithValue(ctx, adminActorContextKey{}, actor)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.executeTool(rec, req)
	return rec
}

// leakyRegistry 注册一个只读工具,其 Run 返回固定的 summary,内含一个置于敏感键
// 之下的注入秘密,用以证明持久化的行会将其脱敏。
func leakyRegistry() *hermesops.Registry {
	reg := hermesops.NewRegistry()
	reg.Register(hermesops.ToolSpec{
		Name: hermesops.ToolDLQInspect, Category: hermesops.CategoryDiagnostic,
		ReadOnly: true, RequiredRole: hermesops.RoleTenantOperator,
		Run: func(_ context.Context, _ hermesops.ToolRequest) (hermesops.ToolResult, error) {
			return hermesops.ToolResult{Summary: map[string]any{
				"dlq_count":    1,
				"secret_token": handlerSecretSentinel, // 敏感键在持久化时必须被脱敏
			}}, nil
		},
	})
	// 一个仅限 platform-admin 的工具,用于检验 RBAC 拒绝路径。
	reg.Register(hermesops.ToolSpec{
		Name: "admin_only_probe", Category: hermesops.CategoryDiagnostic,
		ReadOnly: true, RequiredRole: hermesops.RolePlatformAdmin,
		Run: func(_ context.Context, _ hermesops.ToolRequest) (hermesops.ToolResult, error) {
			return hermesops.ToolResult{Summary: map[string]any{"ran": true}}, nil
		},
	})
	return reg
}

func operator(tenant int64) (sessionauth.Identity, adminActor) {
	return sessionauth.Identity{TenantID: tenant, UserID: 42}, adminActor{TokenID: 99, Role: admin.RoleTenantOperator}
}

// --- tests ------------------------------------------------------------------

func TestToolExecuteRecordsOkRowAndMirrorsAudit(t *testing.T) {
	// 回归:一次成功的工具调用必须 (1) 返回 200 及结构化结果,(2) 恰好记录一条
	// 状态为 'ok' 且带 operator token id 的 hermes_tool_calls 行,(3) 镜像写入一条
	// hermes_audit_events 行。变异:去掉 recordToolCall 调用会让 calls.rows 为空;
	// 去掉镜像会让 auditCalls 为 0。
	calls := &fakeToolCalls{}
	h, store := buildToolHandler(t, leakyRegistry(), calls)
	ident, actor := operator(7)

	rec := execute(h, ident, actor, `{"tool_name":"dlq_inspect","args":{"status":"pending"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(calls.rows) != 1 {
		t.Fatalf("tool-call rows=%d want 1", len(calls.rows))
	}
	if calls.rows[0].ResultStatus != string(hermesops.ResultOK) {
		t.Fatalf("result_status=%q want ok", calls.rows[0].ResultStatus)
	}
	if calls.rows[0].AdminActorTokenID == nil || *calls.rows[0].AdminActorTokenID != 99 {
		t.Fatalf("admin_actor_token_id=%v want 99", calls.rows[0].AdminActorTokenID)
	}
	if store.auditCalls != 1 {
		t.Fatalf("hermes audit mirror calls=%d want 1", store.auditCalls)
	}
}

func TestToolExecuteRedactsSecretInPersistedRow(t *testing.T) {
	// 回归(隐私,区分性):工具放在敏感 summary 键之下的秘密,在持久化的行里
	// 必须被脱敏。变异:去掉 RecordToolCall 里的脱敏步骤会把原始哨兵原样持久化。
	calls := &fakeToolCalls{}
	h, _ := buildToolHandler(t, leakyRegistry(), calls)
	ident, actor := operator(7)

	rec := execute(h, ident, actor, `{"tool_name":"dlq_inspect","args":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if len(calls.rows) != 1 {
		t.Fatalf("rows=%d want 1", len(calls.rows))
	}
	persisted := string(calls.rows[0].ResultSummary)
	if strings.Contains(persisted, handlerSecretSentinel) {
		t.Fatalf("persisted summary leaked secret: %s", persisted)
	}
	if !strings.Contains(persisted, "[REDACTED]") {
		t.Fatalf("expected secret_token redacted, got %s", persisted)
	}
}

func TestToolExecuteRBACDenialRecordsDeniedRow(t *testing.T) {
	// 回归(RBAC + 审计):tenant_operator 运行仅限 platform_admin 的工具时,必须
	// 得到 403 并记录一条 'denied' tool-call 行——被拒绝的尝试可审计,且工具主体
	// 从未运行。变异:跳过 denied 插入会让轨迹缺失;先运行后授权则会返回 200。
	calls := &fakeToolCalls{}
	h, store := buildToolHandler(t, leakyRegistry(), calls)
	ident, actor := operator(7)

	rec := execute(h, ident, actor, `{"tool_name":"admin_only_probe","args":{}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
	if len(calls.rows) != 1 || calls.rows[0].ResultStatus != string(hermesops.ResultDenied) {
		t.Fatalf("denied row not recorded: rows=%+v", calls.rows)
	}
	// 拒绝不得镜像出一条 success/failure 的动作行(它已由 denied tool-call 行捕获)。
	if store.auditCalls != 0 {
		t.Fatalf("audit mirror ran on denial: calls=%d want 0", store.auditCalls)
	}
}

func TestToolExecuteUnknownToolIs404WithDeniedRow(t *testing.T) {
	// 回归:未知工具名必须 404,并仍记录一条 denied 行(该尝试可审计)。变异:
	// 返回 200/500 或跳过该行都会使本测试失败。
	calls := &fakeToolCalls{}
	h, _ := buildToolHandler(t, leakyRegistry(), calls)
	ident, actor := operator(7)

	rec := execute(h, ident, actor, `{"tool_name":"no_such_tool","args":{}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
	if len(calls.rows) != 1 || calls.rows[0].ResultStatus != string(hermesops.ResultDenied) {
		t.Fatalf("denied row not recorded for unknown tool: %+v", calls.rows)
	}
}

func TestToolExecuteRequiresIdentity(t *testing.T) {
	// 回归:当 context 中没有已解析的身份时(生产中由 H1 中间件保证),handler
	// 必须拒绝 401 且绝不派发。这证明 handler 复用了 requireIdentity,而非盲目信任
	// 请求。
	calls := &fakeToolCalls{}
	h, _ := buildToolHandler(t, leakyRegistry(), calls)

	req := httptest.NewRequest(http.MethodPost, "/tool-execute", bytes.NewBufferString(`{"tool_name":"dlq_inspect"}`))
	rec := httptest.NewRecorder()
	h.executeTool(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
	if len(calls.rows) != 0 {
		t.Fatalf("tool-call recorded without identity: %+v", calls.rows)
	}
}

func TestToolExecuteTenantThreadedFromIdentityNotBody(t *testing.T) {
	// 回归(跨 tenant):工具的 ToolRequest.TenantID 必须来自中间件解析出的身份,
	// 而非任何 body 字段,使调用方无法在请求 body 中夹带一个外部 tenant。我们注册
	// 一个会回显其收到的 tenant 的工具,并断言:即便 body 指定了不同的 tenant,
	// 它仍等于身份里的 tenant。
	reg := hermesops.NewRegistry()
	reg.Register(hermesops.ToolSpec{
		Name: hermesops.ToolDLQInspect, ReadOnly: true, RequiredRole: hermesops.RoleTenantOperator,
		Run: func(_ context.Context, r hermesops.ToolRequest) (hermesops.ToolResult, error) {
			return hermesops.ToolResult{Summary: map[string]any{"seen_tenant": r.TenantID}}, nil
		},
	})
	calls := &fakeToolCalls{}
	h, _ := buildToolHandler(t, reg, calls)
	ident, actor := operator(7)

	rec := execute(h, ident, actor, `{"tool_name":"dlq_inspect","args":{"tenant_id":999}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if seen := resp.Result["seen_tenant"].(float64); int64(seen) != 7 {
		t.Fatalf("tool saw tenant=%v want 7 (body tenant_id must be ignored)", seen)
	}
}
