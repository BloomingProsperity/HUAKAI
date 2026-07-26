package autolisting

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/accountmodeldiscovery"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// AccountSyncer 按账号真实凭据发现上游模型并投进发现箱(由 accountmodeldiscovery.Service 实现)。
type AccountSyncer interface {
	Sync(context.Context, accountmodeldiscovery.SyncInput) (accountmodeldiscovery.SyncResult, error)
}

// AccountRefresher 定时保鲜反转号车道:官方 API-key 车道由 modelsync 定时 fetcher 覆盖,
// 但 oauth/session 反转号(claude session / codex / code assist / antigravity / grok·kimi oauth)
// 不在其内。这里枚举这些账号逐个走账号级发现,新模型经发现投箱进入上架管道。
type AccountRefresher struct {
	pool  *pgxpool.Pool
	sync  AccountSyncer
	actor string
}

func NewAccountRefresher(pool *pgxpool.Pool, syncer AccountSyncer) *AccountRefresher {
	return &AccountRefresher{pool: pool, sync: syncer, actor: defaultActor}
}

// RefreshResult 汇总一轮保鲜结果供观测。
type RefreshResult struct {
	Accounts int
	OK       int
	Failed   int
	Invested int
}

type refreshTarget struct {
	tenantID  int64
	accountID int64
}

// RefreshReversedAccounts 遍历所有存活的 oauth 反转号,逐个刷新模型目录。单账号失败(如上游
// 429/凭据失效)只记高辨识度日志并继续,不拖垮整轮。返回聚合结果。
func (r *AccountRefresher) RefreshReversedAccounts(ctx context.Context) (RefreshResult, error) {
	if r == nil || r.pool == nil || r.sync == nil {
		return RefreshResult{}, fmt.Errorf("autolisting: account refresher not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := r.pool.Query(ctx, `
SELECT pa.tenant_id, pa.id
FROM provider_accounts pa
JOIN tenants t
  ON t.id = pa.tenant_id
 AND t.status = 'active'
 AND t.deleted_at IS NULL
JOIN providers p ON p.id = pa.provider_id AND p.tenant_id = pa.tenant_id
WHERE pa.account_type = 'oauth'
  AND pa.enabled = true
  AND pa.deleted_at IS NULL
  AND p.deleted_at IS NULL
ORDER BY pa.tenant_id, pa.id`)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("autolisting: list reversed accounts: %w", err)
	}
	targets := make([]refreshTarget, 0)
	for rows.Next() {
		var t refreshTarget
		if err := rows.Scan(&t.tenantID, &t.accountID); err != nil {
			rows.Close()
			return RefreshResult{}, fmt.Errorf("autolisting: scan reversed account: %w", err)
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return RefreshResult{}, fmt.Errorf("autolisting: reversed account rows: %w", err)
	}

	result := RefreshResult{Accounts: len(targets)}
	for _, t := range targets {
		syncResult, err := r.sync.Sync(ctx, accountmodeldiscovery.SyncInput{
			TenantID:  t.tenantID,
			AccountID: t.accountID,
			ActorID:   r.actor,
			ActorRole: admin.RolePlatformAdmin,
			Reason:    "auto-listing scheduled refresh",
		})
		if err != nil {
			result.Failed++
			slog.WarnContext(ctx, "auto-listing account refresh failed",
				"component", "auto_listing_worker",
				"event_class", "account_refresh_failed",
				"tenant_id", t.tenantID,
				"account_id", t.accountID,
				"error", err.Error())
			continue
		}
		result.OK++
		result.Invested += syncResult.InboxInvested
	}
	return result, nil
}
