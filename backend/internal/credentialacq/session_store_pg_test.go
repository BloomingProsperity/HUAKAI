package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type testSessionDB struct {
	mu   sync.Mutex
	now  time.Time
	rows map[string]Session
}

func newTestSessionDB(now time.Time) *testSessionDB {
	return &testSessionDB{now: now, rows: map[string]Session{}}
}

func (db *testSessionDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (db *testSessionDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("test session db: Query not implemented")
}

func (db *testSessionDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.rows == nil {
		db.rows = map[string]Session{}
	}
	switch {
	case strings.Contains(sql, "INSERT INTO credential_acquisition_flow_sessions"):
		row := Session{
			ID: stringArg(args[0]), TenantID: int64Arg(args[1]), ProviderAccountID: int64Arg(args[2]),
			Vendor: stringArg(args[3]), AuthMode: stringArg(args[4]), Kind: FlowKind(stringArg(args[5])), Status: FlowStatus(stringArg(args[6])),
			ActorID: stringArg(args[7]), ActorRole: stringArg(args[8]),
			StateHash: bytesArg(args[9]), NonceHash: bytesArg(args[10]), EncryptedPKCEVerifier: bytesArg(args[11]),
			ClientIdentitySource: stringArg(args[12]), RedirectURI: stringArg(args[13]),
			LongLivedRequested: boolArg(args[16]), IdempotencyKeyHash: bytesArg(args[17]),
			ExpiresAt: timeArg(args[18]), CreatedAt: db.now, UpdatedAt: db.now,
		}
		_ = json.Unmarshal(bytesArg(args[14]), &row.RequestedScopes)
		_ = json.Unmarshal(bytesArg(args[15]), &row.RedactedContext)
		db.rows[row.ID] = row
		return testSessionRow{session: row}
	case strings.Contains(sql, "FROM credential_acquisition_flow_sessions") && strings.Contains(sql, "WHERE id = $1::uuid"):
		return db.rowByID(stringArg(args[0]))
	case strings.Contains(sql, "SET status = $2"):
		id := stringArg(args[0])
		row, ok := db.rows[id]
		if !ok {
			return testSessionRow{err: pgx.ErrNoRows}
		}
		row.Status = FlowStatus(stringArg(args[1]))
		row.ErrorClass = stringArg(args[2])
		row.ErrorMessageRedacted = stringArg(args[3])
		row.UpdatedAt = db.now
		db.rows[id] = row
		return testSessionRow{session: row}
	case strings.Contains(sql, "SET status = 'cancelled'"):
		id := stringArg(args[0])
		row, ok := db.rows[id]
		if !ok || row.Status == StatusFinalized || row.Status == StatusCancelled || row.Status == StatusExpired {
			return testSessionRow{err: pgx.ErrNoRows}
		}
		row.Status = StatusCancelled
		row.CancelledAt = db.now
		row.UpdatedAt = db.now
		db.rows[id] = row
		return testSessionRow{session: row}
	case strings.Contains(sql, "SET consumed_at = NOW()"):
		id := stringArg(args[0])
		row, ok := db.rows[id]
		if !ok || !row.ConsumedAt.IsZero() || row.Status == StatusFinalized || row.Status == StatusCancelled || row.Status == StatusExpired || !row.ExpiresAt.After(db.now) {
			return testSessionRow{err: pgx.ErrNoRows}
		}
		row.ConsumedAt = db.now
		row.UpdatedAt = db.now
		db.rows[id] = row
		return testSessionRow{session: row}
	case strings.Contains(sql, "SET status = 'finalized'"):
		id := stringArg(args[0])
		row, ok := db.rows[id]
		if !ok {
			return testSessionRow{err: pgx.ErrNoRows}
		}
		row.Status = StatusFinalized
		row.ResultAccountCredentialID = int64Arg(args[1])
		if row.ConsumedAt.IsZero() {
			row.ConsumedAt = db.now
		}
		row.ErrorClass = ""
		row.ErrorMessageRedacted = ""
		row.UpdatedAt = db.now
		db.rows[id] = row
		return testSessionRow{session: row}
	default:
		return testSessionRow{err: errors.New("test session db: unhandled query")}
	}
}

func (db *testSessionDB) rowByID(id string) pgx.Row {
	row, ok := db.rows[id]
	if !ok {
		return testSessionRow{err: pgx.ErrNoRows}
	}
	return testSessionRow{session: row}
}

type testSessionRow struct {
	session Session
	err     error
}

func (r testSessionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanTestSession(dest, r.session)
}

func scanTestSession(dest []any, row Session) error {
	if len(dest) != 26 {
		return errors.New("test session row: unexpected scan arity")
	}
	requestedScopes, _ := json.Marshal(row.RequestedScopes)
	redactedContext, _ := json.Marshal(row.RedactedContext)
	values := []any{
		row.ID, row.TenantID, row.ProviderAccountID, row.Vendor, row.AuthMode, row.Kind, row.Status,
		row.ActorID, row.ActorRole, row.StateHash, row.NonceHash, row.EncryptedPKCEVerifier,
		row.ClientIdentitySource, textValue(row.RedirectURI), requestedScopes, redactedContext,
		row.LongLivedRequested, row.IdempotencyKeyHash, int8Value(row.ResultAccountCredentialID),
		textValue(row.ErrorClass), textValue(row.ErrorMessageRedacted), row.ExpiresAt, timestamptzValue(row.ConsumedAt), timestamptzValue(row.CancelledAt),
		row.CreatedAt, row.UpdatedAt,
	}
	for i := range dest {
		assignScanValue(dest[i], values[i])
	}
	return nil
}

func assignScanValue(dest any, value any) {
	switch d := dest.(type) {
	case *string:
		*d = value.(string)
	case *int64:
		*d = value.(int64)
	case *bool:
		*d = value.(bool)
	case *FlowKind:
		*d = value.(FlowKind)
	case *FlowStatus:
		*d = value.(FlowStatus)
	case *[]byte:
		*d = append([]byte(nil), value.([]byte)...)
	case *time.Time:
		*d = value.(time.Time)
	case *pgtype.Text:
		*d = value.(pgtype.Text)
	case *pgtype.Int8:
		*d = value.(pgtype.Int8)
	case *pgtype.Timestamptz:
		*d = value.(pgtype.Timestamptz)
	}
}

func textValue(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: strings.TrimSpace(value) != ""}
}

func int8Value(value int64) pgtype.Int8 {
	return pgtype.Int8{Int64: value, Valid: value != 0}
}

func timestamptzValue(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}

func stringArg(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case FlowKind:
		return string(v)
	case FlowStatus:
		return string(v)
	default:
		return ""
	}
}

func int64Arg(value any) int64 {
	if v, ok := value.(int64); ok {
		return v
	}
	return 0
}

func boolArg(value any) bool {
	if v, ok := value.(bool); ok {
		return v
	}
	return false
}

func bytesArg(value any) []byte {
	if value == nil {
		return nil
	}
	if v, ok := value.([]byte); ok {
		return append([]byte(nil), v...)
	}
	return nil
}

func timeArg(value any) time.Time {
	if v, ok := value.(time.Time); ok {
		return v
	}
	return time.Time{}
}
