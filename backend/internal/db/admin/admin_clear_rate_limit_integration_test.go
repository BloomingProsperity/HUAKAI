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

// TestClearProviderAccountRateLimit_ClearsEveryCascadeColumn 种入一个把
// 每一个冷却级联列都设成各不相同的"非清空"值的池账号,运行
// ClearProviderAccountRateLimit,然后重新查询并断言每一列都被重置。
// 每个字段初始状态都不同于"已清空",所以只要从 UPDATE SET 列表里漏掉任意
// 一列,该列就仍非清空,测试变红 —— 这就是变异守卫。某个恰好以 NULL 起步
// 的字段会成为无区分度的陷阱,因此所有字段一开始都填了值。
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

// TestAdminAuditClearRateLimitActionWhitelisted 证明迁移 0141 确实把两个运维
// 审计 action 加进了白名单,同时保持 CHECK 仍然生效。两个被允许的 insert 必须
// 成功;一个伪造 action 仍必须在同一约束上触发 CHECK 23514。伪造子断言是
// 证明该测试具备区分度(而非"所有 action 都通过")的基线。这正是本可以抓住
// 原始潜伏 bug 的测试。
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

	for _, action := range []string{"clear_provider_account_rate_limit", "recover_provider_account_state", "update_provider_account"} {
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

// TestRecoverProviderAccountState_ResetsHealthAndCascade 种入一个终态 revoked
// (health_state_until IS NULL)且各冷却级联列都非清空的账号,运行 RecoverProviderAccountState,
// 断言 health_state 复位 healthy、health_state_until 清空,且级联列全部重置。核心区分度:
// health_state 起于 'revoked' —— 若 UPDATE 漏掉 health_state 复位(退化成 clear-rate-limit),
// 它仍是 revoked,测试变红。这正是修「终态 revoked 无恢复路径」的守卫。
func TestRecoverProviderAccountState_ResetsHealthAndCascade(t *testing.T) {
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
		 SET health_state = 'revoked',
		     health_state_until = NULL,
		     rate_limited_at = $2,
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
		tenantID, now, now.Add(time.Hour), now.Add(time.Hour), now.Add(time.Hour), now, accountID,
	); err != nil {
		t.Fatalf("seed revoked + benched cooldown columns: %v", err)
	}

	actor := "admin:recover-full"
	row, err := q.RecoverProviderAccountState(ctx, RecoverProviderAccountStateParams{
		ID: accountID, TenantID: tenantID, ActorID: &actor,
	})
	if err != nil {
		t.Fatalf("RecoverProviderAccountState: %v", err)
	}
	if row.ID != accountID {
		t.Fatalf("RETURNING row id=%d want %d", row.ID, accountID)
	}

	var (
		healthState    string
		healthUntil    *time.Time
		rateLimitReset *time.Time
		overloadUntil  *time.Time
		tempUntil      *time.Time
		modelLimits    []byte
		openai403      int32
	)
	if err := pool.QueryRow(ctx,
		`SELECT health_state, health_state_until, rate_limit_reset_at, overload_until,
		        temp_unschedulable_until, model_rate_limits, openai_403_counter
		   FROM provider_accounts WHERE tenant_id = $1 AND id = $2`,
		tenantID, accountID,
	).Scan(&healthState, &healthUntil, &rateLimitReset, &overloadUntil, &tempUntil, &modelLimits, &openai403); err != nil {
		t.Fatalf("re-select recovered row: %v", err)
	}

	if healthState != "healthy" {
		t.Errorf("health_state not reset to healthy: %q (revoked account remained unrecoverable)", healthState)
	}
	if healthUntil != nil {
		t.Errorf("health_state_until not cleared: %v", healthUntil)
	}
	if rateLimitReset != nil || overloadUntil != nil || tempUntil != nil {
		t.Errorf("cooldown columns not cleared: reset=%v overload=%v temp=%v", rateLimitReset, overloadUntil, tempUntil)
	}
	if string(modelLimits) != "{}" {
		t.Errorf("model_rate_limits not reset: %s", string(modelLimits))
	}
	if openai403 != 0 {
		t.Errorf("openai_403_counter not reset: %d", openai403)
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
