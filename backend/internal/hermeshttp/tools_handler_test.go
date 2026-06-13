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

// fakeToolCalls captures every hermes_tool_calls insert (the authoritative
// tool-call ledger) so a test can assert what was recorded on each path.
type fakeToolCalls struct {
	rows []hermestoolsdb.InsertHermesToolCallParams
}

func (f *fakeToolCalls) InsertHermesToolCall(_ context.Context, arg hermestoolsdb.InsertHermesToolCallParams) (hermestoolsdb.InsertHermesToolCallRow, error) {
	f.rows = append(f.rows, arg)
	return hermestoolsdb.InsertHermesToolCallRow{ID: int64(len(f.rows))}, nil
}

// auditCaptureStore is a hermes.Store whose only meaningful method is
// InsertAuditEvent (the mirror target); the rest are unused by these tests.
type auditCaptureStore struct {
	auditCalls int
}

func (s *auditCaptureStore) InsertAuditEvent(_ context.Context, _ dbhermes.InsertAuditEventParams) (dbhermes.HermesAuditEvent, error) {
	s.auditCalls++
	return dbhermes.HermesAuditEvent{}, nil
}

// remaining hermes.Store methods (unused here) — minimal stubs.
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

// buildToolHandler wires a white-box handler with an injected identity + admin
// actor in context (mirroring what AdminAuthMiddleware injects), so the
// tool-execute RBAC + audit can be tested without a DB.
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

// fakeTool registers a single read-only tool whose Run returns a fixed summary
// that includes an injected secret under a sensitive key, to prove the persisted
// row redacts it.
func leakyRegistry() *hermesops.Registry {
	reg := hermesops.NewRegistry()
	reg.Register(hermesops.ToolSpec{
		Name: hermesops.ToolDLQInspect, Category: hermesops.CategoryDiagnostic,
		ReadOnly: true, RequiredRole: hermesops.RoleTenantOperator,
		Run: func(_ context.Context, _ hermesops.ToolRequest) (hermesops.ToolResult, error) {
			return hermesops.ToolResult{Summary: map[string]any{
				"dlq_count":    1,
				"secret_token": handlerSecretSentinel, // sensitive key must be redacted on persist
			}}, nil
		},
	})
	// A platform-admin-only tool to exercise the RBAC denial path.
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
	// Regression: a successful tool call must (1) return 200 with the structured
	// result, (2) record exactly one hermes_tool_calls row with status 'ok' and
	// the operator's token id, and (3) mirror one hermes_audit_events row.
	// Mutation: dropping the recordToolCall call leaves calls.rows empty; dropping
	// the mirror leaves auditCalls 0.
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
	// Regression (PRIVACY, DISCRIMINATING): a secret the tool put under a
	// sensitive summary key must be redacted in the persisted row. Mutation:
	// removing the sanitize pass in RecordToolCall persists the raw sentinel.
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
	// Regression (RBAC + audit): a tenant_operator running a platform_admin-only
	// tool must get 403 AND a recorded 'denied' tool-call row — the rejected
	// attempt is auditable, and the tool body never ran. Mutation: skipping the
	// denied insert leaves no trail; authorizing-after-run would 200.
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
	// A denial must NOT mirror a success/failure action row (it is captured by
	// the denied tool-call row).
	if store.auditCalls != 0 {
		t.Fatalf("audit mirror ran on denial: calls=%d want 0", store.auditCalls)
	}
}

func TestToolExecuteUnknownToolIs404WithDeniedRow(t *testing.T) {
	// Regression: an unknown tool name must 404 and still record a denied row
	// (the attempt is auditable). Mutation: returning 200/500 or skipping the row
	// fails this.
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
	// Regression: without a resolved identity in context (which the H1 middleware
	// guarantees in production), the handler must reject 401 and never dispatch.
	// This proves the handler reuses requireIdentity rather than trusting the
	// request blindly.
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
	// Regression (cross-tenant): the tool's ToolRequest.TenantID must come from
	// the middleware-resolved identity, NOT from any body field, so a caller
	// cannot smuggle a foreign tenant in the request body. We register a tool
	// that echoes the tenant it received and assert it equals the identity tenant
	// even though the body names a different one.
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
