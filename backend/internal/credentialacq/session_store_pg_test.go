package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
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

func (db *testSessionDB) Exec(_ context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.rows == nil {
		db.rows = map[string]Session{}
	}
	if len(args) >= 3 && strings.Contains(sql, "SET auth_type = $2::oauth_acquisition_auth_type") {
		id := stringArg(args[0])
		row, ok := db.rows[id]
		if !ok {
			return pgconn.CommandTag{}, nil
		}
		row.AuthType = AuthType(stringArg(args[1]))
		if raw := bytesArg(args[2]); len(raw) > 0 {
			_ = json.Unmarshal(raw, &row.DeviceCodePayload)
		} else {
			row.DeviceCodePayload = map[string]any{}
		}
		row.UpdatedAt = db.now
		db.rows[id] = row
	}
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
			ClientIdentitySource: stringArg(args[12]), AuthType: AuthTypePKCE, RedirectURI: stringArg(args[13]),
			LongLivedRequested: boolArg(args[16]), IdempotencyKeyHash: bytesArg(args[17]),
			ExpiresAt: timeArg(args[18]), CreatedAt: db.now, UpdatedAt: db.now,
		}
		_ = json.Unmarshal(bytesArg(args[14]), &row.RequestedScopes)
		_ = json.Unmarshal(bytesArg(args[15]), &row.RedactedContext)
		db.rows[row.ID] = row
		return testSessionRow{session: row, sql: sql}
	case strings.Contains(sql, "FROM credential_acquisition_flow_sessions") && strings.Contains(sql, "WHERE id = $1::uuid"):
		return db.rowByID(sql, stringArg(args[0]))
	case strings.Contains(sql, "SET status = $2"):
		id := stringArg(args[0])
		row, ok := db.rows[id]
		// 镜像真 SQL 的 `AND status NOT IN ('finalized','cancelled','expired','failed')` CAS —— 终态
		// 行不可被状态推进,RETURNING 无行 → ErrNoRows(production 再 re-fetch 区分 replay/not-found)。
		// 复用生产 isTerminalStatus,避免 fake 与真 SQL 漂移(教训)。
		if !ok || isTerminalStatus(row.Status) {
			return testSessionRow{err: pgx.ErrNoRows}
		}
		row.Status = FlowStatus(stringArg(args[1]))
		row.ErrorClass = stringArg(args[2])
		row.ErrorMessageRedacted = stringArg(args[3])
		row.UpdatedAt = db.now
		db.rows[id] = row
		return testSessionRow{session: row, sql: sql}
	case strings.Contains(sql, "SET status = 'cancelled'"):
		id := stringArg(args[0])
		row, ok := db.rows[id]
		// Cancel 的 NOT IN 现含 'failed' 且 consumed 行不可再 Cancel —— 与真 SQL CAS 同步。
		if !ok || isTerminalStatus(row.Status) || !row.ConsumedAt.IsZero() {
			return testSessionRow{err: pgx.ErrNoRows}
		}
		row.Status = StatusCancelled
		row.CancelledAt = db.now
		row.UpdatedAt = db.now
		db.rows[id] = row
		return testSessionRow{session: row, sql: sql}
	case strings.Contains(sql, "SET consumed_at = NOW()"):
		id := stringArg(args[0])
		row, ok := db.rows[id]
		// 与真 SQL 的 BeginFinalize predicate 保持一致 —— callback 式 OAuth(非 device_code/sso)
		// 未到 validated 不可 finalize。复用生产 helper RequiresCallbackValidation,避免 fake 与真 SQL 漂移。
		if !ok || !row.ConsumedAt.IsZero() || row.Status == StatusFinalized || row.Status == StatusCancelled || row.Status == StatusExpired || !row.ExpiresAt.After(db.now) ||
			(RequiresCallbackValidation(row.Kind, row.AuthType) && row.Status != StatusValidated) {
			return testSessionRow{err: pgx.ErrNoRows}
		}
		row.ConsumedAt = db.now
		row.UpdatedAt = db.now
		db.rows[id] = row
		return testSessionRow{session: row, sql: sql}
	case strings.Contains(sql, "SET status = 'finalized'"):
		id := stringArg(args[0])
		row, ok := db.rows[id]
		if !ok || !row.CancelledAt.IsZero() || row.Status == StatusCancelled || row.Status == StatusExpired || row.Status == StatusFailed {
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
		return testSessionRow{session: row, sql: sql}
	default:
		return testSessionRow{err: errors.New("test session db: unhandled query")}
	}
}

