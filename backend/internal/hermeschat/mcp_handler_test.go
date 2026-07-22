package hermeschat

import (
	"context"
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

const mcpTestSecret = "mcp-internal-secret-with-enough-entropy"

var mcpTestNow = time.Unix(1_700_000_000, 0).UTC()

type mcpRecordingInserter struct {
	rows []hermestoolsdb.InsertHermesToolCallParams
	err  error
}

func (r *mcpRecordingInserter) InsertHermesToolCall(_ context.Context, arg hermestoolsdb.InsertHermesToolCallParams) (hermestoolsdb.InsertHermesToolCallRow, error) {
	if r.err != nil {
		return hermestoolsdb.InsertHermesToolCallRow{}, r.err
	}
	r.rows = append(r.rows, arg)
	return hermestoolsdb.InsertHermesToolCallRow{ID: int64(len(r.rows))}, nil
}

func TestMCPInitialize和通知符合无状态协议(t *testing.T) {
	h := newMCPTestHandler(t, hermesops.NewRegistry(), nil, false)
	token := mcpTestToken(t, "tenant_operator")

	response := performMCP(t, h, token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"hermes","version":"0.19.0"},"capabilities":{}}}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"protocolVersion":"2025-06-18"`) || !strings.Contains(response.Body.String(), `"tools"`) {
		t.Fatalf("初始化响应=%d %s", response.Code, response.Body.String())
	}
	notification := performMCP(t, h, token, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if notification.Code != http.StatusAccepted || notification.Body.Len() != 0 {
		t.Fatalf("初始化通知响应=%d %q，预期 202 空响应", notification.Code, notification.Body.String())
	}
}

func TestMCP总开关关闭时拒绝全部工具调用(t *testing.T) {
	registry := hermesops.NewRegistry()
	ran := false
	registerMCPReadTool(t, registry, "tenant_read", hermesops.RoleTenantOperator, func(hermesops.ToolRequest) {
		ran = true
	})
	h := NewMCPHandler([]byte(mcpTestSecret), registry, &mcpRecordingInserter{}, hermesconfirm.NewCache(), func() time.Time { return mcpTestNow }, false, true)

	response := performMCP(t, h, mcpTestToken(t, "tenant_operator"), `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"tenant_read","arguments":{}}}`)
	if response.Code != http.StatusForbidden || ran {
		t.Fatalf("总开关关闭响应=%d，工具执行=%v；期望 403/false", response.Code, ran)
	}
}

func TestMCP提议开关关闭时目录和调用都拒绝改动提议(t *testing.T) {
	registry := hermesops.NewRegistry()
	mutated := false
	registerMCPProposalTool(t, registry, "account_pause", hermesops.RolePlatformAdmin, &mutated)
	h := newMCPTestHandler(t, registry, &mcpRecordingInserter{}, false)
	token := mcpTestToken(t, "platform_admin")

	list := performMCP(t, h, token, `{"jsonrpc":"2.0","id":10,"method":"tools/list","params":{}}`)
	if strings.Contains(list.Body.String(), "account_pause") {
		t.Fatalf("提议开关关闭后目录仍暴露改动工具：%s", list.Body.String())
	}
	call := performMCP(t, h, token, `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"account_pause","arguments":{"account_id":5}}}`)
	if !strings.Contains(call.Body.String(), "tool_not_proposable") || mutated {
		t.Fatalf("提议开关关闭调用=%s，实际改动=%v", call.Body.String(), mutated)
	}
}

func TestMCP目录只暴露当前角色可用能力(t *testing.T) {
	registry := hermesops.NewRegistry()
	registerMCPReadTool(t, registry, "tenant_read", hermesops.RoleTenantOperator, nil)
	registerMCPReadTool(t, registry, "platform_read", hermesops.RolePlatformAdmin, nil)
	registerMCPProposalTool(t, registry, "platform_proposal", hermesops.RolePlatformAdmin, nil)
	h := newMCPTestHandler(t, registry, nil, true)

	response := performMCP(t, h, mcpTestToken(t, "tenant_operator"), `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	body := response.Body.String()
	if !strings.Contains(body, `"name":"tenant_read"`) || strings.Contains(body, "platform_read") || strings.Contains(body, "platform_proposal") {
		t.Fatalf("租户管理员目录越权或缺项：%s", body)
	}
}

