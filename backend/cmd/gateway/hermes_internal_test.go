package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

func Test旧自定义运行器端点已彻底移除(t *testing.T) {
	d := newHermesGateTestDeps(t)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/internal/runner/bootstrap"},
		{http.MethodPost, "/internal/runner/refresh"},
		{http.MethodGet, "/internal/keys"},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s 状态码=%d，响应=%s，期望 404", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// TestHermesMCP仅挂在内部路径，防止官方 runner 的工具入口被用户 API 路由误暴露。
func TestHermesMCP仅挂在内部路径(t *testing.T) {
	d := newHermesGateTestDeps(t)
	d.hermesMCPHandler = hermeschat.NewMCPHandler(
		[]byte("mcp-test-secret"), hermesops.NewRegistry(), nil, nil, nil, true, false,
	)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	internal := httptest.NewRequest(http.MethodPost, "/internal/hermes/mcp", nil)
	internalRec := httptest.NewRecorder()
	r.ServeHTTP(internalRec, internal)
	if internalRec.Code != http.StatusUnauthorized {
		t.Fatalf("内部 MCP 无令牌状态码=%d，响应=%s，期望 401", internalRec.Code, internalRec.Body.String())
	}

	public := httptest.NewRequest(http.MethodPost, "/v1/hermes/mcp", nil)
	publicRec := httptest.NewRecorder()
	r.ServeHTTP(publicRec, public)
	if publicRec.Code != http.StatusServiceUnavailable || !strings.Contains(publicRec.Body.String(), "hermes_admin_backend_error") {
		t.Fatalf("公开 Hermes 前缀状态码=%d，响应=%s；不得落到 MCP 处理器或普通用户入口", publicRec.Code, publicRec.Body.String())
	}
}

func TestBuildHermesChatBridge必须使用专用内部令牌密钥(t *testing.T) {
	keys := mustGatewayHermesContentKeys(t)
	service := hermes.NewService(&hermesAuditStoreSpy{})
	bridge, err := buildHermesChatBridge(service, nil, nil, keys, nil)
	if !errors.Is(err, hermes.ErrMisconfigured) || bridge != nil {
		t.Fatalf("桥接器=%v，错误=%v；缺少 %s 时应关闭入口", bridge, err, hermeschat.InternalTokenSecretEnv)
	}

	bridge, err = buildHermesChatBridge(service, nil, nil, keys, []byte("dedicated-internal-token-secret"))
	if err != nil || bridge == nil {
		t.Fatalf("桥接器=%v，错误=%v；配置专用密钥后应成功", bridge, err)
	}
}

func mustGatewayHermesContentKeys(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("gateway-hermes-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("创建测试内容密钥失败：%v", err)
	}
	return keys
}

type hermesAuditStoreSpy struct {
	auditArgs []dbhermes.InsertAuditEventParams
}

func (s *hermesAuditStoreSpy) AppendMessage(context.Context, dbhermes.AppendMessageParams) (int64, error) {
	return 1, nil
}

func (s *hermesAuditStoreSpy) CreateConversation(context.Context, dbhermes.CreateConversationParams) (int64, error) {
	return 1, nil
}

func (s *hermesAuditStoreSpy) CreateProfile(context.Context, dbhermes.CreateProfileParams) (dbhermes.HermesApiProfile, error) {
	return dbhermes.HermesApiProfile{}, nil
}

func (s *hermesAuditStoreSpy) DeleteProfile(context.Context, dbhermes.DeleteProfileParams) (int64, error) {
	return 0, nil
}

func (s *hermesAuditStoreSpy) DisableHermes(context.Context, dbhermes.DisableHermesParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

func (s *hermesAuditStoreSpy) GetConversation(context.Context, dbhermes.GetConversationParams) (dbhermes.HermesConversation, error) {
	return dbhermes.HermesConversation{}, nil
}

func (s *hermesAuditStoreSpy) ListConversationsByOwner(context.Context, dbhermes.ListConversationsByOwnerParams) ([]dbhermes.HermesConversation, error) {
	return nil, nil
}

func (s *hermesAuditStoreSpy) ListMessagesByConversation(context.Context, dbhermes.ListMessagesByConversationParams) ([]dbhermes.ListMessagesByConversationRow, error) {
	return nil, nil
}

func (s *hermesAuditStoreSpy) GetProfile(context.Context, dbhermes.GetProfileParams) (dbhermes.HermesApiProfile, error) {
	return dbhermes.HermesApiProfile{}, nil
}

func (s *hermesAuditStoreSpy) GetSettings(context.Context, dbhermes.GetSettingsParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

func (s *hermesAuditStoreSpy) InsertAuditEvent(_ context.Context, arg dbhermes.InsertAuditEventParams) (dbhermes.HermesAuditEvent, error) {
	s.auditArgs = append(s.auditArgs, arg)
	return dbhermes.HermesAuditEvent{
		ID: int64(len(s.auditArgs)), TenantID: arg.TenantID,
		Action: arg.Action, Result: arg.Result, LogCategory: arg.LogCategory,
		ActorSource: arg.ActorSource, ActorID: arg.ActorID, ActorRole: gatewayStringPtr(arg.ActorRole),
	}, nil
}

func (s *hermesAuditStoreSpy) ListProfilesByOwner(context.Context, dbhermes.ListProfilesByOwnerParams) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *hermesAuditStoreSpy) ListProfilesByTenant(context.Context, int64) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *hermesAuditStoreSpy) ProfileInUse(context.Context, dbhermes.ProfileInUseParams) (bool, error) {
	return false, nil
}

func (s *hermesAuditStoreSpy) SoftDeleteConversation(context.Context, dbhermes.SoftDeleteConversationParams) (int64, error) {
	return 0, nil
}

func (s *hermesAuditStoreSpy) UpdateConversationLastMessageAt(context.Context, dbhermes.UpdateConversationLastMessageAtParams) (int64, error) {
	return 1, nil
}

func (s *hermesAuditStoreSpy) UpsertSettings(context.Context, dbhermes.UpsertSettingsParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

func gatewayStringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
