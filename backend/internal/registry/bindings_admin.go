// model_pool_bindings(租户域的 模型→pool 路由绑定)的 admin 写入面。
// 读路径(ListModelPoolBindings)是 sqlc 生成、且仅供路由用;本文件补上缺失的
// 运维 CRUD —— 列和 resolver 早就有,却没有 admin 写入路径(典型的"能力建了却够不着"
// inert gap)。
//
// 写法:用 pgx.Tx 裸内联 SQL,贴合本包既有写方法(model_alias_import.go /
// model_sync_writer.go)。刻意【不】走 sqlc 查询:绑定写入 + snapshot 版本 bump
// 必须共用同一个 Serializable Tx,而保持裸 SQL 还能让 registry.sql 不被触碰。
//
// 头号不变量:每次 mutation 都在【同一个 Tx】里 bump model_registry_snapshots.version
//(schema 0008 钦定;resolver 靠该 version 做时点一致读)。INSERT/UPDATE 复用
// bumpAffectedSnapshots(其 CTE 命中存活行)。DELETE【不能】用那个 CTE —— 软删后行的
// deleted_at 非空,CTE 会漏掉它;若删的是某租户某 model 的最后一条存活绑定,版本就不会
// 前进。故 DELETE 先抓 tenant_id,再对该单租户直接 bump。
//
// 租户交叉校验:绑定可指向 租户自有 model 或 全局 model(继承路径);谓词照
// model_alias_import.go(全局 model 的 tenant_id 为 NULL,简单的 tenant_id=$ 会误拒)。

package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 哨兵错误,由 admin handler 映射成 HTTP 状态码。
var (
	ErrBindingNotFound   = errors.New("registry: model pool binding not found")
	ErrModelNotBindable  = errors.New("registry: model not found or not bindable by tenant")
	ErrPoolGroupNotFound = errors.New("registry: pool group not found for tenant")
	ErrBindingConflict   = errors.New("registry: binding already exists for (tenant, model, pool)")
)

