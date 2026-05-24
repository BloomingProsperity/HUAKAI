package credentialworker

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// W5 RR-W5-002 同事务 + fail-closed 真 PG 验证 (env-var skip — 无需 build tag,
// 与 admin pools handler_test 同模式)。
//
// 闭合 RR-W5-002:
//   1. recordAudit 同事务路径 (BeginFunc): audit insert + ledger append 同
//      pgx.Tx — audit 拒 → 两表都不留行
//   2. dbAuditWriter nil queries → ErrAuditWriterMissing (不再 silent nil)
//
// Mutation 自检见每测试块注释:删 BeginFunc 改 2-step → ledger 行落 → 测试 red。

// TestDBAuditWriter_NilQueriesReturnsErrAuditWriterMissing — unit (无需 PG)
//
// RR-W5-002 步骤 2 修复:nil queries production 必须显式 ErrAuditWriterMissing,
// 防 audit 字段静默丢失。Mutation:把 return ErrAuditWriterMissing 改回 nil →
// 本用例 red。
func TestDBAuditWriter_NilQueriesReturnsErrAuditWriterMissing(t *testing.T) {
	w := dbAuditWriter{queries: nil}
	entry := &auth.RefreshAuditEntry{
		TenantID:          7,
		ProviderAccountID: 99,
		Outcome:           auth.Outcome("rotated"),
		RequestID:         "test-req",
		OccurredAt:        time.Now().UTC(),
	}
	err := w.WriteRefreshAudit(context.Background(), entry)
	if !errors.Is(err, ErrAuditWriterMissing) {
		t.Fatalf("nil queries: want ErrAuditWriterMissing; got %v", err)
	}
}

func openCredentialWorkerTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// seedCredentialWorkerProviderAccount 种 tenant + pool_group + channel + provider +
// provider_account 整条 FK 链,返回 (tenantID, providerAccountID) + cleanup。
//
// (huakai 角色无 session_replication_role 权限,只能正经走完整 FK chain。)
func seedCredentialWorkerProviderAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (int64, int64) {
	t.Helper()
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"cw-tx-tenant-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	var poolGroupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name, top_k_default, capability_default, allow_last_resort)
		 VALUES ($1, $2, 1, 'exact_capability_only', false) RETURNING id`,
		tenantID, "cw-pg-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}

	var channelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name)
		 VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "cw-ch-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	var providerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'anthropic_messages') RETURNING id`,
		tenantID, "cw-prv-"+suffix, "cw-tx-provider-"+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}

	var paID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type)
		 VALUES ($1, $2, $3, $4, 'oauth') RETURNING id`,
		tenantID, providerID, channelID, "cw-tx-pa-"+suffix,
	).Scan(&paID); err != nil {
		t.Fatalf("seed provider_account: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_ledger_entries WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM oauth_refresh_audit_events WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE id = $1`, paID)
		_, _ = pool.Exec(c, `DELETE FROM providers WHERE id = $1`, providerID)
		_, _ = pool.Exec(c, `DELETE FROM channels WHERE id = $1`, channelID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE id = $1`, poolGroupID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return tenantID, paID
}

// installOAuthAuditRejectTrigger 装 BEFORE INSERT trigger 拒 outcome == reject 的 oauth_refresh_audit_events 行。
func installOAuthAuditRejectTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, rejectOutcome string) {
	t.Helper()
	fnName := "oauth_audit_reject_" + name
	trigName := "trg_" + name
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION `+fnName+`() RETURNS trigger AS $$
		BEGIN
			IF NEW.outcome = '`+rejectOutcome+`' THEN
				RAISE EXCEPTION 'credentialworker tx test reject outcome %', NEW.outcome;
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create reject fn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DROP TRIGGER IF EXISTS `+trigName+` ON oauth_refresh_audit_events;
		CREATE TRIGGER `+trigName+` BEFORE INSERT ON oauth_refresh_audit_events
		FOR EACH ROW EXECUTE FUNCTION `+fnName+`()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DROP TRIGGER IF EXISTS `+trigName+` ON oauth_refresh_audit_events`)
		_, _ = pool.Exec(c, `DROP FUNCTION IF EXISTS `+fnName+`()`)
	})
}

// newTestSigner 构造测试用 ledger signer (固定种子,deterministic)。
func newTestSigner(t *testing.T) *auditledger.LocalEd25519Signer {
	t.Helper()
	seed := bytes.Repeat([]byte{0x42}, ed25519.SeedSize)
	priv := ed25519.NewKeyFromSeed(seed)
	signer, err := auditledger.NewLocalEd25519Signer(priv, nil)
	if err != nil {
		t.Fatalf("NewLocalEd25519Signer: %v", err)
	}
	return signer
}

