//go:build integration_pg

package sunoclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
)

func TestSunoSubmitFetchRoundTrip(t *testing.T) {
	// MUTATION: fetch looks up a different task id; the returned task id below
	// will not match the submitted id.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openSunoPool(t, ctx)
	seed := seedSunoUser(t, ctx, pool, "roundtrip")
	svc := newSunoIntegrationService(pool)
	mux := mountSunoIntegrationRouter(svc, seed)

	submitRec := httptest.NewRecorder()
	submitReq := httptest.NewRequest(http.MethodPost, "/suno/submit",
		strings.NewReader(`{"prompt":"round trip","mv":"chirp-v4","title":"Round Trip"}`))
	submitReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(submitRec, submitReq)
	if submitRec.Code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s want 202", submitRec.Code, submitRec.Body.String())
	}
	var submit struct {
		TaskID int64 `json:"task_id"`
	}
	if err := json.Unmarshal(submitRec.Body.Bytes(), &submit); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	if submit.TaskID <= 0 {
		t.Fatalf("task_id=%d want positive", submit.TaskID)
	}

	fetchRec := httptest.NewRecorder()
	mux.ServeHTTP(fetchRec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/suno/fetch/%d", submit.TaskID), nil))
	if fetchRec.Code != http.StatusOK {
		t.Fatalf("fetch status=%d body=%s want 200", fetchRec.Code, fetchRec.Body.String())
	}
	var fetched mediatask.Task
	if err := json.Unmarshal(fetchRec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode fetch: %v", err)
	}
	if fetched.ID != submit.TaskID || fetched.TaskType != "suno_generate" || fetched.Provider != "suno" {
		t.Fatalf("fetched task id/type/provider=%d/%q/%q", fetched.ID, fetched.TaskType, fetched.Provider)
	}
}

func mountSunoIntegrationRouter(svc Service, seed sunoSeed) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ident := sessionauth.SessionIdentity{TenantID: seed.tenantID, UserID: seed.userID}
			next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), ident)))
		})
	})
	MountRoutes(r, svc)
	return r
}

type sunoSeed struct {
	tenantID int64
	userID   int64
	apiKeyID int64
}

func openSunoPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedSunoUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) sunoSeed {
	t.Helper()
	var seed sunoSeed
	name := fmt.Sprintf("suno-%s-%d", suffix, time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`, seed.tenantID, "user-"+suffix).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		seed.tenantID, seed.userID, "key-"+suffix, "$2a$10$suno-placeholder", "hk_test_suno_"+name,
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0)`, seed.tenantID, seed.userID); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM media_tasks WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM balance_holds WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM user_balances WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
	})
	return seed
}

func newSunoIntegrationService(pool *pgxpool.Pool) *mediatask.Service {
	store := mediatask.NewPostgresStore(pool, mediatask.PostgresStoreConfig{
		BillingPolicyVersion: "test-policy",
		RequestClass:         "standard",
	})
	cfg := mediatask.Config{
		Enabled: true, ProviderBaseURL: "http://provider.invalid",
		PollInterval: time.Millisecond, TaskTimeout: time.Minute,
		DefaultEstimatedCents: map[string]int64{
			"suno_generate": 123,
			"suno_custom":   123,
		},
		BillingPolicyVersion: "test-policy", RequestClass: "standard",
	}
	return mediatask.NewService(store, mediatask.StaticConfigSource{Config: cfg}, mediatask.StaticProviderRegistry{
		"suno": mediatask.NewNoopProvider(),
	})
}
