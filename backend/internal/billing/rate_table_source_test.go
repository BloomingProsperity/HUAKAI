package billing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAT_AUDIT_001_pricing_public_only(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	stub := &rateTableQueryStub{
		versions: []rateTableVersionStub{
			{
				id:            201,
				tenantID:      22,
				version:       "shared-version",
				isPublic:      false,
				pricingData:   json.RawMessage(`{"scope":"private"}`),
				effectiveFrom: now.Add(-time.Hour),
				createdAt:     now.Add(-time.Hour),
			},
			{
				id:            101,
				tenantID:      11,
				version:       "shared-version",
				isPublic:      false,
				pricingData:   json.RawMessage(`{"scope":"public"}`),
				effectiveFrom: now,
				createdAt:     now,
			},
		},
	}
	source := &PGXRateTableSource{pool: stub}

	if _, err := source.GetRateTable(ctx, "shared-version"); !errors.Is(err, ErrRateTableNotFound) {
		t.Fatalf("GetRateTable err=%v want %v", err, ErrRateTableNotFound)
	}
	if len(stub.args) != 1 || stub.args[0] != "shared-version" {
		t.Fatalf("GetRateTable args=%#v want version only", stub.args)
	}
	if !strings.Contains(stub.sql, "is_public = true") {
		t.Fatalf("GetRateTable SQL must scope to public rows:\n%s", stub.sql)
	}
	if strings.Contains(stub.sql, "tenant_id") {
		t.Fatalf("GetRateTable SQL must not depend on tenant sentinel for public rows:\n%s", stub.sql)
	}
}

func TestAT_AUDIT_001_pricing_admin_marked_public_returns_row(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	stub := &rateTableQueryStub{
		versions: []rateTableVersionStub{
			{
				id:            201,
				tenantID:      22,
				version:       "shared-version",
				isPublic:      false,
				pricingData:   json.RawMessage(`{"scope":"private"}`),
				effectiveFrom: now.Add(-time.Hour),
				createdAt:     now.Add(-time.Hour),
			},
			{
				id:            101,
				tenantID:      11,
				version:       "shared-version",
				isPublic:      true,
				pricingData:   json.RawMessage(`{"scope":"public"}`),
				effectiveFrom: now,
				createdAt:     now,
			},
		},
	}
	source := &PGXRateTableSource{pool: stub}

	got, err := source.GetRateTable(ctx, "shared-version")
	if err != nil {
		t.Fatalf("GetRateTable err=%v", err)
	}
	if got.ID != 101 || got.Version != "shared-version" || string(got.PricingData) != `{"scope":"public"}` {
		t.Fatalf("GetRateTable returned %#v, want explicitly public row only", got)
	}
	if len(stub.args) != 1 || stub.args[0] != "shared-version" {
		t.Fatalf("GetRateTable args=%#v want version only", stub.args)
	}
	if !strings.Contains(stub.sql, "is_public = true") {
		t.Fatalf("GetRateTable SQL must scope to public rows:\n%s", stub.sql)
	}
	if strings.Contains(stub.sql, "tenant_id") {
		t.Fatalf("GetRateTable SQL must not depend on tenant sentinel for public rows:\n%s", stub.sql)
	}
}

func TestAT_AUDIT_001_024_PublicRateTableQueriesUsePublicFlag(t *testing.T) {
	ctx := context.Background()
	stub := &rateTableQueryStub{row: rateTableRowStub{err: pgx.ErrNoRows}}
	source := &PGXRateTableSource{pool: stub}

	if _, err := source.GetRateTable(ctx, "shared-version"); !errors.Is(err, ErrRateTableNotFound) {
		t.Fatalf("GetRateTable err=%v want %v", err, ErrRateTableNotFound)
	}
	if len(stub.args) != 1 || stub.args[0] != "shared-version" {
		t.Fatalf("GetRateTable args=%#v want version only", stub.args)
	}
	if !strings.Contains(stub.sql, "is_public = true") {
		t.Fatalf("GetRateTable SQL must scope to public rows:\n%s", stub.sql)
	}

	if _, err := source.GetRateTableSnapshot(ctx, 99); !errors.Is(err, ErrRateTableNotFound) {
		t.Fatalf("GetRateTableSnapshot err=%v want %v", err, ErrRateTableNotFound)
	}
	if len(stub.args) != 1 || stub.args[0] != int64(99) {
		t.Fatalf("GetRateTableSnapshot args=%#v want snapshot id only", stub.args)
	}
	if !strings.Contains(stub.sql, "id = $1") || !strings.Contains(stub.sql, "is_public = true") {
		t.Fatalf("GetRateTableSnapshot SQL must use snapshot id and public rows:\n%s", stub.sql)
	}
}