func (db *testSessionDB) rowByID(sql, id string) pgx.Row {
	row, ok := db.rows[id]
	if !ok {
		return testSessionRow{err: pgx.ErrNoRows}
	}
	return testSessionRow{session: row, sql: sql}
}

type testSessionRow struct {
	session Session
	sql     string
	err     error
}

func (r testSessionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanTestSession(dest, r.session, r.sql)
}

func scanTestSession(dest []any, row Session, sql string) error {
	includeAuthPayload := strings.Contains(sql, "auth_type") && strings.Contains(sql, "device_code_payload")
	requestedScopes, _ := json.Marshal(row.RequestedScopes)
	redactedContext, _ := json.Marshal(row.RedactedContext)
	deviceCodePayload, _ := json.Marshal(row.DeviceCodePayload)
	values := []any{
		row.ID, row.TenantID, row.ProviderAccountID, row.Vendor, row.AuthMode, row.Kind, row.Status,
		row.ActorID, row.ActorRole, row.StateHash, row.NonceHash, row.EncryptedPKCEVerifier,
		row.ClientIdentitySource,
	}
	if includeAuthPayload {
		values = append(values, textValue(string(row.AuthType)), deviceCodePayload)
	}
	values = append(values,
		textValue(row.RedirectURI), requestedScopes, redactedContext,
		row.LongLivedRequested, row.IdempotencyKeyHash, int8Value(row.ResultAccountCredentialID),
		textValue(row.ErrorClass), textValue(row.ErrorMessageRedacted), row.ExpiresAt, timestamptzValue(row.ConsumedAt), timestamptzValue(row.CancelledAt),
		row.CreatedAt, row.UpdatedAt,
	)
	if len(dest) != len(values) {
		return errors.New("test session row: unexpected scan arity")
	}
	for i := range dest {
		assignScanValue(dest[i], values[i])
	}
	return nil
}

