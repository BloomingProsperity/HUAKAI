package hermesops

import (
	"context"
	"strings"
	"testing"

	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
)

// fakeInserter 捕获交给 InsertHermesToolCall 的参数,这样测试就能断言已脱敏、已持久化的形态。
type fakeInserter struct {
	got     hermestoolsdb.InsertHermesToolCallParams
	calls   int
	failErr error
}

func (f *fakeInserter) InsertHermesToolCall(_ context.Context, arg hermestoolsdb.InsertHermesToolCallParams) (hermestoolsdb.InsertHermesToolCallRow, error) {
	f.calls++
	f.got = arg
	if f.failErr != nil {
		return hermestoolsdb.InsertHermesToolCallRow{}, f.failErr
	}
	return hermestoolsdb.InsertHermesToolCallRow{ID: 1}, nil
}

func TestRecordToolCallSanitizesArgsAndSummary(t *testing.T) {
	// 回归：敏感键下的密钥在持久化前必须被打码。移除 hermes.SanitizeArgs 会泄露原始密钥。
	// 测试同时断言原始密钥不存在且打码标记存在，防止清洗器被删除或退化成直通。
	const secret = "sk-LIVE-leak-1234"
	f := &fakeInserter{}
	err := RecordToolCall(context.Background(), f, ToolCallAudit{
		TenantID: 7, ActorSource: "token", ActorID: 99, ActorRole: RolePlatformAdmin,
		ToolName: ToolCredentialDiagnose,
		Args:     map[string]any{"account_id": 5, "api_key": secret},
		ResultSummary: map[string]any{
			"credential_ok": false,
			"secret_token":  secret, // 敏感键 'token' 必须被打码
		},
		Status:        ResultOK,
		CorrelationID: "corr-1",
		RequestID:     "req-1",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if f.calls != 1 {
		t.Fatalf("inserter calls=%d want 1", f.calls)
	}

	argsStr := string(f.got.RequestedArgs)
	summaryStr := string(f.got.ResultSummary)
	if strings.Contains(argsStr, secret) || strings.Contains(summaryStr, secret) {
		t.Fatalf("persisted row leaked secret: args=%s summary=%s", argsStr, summaryStr)
	}
	if !strings.Contains(argsStr, "[REDACTED]") {
		t.Fatalf("expected api_key redacted in args, got %s", argsStr)
	}
	if !strings.Contains(summaryStr, "[REDACTED]") {
		t.Fatalf("expected secret_token redacted in summary, got %s", summaryStr)
	}
	// 非敏感的诊断字段必须保留。
	if !strings.Contains(argsStr, "account_id") {
		t.Fatalf("account_id was wrongly dropped from args: %s", argsStr)
	}
	// 运营者归属 + 状态必须被持久化。
	if f.got.ActorSource != "token" || f.got.ActorID != 99 || f.got.ActorRole != RolePlatformAdmin {
		t.Fatalf("管理员归属=%s:%d/%s，期望 token:99/%s", f.got.ActorSource, f.got.ActorID, f.got.ActorRole, RolePlatformAdmin)
	}
	if f.got.ResultStatus != string(ResultOK) {
		t.Fatalf("result_status=%q want ok", f.got.ResultStatus)
	}
}

func TestRecordToolCallDeniedRow(t *testing.T) {
	// 一次拒绝必须持久化安全分类，并保留会话管理员归属。
	f := &fakeInserter{}
	err := RecordToolCall(context.Background(), f, ToolCallAudit{
		TenantID: 7, ActorSource: "session", ActorID: 42, ActorRole: RoleTenantOperator,
		ToolName: ToolDLQInspect,
		Args:     map[string]any{"status": "pending"},
		Status:   ResultDenied,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if f.got.ResultStatus != string(ResultDenied) {
		t.Fatalf("result_status=%q want denied", f.got.ResultStatus)
	}
	if f.got.ActorSource != "session" || f.got.ActorID != 42 || f.got.LogCategory != "security" {
		t.Fatalf("拒绝日志归属/分类错误: %+v", f.got)
	}
}

func TestRecordToolCallFailsClosedOnNilInserter(t *testing.T) {
	// 回归:nil 的 inserter 必须返回 ErrAuditStoreUnavailable,绝不 panic。
	if err := RecordToolCall(context.Background(), nil, ToolCallAudit{ToolName: ToolAuditLookup, Status: ResultOK}); err != ErrAuditStoreUnavailable {
		t.Fatalf("err=%v want ErrAuditStoreUnavailable", err)
	}
}
