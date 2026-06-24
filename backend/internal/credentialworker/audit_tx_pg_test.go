package credentialworker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// 本文件保留无需 PG 的判别式单测；真 PG 事务与健康状态用例在
// audit_tx_pg_integration_test.go。
//
// TestDBAuditWriter_NilQueriesReturnsErrAuditWriterMissing — unit (无需 PG)
//
// RR-W5-002 步骤 2 修复:nil queries production 必须显式 ErrAuditWriterMissing,
// 防 audit 字段静默丢失。Mutation:把 return ErrAuditWriterMissing 改回 nil →
// 本用例 red。
func TestDBAuditWriter_NilQueriesReturnsErrAuditWriterMissing(t *testing.T) {
	w := dbAuditWriter{queries: nil}
	entry := &auth.RefreshAuditEntry{
		TenantID:          7,
		ProviderAccountID: 99,
		Outcome:           auth.Outcome("rotated"),
		RequestID:         "test-req",
		OccurredAt:        time.Now().UTC(),
	}
	err := w.WriteRefreshAudit(context.Background(), entry)
	if !errors.Is(err, ErrAuditWriterMissing) {
		t.Fatalf("nil queries: want ErrAuditWriterMissing; got %v", err)
	}
}

func TestAccountCredentialRefreshQueriesSQLFiltersUnsafeProviderAccountHealth(t *testing.T) {
	// 默认测试门的 SQL 谓词 guard：生产接线使用
	// AccountCredentialRefreshQueries，扫描 SQL 必须携带与真 PG fixture
	// 相同的 provider_account 健康状态谓词。Mutation 自检：从
	// NewAccountCredentialRefreshQueries 删除谓词时，即使没有
	// HUAKAI_DATABASE_URL，本用例也会 red。
	db := &refreshListQueryDBStub{}
	_, err := NewAccountCredentialRefreshQueries(db).ListAccountsForRefresh(context.Background(), dbbilling.ListAccountsForRefreshParams{
		RefreshBefore: pgTimestamptz(time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)),
		LimitCount:    10,
	})
	if err != nil {
		t.Fatalf("ListAccountsForRefresh: %v", err)
	}
	if db.calls != 1 {
		t.Fatalf("query calls=%d want 1", db.calls)
	}
}

func pgTimestamptz(ts time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: ts.UTC(), Valid: true}
}

type refreshListQueryDBStub struct {
	calls int
}

func (s *refreshListQueryDBStub) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (s *refreshListQueryDBStub) Query(_ context.Context, sql string, _ ...interface{}) (pgx.Rows, error) {
	s.calls++
	for _, required := range []string{
		"pa.enabled",
		"pa.health_state = 'healthy'",
		"pa.health_state IN ('throttled', 'cooldown')",
		"pa.health_state_until <= NOW()",
		"pa.health_state <> 'revoked'",
	} {
		if !strings.Contains(sql, required) {
			return nil, errors.New("refresh list SQL missing " + required)
		}
	}
	return emptyRefreshRows{}, nil
}

func (s *refreshListQueryDBStub) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

type emptyRefreshRows struct{}

func (emptyRefreshRows) Close()                                       {}
func (emptyRefreshRows) Err() error                                   { return nil }
func (emptyRefreshRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (emptyRefreshRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (emptyRefreshRows) Next() bool                                   { return false }
func (emptyRefreshRows) Scan(...any) error                            { return errors.New("unexpected Scan") }
func (emptyRefreshRows) Values() ([]any, error)                       { return nil, errors.New("unexpected Values") }
func (emptyRefreshRows) RawValues() [][]byte                          { return nil }
func (emptyRefreshRows) Conn() *pgx.Conn                              { return nil }
