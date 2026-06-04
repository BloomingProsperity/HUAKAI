package adminhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestAT_ADMIN_001_IssueHandlerAuthRoleTenantValidationAndHappyPath(t *testing.T) {
	t.Run("missing auth returns 401", func(t *testing.T) {
		issuer := &apiKeyIssuerStub{}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:   apiKeyAuthStub{err: admin.ErrAdminUnauthorized},
			Issuer: issuer,
		}, http.MethodPost, "/admin/v1/api-keys/", `{"tenant_id":7,"user_id":3,"name":"ops"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusUnauthorized)
		if issuer.called {
			t.Fatalf("unauthorized request touched issuer: %+v", issuer.got)
		}
	})

	t.Run("tenant operator cross tenant returns 403", func(t *testing.T) {
		issuer := &apiKeyIssuerStub{enforceScope: true}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:   apiKeyAuthStub{ident: tenantOperator(7)},
			Issuer: issuer,
		}, http.MethodPost, "/admin/v1/api-keys/", `{"tenant_id":8,"user_id":3,"name":"ops"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusForbidden)
		if !issuer.called || issuer.got.TenantID != 8 {
			t.Fatalf("issuer did not receive the requested tenant scope: called=%v got=%+v", issuer.called, issuer.got)
		}
	})

	t.Run("happy path returns created once only bearer", func(t *testing.T) {
		created := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
		issuer := &apiKeyIssuerStub{result: admin.IssueResult{
			APIKeyID:  44,
			Plaintext: "hk_test_plaintext_for_operator",
			KeyPrefix: "hk_test_plaintex",
			Status:    "active",
			CreatedAt: created,
		}}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:   apiKeyAuthStub{ident: platformAdmin()},
			Issuer: issuer,
		}, http.MethodPost, "/admin/v1/api-keys/",
			`{"tenant_id":7,"user_id":3,"name":"ops","environment":"test","reason":"rotation"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusCreated)
		if issuer.got.Environment != admin.EnvTest || issuer.got.Reason != "rotation" {
			t.Fatalf("issue request mismatch: %+v", issuer.got)
		}
		if got := rec.Header().Get("X-Huakai-Key-Display"); got != "once-only" {
			t.Fatalf("X-Huakai-Key-Display=%q want once-only", got)
		}
		var body issueResponseBody
		decodeAdminAPIKeyBody(t, rec, &body)
		if body.ID != 44 || body.TenantID != 7 || body.UserID != 3 || body.PlaintextBearer == "" {
			t.Fatalf("issue response mismatch: %+v", body)
		}
	})

	t.Run("validation error returns 400 before issuer", func(t *testing.T) {
		issuer := &apiKeyIssuerStub{}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:   apiKeyAuthStub{ident: platformAdmin()},
			Issuer: issuer,
		}, http.MethodPost, "/admin/v1/api-keys/", `{"tenant_id":7,"user_id":3,"name":"ops","environment":"dev"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusBadRequest)
		if issuer.called {
			t.Fatalf("invalid request touched issuer: %+v", issuer.got)
		}
	})
}

func TestAT_ADMIN_001_ListHandlerAuthScopeValidationAndHappyPath(t *testing.T) {
	t.Run("missing auth returns 401", func(t *testing.T) {
		queries := &apiKeyQueriesStub{}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:    apiKeyAuthStub{err: admin.ErrAdminUnauthorized},
			Queries: queries,
		}, http.MethodGet, "/admin/v1/api-keys/?tenant_id=7", nil)
		assertAdminAPIKeyStatus(t, rec, http.StatusUnauthorized)
		if queries.existsCalls != 0 || queries.listCalls != 0 || queries.auditCalls != 0 {
			t.Fatalf("unauthorized request touched queries: %+v", queries)
		}
	})

	t.Run("tenant operator cross tenant returns 403 without query", func(t *testing.T) {
		queries := &apiKeyQueriesStub{exists: true}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Queries: queries,
		}, http.MethodGet, "/admin/v1/api-keys/?tenant_id=8", nil)
		assertAdminAPIKeyStatus(t, rec, http.StatusForbidden)
		if queries.existsCalls != 0 || queries.listCalls != 0 || queries.auditCalls != 0 {
			t.Fatalf("cross-tenant list touched queries: %+v", queries)
		}
	})

	t.Run("happy path returns tenant scoped items and audits read", func(t *testing.T) {
		queries := &apiKeyQueriesStub{
			exists: true,
			rows: []admindb.AdminListAPIKeysForTenantRow{{
				ID:        101,
				TenantID:  7,
				UserID:    3,
				Name:      "ops",
				KeyPrefix: "hk_live_prefix1",
				Status:    "active",
				CreatedAt: pgTimestamp(time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)),
			}},
		}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Queries: queries,
		}, http.MethodGet, "/admin/v1/api-keys/?tenant_id=7&limit=1&offset=2", nil)
		assertAdminAPIKeyStatus(t, rec, http.StatusOK)
		if queries.listArg.TenantID != 7 || queries.listArg.PageLimit != 1 || queries.listArg.PageOffset != 2 {
			t.Fatalf("list params mismatch: %+v", queries.listArg)
		}
		if queries.auditCalls != 1 || queries.auditArg.TenantID == nil || *queries.auditArg.TenantID != 7 {
			t.Fatalf("list audit mismatch: calls=%d arg=%+v", queries.auditCalls, queries.auditArg)
		}
		var body struct {
			Items []listItemBody `json:"items"`
			Limit int32          `json:"limit"`
		}
		decodeAdminAPIKeyBody(t, rec, &body)
		if len(body.Items) != 1 || body.Items[0].TenantID != 7 || body.Limit != 1 {
			t.Fatalf("list response mismatch: %+v", body)
		}
	})

	t.Run("validation error returns 400 before query", func(t *testing.T) {
		queries := &apiKeyQueriesStub{exists: true}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:    apiKeyAuthStub{ident: platformAdmin()},
			Queries: queries,
		}, http.MethodGet, "/admin/v1/api-keys/?tenant_id=7&limit=0", nil)
		assertAdminAPIKeyStatus(t, rec, http.StatusBadRequest)
		if queries.listCalls != 0 || queries.auditCalls != 0 {
			t.Fatalf("invalid list request touched list/audit queries: %+v", queries)
		}
	})
}

