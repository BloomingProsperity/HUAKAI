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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// 哨兵错误,由 admin handler 映射成 HTTP 状态码。
var (
	ErrBindingNotFound   = errors.New("registry: model pool binding not found")
	ErrModelNotBindable  = errors.New("registry: model not found or not bindable by tenant")
	ErrPoolGroupNotFound = errors.New("registry: pool group not found for tenant")
	ErrBindingConflict   = errors.New("registry: binding already exists for (tenant, model, pool)")
	ErrBindingInvalid    = errors.New("registry: invalid model pool binding")
	ErrBindingWindow     = errors.New("registry: invalid binding effective window")
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
	FallbackClass           string // Router 编译 normal 主 phase 与定向目标 phase；各协议 executor 消费精确目标 phase。
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
	FallbackClass           string // Router 编译 normal 主 phase 与定向目标 phase；各协议 executor 消费精确目标 phase。
	Enabled                 bool
	DisabledReason          *string
	EffectiveFrom           *time.Time
	EffectiveUntil          *time.Time
	Reason                  string
	Actor                   string
	ActorRole               string
	RequestID               string
}

// BindingField 表示 PATCH 中一个字段是否出现。Value 可以是指针，以区分“未提供”
// 和“显式清空为 NULL”。
type BindingField[T any] struct {
	Set   bool
	Value T
}

// UpdateBindingInput 更新单条绑定的可调字段(按 id + 租户域)。每个字段都保留
// “未提供/提供值/显式 NULL”语义，避免 PATCH 把未触及字段重置。ModelID /
// PoolGroupID 不可更新，改身份请走删后重建。
type UpdateBindingInput struct {
	ID                      int64
	TenantID                int64
	Priority                BindingField[int32]
	Weight                  BindingField[int32] // 仅存储兼容，无运行时消费，UI 已不暴露。
	SelectionMode           BindingField[string]
	ProviderModelIDOverride BindingField[*string]
	RPMLimit                BindingField[*int32]
	TPMLimit                BindingField[*int32]
	MaxParallelRequests     BindingField[*int32] // binding 全局在途上限；nil 或 0 表示不限。
	FallbackClass           BindingField[string] // Router 编译 normal 主 phase 与定向目标 phase；各协议 executor 消费精确目标 phase。
	Enabled                 BindingField[bool]
	DisabledReason          BindingField[*string]
	EffectiveFrom           BindingField[*time.Time]
	EffectiveUntil          BindingField[*time.Time]
	Reason                  BindingField[string]
	Actor                   string
	ActorRole               string
	RequestID               string
}