// AdminBinding 是 model_pool_bindings 行的完整可编辑投影。区别于路由读
// (ListModelPoolBindings):它暴露 disabled / 未生效 / 已过期 的行以及仅编辑用的列,
// 因为运维需要管理它们。
type AdminBinding struct {
	ID                      int64
	TenantID                int64
	ModelID                 int64
	PoolGroupID             int64
	Priority                int32
	Weight                  int32 // 仅存储兼容，无运行时消费，UI 已不暴露。
	SelectionMode           string
	ProviderModelIDOverride *string
	RPMLimit                *int32
	TPMLimit                *int32
	MaxParallelRequests     *int32 // binding 全局在途上限；nil 或 0 表示不限。
	FallbackClass           string // Router 编译 normal 主 phase 与定向目标 phase；executor 尚未消费目标 phase。
	Enabled                 bool
	DisabledReason          *string
	EffectiveFrom           *time.Time
	EffectiveUntil          *time.Time
	Reason                  string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// CreateBindingInput 是创建载荷。ModelID + PoolGroupID 定义绑定身份(租户内唯一);
// Update 不能改它们。
type CreateBindingInput struct {
	TenantID                int64
	ModelID                 int64
	PoolGroupID             int64
	Priority                int32
	Weight                  int32 // 仅存储兼容，无运行时消费，UI 已不暴露。
	SelectionMode           string
	ProviderModelIDOverride *string
	RPMLimit                *int32
	TPMLimit                *int32
	MaxParallelRequests     *int32 // binding 全局在途上限；nil 或 0 表示不限。
	FallbackClass           string // Router 编译 normal 主 phase 与定向目标 phase；executor 尚未消费目标 phase。
	Enabled                 bool
	DisabledReason          *string
	EffectiveFrom           *time.Time
	EffectiveUntil          *time.Time
	Reason                  string
	Actor                   string
}

// UpdateBindingInput 更新单条绑定的可调字段(按 id + 租户域)。ModelID / PoolGroupID
// 【不可】更新 —— 改身份请走 删+建,顺便也绕开了唯一冲突这一情形。
type UpdateBindingInput struct {
	ID                      int64
	TenantID                int64
	Priority                int32
	Weight                  int32 // 仅存储兼容，无运行时消费，UI 已不暴露。
	SelectionMode           string
	ProviderModelIDOverride *string
	RPMLimit                *int32
	TPMLimit                *int32
	MaxParallelRequests     *int32 // binding 全局在途上限；nil 或 0 表示不限。
	FallbackClass           string // Router 编译 normal 主 phase 与定向目标 phase；executor 尚未消费目标 phase。
	Enabled                 bool
	DisabledReason          *string
	EffectiveFrom           *time.Time
	EffectiveUntil          *time.Time
	Reason                  string
	Actor                   string
}

const adminBindingCols = `id, tenant_id, model_id, pool_group_id, priority, weight,
	selection_mode, provider_model_id_override, rpm_limit, tpm_limit,
	max_parallel_requests, fallback_class, enabled, disabled_reason,
	effective_from, effective_until, reason, created_at, updated_at`

func scanAdminBinding(row pgx.Row) (AdminBinding, error) {
	var b AdminBinding
	err := row.Scan(
		&b.ID, &b.TenantID, &b.ModelID, &b.PoolGroupID, &b.Priority, &b.Weight,
		&b.SelectionMode, &b.ProviderModelIDOverride, &b.RPMLimit, &b.TPMLimit,
		&b.MaxParallelRequests, &b.FallbackClass, &b.Enabled, &b.DisabledReason,
		&b.EffectiveFrom, &b.EffectiveUntil, &b.Reason, &b.CreatedAt, &b.UpdatedAt,
	)
	return b, err
}

// ListPoolBindingsAdmin 返回某租户全部未软删的绑定(【不】按 enabled / 时间窗过滤 ——
// 运维必须看到 disabled、未生效、已过期 的行)。modelID / poolGroupID 为可选过滤。
func (r *PostgresRegistry) ListPoolBindingsAdmin(ctx context.Context, tenantID int64, modelID, poolGroupID *int64) ([]AdminBinding, error) {
	if r == nil || r.pool == nil {
		return nil, ErrRegistryBackend
	}
	rows, err := r.pool.Query(ctx, `
SELECT `+adminBindingCols+`
FROM model_pool_bindings
WHERE tenant_id = $1
  AND deleted_at IS NULL
  AND ($2::bigint IS NULL OR model_id = $2)
  AND ($3::bigint IS NULL OR pool_group_id = $3)
ORDER BY model_id ASC, priority ASC, id ASC`, tenantID, modelID, poolGroupID)
	if err != nil {
		return nil, fmt.Errorf("%w: list admin bindings: %v", ErrRegistryBackend, err)
	}
	defer rows.Close()
	out := make([]AdminBinding, 0, 16)
	for rows.Next() {
		b, err := scanAdminBinding(rows)
		if err != nil {
			return nil, fmt.Errorf("%w: scan admin binding: %v", ErrRegistryBackend, err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate admin bindings: %v", ErrRegistryBackend, err)
	}
	return out, nil
}

// GetPoolBindingByID 返回一条绑定,按租户域(tenant 谓词在【查询里】,不只在门上 ——
// 跨租户 id 读不到)。
func (r *PostgresRegistry) GetPoolBindingByID(ctx context.Context, id, tenantID int64) (AdminBinding, error) {
	if r == nil || r.pool == nil {
		return AdminBinding{}, ErrRegistryBackend
	}
	b, err := scanAdminBinding(r.pool.QueryRow(ctx, `
SELECT `+adminBindingCols+`
FROM model_pool_bindings
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminBinding{}, ErrBindingNotFound
	}
	if err != nil {
		return AdminBinding{}, fmt.Errorf("%w: get admin binding: %v", ErrRegistryBackend, err)
	}
	return b, nil
}

// CreatePoolBinding 在一个 Serializable Tx 内插入绑定并 bump registry 快照。先校验
// model 可绑(租户自有或全局)+ pool_group 归属,给出友好 4xx 而非裸 FK 500。
func (r *PostgresRegistry) CreatePoolBinding(ctx context.Context, in CreateBindingInput) (AdminBinding, error) {
	if r == nil || r.pool == nil {
		return AdminBinding{}, ErrRegistryBackend
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AdminBinding{}, fmt.Errorf("%w: begin create binding: %v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := checkModelBindable(ctx, tx, in.ModelID, in.TenantID); err != nil {
		return AdminBinding{}, err
	}
	if err := checkPoolGroupOwned(ctx, tx, in.PoolGroupID, in.TenantID); err != nil {
		return AdminBinding{}, err
	}

	b, err := scanAdminBinding(tx.QueryRow(ctx, `
INSERT INTO model_pool_bindings
	(tenant_id, model_id, pool_group_id, priority, weight, selection_mode,
	 provider_model_id_override, rpm_limit, tpm_limit, max_parallel_requests,
	 fallback_class, enabled, disabled_reason, effective_from, effective_until, reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
RETURNING `+adminBindingCols,
		in.TenantID, in.ModelID, in.PoolGroupID, in.Priority, in.Weight, in.SelectionMode,
		in.ProviderModelIDOverride, in.RPMLimit, in.TPMLimit, in.MaxParallelRequests,
		in.FallbackClass, in.Enabled, in.DisabledReason, in.EffectiveFrom, in.EffectiveUntil, in.Reason,
	))
	if isUniqueViolation(err) {
		return AdminBinding{}, ErrBindingConflict
	}
	if err != nil {
		return AdminBinding{}, fmt.Errorf("%w: insert binding: %v", ErrRegistryBackend, err)
	}

	if _, err := bumpAffectedSnapshots(ctx, tx, []int64{in.ModelID}, bindingReason(in.Reason, "create"), bindingActor(in.Actor)); err != nil {
		return AdminBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminBinding{}, fmt.Errorf("%w: commit create binding: %v", ErrRegistryBackend, err)
	}
	return b, nil
}

// UpdatePoolBinding 更新可调字段(id + 租户域)并在同一 Tx 内 bump 快照。
// model_id / pool_group_id 在此不可变。
func (r *PostgresRegistry) UpdatePoolBinding(ctx context.Context, in UpdateBindingInput) (AdminBinding, error) {
	if r == nil || r.pool == nil {
		return AdminBinding{}, ErrRegistryBackend
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AdminBinding{}, fmt.Errorf("%w: begin update binding: %v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	b, err := scanAdminBinding(tx.QueryRow(ctx, `
UPDATE model_pool_bindings SET
	priority = $3, weight = $4, selection_mode = $5, provider_model_id_override = $6,
	rpm_limit = $7, tpm_limit = $8, max_parallel_requests = $9, fallback_class = $10,
	enabled = $11, disabled_reason = $12, effective_from = $13, effective_until = $14,
	reason = $15, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
RETURNING `+adminBindingCols,
		in.ID, in.TenantID, in.Priority, in.Weight, in.SelectionMode, in.ProviderModelIDOverride,
		in.RPMLimit, in.TPMLimit, in.MaxParallelRequests, in.FallbackClass,
		in.Enabled, in.DisabledReason, in.EffectiveFrom, in.EffectiveUntil, in.Reason,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminBinding{}, ErrBindingNotFound
	}
	if err != nil {
		return AdminBinding{}, fmt.Errorf("%w: update binding: %v", ErrRegistryBackend, err)
	}

	if _, err := bumpAffectedSnapshots(ctx, tx, []int64{b.ModelID}, bindingReason(in.Reason, "update"), bindingActor(in.Actor)); err != nil {
		return AdminBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminBinding{}, fmt.Errorf("%w: commit update binding: %v", ErrRegistryBackend, err)
	}
	return b, nil
}

// DeletePoolBinding 软删一条绑定(id + 租户域)并 bump 快照。用【单租户直接 bump】
// (而非 bumpAffectedSnapshots):软删后该行对那个 CTE 不可见,删掉某租户某 model 的最后
// 一条存活绑定时,否则会漏掉版本 bump。
func (r *PostgresRegistry) DeletePoolBinding(ctx context.Context, id, tenantID int64, actor, reason string) error {
	if r == nil || r.pool == nil {
		return ErrRegistryBackend
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("%w: begin delete binding: %v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
UPDATE model_pool_bindings SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, id, tenantID)
	if err != nil {
		return fmt.Errorf("%w: soft-delete binding: %v", ErrRegistryBackend, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBindingNotFound
	}

	// 单租户直接 bump —— 不依赖任何存活的绑定行。
	if _, err := tx.Exec(ctx, `
INSERT INTO model_registry_snapshots (tenant_id, version, reason, updated_by_actor)
VALUES ($1, 2, $2, $3)
ON CONFLICT (tenant_id) DO UPDATE SET
	version = model_registry_snapshots.version + 1,
	reason = EXCLUDED.reason,
	updated_by_actor = EXCLUDED.updated_by_actor,
	updated_at = now()`, tenantID, bindingReason(reason, "delete"), bindingActor(actor)); err != nil {
		return fmt.Errorf("%w: bump delete binding snapshot: %v", ErrRegistryBackend, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit delete binding: %v", ErrRegistryBackend, err)
	}
	return nil
}

// checkModelBindable 校验 model 存在且该租户可绑:同租户的租户自有 model,或全局 model。
// 谓词对齐 model_alias_import.go 的继承谓词(全局 model 的 tenant_id 为 NULL,简单的
// tenant_id = $ 会误拒它们)。
func checkModelBindable(ctx context.Context, tx pgx.Tx, modelID, tenantID int64) error {
	var one int
	err := tx.QueryRow(ctx, `
SELECT 1 FROM models m
WHERE m.id = $1 AND m.deleted_at IS NULL
  AND ((m.scope = 'tenant' AND m.tenant_id = $2)
       OR (m.scope = 'global' AND m.tenant_id IS NULL))`, modelID, tenantID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrModelNotBindable
	}
	if err != nil {
		return fmt.Errorf("%w: check model bindable: %v", ErrRegistryBackend, err)
	}
	return nil
}

// checkPoolGroupOwned 校验 pool_group 归属该租户(复合 FK 在 insert 时也会兜底,但显式
// 预检给的是 4xx 而非裸 FK 500)。
func checkPoolGroupOwned(ctx context.Context, tx pgx.Tx, poolGroupID, tenantID int64) error {
	var one int
	err := tx.QueryRow(ctx, `
SELECT 1 FROM pool_groups
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, poolGroupID, tenantID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPoolGroupNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: check pool group owned: %v", ErrRegistryBackend, err)
	}
	return nil
}

// isUniqueViolation 判断是否唯一约束冲突(pg 错误码 23505)。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func bindingReason(reason, op string) string {
	if reason == "" {
		return "admin binding " + op
	}
	return reason
}

func bindingActor(actor string) string {
	if actor == "" {
		return "admin"
	}
	return actor
}
