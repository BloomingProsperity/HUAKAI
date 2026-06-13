package hermesops

import (
	"context"
	"strings"
	"testing"

	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
)

// fakeInserter captures the params handed to InsertHermesToolCall so tests can
// assert the sanitized, persisted shape.
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
	// Regression (PRIVACY, DISCRIMINATING): a secret that slips into the args or
	// summary map under a sensitive key MUST be redacted before it reaches the
	// row. Mutation: removing the hermes.SanitizeArgs pass persists the raw
	// secret. We assert both the raw secret is ABSENT and the redaction marker is
	// PRESENT, so the test fails whether the sanitizer is dropped OR replaced with
	// a no-op passthrough.
	const secret = "sk-LIVE-leak-1234"
	f := &fakeInserter{}
	err := RecordToolCall(context.Background(), f, ToolCallAudit{
		TenantID: 7, ActorUserID: 42, AdminActorTokenID: 99,
		ToolName: ToolCredentialDiagnose,
		Args:     map[string]any{"account_id": 5, "api_key": secret},
		ResultSummary: map[string]any{
			"credential_ok": false,
			"secret_token":  secret, // sensitive key 'token' must be redacted
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
	// Non-sensitive diagnostic fields must survive.
	if !strings.Contains(argsStr, "account_id") {
		t.Fatalf("account_id was wrongly dropped from args: %s", argsStr)
	}
	// Operator attribution + status must be persisted.
	if f.got.AdminActorTokenID == nil || *f.got.AdminActorTokenID != 99 {
		t.Fatalf("admin_actor_token_id=%v want 99", f.got.AdminActorTokenID)
	}
	if f.got.ResultStatus != string(ResultOK) {
		t.Fatalf("result_status=%q want ok", f.got.ResultStatus)
	}
}

func TestRecordToolCallDeniedRow(t *testing.T) {
	// Regression: a denial must persist a row with status 'denied' and NO admin
	// actor coercion error — proving the denied path is auditable. Mutation:
	// skipping the denied insert leaves no trail of the rejected attempt.
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
	// No admin actor => token id column is NULL.
	if f.got.AdminActorTokenID != nil {
		t.Fatalf("admin_actor_token_id=%v want nil for non-admin actor", f.got.AdminActorTokenID)
	}
}

func TestRecordToolCallFailsClosedOnNilInserter(t *testing.T) {
	// Regression: a nil inserter must return ErrAuditStoreUnavailable, never panic.
	if err := RecordToolCall(context.Background(), nil, ToolCallAudit{ToolName: ToolAuditLookup, Status: ResultOK}); err != ErrAuditStoreUnavailable {
		t.Fatalf("err=%v want ErrAuditStoreUnavailable", err)
	}
}
