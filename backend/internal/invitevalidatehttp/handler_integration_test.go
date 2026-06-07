//go:build integration_pg

package invitevalidatehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPGValidateInviteNonConsuming(t *testing.T) {
	ctx := context.Background()
	pool := openInviteValidatePool(t, ctx)
	t.Cleanup(pool.Close)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID := seedInviteValidateTenant(t, ctx, pool, "validate-invite-"+suffix)
	t.Cleanup(func() { cleanupInviteValidateTenant(t, ctx, pool, tenantID) })

	rawCode := "hki_validate_" + suffix
	codeHash := userauth.HashInviteCode(rawCode)
	if _, err := pool.Exec(ctx, `
INSERT INTO invite_codes (code, tenant_id, max_uses, used_count, valid_until, status)
VALUES ($1, $2, 1, 0, $3, 'active')
`, codeHash, tenantID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("insert invite_code: %v", err)
	}

	handler := NewHandler(Deps{
		Store:    userauth.NewPostgresStore(pool),
		Settings: platformsettings.NewService(platformsettings.NewMemoryStore(), nil),
	})

	for i := 0; i < 2; i++ {
		rec := postInviteValidateJSON(t, handler, tenantID, rawCode)
		if rec.Code != http.StatusOK {
			t.Fatalf("validate call %d status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
		var body struct {
			Valid  bool   `json:"valid"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode validate call %d: %v", i+1, err)
		}
		if !body.Valid || body.Reason != string(userauth.InviteCodeStatusValid) {
			t.Fatalf("validate call %d body=%+v want valid=true reason=valid", i+1, body)
		}
		if used := readInviteValidateUsedCount(t, ctx, pool, tenantID, codeHash); used != 0 {
			t.Fatalf("validate call %d used_count=%d want 0; MUTATION: read-only validation consumed invite", i+1, used)
		}
	}
}

func postInviteValidateJSON(t *testing.T, handler http.Handler, tenantID int64, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"tenant_id": tenantID, "invite_code": code})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/validate-invitation-code", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func openInviteValidatePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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

func seedInviteValidateTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	return id
}

func readInviteValidateUsedCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, codeHash string) int {
	t.Helper()
	var used int
	if err := pool.QueryRow(ctx, `SELECT used_count FROM invite_codes WHERE tenant_id=$1 AND code=$2`, tenantID, codeHash).Scan(&used); err != nil {
		t.Fatalf("read used_count: %v", err)
	}
	return used
}

func cleanupInviteValidateTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM invite_codes WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup invite_codes: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID); err != nil {
		t.Fatalf("cleanup tenant: %v", err)
	}
}