// newSchedulerForAuditTx 构造同事务 Scheduler — 所有 tx 三件套 (pool/signer/queries) 都配齐。
//
// 关键:legacy path 的 AuditLedger 必须是真 PostgresLedger (不能 NoopLedger),
// 否则 mutation 自检对 tx vs 非 tx 路径无区分力 — 非 tx 路径用 noop ledger
// 不写库时 audit fail 后 ledger count 仍是 0,造成假绿。
func newSchedulerForAuditTx(t *testing.T, pool *pgxpool.Pool, signer *auditledger.LocalEd25519Signer) *Scheduler {
	t.Helper()
	pgLedger, err := auditledger.NewPostgresLedger(pool, signer)
	if err != nil {
		t.Fatalf("NewPostgresLedger: %v", err)
	}
	s := NewScheduler(
		nil, // billing queries — recordAudit 不需要
		nil, // storm controller — 同上
		nil, // type Signer (用 mode_refresh signer,不是 ledger signer)
		nil, // refresher
		WithAuditQueries(dbauth.New(pool)),
		WithAuditLedger(pgLedger),
		WithTxPool(pool),
		WithAuditLedgerSigner(signer),
	)
	s.now = func() time.Time { return time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC) }
	return s
}

// TestRecordAudit_TxRollback_AuditFail — 同事务路径:trigger 拒 outcome=rejected,
// audit insert + ledger append 都不落库 (tx 一起 rollback)。
//
// 判别 fixture:跑前后断言两表都无新行;跑后 recordAudit 返非 nil err。
//
// Mutation 自检:把 BeginFunc 包装去掉,recordAudit 退回 legacy 2-step (auditWriter +
// AuditLedger.Append 独立)→ ledger.Append 还是会走 PostgresLedger 独立 tx 提交一行,
// 测试 red (audit_ledger_entries 计数 > 0)。
func TestRecordAudit_TxRollback_AuditFail(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialWorkerTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID, paID := seedCredentialWorkerProviderAccount(t, ctx, pool, suffix)
	// 选用合法但稀有的 outcome 'permanent_disable' 让 trigger 拒;不能用自造串,
	// 否则 outcome CHECK constraint 自己就先报 23514,测试无法判别"tx 回滚"。
	rejectOutcome := "permanent_disable"
	installOAuthAuditRejectTrigger(t, ctx, pool, "rej_"+strings.ReplaceAll(suffix, "-", "_"), rejectOutcome)

	signer := newTestSigner(t)
	s := newSchedulerForAuditTx(t, pool, signer)

	row := dbbilling.ListAccountsForRefreshRow{ID: paID, TenantID: tenantID}
	err := s.recordAudit(ctx, row, auth.Outcome(rejectOutcome), "account", nil)
	if err == nil {
		t.Fatalf("recordAudit must fail when audit trigger rejects; got nil")
	}

	// 验:oauth_refresh_audit_events 没行
	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM oauth_refresh_audit_events WHERE tenant_id = $1`,
		tenantID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("audit row MUST NOT be committed when trigger rejects; got %d", auditCount)
	}

	// 验:audit_ledger_entries 没行 (tx 同生死)
	var ledgerCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_ledger_entries WHERE tenant_id = $1`,
		tenantID,
	).Scan(&ledgerCount); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("ledger row MUST NOT be committed when audit insert fails in same tx; got %d", ledgerCount)
	}
}

// TestRecordAudit_TxCommit_HappyPath — 同事务正向保持:audit OK 时两表都落库。
// 跟 _Rollback_ 配对,验"audit OK 时 ledger 也跟着提交"。
func TestRecordAudit_TxCommit_HappyPath(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialWorkerTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID, paID := seedCredentialWorkerProviderAccount(t, ctx, pool, suffix)

	signer := newTestSigner(t)
	s := newSchedulerForAuditTx(t, pool, signer)

	row := dbbilling.ListAccountsForRefreshRow{ID: paID, TenantID: tenantID}
	if err := s.recordAudit(ctx, row, auth.Outcome("refresh_succeeded"), "account", nil); err != nil {
		t.Fatalf("recordAudit happy path: %v", err)
	}

	var auditCount, ledgerCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM oauth_refresh_audit_events WHERE tenant_id = $1`, tenantID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit row missing; want 1 got %d", auditCount)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_ledger_entries WHERE tenant_id = $1`, tenantID,
	).Scan(&ledgerCount); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if ledgerCount != 1 {
		t.Fatalf("ledger row missing; want 1 got %d", ledgerCount)
	}
}