type DeleteBindingInput struct {
	ID        int64
	TenantID  int64
	Actor     string
	ActorRole string
	RequestID string
	Reason    string
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
	if in.TenantID <= 0 || in.ModelID <= 0 || in.PoolGroupID <= 0 {
		return AdminBinding{}, fmt.Errorf("%w: tenant, model and pool ids must be positive", ErrBindingInvalid)
	}
	if err := validateBindingAudit(in.Actor, in.ActorRole); err != nil {
		return AdminBinding{}, err
	}
	if err := validateAdminBinding(AdminBinding{
		TenantID:                in.TenantID,
		ModelID:                 in.ModelID,
		PoolGroupID:             in.PoolGroupID,
		Priority:                in.Priority,
		Weight:                  in.Weight,
		SelectionMode:           in.SelectionMode,
		ProviderModelIDOverride: in.ProviderModelIDOverride,
		RPMLimit:                in.RPMLimit,
		TPMLimit:                in.TPMLimit,
		MaxParallelRequests:     in.MaxParallelRequests,
		FallbackClass:           in.FallbackClass,
		Enabled:                 in.Enabled,
		DisabledReason:          in.DisabledReason,
		EffectiveFrom:           in.EffectiveFrom,
		EffectiveUntil:          in.EffectiveUntil,
		Reason:                  in.Reason,
	}); err != nil {
		return AdminBinding{}, err
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
	payload, err := json.Marshal(map[string]any{
		"model_id":       b.ModelID,
		"pool_group_id":  b.PoolGroupID,
		"enabled":        b.Enabled,
		"selection_mode": b.SelectionMode,
		"fallback_class": b.FallbackClass,
	})
	if err != nil {
		return AdminBinding{}, fmt.Errorf("%w: encode create binding log: %v", ErrRegistryBackend, err)
	}
	if err := insertBindingMutationLog(ctx, tx, b.TenantID, b.ID, "create_model_pool_binding", in.Actor, in.ActorRole, in.RequestID, in.Reason, payload); err != nil {
		return AdminBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminBinding{}, fmt.Errorf("%w: commit create binding: %v", ErrRegistryBackend, err)
	}
	return b, nil
}

// UpdatePoolBinding 在同一个 Serializable Tx 中锁定当前行、合并 PATCH、写入并
// bump 快照。先锁后合并可防止两个并发 PATCH 用各自的旧快照覆盖对方字段。
func (r *PostgresRegistry) UpdatePoolBinding(ctx context.Context, in UpdateBindingInput) (AdminBinding, error) {
	if r == nil || r.pool == nil {
		return AdminBinding{}, ErrRegistryBackend
	}
	if err := validateBindingAudit(in.Actor, in.ActorRole); err != nil {
		return AdminBinding{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AdminBinding{}, fmt.Errorf("%w: begin update binding: %v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	b, err := scanAdminBinding(tx.QueryRow(ctx, `
SELECT `+adminBindingCols+`
FROM model_pool_bindings
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
FOR UPDATE`, in.ID, in.TenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminBinding{}, ErrBindingNotFound
	}
	if err != nil {
		return AdminBinding{}, fmt.Errorf("%w: lock binding for update: %v", ErrRegistryBackend, err)
	}

	if err := applyBindingPatch(&b, in); err != nil {
		return AdminBinding{}, err
	}

	b, err = scanAdminBinding(tx.QueryRow(ctx, `
UPDATE model_pool_bindings SET
	priority = $3, weight = $4, selection_mode = $5, provider_model_id_override = $6,
	rpm_limit = $7, tpm_limit = $8, max_parallel_requests = $9, fallback_class = $10,
	enabled = $11, disabled_reason = $12, effective_from = $13, effective_until = $14,
	reason = $15, updated_at = now()
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
RETURNING `+adminBindingCols,
		in.ID, in.TenantID, b.Priority, b.Weight, b.SelectionMode, b.ProviderModelIDOverride,
		b.RPMLimit, b.TPMLimit, b.MaxParallelRequests, b.FallbackClass,
		b.Enabled, b.DisabledReason, b.EffectiveFrom, b.EffectiveUntil, b.Reason,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminBinding{}, ErrBindingNotFound
	}
	if err != nil {
		return AdminBinding{}, fmt.Errorf("%w: update binding: %v", ErrRegistryBackend, err)
	}

	if _, err := bumpAffectedSnapshots(ctx, tx, []int64{b.ModelID}, bindingReason(b.Reason, "update"), bindingActor(in.Actor)); err != nil {
		return AdminBinding{}, err
	}
	payload, err := json.Marshal(map[string]any{"changed_fields": bindingChangedFields(in)})
	if err != nil {
		return AdminBinding{}, fmt.Errorf("%w: encode update binding log: %v", ErrRegistryBackend, err)
	}
	if err := insertBindingMutationLog(ctx, tx, b.TenantID, b.ID, "update_model_pool_binding", in.Actor, in.ActorRole, in.RequestID, b.Reason, payload); err != nil {
		return AdminBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminBinding{}, fmt.Errorf("%w: commit update binding: %v", ErrRegistryBackend, err)
	}
	return b, nil
}

func applyBindingPatch(b *AdminBinding, in UpdateBindingInput) error {
	if in.Priority.Set {
		b.Priority = in.Priority.Value
	}
	if in.Weight.Set {
		b.Weight = in.Weight.Value
	}
	if in.SelectionMode.Set {
		b.SelectionMode = in.SelectionMode.Value
	}
	if in.ProviderModelIDOverride.Set {
		b.ProviderModelIDOverride = in.ProviderModelIDOverride.Value
	}
	if in.RPMLimit.Set {
		b.RPMLimit = in.RPMLimit.Value
	}
	if in.TPMLimit.Set {
		b.TPMLimit = in.TPMLimit.Value
	}
	if in.MaxParallelRequests.Set {
		b.MaxParallelRequests = in.MaxParallelRequests.Value
	}
	if in.FallbackClass.Set {
		b.FallbackClass = in.FallbackClass.Value
	}
	if in.Enabled.Set {
		b.Enabled = in.Enabled.Value
	}
	if in.DisabledReason.Set {
		b.DisabledReason = in.DisabledReason.Value
	}
	if in.EffectiveFrom.Set {
		b.EffectiveFrom = in.EffectiveFrom.Value
	}
	if in.EffectiveUntil.Set {
		b.EffectiveUntil = in.EffectiveUntil.Value
	}
	if in.Reason.Set {
		b.Reason = in.Reason.Value
	}
	return validateAdminBinding(*b)
}

func validateAdminBinding(b AdminBinding) error {
	if b.Priority < 0 {
		return fmt.Errorf("%w: priority must be nonnegative", ErrBindingInvalid)
	}
	if b.Weight <= 0 {
		return fmt.Errorf("%w: weight must be positive", ErrBindingInvalid)
	}
	switch b.SelectionMode {
	case "strict_priority", "priority_weighted":
	default:
		return fmt.Errorf("%w: unsupported selection mode", ErrBindingInvalid)
	}
	if b.RPMLimit != nil && *b.RPMLimit < 0 {
		return fmt.Errorf("%w: rpm limit must be nonnegative", ErrBindingInvalid)
	}
	if b.TPMLimit != nil && *b.TPMLimit < 0 {
		return fmt.Errorf("%w: tpm limit must be nonnegative", ErrBindingInvalid)
	}
	if b.MaxParallelRequests != nil && *b.MaxParallelRequests < 0 {
		return fmt.Errorf("%w: max parallel requests must be nonnegative", ErrBindingInvalid)
	}
	switch b.FallbackClass {
	case "normal", "context_window", "safety", "quota", "manual":
	default:
		return fmt.Errorf("%w: unsupported fallback class", ErrBindingInvalid)
	}
	if b.EffectiveFrom != nil && b.EffectiveUntil != nil && !b.EffectiveFrom.Before(*b.EffectiveUntil) {
		return ErrBindingWindow
	}
	return nil
}

// DeletePoolBinding 软删一条绑定(id + 租户域)并 bump 快照。用【单租户直接 bump】
// (而非 bumpAffectedSnapshots):软删后该行对那个 CTE 不可见,删掉某租户某 model 的最后
// 一条存活绑定时,否则会漏掉版本 bump。
func (r *PostgresRegistry) DeletePoolBinding(ctx context.Context, in DeleteBindingInput) error {
	if r == nil || r.pool == nil {
		return ErrRegistryBackend
	}
	if in.ID <= 0 || in.TenantID <= 0 {
		return ErrBindingInvalid
	}
	if err := validateBindingAudit(in.Actor, in.ActorRole); err != nil {
		return err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("%w: begin delete binding: %v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var modelID, poolGroupID int64
	err = tx.QueryRow(ctx, `
	UPDATE model_pool_bindings SET deleted_at = now(), updated_at = now()
	WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL
	RETURNING model_id, pool_group_id`, in.ID, in.TenantID).Scan(&modelID, &poolGroupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBindingNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: soft-delete binding: %v", ErrRegistryBackend, err)
	}

	// 单租户直接 bump —— 不依赖任何存活的绑定行。
	if _, err := tx.Exec(ctx, `
INSERT INTO model_registry_snapshots (tenant_id, version, reason, updated_by_actor)
VALUES ($1, 2, $2, $3)
ON CONFLICT (tenant_id) DO UPDATE SET
		version = model_registry_snapshots.version + 1,
		reason = EXCLUDED.reason,
		updated_by_actor = EXCLUDED.updated_by_actor,
		updated_at = now()`, in.TenantID, bindingReason(in.Reason, "delete"), bindingActor(in.Actor)); err != nil {
		return fmt.Errorf("%w: bump delete binding snapshot: %v", ErrRegistryBackend, err)
	}
	payload, err := json.Marshal(map[string]any{
		"model_id":      modelID,
		"pool_group_id": poolGroupID,
	})
	if err != nil {
		return fmt.Errorf("%w: encode delete binding log: %v", ErrRegistryBackend, err)
	}
	if err := insertBindingMutationLog(ctx, tx, in.TenantID, in.ID, "delete_model_pool_binding", in.Actor, in.ActorRole, in.RequestID, in.Reason, payload); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit delete binding: %v", ErrRegistryBackend, err)
	}
	return nil
}
