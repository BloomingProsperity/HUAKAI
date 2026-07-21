//go:build integration_pg

package balanceledger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type balanceLedgerFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	suffix  string
	tenantA int64
	userA   int64
	tenantB int64
	userB   int64
}

func openBalanceLedgerIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 PostgreSQL 集成测试")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 40})
	if err != nil {
		t.Fatalf("打开 PostgreSQL：%v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newBalanceLedgerFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *balanceLedgerFixture {
	t.Helper()
	fixture := &balanceLedgerFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	fixture.tenantA, fixture.userA = fixture.seedTenantUser("balance-platform")
	fixture.tenantB, fixture.userB = fixture.seedTenantUser("balance-downstream")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, tenantID := range []int64{fixture.tenantA, fixture.tenantB} {
			if _, err := pool.Exec(cleanupCtx, `DELETE FROM user_balances WHERE tenant_id=$1`, tenantID); err != nil {
				t.Errorf("清理用户余额：%v", err)
			}
			if _, err := pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1`, tenantID); err != nil {
				t.Errorf("清理用户：%v", err)
			}
			if _, err := pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, tenantID); err != nil {
				t.Errorf("清理租户：%v", err)
			}
		}
	})
	return fixture
}

func (f *balanceLedgerFixture) seedTenantUser(label string) (int64, int64) {
	f.t.Helper()
	var tenantID int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, label+"-"+f.suffix).Scan(&tenantID); err != nil {
		f.t.Fatalf("创建测试租户：%v", err)
	}
	var userID int64
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO users (tenant_id, display_name, role) VALUES ($1, $2, 'user') RETURNING id`,
		tenantID, "user-"+label+"-"+f.suffix).Scan(&userID); err != nil {
		f.t.Fatalf("创建测试用户：%v", err)
	}
	return tenantID, userID
}

func TestAdminBalanceLedgerThreeIdentityFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := openBalanceLedgerIntegrationPool(t, ctx)
	f := newBalanceLedgerFixture(t, ctx, pool)
	registerBalanceLedgerCleanup(t, pool, f.tenantA, f.tenantB)
	svc := NewService(NewPostgresStore(pool, f.tenantA))

	grant := balanceAdjustment(f.tenantB, 0, "100", BalanceActorPlatformAdmin, 0, "admin_token:11", "grant-tenant-100")
	granted, err := svc.AdminAdjustBalance(ctx, grant)
	if err != nil {
		t.Fatalf("部署者给下级租户下发额度：%v", err)
	}
	if granted.TargetKind != BalanceTargetTenant || granted.UserID != 0 || !granted.NewBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("租户钱包下发结果错误：%+v", granted)
	}
	assertBalancedLedgerTransaction(t, ctx, pool, f.tenantB, granted.TransactionID, 2)
	assertNoPaymentOrderForBalanceKey(t, ctx, pool, f.tenantB, grant.IdempotencyKey)

	replay, err := svc.AdminAdjustBalance(ctx, grant)
	if err != nil || !replay.Idempotent || replay.TransactionID != granted.TransactionID || !replay.NewBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("同键同事实应返回首次结果：first=%+v replay=%+v err=%v", granted, replay, err)
	}
	conflict := grant
	conflict.Amount = decimal.NewFromInt(101)
	if _, err := svc.AdminAdjustBalance(ctx, conflict); !errors.Is(err, ErrExternalTradeConflict) {
		t.Fatalf("同键不同金额 err=%v，want ErrExternalTradeConflict", err)
	}

	distribute := balanceAdjustment(f.tenantB, f.userB, "40", BalanceActorTenantOperator, f.tenantB, "admin_user:22", "tenant-user-40")
	distributed, err := svc.AdminAdjustBalance(ctx, distribute)
	if err != nil {
		t.Fatalf("租户管理员给本租户用户分发：%v", err)
	}
	if !distributed.NewBalance.Equal(decimal.NewFromInt(40)) {
		t.Fatalf("用户余额=%s，want 40", distributed.NewBalance)
	}
	assertTenantAndUserBalance(t, ctx, pool, f.tenantB, f.userB, "60.00000000", "40.00000000", "0.00000000")
	assertBalancedLedgerTransaction(t, ctx, pool, f.tenantB, distributed.TransactionID, 2)

	reclaim := balanceAdjustment(f.tenantB, f.userB, "-10", BalanceActorTenantOperator, f.tenantB, "admin_user:22", "tenant-user-reclaim-10")
	reclaimed, err := svc.AdminAdjustBalance(ctx, reclaim)
	if err != nil {
		t.Fatalf("租户管理员从本租户用户收回：%v", err)
	}
	if !reclaimed.NewBalance.Equal(decimal.NewFromInt(30)) {
		t.Fatalf("收回后用户余额=%s，want 30", reclaimed.NewBalance)
	}
	assertTenantAndUserBalance(t, ctx, pool, f.tenantB, f.userB, "70.00000000", "30.00000000", "0.00000000")
	history, err := admindb.New(pool).AdminListUserBalanceHistoryForTenant(ctx, admindb.AdminListUserBalanceHistoryForTenantParams{
		TenantID: f.tenantB, UserID: f.userB, PageLimit: 10,
	})
	if err != nil {
		t.Fatalf("读取用户统一余额历史：%v", err)
	}
	if len(history) != 2 || history[0].EventType != balanceOperationTenantUserDebit || history[0].Amount != "-10.00000000" ||
		history[1].EventType != balanceOperationTenantUserCredit || history[1].Amount != "40.00000000" {
		t.Fatalf("人工分发没有按真实正负号进入统一余额历史：%+v", history)
	}
	for _, item := range history {
		if item.SourceType != "balance_ledger_transaction" {
			t.Fatalf("人工分发历史来源错误：%+v", item)
		}
	}
	wallet, err := svc.GetTenantWallet(ctx, f.tenantB)
	if err != nil || !wallet.Balance.Equal(decimal.NewFromInt(70)) || wallet.CurrencyCode != "USD" {
		t.Fatalf("租户钱包只读投影错误：wallet=%+v err=%v", wallet, err)
	}
	transactions, err := svc.ListBalanceTransactions(ctx, ListTransactionsInput{TenantID: f.tenantB, Limit: 10})
	if err != nil {
		t.Fatalf("查询租户交易日志：%v", err)
	}
	if len(transactions) != 3 || transactions[0].Operation != balanceOperationTenantUserDebit ||
		!transactions[0].SignedAmount.Equal(decimal.NewFromInt(-10)) {
		t.Fatalf("租户交易日志顺序或符号错误：%+v", transactions)
	}
	userTransactions, err := svc.ListBalanceTransactions(ctx, ListTransactionsInput{TenantID: f.tenantB, UserID: f.userB, Limit: 10})
	if err != nil || len(userTransactions) != 2 {
		t.Fatalf("按用户筛选交易日志错误：items=%+v err=%v", userTransactions, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE user_balances SET held=25 WHERE tenant_id=$1 AND user_id=$2`, f.tenantB, f.userB); err != nil {
		t.Fatalf("设置在途占用：%v", err)
	}
	blocked := balanceAdjustment(f.tenantB, f.userB, "-10", BalanceActorTenantOperator, f.tenantB, "admin_user:22", "tenant-user-held-block")
	if _, err := svc.AdminAdjustBalance(ctx, blocked); !errors.Is(err, ErrBalanceInsufficient) {
		t.Fatalf("收回超过可用余额 err=%v，want ErrBalanceInsufficient", err)
	}
	assertTenantAndUserBalance(t, ctx, pool, f.tenantB, f.userB, "70.00000000", "30.00000000", "25.00000000")
	assertBalanceLedgerKeyCount(t, ctx, pool, f.tenantB, blocked.ActorRef, blocked.IdempotencyKey, 0)
}

func TestAdminBalanceLedgerRejectsEscalationAndAdminTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openBalanceLedgerIntegrationPool(t, ctx)
	f := newBalanceLedgerFixture(t, ctx, pool)
	registerBalanceLedgerCleanup(t, pool, f.tenantA, f.tenantB)
	svc := NewService(NewPostgresStore(pool, f.tenantA))

	overreach := balanceAdjustment(f.tenantB, f.userB, "10", BalanceActorPlatformAdmin, 0, "admin_token:11", "platform-overreach")
	if _, err := svc.AdminAdjustBalance(ctx, overreach); !errors.Is(err, ErrBalanceAdjustmentForbidden) {
		t.Fatalf("部署者越级动下级用户 err=%v，want ErrBalanceAdjustmentForbidden", err)
	}
	crossTenant := balanceAdjustment(f.tenantA, f.userA, "10", BalanceActorTenantOperator, f.tenantB, "admin_user:22", "tenant-cross")
	if _, err := svc.AdminAdjustBalance(ctx, crossTenant); !errors.Is(err, ErrBalanceAdjustmentForbidden) {
		t.Fatalf("租户管理员跨租户 err=%v，want ErrBalanceAdjustmentForbidden", err)
	}

	var adminUserID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO users (tenant_id, display_name, role) VALUES ($1, $2, 'admin') RETURNING id`,
		f.tenantB, "tenant-admin-"+f.suffix).Scan(&adminUserID); err != nil {
		t.Fatalf("创建下级租户管理员：%v", err)
	}
	adminTarget := balanceAdjustment(f.tenantB, adminUserID, "10", BalanceActorTenantOperator, f.tenantB, "admin_user:22", "tenant-admin-target")
	if _, err := svc.AdminAdjustBalance(ctx, adminTarget); !errors.Is(err, ErrBalanceAdjustmentForbidden) {
		t.Fatalf("租户经营额度不得进入管理员个人余额 err=%v，want ErrBalanceAdjustmentForbidden", err)
	}
	assertBalanceLedgerKeyCount(t, ctx, pool, f.tenantB, adminTarget.ActorRef, adminTarget.IdempotencyKey, 0)
}