func TestMCP只读调用钉死租户和管理员并写日志(t *testing.T) {
	registry := hermesops.NewRegistry()
	var ran hermesops.ToolRequest
	registerMCPReadTool(t, registry, "audit_lookup", hermesops.RoleTenantOperator, func(req hermesops.ToolRequest) {
		ran = req
	})
	inserter := &mcpRecordingInserter{}
	h := newMCPTestHandler(t, registry, inserter, false)

	response := performMCP(t, h, mcpTestToken(t, "tenant_operator"), `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"audit_lookup","arguments":{}}}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"isError":false`) {
		t.Fatalf("工具调用失败：%d %s", response.Code, response.Body.String())
	}
	if ran.TenantID != 7 || ran.ActorSource != "token" || ran.ActorID != 99 || ran.Role != "tenant_operator" {
		t.Fatalf("工具身份=%+v，没有钉死到令牌声明", ran)
	}
	if len(inserter.rows) != 1 || inserter.rows[0].TenantID != 7 || inserter.rows[0].ActorID != 99 || inserter.rows[0].ResultStatus != string(hermesops.ResultOK) {
		t.Fatalf("日志记录=%+v，不符合预期", inserter.rows)
	}
}

func TestMCP提议不会直接执行改动(t *testing.T) {
	registry := hermesops.NewRegistry()
	mutated := false
	registerMCPProposalTool(t, registry, "account_pause", hermesops.RoleTenantOperator, &mutated)
	h := newMCPTestHandler(t, registry, &mcpRecordingInserter{}, true)

	response := performMCP(t, h, mcpTestToken(t, "tenant_operator"), `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"account_pause","arguments":{"account_id":5}}}`)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "needs_confirmation") || !strings.Contains(body, "correlation_id") {
		t.Fatalf("提议响应=%d %s", response.Code, body)
	}
	if mutated {
		t.Fatal("模型提议直接运行了改动函数")
	}
}

func TestMCP只读结果在日志失败时不会交给模型(t *testing.T) {
	registry := hermesops.NewRegistry()
	registerMCPReadTool(t, registry, "audit_lookup", hermesops.RoleTenantOperator, nil)
	h := newMCPTestHandler(t, registry, &mcpRecordingInserter{err: errors.New("日志库不可用")}, false)

	response := performMCP(t, h, mcpTestToken(t, "tenant_operator"), `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"audit_lookup","arguments":{}}}`)
	body := response.Body.String()
	if !strings.Contains(body, `"isError":true`) || !strings.Contains(body, "audit_unavailable") || strings.Contains(body, `"count":1`) {
		t.Fatalf("日志失败响应未闭锁诊断结果：%s", body)
	}
}

func TestMCP提议日志失败会撤销确认令牌(t *testing.T) {
	registry := hermesops.NewRegistry()
	registerMCPProposalTool(t, registry, "account_pause", hermesops.RoleTenantOperator, nil)
	if err := registry.Validate(); err != nil {
		t.Fatalf("测试注册表无效：%v", err)
	}
	confirmations := &trackingConfirmationStore{Cache: hermesconfirm.NewCache()}
	h := NewMCPHandler([]byte(mcpTestSecret), registry, &mcpRecordingInserter{err: errors.New("日志库不可用")}, confirmations, func() time.Time { return mcpTestNow }, true, true)

	response := performMCP(t, h, mcpTestToken(t, "tenant_operator"), `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"account_pause","arguments":{"account_id":5}}}`)
	body := response.Body.String()
	if !strings.Contains(body, "audit_unavailable") || strings.Contains(body, "correlation_id") {
		t.Fatalf("日志失败仍暴露确认令牌：%s", body)
	}
	if confirmations.issued != 1 || confirmations.consumed != 1 {
		t.Fatalf("确认令牌签发/撤销=%d/%d，期望 1/1", confirmations.issued, confirmations.consumed)
	}
}

type trackingConfirmationStore struct {
	*hermesconfirm.Cache
	issued   int
	consumed int
}

func (s *trackingConfirmationStore) Issue(ctx context.Context, pending hermesconfirm.PendingConfirmation) (string, error) {
	s.issued++
	return s.Cache.Issue(ctx, pending)
}

