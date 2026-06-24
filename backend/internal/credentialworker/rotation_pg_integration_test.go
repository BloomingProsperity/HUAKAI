package credentialworker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// insertCredentialWithCreatedAt seeds one account_credentials row with an
// explicit created_at (and optional state/vendor/auth_mode) so the rotation-age
// cutoff and recovery routing can be exercised deterministically.
func insertCredentialWithCreatedAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, paID int64, suffix string, createdAt time.Time) int64 {
	t.Helper()
	return insertCredentialFull(t, ctx, pool, tenantID, paID, suffix, "anthropic", "api_key", "active", createdAt, nil)
}

func insertCredentialFull(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, paID int64, suffix, vendor, authMode, state string, createdAt time.Time, refreshBefore *time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO account_credentials (
			tenant_id, provider_account_id, vendor, auth_mode, state,
			encrypted_payload, key_id, nonce, aad_hash, created_at, refresh_before_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id`,
		tenantID, paID, vendor, authMode, state,
		[]byte("ct-"+suffix), "key-"+suffix, []byte("nonce-"+suffix), "aad-"+suffix,
		createdAt, refreshBefore,
	).Scan(&id); err != nil {
		t.Fatalf("insert credential %s: %v", suffix, err)
	}
	return id
}

func credentialState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var st string
	if err := pool.QueryRow(ctx, `SELECT state FROM account_credentials WHERE id = $1`, id).Scan(&st); err != nil {
		t.Fatalf("read state %d: %v", id, err)
	}
	return st
}

func credentialRefreshBefore(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) *time.Time {
	t.Helper()
	var ts *time.Time
	if err := pool.QueryRow(ctx, `SELECT refresh_before_at FROM account_credentials WHERE id = $1`, id).Scan(&ts); err != nil {
		t.Fatalf("read refresh_before_at %d: %v", id, err)
	}
	return ts
}

// CRED-288b end-to-end against a real Postgres: a credential older than the
// rotation cutoff is selected (with its vendor/auth_mode) and a fresh one is not;
// the explicit operator force-rotate FlagNeedsRotation still works and is
// idempotent.
func TestPostgresRotationStore_DueAndFlag(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialWorkerTestPool(t, ctx)
	defer pool.Close()

	run := time.Now().UnixNano()
	tenantOld, paOld := seedCredentialWorkerProviderAccount(t, ctx, pool, fmt.Sprintf("rot288b-%d-old", run))
	tenantFresh, paFresh := seedCredentialWorkerProviderAccount(t, ctx, pool, fmt.Sprintf("rot288b-%d-fresh", run))
	now := time.Now().UTC()
	oldID := insertCredentialWithCreatedAt(t, ctx, pool, tenantOld, paOld, "old", now.Add(-200*24*time.Hour))
	freshID := insertCredentialWithCreatedAt(t, ctx, pool, tenantFresh, paFresh, "fresh", now.Add(-24*time.Hour))

	store := NewPostgresRotationStore(pool)
	cutoff := now.Add(-90 * 24 * time.Hour)

	due, err := store.DueForRotation(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("DueForRotation: %v", err)
	}
	seen := map[int64]RotationCandidate{}
	for _, c := range due {
		seen[c.CredentialID] = c
	}
	if _, ok := seen[oldID]; !ok {
		t.Fatalf("200d-old credential %d must be due for rotation", oldID)
	}
	if _, ok := seen[freshID]; ok {
		t.Fatalf("1d-old credential %d must NOT be due for rotation", freshID)
	}
	// DueForRotation must carry the classifier inputs.
	if oc := seen[oldID]; oc.Vendor != "anthropic" || oc.AuthMode != "api_key" {
		t.Fatalf("due candidate must carry vendor/auth_mode, got vendor=%q auth_mode=%q", oc.Vendor, oc.AuthMode)
	}

	oldCand := seen[oldID]
	if err := store.FlagNeedsRotation(ctx, oldCand); err != nil {
		t.Fatalf("FlagNeedsRotation: %v", err)
	}
	if got := credentialState(t, ctx, pool, oldID); got != "needs_rotation" {
		t.Fatalf("old credential state = %q, want needs_rotation", got)
	}
	if got := credentialState(t, ctx, pool, freshID); got != "active" {
		t.Fatalf("fresh credential state = %q, want active (untouched)", got)
	}
	if err := store.FlagNeedsRotation(ctx, oldCand); err != nil {
		t.Fatalf("re-flag must be a no-op, got %v", err)
	}
	if got := credentialState(t, ctx, pool, oldID); got != "needs_rotation" {
		t.Fatalf("re-flag changed state to %q, want stable needs_rotation", got)
	}
}

// CRED-288c recovery closure end-to-end against a real Postgres.
//
// 抓什么缺陷:超期但【可刷新】的 OAuth 凭据,经 MarkForRefreshRecovery 后必须保持
// active 在线、refresh_before_at 被拉到 now,且能被既有刷新流(ListAccountsForRefresh)
// 真挑中——这就是"回到 active 且能被刷新"的闭环证据。
//
// 同时验证安全红线:
//   - 静态 api_key 凭据:MarkForRefreshRecovery 仍只动 refresh_before_at 不改 state
//     (scan 层不会把它当可刷新),但端到端里它不应被刷新流选作 OAuth 自愈源。
//   - 已 revoked 的凭据:MarkForRefreshRecovery 的 state='active' 守卫使其为 no-op
//     (绝不把失效凭据拖回刷新流);也不被 ListAccountsForRefresh 选中。
//   - 已 deleted 的凭据:同样 no-op、不被刷新流选中。
func TestPostgresRotationStore_RefreshRecoveryClosure(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialWorkerTestPool(t, ctx)
	defer pool.Close()

	run := time.Now().UnixNano()
	now := time.Now().UTC()
	// 每个凭据各自独立 provider_account(避免 per-(tenant,pa,vendor,auth_mode) 唯一冲突)。
	tOAuth, paOAuth := seedCredentialWorkerProviderAccount(t, ctx, pool, fmt.Sprintf("rec-%d-oauth", run))
	tStatic, paStatic := seedCredentialWorkerProviderAccount(t, ctx, pool, fmt.Sprintf("rec-%d-static", run))
	tRevoked, paRevoked := seedCredentialWorkerProviderAccount(t, ctx, pool, fmt.Sprintf("rec-%d-revoked", run))
	tDeleted, paDeleted := seedCredentialWorkerProviderAccount(t, ctx, pool, fmt.Sprintf("rec-%d-deleted", run))

	old := now.Add(-200 * 24 * time.Hour)
	// OAuth 可刷新凭据:超期、refresh_before_at 一开始为 null(静默老化的主因)。
	oauthID := insertCredentialFull(t, ctx, pool, tOAuth, paOAuth, "oauth", "anthropic", "claude_code", "active", old, nil)
	// 静态 api_key:同样超期。
	staticID := insertCredentialFull(t, ctx, pool, tStatic, paStatic, "static", "anthropic", "api_key", "active", old, nil)
	// 已 revoked 的可刷新凭据:绝不能被恢复回刷新流。
	revokedID := insertCredentialFull(t, ctx, pool, tRevoked, paRevoked, "revoked", "anthropic", "claude_code", "revoked", old, nil)
	// 已 deleted 的可刷新凭据。
	deletedID := insertCredentialFull(t, ctx, pool, tDeleted, paDeleted, "deleted", "anthropic", "claude_code", "active", old, nil)
	if _, err := pool.Exec(ctx, `UPDATE account_credentials SET deleted_at = now() WHERE id = $1`, deletedID); err != nil {
		t.Fatalf("soft-delete credential: %v", err)
	}

	store := NewPostgresRotationStore(pool)
	cutoff := now.Add(-90 * 24 * time.Hour)

	due, err := store.DueForRotation(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("DueForRotation: %v", err)
	}
	dueSet := map[int64]RotationCandidate{}
	for _, c := range due {
		dueSet[c.CredentialID] = c
	}
	// 判别:active 的 oauth/static 超期凭据进扫描;revoked/deleted 不进扫描(state/deleted 守卫)。
	if _, ok := dueSet[oauthID]; !ok {
		t.Fatalf("active aged OAuth credential %d must be due", oauthID)
	}
	if _, ok := dueSet[staticID]; !ok {
		t.Fatalf("active aged static credential %d must be due", staticID)
	}
	if _, ok := dueSet[revokedID]; ok {
		t.Fatalf("revoked credential %d must NOT be due (never auto-touched)", revokedID)
	}
	if _, ok := dueSet[deletedID]; ok {
		t.Fatalf("soft-deleted credential %d must NOT be due", deletedID)
	}

	// 用真实 registry classifier 跑恢复闭环。注:测试库为共享非重置库,DueForRotation
	// 是全局扫描(无 tenant 过滤),处理计数 n 会含其它用例残留行,故不对全局 n 断言,
	// 改为对本用例 seed 的具体凭据断言结果状态/refresh_before_at。limit 设大确保本用例
	// 4 个超期行都被本次扫描覆盖到。
	classifier := DefaultRefreshClassifier()
	if _, err := ScanRotationDue(ctx, store, classifier, nil, 90*24*time.Hour, now, 100000); err != nil {
		t.Fatalf("ScanRotationDue: %v", err)
	}

	// OAuth 可刷新:仍 active(不下线/不 brownout),refresh_before_at 被拉到 ~now。
	if got := credentialState(t, ctx, pool, oauthID); got != "active" {
		t.Fatalf("recovered OAuth credential must stay active (no brownout), got %q", got)
	}
	rb := credentialRefreshBefore(t, ctx, pool, oauthID)
	if rb == nil {
		t.Fatalf("recovery must set refresh_before_at on the OAuth credential, got NULL")
	}
	if rb.After(now.Add(time.Minute)) {
		t.Fatalf("refresh_before_at must be pulled to ~now (due now), got %v (now=%v)", rb, now)
	}

	// 静态 key:scan 不把它当可刷新,故 refresh_before_at 仍为 NULL(没被恢复路径触碰),状态仍 active。
	if got := credentialState(t, ctx, pool, staticID); got != "active" {
		t.Fatalf("static credential must stay active (alert-only), got %q", got)
	}
	if rb := credentialRefreshBefore(t, ctx, pool, staticID); rb != nil {
		t.Fatalf("static key must NOT be refresh-recovered; refresh_before_at must stay NULL, got %v", rb)
	}

	// 闭环关键证据:恢复后的 OAuth 凭据现在能被既有刷新流挑中(refresh_before_at 非空且 due)。
	// provider_account 默认 health_state 必须满足刷新流健康谓词。
	if _, err := pool.Exec(ctx, `UPDATE provider_accounts SET enabled = true, health_state = 'healthy', health_state_until = NULL WHERE id = $1`, paOAuth); err != nil {
		t.Fatalf("normalize provider_account health: %v", err)
	}
	rows, err := NewAccountCredentialRefreshQueries(pool).ListAccountsForRefresh(ctx, dbbilling.ListAccountsForRefreshParams{
		RefreshBefore: pgTimestamptz(now.Add(time.Hour)),
		LimitCount:    1000,
	})
	if err != nil {
		t.Fatalf("ListAccountsForRefresh: %v", err)
	}
	foundOAuth := false
	for _, r := range rows {
		if r.ID == paOAuth {
			foundOAuth = true
		}
		// revoked 账号的凭据绝不能出现在刷新流里。
		if r.ID == paRevoked {
			t.Fatalf("revoked-account credential must never appear in the refresh scan")
		}
	}
	if !foundOAuth {
		t.Fatalf("recovered OAuth credential (pa=%d) must be picked up by the existing refresh flow", paOAuth)
	}

	// TOCTOU 直测(红线 1+4):即便候选在扫描后被并发 revoke/delete,直接对其调用
	// MarkForRefreshRecovery 也必须是 no-op —— 绝不把已失效凭据拖回刷新流。
	// Mutation 注入:删掉 MarkForRefreshRecovery SQL 里的 state='active'/deleted_at
	// 守卫 → revoked/deleted 凭据的 refresh_before_at 被写非空 → 本断言 red。
	revokedCand := RotationCandidate{TenantID: tRevoked, ProviderAccountID: paRevoked, CredentialID: revokedID,
		Vendor: "anthropic", AuthMode: "claude_code"}
	if err := store.MarkForRefreshRecovery(ctx, revokedCand, now); err != nil {
		t.Fatalf("MarkForRefreshRecovery on revoked: %v", err)
	}
	if rb := credentialRefreshBefore(t, ctx, pool, revokedID); rb != nil {
		t.Fatalf("revoked credential must stay untouched (refresh_before_at NULL), got %v", rb)
	}
	if got := credentialState(t, ctx, pool, revokedID); got != "revoked" {
		t.Fatalf("revoked credential state must stay revoked, got %q", got)
	}

	deletedCand := RotationCandidate{TenantID: tDeleted, ProviderAccountID: paDeleted, CredentialID: deletedID,
		Vendor: "anthropic", AuthMode: "claude_code"}
	if err := store.MarkForRefreshRecovery(ctx, deletedCand, now); err != nil {
		t.Fatalf("MarkForRefreshRecovery on deleted: %v", err)
	}
	if rb := credentialRefreshBefore(t, ctx, pool, deletedID); rb != nil {
		t.Fatalf("soft-deleted credential must stay untouched (refresh_before_at NULL), got %v", rb)
	}
}

// TestPostgresRotationStore_KeysOnLastRefreshNotCreatedAt 证明 DueForRotation 按
// COALESCE(last_refresh_at, created_at) 判超期,而非裸 created_at。这是 CRED-288 扫描
// 默认开启后【不造成刷新风暴】的关键:一把签发很久、但最近刚成功刷新过的凭据绝不能
// 被判为超期——否则恢复闭环会每个扫描 tick 把它的 refresh_before_at 反复拉到 now、
// 反复强刷,锤爆上游 OAuth 端点(created_at 永不变,裸按它判会永远 due)。
//
// 变异:把 DueForRotation 的 WHERE/SELECT 改回裸 created_at,refreshed 凭据被错误判 due
// → 第一个 t.Fatalf 变红。该 fixture 对"按 created_at 还是按 last_refresh"有判别力。
func TestPostgresRotationStore_KeysOnLastRefreshNotCreatedAt(t *testing.T) {
	ctx := context.Background()
	pool := openCredentialWorkerTestPool(t, ctx)
	defer pool.Close()

	run := time.Now().UnixNano()
	tA, paA := seedCredentialWorkerProviderAccount(t, ctx, pool, fmt.Sprintf("rot288d-%d-refreshed", run))
	tB, paB := seedCredentialWorkerProviderAccount(t, ctx, pool, fmt.Sprintf("rot288d-%d-stale", run))
	now := time.Now().UTC()

	// A:签发 200 天前,但 last_refresh_at=1 小时前(最近刚刷过)→ 按有效刷新时间应【不超期】。
	refreshedID := insertCredentialWithCreatedAt(t, ctx, pool, tA, paA, "refreshed", now.Add(-200*24*time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE account_credentials SET last_refresh_at = $1 WHERE id = $2`,
		now.Add(-1*time.Hour), refreshedID); err != nil {
		t.Fatalf("set last_refresh_at: %v", err)
	}
	// B:签发 200 天前,last_refresh_at=NULL(从没刷过)→ COALESCE 回退按 created_at,应【超期】。
	staleID := insertCredentialWithCreatedAt(t, ctx, pool, tB, paB, "stale", now.Add(-200*24*time.Hour))

	store := NewPostgresRotationStore(pool)
	cutoff := now.Add(-90 * 24 * time.Hour)
	due, err := store.DueForRotation(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("DueForRotation: %v", err)
	}
	seen := map[int64]bool{}
	for _, c := range due {
		seen[c.CredentialID] = true
	}
	if seen[refreshedID] {
		t.Fatalf("1 小时前刚刷新过的凭据 %d 绝不能判为超期(否则默认开启后每 tick 强刷=刷新风暴锤上游)", refreshedID)
	}
	if !seen[staleID] {
		t.Fatalf("从没刷新过(last_refresh_at NULL)、签发 200 天的凭据 %d 必须按 created_at 回退判为超期", staleID)
	}
}
