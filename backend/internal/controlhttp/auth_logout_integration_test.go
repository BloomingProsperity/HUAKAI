//go:build integration_pg

package controlhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestPGAuthLogoutRevokesOnlyCurrentFamily(t *testing.T) {
	ctx := context.Background()
	pool := openControlAuthPool(t, ctx)
	t.Cleanup(pool.Close)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedControlAuthTenant(t, ctx, pool, "logout-"+suffix)
	userID := seedControlAuthUser(t, ctx, pool, tenantID, "logout-user-"+suffix)
	t.Cleanup(func() { cleanupControlAuthTenant(t, context.Background(), pool, tenantID) })

	sessionSvc := usersession.NewService(usersession.NewPostgresStore(pool))
	sessionSvc.SigningKey = bytes.Repeat([]byte{9}, 32)
	sessionSvc.Now = func() time.Time { return time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC) }
	current, err := sessionSvc.Create(ctx, usersession.CreateInput{
		TenantID: tenantID, UserID: userID, IP: "192.0.2.1", UserAgent: "integration-pg/current",
	})
	if err != nil {
		t.Fatalf("create current session: %v", err)
	}
	other, err := sessionSvc.Create(ctx, usersession.CreateInput{
		TenantID: tenantID, UserID: userID, IP: "192.0.2.1", UserAgent: "integration-pg/other",
	})
	if err != nil {
		t.Fatalf("create other session: %v", err)
	}

	router := chi.NewRouter()
	router.Route("/v1/auth", func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessionSvc, nil))
		MountAuthMeRoutes(r, AuthMeDeps{Sessions: sessionSvc})
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+current.SessionToken)
	req.Header.Set("User-Agent", "integration-pg/current")
	req.RemoteAddr = "192.0.2.1:12345"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("logout status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Revoked int64 `json:"revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode logout body: %v body=%s", err, rec.Body.String())
	}
	if body.Revoked != 1 {
		t.Fatalf("revoked=%d want 1", body.Revoked)
	}
	if _, err := sessionSvc.Validate(ctx, current.SessionToken, "192.0.2.1", "integration-pg/current"); !errors.Is(err, usersession.ErrFamilyRevoked) {
		t.Fatalf("current session validate err=%v want ErrFamilyRevoked; MUTATION: skipping Revoke or revoking a different family leaves this valid", err)
	}
	if _, err := sessionSvc.Refresh(ctx, usersession.RefreshInput{RefreshToken: current.RefreshToken, IP: "192.0.2.1", UserAgent: "integration-pg/current"}); !errors.Is(err, usersession.ErrFamilyRevoked) {
		t.Fatalf("current refresh err=%v want ErrFamilyRevoked", err)
	}
	if _, err := sessionSvc.Validate(ctx, other.SessionToken, "192.0.2.1", "integration-pg/other"); err != nil {
		t.Fatalf("other family was revoked; logout must only revoke caller family: %v", err)
	}
}

func openControlAuthPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping postgres: %v", err)
	}
	return pool
}

func seedControlAuthTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	return id
}

func seedControlAuthUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, displayName string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name, status) VALUES ($1, $2, 'active') RETURNING id`, tenantID, displayName).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func cleanupControlAuthTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM session_tokens WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup session_tokens: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup refresh_tokens: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM session_families WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup session_families: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup users: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup tenant: %v", err)
	}
}
