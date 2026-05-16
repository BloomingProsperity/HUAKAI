package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestAdminCredentialAcquisitionRoutesIntegration(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAdmin())

	oauthFlow := fx.seedOAuthFlow(t, 101)
	pasteFlow := fx.seedPasteFlow(t, 101)
	cancelFlow := fx.seedPasteFlow(t, 101)
	finalizeFlow := fx.seedPasteFlow(t, 101)
	helperCallbackFlow := fx.seedOAuthFlow(t, 101)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{
			name: "canonical create", method: http.MethodPost, path: "/v1/admin/pool-accounts/101/credential-acquisitions",
			body: `{"tenant_id":1,"vendor":"openai","auth_mode":"api_key","flow_kind":"paste"}`, want: http.StatusCreated,
		},
		{name: "canonical status", method: http.MethodGet, path: "/v1/admin/pool-accounts/101/credential-acquisitions/" + pasteFlow.ID, want: http.StatusOK},
		{
			name: "canonical callback", method: http.MethodPost, path: "/v1/admin/pool-accounts/101/credential-acquisitions/" + oauthFlow.ID + "/callback",
			body: `{"state":"` + oauthFlow.State + `","code":"auth-code","credentials":{"session_token":"session-value","refresh_token":"refresh-value"}}`, want: http.StatusOK,
		},
		{name: "canonical cancel", method: http.MethodPost, path: "/v1/admin/pool-accounts/101/credential-acquisitions/" + cancelFlow.ID + "/cancel", body: `{}`, want: http.StatusOK},
		{
			name: "canonical finalize", method: http.MethodPost, path: "/v1/admin/pool-accounts/101/credential-acquisitions/" + finalizeFlow.ID + "/finalize",
			body: `{"credentials":{"api_key":"sk-test-finalize"}}`, want: http.StatusOK,
		},
		{
			name: "helper paste", method: http.MethodPost, path: "/admin/v1/credentials/paste",
			body: `{"tenant_id":1,"provider_account_id":101,"vendor":"openai","auth_mode":"api_key","credentials":{"api_key":"sk-test-paste"}}`, want: http.StatusCreated,
		},
		{
			name: "helper cli import", method: http.MethodPost, path: "/admin/v1/credentials/cli-import",
			body: `{"tenant_id":1,"provider_account_id":101,"content":"{\"session_token\":\"session-cli\",\"refresh_token\":\"refresh-cli\"}"}`, want: http.StatusCreated,
		},
		{
			name: "helper csv import", method: http.MethodPost, path: "/admin/v1/credentials/csv-import",
			body: `{"tenant_id":1,"provider_account_id":101,"vendor":"openai","auth_mode":"api_key","content":"api_key,vendor,auth_mode\nsk-test-csv,openai,api_key\n"}`, want: http.StatusCreated,
		},
		{
			name: "helper json import", method: http.MethodPost, path: "/admin/v1/credentials/json-import",
			body: `{"tenant_id":1,"provider_account_id":101,"vendor":"openai","auth_mode":"api_key","content":"[{\"api_key\":\"sk-test-json\",\"vendor\":\"openai\",\"auth_mode\":\"api_key\"}]"}`, want: http.StatusCreated,
		},
		{
			name: "helper oauth init", method: http.MethodPost, path: "/admin/v1/credentials/oauth-init",
			body: `{"tenant_id":1,"provider_account_id":101,"vendor":"openai","auth_mode":"chatgpt_oauth","oauth_client":{"client_id":"client-id","auth_url":"https://auth.example.test/oauth","redirect_uri":"https://huakai.example.test/callback"}}`, want: http.StatusCreated,
		},
		{
			name: "helper oauth callback error path", method: http.MethodGet,
			path: "/admin/v1/credentials/oauth-callback?flow_id=" + helperCallbackFlow.ID + "&state=" + helperCallbackFlow.State + "&code=auth-code",
			want: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := fx.do(t, tc.method, tc.path, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestAdminCredentialAcquisitionRequiresAdminAuth(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAuthStub{err: admin.ErrAdminUnauthorized})
	rec := fx.do(t, http.MethodPost, "/v1/admin/pool-accounts/101/credential-acquisitions",
		`{"tenant_id":1,"vendor":"openai","auth_mode":"api_key","flow_kind":"paste"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminCredentialAcquisitionRejectsPathAccountMismatch(t *testing.T) {
	fx := newCredentialAcqHTTPFixture(t, adminPoolAdmin())
	flow := fx.seedPasteFlow(t, 202)
	rec := fx.do(t, http.MethodGet, "/v1/admin/pool-accounts/101/credential-acquisitions/"+flow.ID, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 body=%s", rec.Code, rec.Body.String())
	}
}

type credentialAcqHTTPFixture struct {
	handler http.Handler
	store   *credentialacq.PostgresSessionStore
	db      *credentialAcqSessionDB
}

type seededCredentialAcqFlow struct {
	ID    string
	State string
}

func newCredentialAcqHTTPFixture(t *testing.T, auth AdminCredentialAuth) *credentialAcqHTTPFixture {
	t.Helper()
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db := newCredentialAcqSessionDB(now)
	store := credentialacq.NewPostgresSessionStoreWithKeys(db, keys).WithNow(func() time.Time { return now })
	deps := AdminCredentialAcquisitionDeps{
		Auth: auth, Sessions: store,
		Credentials:     &credentialAcqCreatorStub{},
		CredentialAudit: &credentialAcqAuditStub{},
		AuditStore:      &adminPoolStoreStub{},
	}
	r := chi.NewRouter()
	r.Route("/v1/admin/pool-accounts", func(r chi.Router) {
		MountAdminCredentialAcquisitionRoutes(r, deps)
	})
	r.Route("/admin/v1/credentials", func(r chi.Router) {
		MountAdminCredentialAcquisitionHelperRoutes(r, deps)
	})
	return &credentialAcqHTTPFixture{handler: r, store: store, db: db}
}

func (fx *credentialAcqHTTPFixture) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	fx.handler.ServeHTTP(rec, req)
	return rec
}

func (fx *credentialAcqHTTPFixture) seedPasteFlow(t *testing.T, providerAccountID int64) seededCredentialAcqFlow {
	t.Helper()
	session, err := fx.store.CreateFromStart(context.Background(), credentialacq.StartInput{
		TenantID: 1, ProviderAccountID: providerAccountID,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey, Kind: credentialacq.FlowKindPaste,
		ActorID: "11", ActorRole: "platform_admin",
		ClientIdentitySource: credentialacq.ClientSourceNone,
	})
	if err != nil {
		t.Fatalf("seed paste flow: %v", err)
	}
	return seededCredentialAcqFlow{ID: session.ID}
}

func (fx *credentialAcqHTTPFixture) seedOAuthFlow(t *testing.T, providerAccountID int64) seededCredentialAcqFlow {
	t.Helper()
	result, err := credentialacq.StartOAuthFlow(context.Background(), fx.store, credentialacq.StartInput{
		TenantID: 1, ProviderAccountID: providerAccountID,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		ActorID: "11", ActorRole: "platform_admin",
	}, credentialacq.OAuthClientConfig{
		ClientID: "client-id", AuthURL: "https://auth.example.test/oauth", RedirectURI: "https://huakai.example.test/callback",
	})
	if err != nil {
		t.Fatalf("seed oauth flow: %v", err)
	}
	return seededCredentialAcqFlow{ID: result.Session.ID, State: result.State}
}

type credentialAcqCreatorStub struct {
	next int64
}

func (s *credentialAcqCreatorStub) Create(_ context.Context, in credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error) {
	s.next++
	return credentialstore.CredentialMetadata{
		ID: s.next, TenantID: in.TenantID, ProviderAccountID: in.ProviderAccountID,
		Vendor: credentialstore.Normalize(in.Vendor), AuthMode: credentialstore.Normalize(in.AuthMode),
		State: credentialstore.StateActive, Version: 1,
	}, nil
}

type credentialAcqAuditStub struct {
	events []credentialstore.AuditEvent
}

func (s *credentialAcqAuditStub) InsertAuditEvent(_ context.Context, e credentialstore.AuditEvent) error {
	s.events = append(s.events, e)
	return nil
}

type credentialAcqSessionDB struct {
	mu   sync.Mutex
	now  time.Time
	rows map[string]credentialacq.Session
}

func newCredentialAcqSessionDB(now time.Time) *credentialAcqSessionDB {
	return &credentialAcqSessionDB{now: now, rows: map[string]credentialacq.Session{}}
}

func (db *credentialAcqSessionDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (db *credentialAcqSessionDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("credential acquisition test db: Query not implemented")
}

func (db *credentialAcqSessionDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	db.mu.Lock()
	defer db.mu.Unlock()
	switch {
	case strings.Contains(sql, "INSERT INTO credential_acquisition_flow_sessions"):
		row := credentialacq.Session{
			ID: argString(args[0]), TenantID: argInt64(args[1]), ProviderAccountID: argInt64(args[2]),
			Vendor: argString(args[3]), AuthMode: argString(args[4]), Kind: credentialacq.FlowKind(argString(args[5])), Status: credentialacq.FlowStatus(argString(args[6])),
			ActorID: argString(args[7]), ActorRole: argString(args[8]),
			StateHash: argBytes(args[9]), NonceHash: argBytes(args[10]), EncryptedPKCEVerifier: argBytes(args[11]),
			ClientIdentitySource: argString(args[12]), RedirectURI: argString(args[13]),
			LongLivedRequested: argBool(args[16]), IdempotencyKeyHash: argBytes(args[17]),
			ExpiresAt: argTime(args[18]), CreatedAt: db.now, UpdatedAt: db.now,
		}
		_ = json.Unmarshal(argBytes(args[14]), &row.RequestedScopes)
		_ = json.Unmarshal(argBytes(args[15]), &row.RedactedContext)
		db.rows[row.ID] = row
		return credentialAcqRow{session: row}
	case strings.Contains(sql, "FROM credential_acquisition_flow_sessions") && strings.Contains(sql, "WHERE id = $1::uuid"):
		row, ok := db.rows[argString(args[0])]
		if !ok {
			return credentialAcqRow{err: pgx.ErrNoRows}
		}
		return credentialAcqRow{session: row}
	case strings.Contains(sql, "SET status = $2"):
		row, ok := db.rows[argString(args[0])]
		if !ok {
			return credentialAcqRow{err: pgx.ErrNoRows}
		}
		row.Status = credentialacq.FlowStatus(argString(args[1]))
		row.ErrorClass = argString(args[2])
		row.ErrorMessageRedacted = argString(args[3])
		row.UpdatedAt = db.now
		db.rows[row.ID] = row
		return credentialAcqRow{session: row}
	case strings.Contains(sql, "SET status = 'cancelled'"):
		row, ok := db.rows[argString(args[0])]
		if !ok || row.Status == credentialacq.StatusFinalized || row.Status == credentialacq.StatusCancelled || row.Status == credentialacq.StatusExpired {
			return credentialAcqRow{err: pgx.ErrNoRows}
		}
		row.Status = credentialacq.StatusCancelled
		row.CancelledAt = db.now
		row.UpdatedAt = db.now
		db.rows[row.ID] = row
		return credentialAcqRow{session: row}
	case strings.Contains(sql, "SET consumed_at = NOW()"):
		row, ok := db.rows[argString(args[0])]
		if !ok || !row.ConsumedAt.IsZero() || row.Status == credentialacq.StatusFinalized || row.Status == credentialacq.StatusCancelled || row.Status == credentialacq.StatusExpired || !row.ExpiresAt.After(db.now) {
			return credentialAcqRow{err: pgx.ErrNoRows}
		}
		row.ConsumedAt = db.now
		row.UpdatedAt = db.now
		db.rows[row.ID] = row
		return credentialAcqRow{session: row}
	case strings.Contains(sql, "SET status = 'finalized'"):
		row, ok := db.rows[argString(args[0])]
		if !ok {
			return credentialAcqRow{err: pgx.ErrNoRows}
		}
		row.Status = credentialacq.StatusFinalized
		row.ResultAccountCredentialID = argInt64(args[1])
		if row.ConsumedAt.IsZero() {
			row.ConsumedAt = db.now
		}
		row.UpdatedAt = db.now
		db.rows[row.ID] = row
		return credentialAcqRow{session: row}
	default:
		return credentialAcqRow{err: errors.New("credential acquisition test db: unhandled query")}
	}
}

type credentialAcqRow struct {
	session credentialacq.Session
	err     error
}

func (r credentialAcqRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanCredentialAcqSession(dest, r.session)
}

func scanCredentialAcqSession(dest []any, row credentialacq.Session) error {
	scopes, _ := json.Marshal(row.RequestedScopes)
	redacted, _ := json.Marshal(row.RedactedContext)
	values := []any{
		row.ID, row.TenantID, row.ProviderAccountID, row.Vendor, row.AuthMode, row.Kind, row.Status,
		row.ActorID, row.ActorRole, row.StateHash, row.NonceHash, row.EncryptedPKCEVerifier,
		row.ClientIdentitySource, pgText(row.RedirectURI), scopes, redacted,
		row.LongLivedRequested, row.IdempotencyKeyHash, pgInt8(row.ResultAccountCredentialID),
		pgText(row.ErrorClass), pgText(row.ErrorMessageRedacted), row.ExpiresAt, pgTime(row.ConsumedAt), pgTime(row.CancelledAt),
		row.CreatedAt, row.UpdatedAt,
	}
	for i := range dest {
		assignCredentialAcqScan(dest[i], values[i])
	}
	return nil
}

func assignCredentialAcqScan(dest any, value any) {
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

func pgText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: strings.TrimSpace(value) != ""}
}

func pgInt8(value int64) pgtype.Int8 {
	return pgtype.Int8{Int64: value, Valid: value != 0}
}

func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}

func argString(value any) string {
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

func argInt64(value any) int64 {
	if v, ok := value.(int64); ok {
		return v
	}
	return 0
}

func argBool(value any) bool {
	if v, ok := value.(bool); ok {
		return v
	}
	return false
}

func argBytes(value any) []byte {
	if v, ok := value.([]byte); ok {
		return append([]byte(nil), v...)
	}
	return nil
}

func argTime(value any) time.Time {
	if v, ok := value.(time.Time); ok {
		return v
	}
	return time.Time{}
}
