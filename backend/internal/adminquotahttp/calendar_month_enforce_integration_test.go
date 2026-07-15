//go:build integration_pg

package adminquotahttp

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	runtimequota "github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// TestAdminCreateCalendarMonthPolicyEnforcesAcrossMonthBoundaryRealPG 验证管理 API、
// 策略落库、配额解析与自然月换窗是一条真实链路。收回管理面白名单会在创建处变红；
// 把月窗误算成累计窗口会让二月首个请求继续被拒。
func TestAdminCreateCalendarMonthPolicyEnforcesAcrossMonthBoundaryRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openQuotaPool(t, ctx)
	f := newQuotaFixture(t, ctx, pool)
	userID, apiKeyID := seedCalendarMonthIdentity(t, ctx, pool, f)

	item := f.createPolicy(f.tenantA,
		`{"scope_kind":"global","scope_id":"*","metric":"requests","window_kind":"calendar_month","limit_value":"1","mode":"enforce","valid_from":"2026-01-01T00:00:00Z"}`)
	if item.WindowKind != "calendar_month" {
		t.Fatalf("created window_kind=%q want calendar_month", item.WindowKind)
	}
	var storedWindowKind string
	if err := pool.QueryRow(ctx,
		`SELECT window_kind FROM quota_policies WHERE tenant_id=$1 AND id=$2`,
		f.tenantA, item.ID,
	).Scan(&storedWindowKind); err != nil {
		t.Fatalf("read persisted calendar_month policy: %v", err)
	}
	if storedWindowKind != "calendar_month" {
		t.Fatalf("persisted window_kind=%q want calendar_month", storedWindowKind)
	}

	service := runtimequota.NewService(runtimequota.NewPostgresStore(pool))
	reserveAt := func(label string, at time.Time) (runtimequota.ReserveResult, error) {
		claimID, fingerprint := seedCalendarMonthClaim(t, ctx, pool, f.tenantA, userID, apiKeyID, label)
		return service.Reserve(ctx, runtimequota.ReserveRequest{
			TenantID:           f.tenantA,
			ClaimID:            claimID,
			RequestFingerprint: fingerprint,
			Scopes: []runtimequota.Scope{{
				TenantID: f.tenantA,
				Kind:     runtimequota.ScopeGlobal,
				ID:       "*",
			}},
			PredictedCost:  decimal.RequireFromString("0.01"),
			LeaseExpiresAt: at.Add(5 * time.Minute),
			At:             at,
		})
	}

	january := time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC)
	first, err := reserveAt("jan-first", january)
	if err != nil || !first.Allowed {
		t.Fatalf("January first reserve result=%+v err=%v; want allowed", first, err)
	}
	second, err := reserveAt("jan-second", january)
	if !runtimequota.IsDenied(err) || second.Allowed {
		t.Fatalf("January second reserve result=%+v err=%v; want denied at monthly cap", second, err)
	}

	february := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	third, err := reserveAt("feb-first", february)
	if err != nil || !third.Allowed {
		t.Fatalf("February first reserve result=%+v err=%v; want allowed after month reset", third, err)
	}

	rows, err := pool.Query(ctx,
		`SELECT window_start, window_end, reserved_value::text
		   FROM quota_windows
		  WHERE tenant_id=$1 AND policy_id=$2
		  ORDER BY window_start`,
		f.tenantA, item.ID,
	)
	if err != nil {
		t.Fatalf("read calendar_month windows: %v", err)
	}
	defer rows.Close()
	type windowRow struct {
		start    time.Time
		end      time.Time
		reserved string
	}
	var windows []windowRow
	for rows.Next() {
		var row windowRow
		if err := rows.Scan(&row.start, &row.end, &row.reserved); err != nil {
			t.Fatalf("scan calendar_month window: %v", err)
		}
		windows = append(windows, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate calendar_month windows: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("window rows=%d want 2: %+v", len(windows), windows)
	}
	wantStarts := []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	wantEnds := []time.Time{
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	for i, row := range windows {
		if !row.start.Equal(wantStarts[i]) || !row.end.Equal(wantEnds[i]) || row.reserved != "1.00000000" {
			t.Fatalf("window[%d]=%s/%s reserved=%s want %s/%s reserved=1.00000000",
				i, row.start, row.end, row.reserved, wantStarts[i], wantEnds[i])
		}
	}
}

func seedCalendarMonthIdentity(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f *quotaFixture) (int64, int64) {
	t.Helper()
	var userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		f.tenantA, "calendar-month-"+f.suffix,
	).Scan(&userID); err != nil {
		t.Fatalf("seed calendar_month user: %v", err)
	}
	var apiKeyID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		f.tenantA, userID, "calendar-month-key", "$2a$10$calendar-month-placeholder",
		"hk_cm_"+f.suffix[:12],
	).Scan(&apiKeyID); err != nil {
		t.Fatalf("seed calendar_month api key: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM quota_audit_events WHERE tenant_id=$1`, f.tenantA)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM quota_reservations WHERE tenant_id=$1`, f.tenantA)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, f.tenantA)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE tenant_id=$1`, f.tenantA)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1`, f.tenantA)
	})
	return userID, apiKeyID
}

func seedCalendarMonthClaim(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
	userID int64,
	apiKeyID int64,
	label string,
) (int64, string) {
	t.Helper()
	unique := label + "-" + uuid.NewString()
	fingerprint := "calendar-month-fp-" + unique
	var claimID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint,
			api_key_id, user_id, logical_request_id, endpoint_family,
			requested_model, billing_policy_version, request_class,
			predicted_cost, currency_code, lease_expires_at
		) VALUES (
			$1, $2, $3,
			$4, $5, $6, 'chat',
			'gpt-4.1-mini', 'calendar-month-test', 'standard',
			0.01, 'USD', $7
		) RETURNING id`,
		tenantID, "calendar-month-idem-"+unique, fingerprint,
		apiKeyID, userID, "calendar-month-logical-"+unique, time.Now().UTC().Add(10*time.Minute),
	).Scan(&claimID); err != nil {
		t.Fatalf("seed calendar_month claim %s: %v", label, err)
	}
	return claimID, fingerprint
}