func TestAdminBalanceLedgerConcurrentDistributionNeverOverdraws(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := openBalanceLedgerIntegrationPool(t, ctx)
	f := newBalanceLedgerFixture(t, ctx, pool)
	registerBalanceLedgerCleanup(t, pool, f.tenantA, f.tenantB)
	svc := NewService(NewPostgresStore(pool, f.tenantA))

	if _, err := svc.AdminAdjustBalance(ctx, balanceAdjustment(
		f.tenantB, 0, "100", BalanceActorPlatformAdmin, 0, "admin_token:11", "concurrent-seed-100")); err != nil {
		t.Fatalf("准备租户钱包：%v", err)
	}

	const workers = 20
	var succeeded, insufficient, unexpected atomic.Int64
	unexpectedErrors := make(chan error, workers)
	var wg sync.WaitGroup
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			input := balanceAdjustment(f.tenantB, f.userB, "10", BalanceActorTenantOperator, f.tenantB,
				"admin_user:22", fmt.Sprintf("concurrent-distribute-%02d", index))
			_, err := svc.AdminAdjustBalance(ctx, input)
			switch {
			case err == nil:
				succeeded.Add(1)
			case errors.Is(err, ErrBalanceInsufficient):
				insufficient.Add(1)
			default:
				unexpected.Add(1)
				unexpectedErrors <- err
			}
		}(index)
	}
	wg.Wait()
	close(unexpectedErrors)
	if succeeded.Load() != 10 || insufficient.Load() != 10 || unexpected.Load() != 0 {
		var firstUnexpected error
		for err := range unexpectedErrors {
			firstUnexpected = err
			break
		}
		t.Fatalf("并发结果 success/insufficient/unexpected=%d/%d/%d，want 10/10/0；首个意外错误=%v",
			succeeded.Load(), insufficient.Load(), unexpected.Load(), firstUnexpected)
	}
	assertTenantAndUserBalance(t, ctx, pool, f.tenantB, f.userB, "0.00000000", "100.00000000", "0.00000000")
	var distributed int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM balance_ledger_transactions