func (s *trackingConfirmationStore) ConsumeWithStatus(ctx context.Context, id string, pending hermesconfirm.PendingConfirmation) (hermesconfirm.PendingConfirmation, hermesconfirm.ConsumeStatus, error) {
	s.consumed++
	return s.Cache.ConsumeWithStatus(ctx, id, pending)
}

func TestMCP拒绝未声明参数和伪造令牌(t *testing.T) {
	registry := hermesops.NewRegistry()
	registerMCPReadTool(t, registry, "audit_lookup", hermesops.RoleTenantOperator, nil)
	h := newMCPTestHandler(t, registry, &mcpRecordingInserter{}, false)

	invalid := performMCP(t, h, mcpTestToken(t, "tenant_operator"), `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"audit_lookup","arguments":{"tenant_id":999}}}`)
	if !strings.Contains(invalid.Body.String(), "invalid_args") {
		t.Fatalf("未声明参数未被拒绝：%s", invalid.Body.String())
	}
	unauthorized := performMCP(t, h, "forged", `{"jsonrpc":"2.0","id":6,"method":"tools/list","params":{}}`)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("伪造令牌状态码=%d，预期 401", unauthorized.Code)
	}
}

func newMCPTestHandler(t *testing.T, registry *hermesops.Registry, inserter hermesops.ToolCallInserter, proposalEnabled bool) *MCPHandler {
	t.Helper()
	if err := registry.Validate(); err != nil {
		t.Fatalf("测试注册表无效：%v", err)
	}
	return NewMCPHandler([]byte(mcpTestSecret), registry, inserter, hermesconfirm.NewCache(), func() time.Time { return mcpTestNow }, true, proposalEnabled)
}

func registerMCPReadTool(t *testing.T, registry *hermesops.Registry, name, role string, observe func(hermesops.ToolRequest)) {
	t.Helper()
	err := registry.Register(hermesops.ToolSpec{
		Name: name, Category: hermesops.CategoryDiagnostic, Description: "读取运维信息",
		ReadOnly: true, RequiredRole: role, InputSchema: hermesops.ObjectSchema(nil),
		Run: func(_ context.Context, req hermesops.ToolRequest) (hermesops.ToolResult, error) {
			if observe != nil {
				observe(req)
			}
			return hermesops.ToolResult{Summary: map[string]any{"count": 1}}, nil
		},
	})
	if err != nil {
		t.Fatalf("注册只读工具：%v", err)
	}
}

func registerMCPProposalTool(t *testing.T, registry *hermesops.Registry, name, role string, mutated *bool) {
	t.Helper()
	err := registry.Register(hermesops.ToolSpec{
		Name: name, Category: hermesops.CategoryMutating, Description: "生成账号状态变更提议",
		Mutating: true, Proposable: true, RequiresConfirmation: true,
		RequiredRole: role,
		InputSchema: hermesops.ObjectSchema(map[string]any{
			"account_id": hermesops.PositiveIntegerSchema("账号 ID"),
		}, "account_id"),
		Resolve: func(context.Context, hermesops.ToolRequest) (hermesops.MutationPlan, error) {
			return hermesops.MutationPlan{TargetType: "provider_account", TargetID: 5, Preview: map[string]any{"enabled": false}}, nil
		},
		Mutate: func(context.Context, hermesops.ToolRequest, hermesops.MutationPlan) (hermesops.ToolResult, error) {
			if mutated != nil {
				*mutated = true
			}
			return hermesops.ToolResult{}, nil
		},
	})
	if err != nil {
		t.Fatalf("注册提议工具：%v", err)
	}
}

func mcpTestToken(t *testing.T, role string) string {
	t.Helper()
	token, err := SignInternalToken([]byte(mcpTestSecret), InternalTokenClaims{
		TenantID: 7, UserID: 42, ActorSource: "token", ActorID: 99, ActorRole: role,
		RequestID: "mcp-request",
		IssuedAt:  mcpTestNow, ExpiresAt: mcpTestNow.Add(InternalTokenTTL),
	})
	if err != nil {
		t.Fatalf("签发内部令牌：%v", err)
	}
	return token
}

func performMCP(t *testing.T, handler http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/internal/hermes/mcp", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