func TestPostgresSessionStoreSetAuthPayloadRoundTripReloadsAuthTypeAndDevicePayload(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 45, 0, 0, time.UTC)
	store := NewPostgresSessionStore(newTestSessionDB(now)).WithNow(func() time.Time { return now })
	created, err := store.Create(context.Background(), Session{
		ID: "flow-device-roundtrip", TenantID: 10, ProviderAccountID: 20,
		Vendor: "openai", AuthMode: "codex_cli_oauth", Kind: FlowKindOAuth, Status: StatusStarted,
		ActorID: "admin-1", ActorRole: "platform_admin",
		ClientIdentitySource: ClientSourcePublicCLI,
		RequestedScopes:      []string{"openid", "profile"},
		RedactedContext:      map[string]any{"path": "device_code"},
		ExpiresAt:            now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	payload := map[string]any{
		"auth_type":        string(AuthTypeDeviceCode),
		"device_code":      "dev-from-db",
		"user_code":        "USER-CODE",
		"verification_uri": "https://device.example.test",
		"expires_in":       900,
		"interval":         5,
		"issued_at":        now.Format(time.RFC3339Nano),
		"token_url":        "https://device.example.test/token",
		"client_id":        "client-from-payload",
	}
	if err := store.SetAuthPayload(context.Background(), created.ID, AuthTypeDeviceCode, payload); err != nil {
		t.Fatalf("SetAuthPayload: %v", err)
	}

	reloaded, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertAuthPayloadRoundTrip(t, reloaded, AuthTypeDeviceCode, payload)

	waiting, err := store.UpdateStatus(context.Background(), created.ID, StatusWaitingForUser, "", "")
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	assertAuthPayloadRoundTrip(t, waiting, AuthTypeDeviceCode, payload)

	begin, err := store.BeginFinalize(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("BeginFinalize: %v", err)
	}
	assertAuthPayloadRoundTrip(t, begin, AuthTypeDeviceCode, payload)

	finalized, err := store.MarkFinalized(context.Background(), created.ID, 1234)
	if err != nil {
		t.Fatalf("MarkFinalized: %v", err)
	}
	assertAuthPayloadRoundTrip(t, finalized, AuthTypeDeviceCode, payload)
}

func TestDeviceCodePollUsesReloadedSessionPayload(t *testing.T) {
	now := time.Date(2026, 5, 24, 10, 50, 0, 0, time.UTC)
	store := NewPostgresSessionStore(newTestSessionDB(now)).WithNow(func() time.Time { return now })
	created, err := store.Create(context.Background(), Session{
		ID: "flow-device-poll", TenantID: 11, ProviderAccountID: 21,
		Vendor: "openai", AuthMode: "codex_cli_oauth", Kind: FlowKindOAuth, Status: StatusWaitingForUser,
		ActorID: "admin-2", ActorRole: "platform_admin",
		ClientIdentitySource: ClientSourcePublicCLI,
		RedactedContext:      map[string]any{},
		ExpiresAt:            now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	payload := map[string]any{
		"auth_type":   string(AuthTypeDeviceCode),
		"device_code": "dev-poll-from-db",
		"expires_in":  900,
		"interval":    5,
		"issued_at":   now.Format(time.RFC3339Nano),
		"token_url":   "https://device.example.test/token",
		"client_id":   "client-from-db-payload",
	}
	if err := store.SetAuthPayload(context.Background(), created.ID, AuthTypeDeviceCode, payload); err != nil {
		t.Fatalf("SetAuthPayload: %v", err)
	}
	reloaded, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	var seenDeviceCode, seenClientID string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		seenDeviceCode = stringField(body, "device_code")
		seenClientID = stringField(body, "client_id")
		return jsonHTTPResponse(t, map[string]any{"access_token": "access-from-poll"}), nil
	})}
	candidate, err := PollDeviceCodeToken(context.Background(), reloaded, OAuthClientConfig{},
		WithDeviceCodeHTTPClient(client),
		WithDeviceCodeNow(func() time.Time { return now }),
		WithDeviceCodeSleeper(func(context.Context, time.Duration) error {
			return errors.New("poll should not sleep after immediate success")
		}),
	)
	if err != nil {
		t.Fatalf("PollDeviceCodeToken with reloaded session: %v", err)
	}
	if seenDeviceCode != "dev-poll-from-db" || seenClientID != "client-from-db-payload" {
		t.Fatalf("poll request device_code=%q client_id=%q", seenDeviceCode, seenClientID)
	}
	if candidate.TenantID != 11 || candidate.ProviderAccountID != 21 {
		t.Fatalf("candidate target tenant/account=%d/%d", candidate.TenantID, candidate.ProviderAccountID)
	}
}

