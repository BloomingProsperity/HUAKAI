// HUAKAI · iKun

package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrUnknownTenant 表示目标 tenant 不存在 (model_registry_tenant_policies.tenant_id FK 违反)。
var ErrUnknownTenant = errors.New("registry: unknown tenant")

// TenantPolicy 是 model_registry_tenant_policies 的管理视图(每租户一行的目录策略)。
type TenantPolicy struct {
	TenantID             int64
	InheritGlobalCatalog bool
	UpdatedAt            time.Time
	UpdatedByActor       string
}

// GetTenantPolicy 读一个租户的目录继承策略。无策略行 = 不继承(与 ResolveModel 的 "no policy row = no
// inheritance" 一致, postgres_registry.go:GetTenantInheritGlobal 路径), 故无行返回默认(InheritGlobalCatalog=false)
// 而非 ErrNoRows —— 让 admin GET 总能读到"当前生效值"(默认 false)。
func (r *PostgresRegistry) GetTenantPolicy(ctx context.Context, tenantID int64) (TenantPolicy, error) {
	if r == nil || r.pool == nil {
		return TenantPolicy{}, ErrRegistryBackend
	}
	var p TenantPolicy
	var actor *string
	err := r.pool.QueryRow(ctx, `
SELECT tenant_id, inherit_global_catalog, updated_at, updated_by_actor
FROM model_registry_tenant_policies
WHERE tenant_id = $1`, tenantID).Scan(&p.TenantID, &p.InheritGlobalCatalog, &p.UpdatedAt, &actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return TenantPolicy{TenantID: tenantID, InheritGlobalCatalog: false}, nil
	}
	if err != nil {
		return TenantPolicy{}, fmt.Errorf("%w: get tenant policy: %v", ErrRegistryBackend, err)
	}
	if actor != nil {
		p.UpdatedByActor = *actor
	}
	return p, nil
}

// SetTenantInheritGlobal upsert 一个租户的 inherit_global_catalog 闸, 并在同一 Tx 内 bump 该租户的 registry
// 快照版本 —— model_registry_snapshots 的表注释要求: 凡改变租户可见目录的 admin 写都必须在同 TX 内 version+1
// (client 按 SnapshotVersion=registry:<tid>:<v> 缓存 /v1/models, 改继承策略改了可见模型集, 须失效)。
// 改 inherit 直接改 ResolveModel 的 global 回落 + /v1/models discovery 的 live JOIN 过滤, 下次查询即生效。
// 目标 tenant 不存在 → FK 违反 → ErrUnknownTenant(映射 4xx, 非 503)。actor 取自已认证身份, 仅作审计归属。
func (r *PostgresRegistry) SetTenantInheritGlobal(ctx context.Context, tenantID int64, inherit bool, actor string) (TenantPolicy, error) {
	if r == nil || r.pool == nil {
		return TenantPolicy{}, ErrRegistryBackend
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return TenantPolicy{}, fmt.Errorf("%w: begin set tenant policy: %v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var p TenantPolicy
	var actorOut *string
	err = tx.QueryRow(ctx, `
INSERT INTO model_registry_tenant_policies (tenant_id, inherit_global_catalog, updated_by_actor, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (tenant_id) DO UPDATE SET
    inherit_global_catalog = EXCLUDED.inherit_global_catalog,
    updated_by_actor = EXCLUDED.updated_by_actor,
    updated_at = now()
RETURNING tenant_id, inherit_global_catalog, updated_at, updated_by_actor`,
		tenantID, inherit, nullableActor(actor)).Scan(&p.TenantID, &p.InheritGlobalCatalog, &p.UpdatedAt, &actorOut)
	if err != nil {
		if isForeignKeyViolation(err) {
			return TenantPolicy{}, ErrUnknownTenant
		}
		return TenantPolicy{}, fmt.Errorf("%w: upsert tenant policy: %v", ErrRegistryBackend, err)
	}
	if actorOut != nil {
		p.UpdatedByActor = *actorOut
	}

	// 单租户快照版本 bump(镜像 model_sync_writer.bumpAffectedSnapshots 的 version 语义: 首写 2, 冲突 +1)。
	if _, err := tx.Exec(ctx, `
INSERT INTO model_registry_snapshots (tenant_id, version, reason, updated_by_actor)
VALUES ($1, 2, $2, $3)
ON CONFLICT (tenant_id) DO UPDATE SET
    version = model_registry_snapshots.version + 1,
    reason = EXCLUDED.reason,
    updated_by_actor = EXCLUDED.updated_by_actor,
    updated_at = now()`,
		tenantID, "tenant_policy:inherit_global_catalog", nullableActor(actor)); err != nil {
		return TenantPolicy{}, fmt.Errorf("%w: bump tenant snapshot: %v", ErrRegistryBackend, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantPolicy{}, fmt.Errorf("%w: commit set tenant policy: %v", ErrRegistryBackend, err)
	}
	return p, nil
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// nullableActor 把空 actor 转成 SQL NULL(updated_by_actor 列允许 NULL), 避免存空串。
func nullableActor(actor string) any {
	if actor == "" {
		return nil
	}
	return actor
}
