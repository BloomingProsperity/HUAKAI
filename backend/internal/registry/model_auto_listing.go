package registry

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// AutoBindModelToEligiblePoolsTx 把一个已上架的 model 自动绑到"能提供它"的全部 pool_group
// (上架管道第 4 关)。判据镜像 pool_accounts.sql 的选号资格谓词(取其持久子集):
//   - provider.upstream_protocol = model.protocol_family(该协议可派发此模型);
//   - 账号白名单放行:空数组=不限,非空=必须包含 provider_model_id;
//   - 账号/渠道/provider 均 enabled 且未软删、账号未过期、且存在至少一条可服务凭据
//     (account_credentials active 或 grace 未过期)——即"有永久可服务潜力"的账号。
//
// 刻意【不】看 health_state:健康是运行期瞬态开关(429 冷却/抖动),绑定是持久路由配置,
// 不应随健康抖动增删;瞬态不健康账号由 selector 选号时过滤。但永久不可服务的账号(停用、
// 过期、无有效凭据)不触发绑定,否则会建出"目录里有、选号必落空"的坏绑定。按 pool_group
// 所属租户建 binding;已存在则跳过(ON CONFLICT DO NOTHING);无合格账号则不建空绑定。
// 返回新建条数,并在同一事务内 bump 受影响租户快照版本(resolver 靠版本做时点一致读)。
func (r *PostgresRegistry) AutoBindModelToEligiblePoolsTx(ctx context.Context, tx pgx.Tx, modelID int64, protocolFamily, providerModelID, actor, reason string) (int, error) {
	if tx == nil {
		return 0, ErrRegistryBackend
	}
	protocolFamily = strings.TrimSpace(protocolFamily)
	providerModelID = strings.TrimSpace(providerModelID)
	if modelID <= 0 || protocolFamily == "" || providerModelID == "" {
		return 0, ErrModelDiscoveryInvalid
	}

	rows, err := tx.Query(ctx, `
INSERT INTO model_pool_bindings (tenant_id, model_id, pool_group_id, reason)
SELECT DISTINCT pa.tenant_id, $1::bigint, c.pool_group_id, $4
FROM provider_accounts pa
JOIN providers p
  ON p.id = pa.provider_id AND p.tenant_id = pa.tenant_id
JOIN channels c
  ON c.id = pa.channel_id AND c.tenant_id = pa.tenant_id
JOIN pool_groups pg
  ON pg.id = c.pool_group_id AND pg.tenant_id = c.tenant_id
WHERE p.upstream_protocol = $2
  AND p.enabled = true
  AND p.deleted_at IS NULL
  AND c.enabled = true
  AND c.deleted_at IS NULL
  AND pg.deleted_at IS NULL
  AND pa.enabled = true
  AND pa.deleted_at IS NULL
  AND (pa.expires_at IS NULL OR pa.expires_at > NOW())
  AND (cardinality(pa.model_allow_list) = 0 OR pa.model_allow_list @> ARRAY[$3::text])
  AND EXISTS (
      SELECT 1 FROM account_credentials ac
      WHERE ac.provider_account_id = pa.id
        AND ac.tenant_id = pa.tenant_id
        AND ac.deleted_at IS NULL
        AND (
            ac.state = 'active'
            OR (ac.state = 'refreshing_with_grace' AND (ac.grace_until IS NULL OR ac.grace_until > NOW()))
        )
  )
ON CONFLICT (tenant_id, model_id, pool_group_id) WHERE deleted_at IS NULL
DO NOTHING
RETURNING tenant_id
`, modelID, protocolFamily, providerModelID, bindingReason(reason, "auto_bind"))
	if err != nil {
		return 0, fmt.Errorf("%w: auto-bind model to pools: %w", ErrRegistryBackend, err)
	}
	bound := 0
	for rows.Next() {
		var tenantID int64
		if err := rows.Scan(&tenantID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("%w: scan auto-bind result: %w", ErrRegistryBackend, err)
		}
		bound++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("%w: auto-bind result rows: %w", ErrRegistryBackend, err)
	}
	if bound == 0 {
		return 0, nil
	}
	if _, err := bumpAffectedSnapshots(ctx, tx, []int64{modelID}, bindingReason(reason, "auto_bind"), bindingActor(actor)); err != nil {
		return bound, err
	}
	return bound, nil
}
