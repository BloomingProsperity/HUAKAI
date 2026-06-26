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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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

func TestAccountCredentialRefreshQueriesSQLFiltersUnsafeProviderAccountHealth(t *testing.T) {
	// 非 PG 守卫:生产 wiring 使用 AccountCredentialRefreshQueries,因此它的扫描
	// SQL 必须携带与下方真 PG fixture 相同的 provider-account health 谓词。
	// Mutation:从 NewAccountCredentialRefreshQueries 删掉该谓词,即使
	// HUAKAI_DATABASE_URL 未设置,本测试也会失败。
	db := &refreshListQueryDBStub{}
	_, err := NewAccountCredentialRefreshQueries(db).ListAccountsForRefresh(context.Background(), dbbilling.ListAccountsForRefreshParams{
		RefreshBefore: pgTimestamptz(time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)),
		LimitCount:    10,
	})
	if err != nil {
		t.Fatalf("ListAccountsForRefresh: %v", err)
	}
	if db.calls != 1 {
		t.Fatalf("query calls=%d want 1", db.calls)
	}
}

func TestListAccountsForRefreshSkipsUnsafeProviderAccountHealthPG(t *testing.T) {
	// refresh 扫描绝不能反复打已被撤销或仍在冷却中的 provider account。
	// Mutation check:从任一 refresh list query 删掉 provider-account health 谓词,
	// 下方 revoked/future-cooldown 的 ID 就会出现在结果集中,使本测试转红。expired
	// cooldown 这一对照证明该守卫并非一个过宽的 health_state='healthy' 过滤——后者会在
	// 瞬态冷却截止时刻过去后仍把容量搁置不用。
	ctx := context.Background()
	pool := openCredentialWorkerTestPool(t, ctx)
	suffix := uuid.NewString()
	now := time.Now().UTC()
	refreshBefore := dbbilling.ListAccountsForRefreshParams{
		RefreshBefore: pgTimestamptz(now.Add(time.Hour)),
		LimitCount:    1000,
	}

	healthyTenant, healthyID := seedCredentialWorkerProviderAccount(t, ctx, pool, suffix+"-healthy")
	revokedTenant, revokedID := seedCredentialWorkerProviderAccount(t, ctx, pool, suffix+"-revoked")
	futureTenant, futureID := seedCredentialWorkerProviderAccount(t, ctx, pool, suffix+"-future")
	expiredTenant, expiredID := seedCredentialWorkerProviderAccount(t, ctx, pool, suffix+"-expired")

	seedRefreshCandidateCredential(t, ctx, pool, healthyTenant, healthyID, "healthy", now.Add(-time.Minute))
	seedRefreshCandidateCredential(t, ctx, pool, revokedTenant, revokedID, "revoked", now.Add(-time.Minute))
	seedRefreshCandidateCredential(t, ctx, pool, futureTenant, futureID, "future", now.Add(-time.Minute))
	seedRefreshCandidateCredential(t, ctx, pool, expiredTenant, expiredID, "expired", now.Add(-time.Minute))

	if _, err := pool.Exec(ctx,
		`UPDATE provider_accounts SET health_state = 'revoked', health_state_until = NULL WHERE id = $1`,
		revokedID,
	); err != nil {
		t.Fatalf("mark revoked provider account: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE provider_accounts SET health_state = 'cooldown', health_state_until = $2 WHERE id = $1`,
		futureID, now.Add(10*time.Minute),
	); err != nil {
		t.Fatalf("mark future cooldown provider account: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE provider_accounts SET health_state = 'cooldown', health_state_until = $2 WHERE id = $1`,
		expiredID, now.Add(-10*time.Minute),
	); err != nil {
		t.Fatalf("mark expired cooldown provider account: %v", err)
	}

	modeRows, err := NewAccountCredentialRefreshQueries(pool).ListAccountsForRefresh(ctx, refreshBefore)
	if err != nil {
		t.Fatalf("mode ListAccountsForRefresh: %v", err)
	}
	assertRefreshScanHealthSet(t, "mode", modeRows, []int64{healthyID, expiredID}, []int64{revokedID, futureID})

	legacyRows, err := dbbilling.New(pool).ListAccountsForRefresh(ctx, refreshBefore)
	if err != nil {
		t.Fatalf("legacy ListAccountsForRefresh: %v", err)
	}
	assertRefreshScanHealthSet(t, "legacy", legacyRows, []int64{healthyID, expiredID}, []int64{revokedID, futureID})
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
		_, _ = pool.Exec(c, `DELETE FROM account_credentials WHERE tenant_id = $1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE id = $1`, paID)
		_, _ = pool.Exec(c, `DELETE FROM providers WHERE id = $1`, providerID)
		_, _ = pool.Exec(c, `DELETE FROM channels WHERE id = $1`, channelID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE id = $1`, poolGroupID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	return tenantID, paID
}

func seedRefreshCandidateCredential(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, providerAccountID int64, suffix string, refreshBefore time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO account_credentials (
			tenant_id, provider_account_id, vendor, auth_mode, state,
			encrypted_payload, key_id, nonce, aad_hash, refresh_before_at
		) VALUES ($1, $2, 'anthropic', 'api_key', 'active', $3, $4, $5, $6, $7)`,
		tenantID, providerAccountID,
		[]byte("ciphertext-"+suffix),
		"key-"+suffix,
		[]byte("nonce-"+suffix),
		"aad-"+suffix,
		refreshBefore,
	); err != nil {
		t.Fatalf("seed account credential %s: %v", suffix, err)
	}
}

func pgTimestamptz(ts time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: ts.UTC(), Valid: true}
}

func assertRefreshScanHealthSet(t *testing.T, label string, rows []dbbilling.ListAccountsForRefreshRow, wantPresent, wantAbsent []int64) {
	t.Helper()
	seen := make(map[int64]bool, len(rows))
	for _, row := range rows {
		seen[row.ID] = true
	}
	for _, id := range wantPresent {
		if !seen[id] {
			t.Fatalf("%s refresh scan missing safe account %d; rows=%v", label, id, refreshScanIDs(rows))
		}
	}
	for _, id := range wantAbsent {
		if seen[id] {
			t.Fatalf("%s refresh scan returned unsafe account %d; rows=%v", label, id, refreshScanIDs(rows))
		}
	}
}

func refreshScanIDs(rows []dbbilling.ListAccountsForRefreshRow) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

type refreshListQueryDBStub struct {
	calls int
}

func (s *refreshListQueryDBStub) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (s *refreshListQueryDBStub) Query(_ context.Context, sql string, _ ...interface{}) (pgx.Rows, error) {
	s.calls++
	for _, required := range []string{
		"pa.enabled",
		"pa.health_state = 'healthy'",
		"pa.health_state IN ('throttled', 'cooldown')",
		"pa.health_state_until <= NOW()",
		"pa.health_state <> 'revoked'",
	} {
		if !strings.Contains(sql, required) {
			return nil, errors.New("refresh list SQL missing " + required)
		}
	}
	return emptyRefreshRows{}, nil
}

func (s *refreshListQueryDBStub) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

type emptyRefreshRows struct{}

func (emptyRefreshRows) Close()                                       {}
func (emptyRefreshRows) Err() error                                   { return nil }
func (emptyRefreshRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (emptyRefreshRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (emptyRefreshRows) Next() bool                                   { return false }
func (emptyRefreshRows) Scan(...any) error                            { return errors.New("unexpected Scan") }
func (emptyRefreshRows) Values() ([]any, error)                       { return nil, errors.New("unexpected Values") }
func (emptyRefreshRows) RawValues() [][]byte                          { return nil }
func (emptyRefreshRows) Conn() *pgx.Conn                              { return nil }

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

func TestRecordAuditString_NewOutcomePersistsToPG(t *testing.T) {
	// 修掉的回归:S2 refresher 以字符串形式传入已分类的 outcome;audit append
	// helper 必须同时持久化 legacy 与新的 4-class 取值。Mutation 自检:把未知字符串
	// 映射成 permanent_disable 会让下方 SELECT 取不到精确那一行。
	ctx := context.Background()
	pool := openCredentialWorkerTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID, paID := seedCredentialWorkerProviderAccount(t, ctx, pool, suffix)

	signer := newTestSigner(t)
	s := newSchedulerForAuditTx(t, pool, signer)
	row := dbbilling.ListAccountsForRefreshRow{ID: paID, TenantID: tenantID}

	for _, outcome := range []string{
		"auth_expired",
		"rate_limit_exceeded",
		"risk_control_triggered",
		"account_disabled",
	} {
		if err := s.recordAuditString(ctx, row, outcome, "account", errors.New("classified refresh failure")); err != nil {
			t.Fatalf("recordAuditString(%q): %v", outcome, err)
		}
		var got string
		if err := pool.QueryRow(ctx, `
			SELECT outcome
			FROM oauth_refresh_audit_events
			WHERE tenant_id = $1 AND provider_account_id = $2 AND outcome = $3
			ORDER BY occurred_at DESC, id DESC
			LIMIT 1`,
			tenantID, paID, outcome,
		).Scan(&got); err != nil {
			t.Fatalf("select persisted outcome %q: %v", outcome, err)
		}
		if got != outcome {
			t.Fatalf("persisted outcome=%q, want %q", got, outcome)
		}
	}
}

func TestRecordAudit_HealthStateTransitionRoundTripPG(t *testing.T) {
	// 修掉的回归:audit outcome 必须在同一条本地 DB 路径里更新 provider_accounts
	// 的 health state,而不仅仅是追加 audit 行。Mutation 自检:删掉 health 更新会让
	// 下方 SELECT 停留在 seed/默认状态,本测试转红。
	ctx := context.Background()
	pool := openCredentialWorkerTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID, paID := seedCredentialWorkerProviderAccount(t, ctx, pool, suffix)

	signer := newTestSigner(t)
	s := newSchedulerForAuditTx(t, pool, signer)
	row := dbbilling.ListAccountsForRefreshRow{ID: paID, TenantID: tenantID}

	if err := s.recordAuditString(ctx, row, "auth_expired", "account", errors.New("expired refresh")); err != nil {
		t.Fatalf("record auth_expired: %v", err)
	}
	// auth_expired 是终态——它必须把 health_state_until 持久化为 NULL,这样
	// eligibility CTE(只规范化 WHERE health_state_until IS NOT NULL AND <= NOW 的行)
	// 就绝不会靠一个 30 分钟定时器自动恢复一个已失授权的账号。Mutation check:还原
	// now+cooldown 截止时间,下方 `until != nil` 断言就会转红。
	var state string
	var until *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT health_state, health_state_until FROM provider_accounts WHERE id = $1`,
		paID,
	).Scan(&state, &until); err != nil {
		t.Fatalf("select revoked state: %v", err)
	}
	if state != "revoked" || until != nil {
		t.Fatalf("after auth_expired health=(%q,%v), want terminal revoked with NULL until", state, until)
	}

	if err := s.recordAudit(ctx, row, auth.OutcomeRefreshSucceeded, "account", nil); err != nil {
		t.Fatalf("record refresh_succeeded: %v", err)
	}
	var resetState string
	var resetUntil *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT health_state, health_state_until FROM provider_accounts WHERE id = $1`,
		paID,
	).Scan(&resetState, &resetUntil); err != nil {
		t.Fatalf("select healthy state: %v", err)
	}
	if resetState != "healthy" || resetUntil != nil {
		t.Fatalf("after refresh_succeeded health=(%q,%v), want healthy NULL", resetState, resetUntil)
	}
}

// TestUpdateProviderAccountHealthTerminalStickyAgainstTransientPG 守卫:一次
// 瞬态(throttled + deadline)的 health 写入绝不能降级一个已经终态撤销
// (revoked + NULL deadline)的账号。若 updateProviderAccountHealthSQL 中没有 CASE
// 守卫,一次对仍失授权账号的偶发 rate_limit 重试会把它改写成 throttled+3m,之后
// eligibility CTE 会在没有任何成功刷新或 operator 动作的情况下把它自愈回 healthy
// ——重新打开了终态本要关闭的那条定时恢复通道。
//
// Mutation check:把两个 CASE 表达式换成无条件的 $3/$4 赋值,"terminal revocation
// downgraded" 断言就会转红。non-terminal 对照证明该守卫只针对终态行(它不会一刀切
// 地阻止 throttling),success 步骤证明恢复路径(healthy 清除终态)得以保留。
func TestUpdateProviderAccountHealthTerminalStickyAgainstTransientPG(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialWorkerTestPool(t, ctx)
	suffix := uuid.NewString()
	tenantID, paID := seedCredentialWorkerProviderAccount(t, ctx, pool, suffix)

	setHealth := func(state string, until *time.Time) {
		t.Helper()
		if err := updateProviderAccountHealth(ctx, pool, ProviderAccountHealthChange{
			TenantID: tenantID, ProviderAccountID: paID, HealthState: state, HealthStateUntil: until,
		}); err != nil {
			t.Fatalf("updateProviderAccountHealth(%s): %v", state, err)
		}
	}
	readHealth := func() (string, *time.Time) {
		t.Helper()
		var state string
		var until *time.Time
		if err := pool.QueryRow(ctx,
			`SELECT health_state, health_state_until FROM provider_accounts WHERE id = $1`, paID,
		).Scan(&state, &until); err != nil {
			t.Fatalf("read health: %v", err)
		}
		return state, until
	}

	// 1) 终态撤销(auth_expired / risk_control_triggered 风格):revoked + NULL deadline。
	setHealth("revoked", nil)
	// 2) 之后对同一个(仍失授权)账号的一次 rate_limit 重试被分类为瞬态。
	cooldown := time.Now().UTC().Add(3 * time.Minute)
	setHealth("throttled", &cooldown)
	if state, until := readHealth(); state != "revoked" || until != nil {
		t.Fatalf("terminal revocation downgraded by transient write: state=%q until=%v, want revoked/NULL", state, until)
	}
	// 3) 恢复得以保留:一次成功刷新(healthy,无 deadline)确实会清除终态。
	setHealth("healthy", nil)
	if state, until := readHealth(); state != "healthy" || until != nil {
		t.Fatalf("successful refresh failed to clear terminal: state=%q until=%v, want healthy/NULL", state, until)
	}

	// 对照(有区分力):一个 non-terminal(healthy)账号在一次瞬态写入下必须仍被
	// throttle——该守卫只针对终态行,不会一刀切地抑制 throttling。
	setHealth("throttled", &cooldown)
	if state, until := readHealth(); state != "throttled" || until == nil {
		t.Fatalf("transient throttle wrongly suppressed on non-terminal account: state=%q until=%v, want throttled/deadline", state, until)
	}
}
