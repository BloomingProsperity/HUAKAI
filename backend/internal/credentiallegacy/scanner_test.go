package credentiallegacy

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type countRow struct {
	count int64
	err   error
}

func (r countRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*int64)) = r.count
	return nil
}

type countDB struct {
	row countRow
}

func (d countDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return d.row
}

func TestCountReportsLegacyRowsWithoutReadingPayload(t *testing.T) {
	got, err := Count(context.Background(), countDB{row: countRow{count: 7}})
	if err != nil || got != 7 {
		t.Fatalf("Count()=(%d,%v)，期望 (7,nil)", got, err)
	}
}

func TestCountFailsClosedOnReadError(t *testing.T) {
	_, err := Count(context.Background(), countDB{row: countRow{err: errors.New("读取失败")}})
	if err == nil {
		t.Fatal("数据库读取失败时不得误报零条")
	}
}
