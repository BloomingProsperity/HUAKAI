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
	// 回归(隐私、有区分度):一个在敏感键下溜进 args 或 summary map 的密钥,在它到达该行之前
	// MUST(必须)被打码。变异:移除 hermes.SanitizeArgs 这一遍,就会持久化原始密钥。我们同时断言
	// 原始密钥 ABSENT(缺席)AND(且)打码标记 PRESENT(在场),这样无论 sanitizer 被去掉 OR(还是)
	// 被替换成一个 no-op 直通,测试都会失败。
	const secret = "sk-LIVE-leak-1234"
	f := &fakeInserter{}
	err := RecordToolCall(context.Background(), f, ToolCallAudit{
		TenantID: 7, ActorUserID: 42, AdminActorTokenID: 99,
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
	if f.got.AdminActorTokenID == nil || *f.got.AdminActorTokenID != 99 {
		t.Fatalf("admin_actor_token_id=%v want 99", f.got.AdminActorTokenID)
	}
	if f.got.ResultStatus != string(ResultOK) {
		t.Fatalf("result_status=%q want ok", f.got.ResultStatus)
	}
}

func TestRecordToolCallDeniedRow(t *testing.T) {
	// 回归:一次拒绝必须持久化一条 status 为 'denied' 的行,且 NO(无)admin actor 强转错误——
	// 证明 denied 路径可审计。变异:跳过 denied insert,被拒绝的尝试就不留任何轨迹。
	f := &fakeInserter{}
	err := RecordToolCall(context.Background(), f, ToolCallAudit{
		TenantID: 7, ActorUserID: 42,
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
	// 无 admin actor => token id 列为 NULL。
	if f.got.AdminActorTokenID != nil {
		t.Fatalf("admin_actor_token_id=%v want nil for non-admin actor", f.got.AdminActorTokenID)
	}
}

func TestRecordToolCallFailsClosedOnNilInserter(t *testing.T) {
	// 回归:nil 的 inserter 必须返回 ErrAuditStoreUnavailable,绝不 panic。
	if err := RecordToolCall(context.Background(), nil, ToolCallAudit{ToolName: ToolAuditLookup, Status: ResultOK}); err != ErrAuditStoreUnavailable {
		t.Fatalf("err=%v want ErrAuditStoreUnavailable", err)
	}
}
