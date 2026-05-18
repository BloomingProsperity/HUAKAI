package billing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestAT_AUDIT_001_024_PublicRateTableQueriesUseTenantZero(t *testing.T) {
	ctx := context.Background()
	stub := &rateTableQueryStub{row: rateTableRowStub{err: pgx.ErrNoRows}}
	source := &PGXRateTableSource{pool: stub}

	if _, err := source.GetRateTable(ctx, "shared-version"); !errors.Is(err, ErrRateTableNotFound) {
		t.Fatalf("GetRateTable err=%v want %v", err, ErrRateTableNotFound)
	}
	if len(stub.args) != 2 || stub.args[0] != "shared-version" || stub.args[1] != PublicScopeTenantID {
		t.Fatalf("GetRateTable args=%#v want version + public tenant", stub.args)
	}
	if !strings.Contains(stub.sql, "tenant_id = $2") {
		t.Fatalf("GetRateTable SQL must scope to public tenant:\n%s", stub.sql)
	}

	if _, err := source.GetRateTableSnapshot(ctx, 99); !errors.Is(err, ErrRateTableNotFound) {
		t.Fatalf("GetRateTableSnapshot err=%v want %v", err, ErrRateTableNotFound)
	}
	if len(stub.args) != 2 || stub.args[0] != int64(99) || stub.args[1] != PublicScopeTenantID {
		t.Fatalf("GetRateTableSnapshot args=%#v want snapshot id + public tenant", stub.args)
	}
	if !strings.Contains(stub.sql, "id = $1") || !strings.Contains(stub.sql, "tenant_id = $2") {
		t.Fatalf("GetRateTableSnapshot SQL must use snapshot id and public tenant:\n%s", stub.sql)
	}
}

func TestAT_AUDIT_001_025_ListRateTableSnapshotsUsesTenantZero(t *testing.T) {
	ctx := context.Background()
	stub := &rateTableQueryStub{queryErr: errors.New("stop after query capture")}
	source := &PGXRateTableSource{pool: stub}

	if _, err := source.ListRateTableSnapshots(ctx); err == nil {
		t.Fatal("ListRateTableSnapshots expected query capture error")
	}
	if len(stub.args) != 1 || stub.args[0] != PublicScopeTenantID {
		t.Fatalf("ListRateTableSnapshots args=%#v want public tenant", stub.args)
	}
	if !strings.Contains(stub.sql, "tenant_id = $1") {
		t.Fatalf("ListRateTableSnapshots SQL must scope to public tenant:\n%s", stub.sql)
	}
}

type rateTableQueryStub struct {
	sql      string
	args     []any
	row      pgx.Row
	queryErr error
}

func (s *rateTableQueryStub) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.sql = sql
	s.args = append([]any(nil), args...)
	if s.row != nil {
		return s.row
	}
	return rateTableRowStub{err: pgx.ErrNoRows}
}

func (s *rateTableQueryStub) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	s.sql = sql
	s.args = append([]any(nil), args...)
	return nil, s.queryErr
}

type rateTableRowStub struct {
	err error
}

func (r rateTableRowStub) Scan(...any) error {
	return r.err
}