func assertAuthPayloadRoundTrip(t *testing.T, session Session, wantAuthType AuthType, wantPayload map[string]any) {
	t.Helper()
	if session.AuthType != wantAuthType {
		t.Fatalf("AuthType=%q want %q", session.AuthType, wantAuthType)
	}
	gotRaw, err := json.Marshal(session.DeviceCodePayload)
	if err != nil {
		t.Fatalf("marshal got payload: %v", err)
	}
	wantRaw, err := json.Marshal(wantPayload)
	if err != nil {
		t.Fatalf("marshal want payload: %v", err)
	}
	if string(gotRaw) != string(wantRaw) {
		t.Fatalf("DeviceCodePayload=%s want %s", gotRaw, wantRaw)
	}
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

// TestBeginFinalizeRequiresCallbackValidationForCallbackOAuth guards a callback-style OAuth
// flow (PKCE) must NOT be finalizable while status is started/waiting_for_user — it must first pass
// CompleteOAuthCallback (state check + code exchange) to reach validated. Otherwise an over-privileged
// admin could skip the callback and finalize with a hand-written credentials body, injecting arbitrary
// upstream bearer material. The fake DB reuses the production RequiresCallbackValidation helper, so it
// tracks the real BeginFinalize SQL predicate.
//
// Discriminating controls prove the gate is precise, not blanket: (b) a validated callback flow
// finalizes; (c) a device_code flow at waiting_for_user is EXEMPT and finalizes — without this the fix
// would permanently break device-code/sso credential acquisition.
//
// Mutation: make RequiresCallbackValidation return false unconditionally → case (a) finalizes and the
// ErrOAuthRequiresCallback assertion goes red.
func TestBeginFinalizeRequiresCallbackValidationForCallbackOAuth(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	store := NewPostgresSessionStore(newTestSessionDB(now)).WithNow(func() time.Time { return now })
	ctx := context.Background()
	mk := func(id string, status FlowStatus) Session {
		s, err := store.Create(ctx, Session{
			ID: id, TenantID: 1, ProviderAccountID: 2, Vendor: "openai", AuthMode: "chatgpt_oauth",
			Kind: FlowKindOAuth, Status: status,
			ActorID: "admin-1", ActorRole: "platform_admin", ExpiresAt: now.Add(10 * time.Minute),
		})
		if err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
		return s
	}

	// (a) callback OAuth (default pkce auth_type) still at started → refused.
	cb := mk("cb-started", StatusStarted)
	if _, err := store.BeginFinalize(ctx, cb.ID); !errors.Is(err, ErrOAuthRequiresCallback) {
		t.Fatalf("callback OAuth at started: err=%v, want ErrOAuthRequiresCallback", err)
	}
	// (b) callback OAuth advanced to validated → finalize proceeds.
	cbv := mk("cb-validated", StatusValidated)
	if _, err := store.BeginFinalize(ctx, cbv.ID); err != nil {
		t.Fatalf("validated callback OAuth must finalize: %v", err)
	}
	// (c) device_code flow at waiting_for_user → EXEMPT (auth_type=device_code), finalize proceeds.
	dc := mk("dc-waiting", StatusWaitingForUser)
	if err := store.SetAuthPayload(ctx, dc.ID, AuthTypeDeviceCode, map[string]any{
		"auth_type": string(AuthTypeDeviceCode), "device_code": "dev", "user_code": "U-1",
		"verification_uri": "https://device.example.test", "expires_in": 900, "interval": 5,
		"issued_at": now.Format(time.RFC3339Nano), "token_url": "https://device.example.test/token", "client_id": "c",
	}); err != nil {
		t.Fatalf("SetAuthPayload device_code: %v", err)
	}
	if _, err := store.BeginFinalize(ctx, dc.ID); err != nil {
		t.Fatalf("device_code flow must be exempt from callback-validation gate: %v", err)
	}
	// (d) a terminal (cancelled) callback OAuth flow must surface the replay/terminal
	// error, NOT oauth_requires_callback — the gate is limited to active pre-validation statuses so it
	// doesn't mask the real state. Mutation: drop the status switch (run the check on any non-validated
	// status) and a cancelled flow reports ErrOAuthRequiresCallback → this assertion goes red.
	cc := mk("cb-cancelled", StatusCancelled)
	if _, err := store.BeginFinalize(ctx, cc.ID); !errors.Is(err, ErrFlowReplay) {
		t.Fatalf("cancelled callback OAuth: err=%v, want ErrFlowReplay (not ErrOAuthRequiresCallback)", err)
	}
}
