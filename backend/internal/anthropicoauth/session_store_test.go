package anthropicoauth

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

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
)

type testSessionDB struct {
	mu   sync.Mutex
	now  time.Time
	rows map[string]credentialacq.Session
}

func newTestSessionDB(now time.Time) *testSessionDB {
	return &testSessionDB{now: now, rows: map[string]credentialacq.Session{}}
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
		db.rows = map[string]credentialacq.Session{}
	}
	switch {
	case strings.Contains(sql, "INSERT INTO credential_acquisition_flow_sessions"):
		row := credentialacq.Session{
			ID: stringArg(args[0]), TenantID: int64Arg(args[1]), ProviderAccountID: int64Arg(args[2]),
			Vendor: stringArg(args[3]), AuthMode: stringArg(args[4]), Kind: credentialacq.FlowKind(stringArg(args[5])), Status: credentialacq.FlowStatus(stringArg(args[6])),
			ActorID: stringArg(args[7]), ActorRole: stringArg(args[8]),
			StateHash: stringBytesArg(args[9]), NonceHash: stringBytesArg(args[10]), EncryptedPKCEVerifier: stringBytesArg(args[11]),
			ClientIdentitySource: stringArg(args[12]), RedirectURI: stringArg(args[13]),
			LongLivedRequested: boolArg(args[16]), IdempotencyKeyHash: stringBytesArg(args[17]),
			ExpiresAt: timeArg(args[18]), CreatedAt: db.now, UpdatedAt: db.now,
		}
		_ = json.Unmarshal(stringBytesArg(args[14]), &row.RequestedScopes)
		_ = json.Unmarshal(stringBytesArg(args[15]), &row.RedactedContext)
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
		row.Status = credentialacq.FlowStatus(stringArg(args[1]))
		row.ErrorClass = stringArg(args[2])
		row.ErrorMessageRedacted = stringArg(args[3])
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
	session credentialacq.Session
	err     error
}

func (r testSessionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 26 {
		return errors.New("test session row: unexpected scan arity")
	}
	requestedScopes, _ := json.Marshal(r.session.RequestedScopes)
	redactedContext, _ := json.Marshal(r.session.RedactedContext)
	values := []any{
		r.session.ID, r.session.TenantID, r.session.ProviderAccountID, r.session.Vendor, r.session.AuthMode, r.session.Kind, r.session.Status,
		r.session.ActorID, r.session.ActorRole, r.session.StateHash, r.session.NonceHash, r.session.EncryptedPKCEVerifier,
		r.session.ClientIdentitySource, textValue(r.session.RedirectURI), requestedScopes, redactedContext,
		r.session.LongLivedRequested, r.session.IdempotencyKeyHash, int8Value(r.session.ResultAccountCredentialID),
		textValue(r.session.ErrorClass), textValue(r.session.ErrorMessageRedacted), r.session.ExpiresAt, timestamptzValue(r.session.ConsumedAt), timestamptzValue(r.session.CancelledAt),
		r.session.CreatedAt, r.session.UpdatedAt,
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
	case *credentialacq.FlowKind:
		*d = value.(credentialacq.FlowKind)
	case *credentialacq.FlowStatus:
		*d = value.(credentialacq.FlowStatus)
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
	case credentialacq.FlowKind:
		return string(v)
	case credentialacq.FlowStatus:
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

func stringBytesArg(value any) []byte {
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