func TestAT_AUDIT_001_025_ListRateTableSnapshotsUsesPublicFlag(t *testing.T) {
	ctx := context.Background()
	stub := &rateTableQueryStub{queryErr: errors.New("stop after query capture")}
	source := &PGXRateTableSource{pool: stub}

	if _, err := source.ListRateTableSnapshots(ctx); err == nil {
		t.Fatal("ListRateTableSnapshots expected query capture error")
	}
	if len(stub.args) != 0 {
		t.Fatalf("ListRateTableSnapshots args=%#v want none", stub.args)
	}
	if !strings.Contains(stub.sql, "is_public = true") {
		t.Fatalf("ListRateTableSnapshots SQL must scope to public rows:\n%s", stub.sql)
	}
}

type rateTableQueryStub struct {
	sql      string
	args     []any
	row      pgx.Row
	queryErr error
	versions []rateTableVersionStub
	calls    []rateTableQueryCall
}

type rateTableQueryCall struct {
	sql  string
	args []any
}

func (s *rateTableQueryStub) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.sql = sql
	s.args = append([]any(nil), args...)
	s.calls = append(s.calls, rateTableQueryCall{sql: sql, args: append([]any(nil), args...)})
	if len(s.versions) > 0 {
		return s.lookupRow(sql, args...)
	}
	if s.row != nil {
		return s.row
	}
	return rateTableRowStub{err: pgx.ErrNoRows}
}

func (s *rateTableQueryStub) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	s.sql = sql
	s.args = append([]any(nil), args...)
	s.calls = append(s.calls, rateTableQueryCall{sql: sql, args: append([]any(nil), args...)})
	return nil, s.queryErr
}

func (s *rateTableQueryStub) lookupRow(sql string, args ...any) pgx.Row {
	lowerSQL := strings.ToLower(sql)
	usesPublicFlag := strings.Contains(sql, "is_public = true")
	tenantArg := -1
	switch {
	case strings.Contains(sql, "tenant_id = $1") || strings.Contains(sql, "tenant_id=$1"):
		tenantArg = 0
	case strings.Contains(sql, "tenant_id = $2") || strings.Contains(sql, "tenant_id=$2"):
		tenantArg = 1
	}
	currentOnly := strings.Contains(lowerSQL, "effective_to is null")
	for _, row := range s.versions {
		if strings.Contains(sql, "version = $1") && (len(args) < 1 || args[0] != row.version) {
			continue
		}
		if strings.Contains(lowerSQL, "where id = $1") && (len(args) < 1 || args[0] != row.id) {
			continue
		}
		if tenantArg >= 0 {
			if len(args) <= tenantArg || args[tenantArg] != row.tenantID {
				continue
			}
		}
		if currentOnly && row.effectiveTo != nil {
			continue
		}
		if usesPublicFlag && !row.isPublic {
			continue
		}
		return rateTableRowStub{version: &row}
	}
	return rateTableRowStub{err: pgx.ErrNoRows}
}

type rateTableVersionStub struct {
	id            int64
	tenantID      int64
	version       string
	isPublic      bool
	pricingData   json.RawMessage
	effectiveFrom time.Time
	effectiveTo   *time.Time
	createdAt     time.Time
}

type rateTableRowStub struct {
	err     error
	version *rateTableVersionStub
}

func (r rateTableRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.version == nil {
		return pgx.ErrNoRows
	}
	*(dest[0].(*int64)) = r.version.id
	*(dest[1].(*string)) = r.version.version
	*(dest[2].(*json.RawMessage)) = append(json.RawMessage(nil), r.version.pricingData...)
	*(dest[3].(*pgtype.Timestamptz)) = pgtype.Timestamptz{Time: r.version.effectiveFrom, Valid: !r.version.effectiveFrom.IsZero()}
	if r.version.effectiveTo != nil {
		*(dest[4].(*pgtype.Timestamptz)) = pgtype.Timestamptz{Time: *r.version.effectiveTo, Valid: true}
	} else {
		*(dest[4].(*pgtype.Timestamptz)) = pgtype.Timestamptz{}
	}
	*(dest[5].(*pgtype.Timestamptz)) = pgtype.Timestamptz{Time: r.version.createdAt, Valid: !r.version.createdAt.IsZero()}
	return nil
}
