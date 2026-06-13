//go:build integration_pg

package admin

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestClearProviderAccountRateLimit_ClearsEveryCascadeColumn seeds a provider
// account with EVERY cooldown-cascade column set to a distinct NON-clear value,
// runs ClearProviderAccountRateLimit, then re-selects and asserts every single
// column is reset. Each field starts in a state that differs from "cleared", so
// dropping any one column from the UPDATE SET list leaves that column non-clear
// and the test goes RED — that is the mutation guard. A field that happened to
// start NULL would be a non-discriminating trap, so all start populated.
func TestClearProviderAccountRateLimit_ClearsEveryCascadeColumn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID, accountID := seedClearRateLimitAccount(t, ctx, pool, suffix)
	t.Cleanup(func() {
		cleanupAdminProviderAccountHealthGraph(t, context.Background(), pool, tenantID)
	})

	now := time.Now().UTC()
	if _, err := pool.Exec(ctx,
		`UPDATE provider_accounts
		 SET rate_limited_at = $2,
		     rate_limit_reset_at = $3,
		     rate_limit_reason = 'rate_limit_5h_exceeded',
		     overload_until = $4,
		     temp_unschedulable_until = $5,
		     temp_unschedulable_reason = 'temp-unsched-y',
		     temp_unschedulable_rule_index = 2,
		     model_rate_limits = '{"gpt-x":1}'::jsonb,
		     openai_403_counter = 3,
		     openai_403_window_start = $6
		 WHERE tenant_id = $1 AND id = $7`,
		tenantID,
		now,
		now.Add(time.Hour),
		now.Add(time.Hour),
		now.Add(time.Hour),
		now,
		accountID,
	); err != nil {
		t.Fatalf("seed benched cooldown columns: %v", err)
	}

	actor := "admin:clear-cascade"
	row, err := q.ClearProviderAccountRateLimit(ctx, ClearProviderAccountRateLimitParams{
		ID: accountID, TenantID: tenantID, ActorID: &actor,
	})
	if err != nil {
		t.Fatalf("ClearProviderAccountRateLimit: %v", err)
	}
	if row.ID != accountID {
		t.Fatalf("RETURNING row id=%d want %d", row.ID, accountID)
	}

	var (
		rateLimitedAt   *time.Time
		rateLimitReset  *time.Time
		rateLimitReason *string
		overloadUntil   *time.Time
		tempUntil       *time.Time
		tempReason      *string
		tempRuleIndex   *int32
		modelRateLimits []byte
		openai403Count  int32
		openai403Start  *time.Time
	)
	if err := pool.QueryRow(ctx,
		`SELECT rate_limited_at, rate_limit_reset_at, rate_limit_reason,
		        overload_until, temp_unschedulable_until, temp_unschedulable_reason,
		        temp_unschedulable_rule_index, model_rate_limits,
		        openai_403_counter, openai_403_window_start
		   FROM provider_accounts WHERE tenant_id = $1 AND id = $2`,
		tenantID, accountID,
	).Scan(
		&rateLimitedAt, &rateLimitReset, &rateLimitReason,
		&overloadUntil, &tempUntil, &tempReason,
		&tempRuleIndex, &modelRateLimits,
		&openai403Count, &openai403Start,
	); err != nil {
		t.Fatalf("re-select cleared row: %v", err)
	}

	if rateLimitedAt != nil {
		t.Errorf("rate_limited_at not cleared: %v", rateLimitedAt)
	}
	if rateLimitReset != nil {
		t.Errorf("rate_limit_reset_at not cleared: %v", rateLimitReset)
	}
	if rateLimitReason != nil {
		t.Errorf("rate_limit_reason not cleared: %v", *rateLimitReason)
	}
	if overloadUntil != nil {
		t.Errorf("overload_until not cleared: %v", overloadUntil)
	}
	if tempUntil != nil {
		t.Errorf("temp_unschedulable_until not cleared: %v", tempUntil)
	}
	if tempReason != nil {
		t.Errorf("temp_unschedulable_reason not cleared: %v", *tempReason)
	}
	if tempRuleIndex != nil {
		t.Errorf("temp_unschedulable_rule_index not cleared: %v", *tempRuleIndex)
	}
	if string(modelRateLimits) != "{}" {
		t.Errorf("model_rate_limits not reset to empty jsonb: %s", string(modelRateLimits))
	}
	if openai403Count != 0 {
		t.Errorf("openai_403_counter not reset to 0: %d", openai403Count)
	}
	if openai403Start != nil {
		t.Errorf("openai_403_window_start not cleared: %v", openai403Start)
	}
}

// TestAdminAuditClearRateLimitActionWhitelisted proves migration 0141 actually
// whitelisted the two operator audit actions while keeping the CHECK live. The
// two permitted inserts must succeed; a bogus action must still raise CHECK
// 23514 on the same constraint. The bogus sub-assert is the baseline that proves
// the test is discriminating (not "all actions pass"). This is the test that
// would have caught the original latent bug.
func TestAdminAuditClearRateLimitActionWhitelisted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	actorID := "admin-audit-clear-rate-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	targetID := int64(77)
	requestID := actorID + "-request"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_audit_events WHERE actor_id = $1`, actorID)
	})

	for _, action := range []string{"clear_provider_account_rate_limit", "update_provider_account"} {
		if _, err := q.InsertAdminAuditEvent(ctx, InsertAdminAuditEventParams{
			ActorID:    actorID,
			ActorRole:  "platform_admin",
			Action:     action,
			TargetType: "provider_account",
			TargetID:   &targetID,
			RequestID:  &requestID,
			Payload:    []byte(`{"source":"integration_pg"}`),
		}); err != nil {
			t.Fatalf("whitelisted action %q must insert without error: %v", action, err)
		}
	}

	_, err := q.InsertAdminAuditEvent(ctx, InsertAdminAuditEventParams{
		ActorID:    actorID,
		ActorRole:  "platform_admin",
		Action:     "bogus_clear_action",
		TargetType: "provider_account",
		TargetID:   &targetID,
		RequestID:  &requestID,
		Payload:    []byte(`{"source":"integration_pg"}`),
	})
	if err == nil {
		t.Fatalf("bogus action was accepted; CHECK constraint not live")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("bogus action returned non-Postgres error: %T %v", err, err)
	}
	if pgErr.Code != "23514" || pgErr.ConstraintName != "admin_audit_events_action_check" {
		t.Fatalf("bogus action error code=%s constraint=%s want CHECK admin_audit_events_action_check",
			pgErr.Code, pgErr.ConstraintName)
	}
}

func seedClearRateLimitAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (tenantID, accountID int64) {
	t.Helper()
	var providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-clear-rl-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, 'Admin ClearRL Provider', 'openai_chat') RETURNING id`,
		tenantID, "admin-clear-rl-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "admin-clear-rl-pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("insert pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "admin-clear-rl-channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type, enabled, health_state
		) VALUES ($1, $2, $3, $4, 'api_key', true, 'healthy') RETURNING id`,
		tenantID, providerID, channelID, "admin-clear-rl-account-"+suffix,
	).Scan(&accountID); err != nil {
		t.Fatalf("insert provider account: %v", err)
	}
	return tenantID, accountID
}
