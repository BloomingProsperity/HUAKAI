//go:build integration_pg

package billing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestListEligibleAccountsByPoolGroupFiltersExpiredAccounts 验证选号候选的到期语义。
// 变异:删除 expires_at WHERE 谓词后,过去已到期账号会重新出现在候选中,
// “已过期账号”子测试必须变红;NULL 到期账号用于防止过滤条件误伤永久账号。
func TestListEligibleAccountsByPoolGroupFiltersExpiredAccounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启账号候选测试事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	suffix := fmt.Sprintf("eligible-expiry-%d", time.Now().UnixNano())
	var tenantID, providerID, poolGroupID, channelID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"tenant-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, 'anthropic_messages') RETURNING id`,
		tenantID, "provider-"+suffix, "Provider "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("插入 pool group: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("插入 channel: %v", err)
	}

	insertAccount := func(name string, expiresAt any) int64 {
		t.Helper()
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO provider_accounts (
				tenant_id, provider_id, channel_id, name, account_type,
				health_state, credential_state, expires_at
			)
			VALUES ($1, $2, $3, $4, 'api_key', 'healthy', 'valid', $5)
			RETURNING id`,
			tenantID, providerID, channelID, name+"-"+suffix, expiresAt,
		).Scan(&id); err != nil {
			t.Fatalf("插入账号 %s: %v", name, err)
		}
		// 选号凭据真相门要求账号至少有一条可服务凭据;给每个账号种一条 active 凭据,
		// 使本测试聚焦到期语义(过期账号仍应被 expires_at 排除,与凭据存在无关)。
		insertServableCredential(t, ctx, tx, tenantID, id, "active")
		return id
	}

	expiredID := insertAccount("expired", time.Now().UTC().Add(-time.Hour))
	withoutExpiryID := insertAccount("without-expiry", nil)

	rows, err := New(tx).ListEligibleAccountsByPoolGroup(ctx, ListEligibleAccountsByPoolGroupParams{
		TenantID:                tenantID,
		PoolGroupID:             poolGroupID,
		RequestedModel:          "claude-test",
		RequestedProtocolFamily: "anthropic_messages",
		RequiredCapabilities:    []string{},
	})
	if err != nil {
		t.Fatalf("查询账号候选: %v", err)
	}
	returned := make(map[int64]bool, len(rows))
	for _, row := range rows {
		returned[row.ID] = true
	}

	t.Run("已过期账号不在选号结果中", func(t *testing.T) {
		if returned[expiredID] {
			t.Fatalf("已过期账号 id=%d 仍出现在选号候选中", expiredID)
		}
	})
	t.Run("NULL 到期时间账号仍在选号结果中", func(t *testing.T) {
		if !returned[withoutExpiryID] {
			t.Fatalf("未设过期时间的账号 id=%d 被错误排除", withoutExpiryID)
		}
	})
}

// TestListEligibleAccountsRequireServableCredential 守卫「凭据真相门」:选号只放至少有一条
// 可服务凭据(account_credentials.state=active / grace 未过期)的账号,读真相列而非冻死的
// provider_accounts.credential_state。变异:删掉两个查询的 EXISTS 谓词后,空壳账号(0 凭据)
// 与仅有死凭据(revoked)的账号会重新进选号候选,本测试变红——正是「credential_state 脱钩 +
// 空壳账号进池」的守卫。两个查询(渠道键 ListEligibleAccounts + 池组键
// ListEligibleAccountsByPoolGroup)都覆盖。
func TestListEligibleAccountsRequireServableCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	suffix := fmt.Sprintf("cred-gate-%d", time.Now().UnixNano())
	var tenantID, providerID, poolGroupID, channelID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "tenant-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, 'anthropic_messages') RETURNING id`,
		tenantID, "provider-"+suffix, "Provider "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, "pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("插入 pool group: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, tenantID, poolGroupID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("插入 channel: %v", err)
	}

	insertAccount := func(name string) int64 {
		t.Helper()
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO provider_accounts (
				tenant_id, provider_id, channel_id, name, account_type, health_state, credential_state
			) VALUES ($1, $2, $3, $4, 'api_key', 'healthy', 'valid') RETURNING id`,
			tenantID, providerID, channelID, name+"-"+suffix,
		).Scan(&id); err != nil {
			t.Fatalf("插入账号 %s: %v", name, err)
		}
		return id
	}

	// 三种账号:活凭据(应选)/ 零凭据空壳(不应选)/ 仅死凭据 revoked(不应选)。
	// 注意:三者的冻死列 provider_accounts.credential_state 全是 'valid' —— 旧过滤放行全部,
	// 是本测试的区分度来源(证明我们读的是真相 account_credentials.state,不是那列)。
	liveID := insertAccount("live")
	insertServableCredential(t, ctx, tx, tenantID, liveID, "active")
	shellID := insertAccount("shell") // 0 凭据
	deadID := insertAccount("dead")
	insertServableCredential(t, ctx, tx, tenantID, deadID, "revoked")

	assertGate := func(t *testing.T, label string, returned map[int64]bool) {
		t.Helper()
		if !returned[liveID] {
			t.Fatalf("%s: 活凭据账号 id=%d 被错误排除", label, liveID)
		}
		if returned[shellID] {
			t.Fatalf("%s: 空壳账号(0 凭据) id=%d 进入选号候选", label, shellID)
		}
		if returned[deadID] {
			t.Fatalf("%s: 仅死凭据(revoked)账号 id=%d 进入选号候选", label, deadID)
		}
	}

	byGroup, err := New(tx).ListEligibleAccountsByPoolGroup(ctx, ListEligibleAccountsByPoolGroupParams{
		TenantID: tenantID, PoolGroupID: poolGroupID, RequestedModel: "claude-test",
		RequestedProtocolFamily: "anthropic_messages", RequiredCapabilities: []string{},
	})
	if err != nil {
		t.Fatalf("ListEligibleAccountsByPoolGroup: %v", err)
	}
	groupReturned := make(map[int64]bool, len(byGroup))
	for _, row := range byGroup {
		groupReturned[row.ID] = true
	}
	assertGate(t, "ByPoolGroup", groupReturned)

	byChannel, err := New(tx).ListEligibleAccounts(ctx, ListEligibleAccountsParams{
		TenantID: tenantID, ChannelID: channelID,
	})
	if err != nil {
		t.Fatalf("ListEligibleAccounts: %v", err)
	}
	channelReturned := make(map[int64]bool, len(byChannel))
	for _, row := range byChannel {
		channelReturned[row.ID] = true
	}
	assertGate(t, "ByChannel", channelReturned)
}

// insertServableCredential 给账号种一条指定 state 的凭据(用于选号凭据真相门测试)。
func insertServableCredential(t *testing.T, ctx context.Context, tx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, tenantID, accountID int64, state string) {
	t.Helper()
	if _, err := tx.Exec(ctx, `
		INSERT INTO account_credentials (
			tenant_id, provider_account_id, vendor, auth_mode, state, credential_version,
			encrypted_payload, key_id, nonce, aad_hash
		) VALUES ($1, $2, 'anthropic', 'api_key', $3, 1, $4, 'test-key', $5, $6)`,
		tenantID, accountID, state,
		[]byte("ciphertext"), []byte("nonce-12345678"),
		fmt.Sprintf("aad-%d-%s", accountID, state),
	); err != nil {
		t.Fatalf("插入凭据 account=%d state=%s: %v", accountID, state, err)
	}
}

// TestListEligibleAccountsMediaRequireModelListed 守卫「媒体端点族清单门」:
// require_model_listed=true(媒体族)时账号必须在 model_allow_list 显式列出请求模型,
// 空清单的"无限制" bypass 不适用;require_model_listed=false(chat 族)保持空清单=
// 无限制的历史语义。变异:删掉查询末尾的 require_model_listed 谓词后,"空清单媒体不选"
// 子测试变红——正是「媒体打到不含该模型的账号→上游终态 4xx 不换号」的选号前守卫。
func TestListEligibleAccountsMediaRequireModelListed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	suffix := fmt.Sprintf("media-listed-%d", time.Now().UnixNano())
	var tenantID, providerID, poolGroupID, channelID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "tenant-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, "provider-"+suffix, "Provider "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, "pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("插入 pool group: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, tenantID, poolGroupID, "channel-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("插入 channel: %v", err)
	}

	insertAccount := func(name string, allowList []string) int64 {
		t.Helper()
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO provider_accounts (
				tenant_id, provider_id, channel_id, name, account_type,
				health_state, credential_state, model_allow_list
			)
			VALUES ($1, $2, $3, $4, 'api_key', 'healthy', 'valid', $5)
			RETURNING id`,
			tenantID, providerID, channelID, name+"-"+suffix, allowList,
		).Scan(&id); err != nil {
			t.Fatalf("插入账号 %s: %v", name, err)
		}
		insertServableCredential(t, ctx, tx, tenantID, id, "active")
		return id
	}

	emptyListID := insertAccount("empty-list", []string{})
	listedID := insertAccount("listed", []string{"dall-e-3", "gpt-image-1"})
	otherListID := insertAccount("other-list", []string{"gpt-4o"})

	query := func(requireListed bool) map[int64]bool {
		t.Helper()
		rows, err := New(tx).ListEligibleAccountsByPoolGroup(ctx, ListEligibleAccountsByPoolGroupParams{
			TenantID:                tenantID,
			PoolGroupID:             poolGroupID,
			RequestedModel:          "dall-e-3",
			RequestedProtocolFamily: "openai_chat",
			RequiredCapabilities:    []string{},
			RequireModelListed:      requireListed,
		})
		if err != nil {
			t.Fatalf("查询账号候选(requireListed=%v): %v", requireListed, err)
		}
		returned := make(map[int64]bool, len(rows))
		for _, row := range rows {
			returned[row.ID] = true
		}
		return returned
	}

	chat := query(false)
	media := query(true)

	t.Run("chat 族空清单保持无限制", func(t *testing.T) {
		if !chat[emptyListID] || !chat[listedID] {
			t.Fatalf("chat 族候选=%v,空清单与已列账号都应在", chat)
		}
		if chat[otherListID] {
			t.Fatalf("清单不含请求模型的账号 id=%d 不应入选(既有清单门)", otherListID)
		}
	})
	t.Run("媒体族空清单不选", func(t *testing.T) {
		if media[emptyListID] {
			t.Fatalf("空清单账号 id=%d 出现在媒体候选中,媒体族必须显式列出模型", emptyListID)
		}
	})
	t.Run("媒体族显式列出才选", func(t *testing.T) {
		if !media[listedID] {
			t.Fatalf("清单含 dall-e-3 的账号 id=%d 应入选媒体候选", listedID)
		}
		if media[otherListID] {
			t.Fatalf("清单不含请求模型的账号 id=%d 不应入选媒体候选", otherListID)
		}
	})
}