WHERE tenant_id=$1 AND operation='tenant_user_credit'`, f.tenantB).Scan(&distributed); err != nil {
		t.Fatalf("统计并发分发交易：%v", err)
	}
	if distributed != 10 {
		t.Fatalf("并发分发交易=%d，want 10", distributed)
	}
}

func TestBalanceLedgerDatabaseRejectsHeaderWithoutEntriesAndMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openBalanceLedgerIntegrationPool(t, ctx)
	fixture := newBalanceLedgerFixture(t, ctx, pool)
	registerBalanceLedgerCleanup(t, pool, fixture.tenantA, fixture.tenantB)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开始零分录交易：%v", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO balance_ledger_transactions (
    tenant_id, platform_tenant_id, operation, amount, currency_code,
    actor_role, actor_ref, idempotency_key, request_fingerprint, reason
) VALUES ($1,$2,'platform_tenant_credit',10,'USD','platform_admin','admin_token:1','missing-entries',$3,'缺失分录')`,
		fixture.tenantB, fixture.tenantA, strings.Repeat("a", 64))
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("交易头插入阶段不应提前失败：%v", err)
	}
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("没有双边分录的交易头不得提交")
	}

	svc := NewService(NewPostgresStore(pool, fixture.tenantA))
	result, err := svc.AdminAdjustBalance(ctx, balanceAdjustment(
		fixture.tenantB, 0, "10", BalanceActorPlatformAdmin, 0, "admin_token:1", "append-only-proof"))
	if err != nil {
		t.Fatalf("创建完整余额交易：%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE balance_ledger_transactions SET reason='篡改' WHERE id=$1`, result.TransactionID); err == nil {
		t.Fatal("永久余额交易不应允许 UPDATE")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM balance_ledger_entries WHERE transaction_id=$1`, result.TransactionID); err == nil {
		t.Fatal("永久余额分录不应允许 DELETE")
	}
}

func balanceAdjustment(tenantID, userID int64, amount, role string, scope int64, actor, key string) AdminBalanceAdjustmentInput {
	return AdminBalanceAdjustmentInput{
		TenantID: tenantID, UserID: userID, Amount: decimal.RequireFromString(amount), CurrencyCode: "USD",
		ActorRole: role, ActorScopeTenantID: scope, ActorRef: actor, Reason: "集成测试人工额度调整",
		IdempotencyKey: key, Now: time.Now().UTC(),
	}
}

func assertTenantAndUserBalance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, wantTenant, wantUser, wantHeld string) {
	t.Helper()
	var tenantBalance, userBalance, held decimal.Decimal
	if err := pool.QueryRow(ctx, `
SELECT wallet.balance, balance.balance, balance.held
FROM tenant_wallets wallet
JOIN user_balances balance ON balance.tenant_id=wallet.tenant_id AND balance.user_id=$2
WHERE wallet.tenant_id=$1`, tenantID, userID).Scan(&tenantBalance, &userBalance, &held); err != nil {
		t.Fatalf("读取租户和用户余额：%v", err)
	}
	if tenantBalance.StringFixed(8) != wantTenant || userBalance.StringFixed(8) != wantUser || held.StringFixed(8) != wantHeld {
		t.Fatalf("tenant/user/held=%s/%s/%s，want %s/%s/%s",
			tenantBalance.StringFixed(8), userBalance.StringFixed(8), held.StringFixed(8), wantTenant, wantUser, wantHeld)
	}
}

func assertBalancedLedgerTransaction(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, transactionID int64, wantEntries int) {
	t.Helper()
	var count int
	var sum decimal.Decimal
	if err := pool.QueryRow(ctx, `
SELECT count(*), COALESCE(sum(delta), 0)
FROM balance_ledger_entries WHERE tenant_id=$1 AND transaction_id=$2`, tenantID, transactionID).Scan(&count, &sum); err != nil {
		t.Fatalf("读取交易分录：%v", err)
	}
	if count != wantEntries || !sum.IsZero() {
		t.Fatalf("交易 %d 的分录 count/sum=%d/%s，want %d/0", transactionID, count, sum, wantEntries)
	}
}

func assertBalanceLedgerKeyCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, actor, key string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM balance_ledger_transactions
WHERE tenant_id=$1 AND actor_ref=$2 AND idempotency_key=$3`, tenantID, actor, key).Scan(&count); err != nil {
		t.Fatalf("统计余额交易：%v", err)
	}
	if count != want {
		t.Fatalf("余额交易数=%d，want %d", count, want)
	}
}

func assertNoPaymentOrderForBalanceKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, key string) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`, tenantID, key).Scan(&count); err != nil {
		t.Fatalf("查询充值订单：%v", err)
	}
	if count != 0 {
		t.Fatalf("人工额度调整不应制造充值订单，got %d", count)
	}
}

func registerBalanceLedgerCleanup(t *testing.T, pool *pgxpool.Pool, tenantIDs ...int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Errorf("开始余额账本清理事务：%v", err)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		for _, statement := range []string{
			`ALTER TABLE balance_ledger_entries DISABLE TRIGGER balance_ledger_entries_append_only_delete`,
			`ALTER TABLE balance_ledger_transactions DISABLE TRIGGER balance_ledger_transactions_append_only_delete`,
		} {
			if _, err := tx.Exec(ctx, statement); err != nil {
				t.Errorf("关闭测试清理触发器：%v", err)
				return
			}
		}
		for _, tenantID := range tenantIDs {
			if _, err := tx.Exec(ctx, `DELETE FROM balance_ledger_entries WHERE tenant_id=$1`, tenantID); err != nil {
				t.Errorf("清理余额分录：%v", err)
				return
			}
			if _, err := tx.Exec(ctx, `DELETE FROM balance_ledger_transactions WHERE tenant_id=$1`, tenantID); err != nil {
				t.Errorf("清理余额交易：%v", err)
				return
			}
			if _, err := tx.Exec(ctx, `DELETE FROM tenant_wallets WHERE tenant_id=$1`, tenantID); err != nil {
				t.Errorf("清理租户钱包：%v", err)
				return
			}
		}
		for _, statement := range []string{
			`ALTER TABLE balance_ledger_entries ENABLE TRIGGER balance_ledger_entries_append_only_delete`,
			`ALTER TABLE balance_ledger_transactions ENABLE TRIGGER balance_ledger_transactions_append_only_delete`,
		} {
			if _, err := tx.Exec(ctx, statement); err != nil {
				t.Errorf("恢复测试清理触发器：%v", err)
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("提交余额账本清理事务：%v", err)
		}
	})
}