func TestAT_ADMIN_001_RevokeHandlerAuthRoleTenantValidationAndHappyPath(t *testing.T) {
	t.Run("missing auth returns 401", func(t *testing.T) {
		revoker := &apiKeyRevokerStub{}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:    apiKeyAuthStub{err: admin.ErrAdminUnauthorized},
			Revoker: revoker,
		}, http.MethodPost, "/admin/v1/api-keys/5/revoke", `{"tenant_id":7,"reason":"rotation"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusUnauthorized)
		if revoker.called {
			t.Fatalf("unauthorized request touched revoker: %+v", revoker.got)
		}
	})

	t.Run("tenant operator cross tenant returns 403", func(t *testing.T) {
		revoker := &apiKeyRevokerStub{enforceScope: true}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
			Revoker: revoker,
		}, http.MethodPost, "/admin/v1/api-keys/5/revoke", `{"tenant_id":8,"reason":"rotation"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusForbidden)
		if !revoker.called || revoker.got.TenantID != 8 || revoker.got.APIKeyID != 5 {
			t.Fatalf("revoker scope mismatch: called=%v got=%+v", revoker.called, revoker.got)
		}
	})

	t.Run("happy path returns 200", func(t *testing.T) {
		revoker := &apiKeyRevokerStub{result: admin.RevokeResult{APIKeyID: 5}}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:    apiKeyAuthStub{ident: platformAdmin()},
			Revoker: revoker,
		}, http.MethodPost, "/admin/v1/api-keys/5/revoke", `{"tenant_id":7,"reason":"rotation"}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusOK)
		if revoker.got.APIKeyID != 5 || revoker.got.TenantID != 7 || revoker.got.Reason != "rotation" {
			t.Fatalf("revoke request mismatch: %+v", revoker.got)
		}
		var body revokeResponseBody
		decodeAdminAPIKeyBody(t, rec, &body)
		if body.ID != 5 || body.AlreadyRevoked {
			t.Fatalf("revoke response mismatch: %+v", body)
		}
	})

	t.Run("validation error returns 400 before revoker", func(t *testing.T) {
		revoker := &apiKeyRevokerStub{}
		rec := invokeAdminAPIKeys(t, AdminAPIKeysDeps{
			Auth:    apiKeyAuthStub{ident: platformAdmin()},
			Revoker: revoker,
		}, http.MethodPost, "/admin/v1/api-keys/not-a-number/revoke", `{"tenant_id":7}`)
		assertAdminAPIKeyStatus(t, rec, http.StatusBadRequest)
		if revoker.called {
			t.Fatalf("invalid revoke request touched revoker: %+v", revoker.got)
		}
	})
}

type apiKeyAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s apiKeyAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

type apiKeyIssuerStub struct {
	result       admin.IssueResult
	err          error
	enforceScope bool
	called       bool
	got          admin.IssueRequest
}

func (s *apiKeyIssuerStub) Issue(_ context.Context, req admin.IssueRequest) (admin.IssueResult, error) {
	s.called = true
	s.got = req
	if s.enforceScope {
		if err := req.Caller.CanIssueForTenant(req.TenantID); err != nil {
			return admin.IssueResult{}, err
		}
	}
	if s.err != nil {
		return admin.IssueResult{}, s.err
	}
	if s.result.APIKeyID == 0 {
		s.result = admin.IssueResult{
			APIKeyID:  1,
			Plaintext: "hk_live_plaintext_for_operator",
			KeyPrefix: "hk_live_plaintex",
			Status:    "active",
			CreatedAt: time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
		}
	}
	return s.result, nil
}

type apiKeyRevokerStub struct {
	result       admin.RevokeResult
	err          error
	enforceScope bool
	called       bool
	got          admin.RevokeRequest
}

func (s *apiKeyRevokerStub) Revoke(_ context.Context, req admin.RevokeRequest) (admin.RevokeResult, error) {
	s.called = true
	s.got = req
	if s.enforceScope {
		if err := req.Caller.CanIssueForTenant(req.TenantID); err != nil {
			return admin.RevokeResult{}, err
		}
	}
	if s.err != nil {
		return admin.RevokeResult{}, s.err
	}
	if s.result.APIKeyID == 0 {
		s.result.APIKeyID = req.APIKeyID
	}
	return s.result, nil
}

type apiKeyQueriesStub struct {
	exists      bool
	existsErr   error
	rows        []admindb.AdminListAPIKeysForTenantRow
	listErr     error
	auditErr    error
	existsID    int64
	listArg     admindb.AdminListAPIKeysForTenantParams
	auditArg    admindb.InsertAdminAuditEventParams
	existsCalls int
	listCalls   int
	auditCalls  int
}

func (s *apiKeyQueriesStub) AdminCheckTenantExists(_ context.Context, tenantID int64) (bool, error) {
	s.existsCalls++
	s.existsID = tenantID
	if s.existsErr != nil {
		return false, s.existsErr
	}
	return s.exists, nil
}

func (s *apiKeyQueriesStub) AdminListAPIKeysForTenant(_ context.Context, arg admindb.AdminListAPIKeysForTenantParams) ([]admindb.AdminListAPIKeysForTenantRow, error) {
	s.listCalls++
	s.listArg = arg
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.rows, nil
}

func (s *apiKeyQueriesStub) InsertAdminAuditEvent(_ context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	s.auditCalls++
	s.auditArg = arg
	if s.auditErr != nil {
		return admindb.InsertAdminAuditEventRow{}, s.auditErr
	}
	return admindb.InsertAdminAuditEventRow{ID: int64(s.auditCalls)}, nil
}

func invokeAdminAPIKeys(t *testing.T, deps AdminAPIKeysDeps, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/api-keys", func(r chi.Router) {
		MountAPIKeyRoutes(r, deps)
	})
	req := httptest.NewRequest(method, target, adminAPIKeyReader(t, body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func adminAPIKeyReader(t *testing.T, body any) *bytes.Reader {
	t.Helper()
	switch v := body.(type) {
	case nil:
		return bytes.NewReader(nil)
	case string:
		return bytes.NewReader([]byte(v))
	case []byte:
		return bytes.NewReader(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		return bytes.NewReader(raw)
	}
}

func decodeAdminAPIKeyBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body: %v body=%s", err, strings.TrimSpace(rec.Body.String()))
	}
}

func assertAdminAPIKeyStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, strings.TrimSpace(rec.Body.String()))
	}
}

func platformAdmin() admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}
}

func tenantOperator(tenantID int64) admin.AdminIdentity {
	return admin.AdminIdentity{TokenID: 12, Role: admin.RoleTenantOperator, ScopeTenantID: tenantID}
}

func pgTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// TestWriteErrorProducesValidJSONForControlChars guards for the admin API-key error
// writer: its default branch echoes err.Error() into message, which can carry control bytes.
// The body must stay RFC-valid JSON. Mutation check: restore the fmt %q hand-formatter and
// json.Valid goes false on the \x01 byte (plus the message no longer round-trips) → red.
func TestWriteErrorProducesValidJSONForControlChars(t *testing.T) {
	rec := httptest.NewRecorder()
	msg := "admin backend failure: \x01\x1f \"q\"\ntab\there"
	writeError(rec, http.StatusInternalServerError, "admin_unknown_error", msg)

	body := rec.Body.Bytes()
	if !json.Valid(body) {
		t.Fatalf("admin error body must be valid JSON even with control chars; got %q", body)
	}
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal admin error body: %v; body=%q", err, body)
	}
	if parsed.Error.Code != "admin_unknown_error" {
		t.Fatalf("code must round-trip; got %q", parsed.Error.Code)
	}
	if parsed.Error.Message != msg {
		t.Fatalf("message must round-trip exactly; want %q got %q", msg, parsed.Error.Message)
	}
}
