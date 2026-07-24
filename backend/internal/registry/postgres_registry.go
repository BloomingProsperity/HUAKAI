// 基于 Postgres 的 Registry 实现。
//
// Resolve 流程：
//
//   1. 规范化 alias。
//   2. 开启一个 REPEATABLE READ + 只读 TX,使所有读取都观察到同一份
//      一致快照 —— 避免盖上一个并不描述所用行的 SnapshotVersion。
//   3. 查找 tenant 级的 alias 行。
//   4. 若该行存在且 status='active',继续做 model 查找。
//   5. 若该行存在且 status='disabled' -> ErrModelDisabled(D3
//      显式拒绝 —— 不会回退到 global)。
//   6. 若没有 tenant 行,查询 model_registry_tenant_policies;若
//      inherit_global_catalog=true,则查找 scope='global' 的 alias。
//   7. 否则返回 ErrUnknownModel。
//   8. 解析限定在 (tenant_id 或 global) 范围内的 canonical model 行 ——
//      防御被误配到外租户 model 的 alias。
//   9. 在同一个 TX 内并发加载 capabilities + bindings + snapshot version。
//  10. 若 bindings 列表为空 -> ErrTenantNoAccess。
//  11. 构建 Resolved 并提交(只读提交无害)。
//
// 本次修订中移除的内容:`model_pool_bindings` 上的
// `scope` 列。bindings 永远是 tenant 级的,因为 pool_groups 由 tenant 所有;
// "global binding" 是一个概念性错误,会让 pool id 跨租户泄露。

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbregistry "github.com/BloomingProsperity/HUAKAI/internal/db/registry"
)

// PostgresRegistry 针对 model_registry_* 表解析 alias。
// 通过 NewPostgresRegistry 构造。Cache 的生命周期由调用方负责;
// 传 nil 则使用 no-op 缓存(L0 默认)。
type PostgresRegistry struct {
	pool  *pgxpool.Pool
	cache Cache
}

// NewPostgresRegistry 包装一个 pgxpool。cache 参数可以为 nil;在
// L0 阶段它始终为 nil(依据 D2 —— cache 在 Slice 5 才落地)。
func NewPostgresRegistry(pool *pgxpool.Pool, cache Cache) *PostgresRegistry {
	if cache == nil {
		cache = noopCache{}
	}
	return &PostgresRegistry{pool: pool, cache: cache}
}

// ResolveModel 实现 Registry。
func (r *PostgresRegistry) ResolveModel(ctx context.Context, publicAlias string, tenantID int64) (Resolved, error) {
	if r == nil || r.pool == nil {
		return Resolved{}, ErrRegistryBackend
	}
	aliasLower := AliasNormalize(publicAlias)
	if aliasLower == "" {
		return Resolved{}, ErrUnknownModel
	}

	// REPEATABLE READ + 只读:所有读取都看到 registry 的同一份时间点
	// 快照,因此我们盖上的 version 能真正描述用于构建 Resolved 的那些行。
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return Resolved{}, fmt.Errorf("%w: begin: %v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbregistry.New(tx)

	aliasRow, err := r.lookupAlias(ctx, q, tenantID, aliasLower)
	if err != nil {
		return Resolved{}, err
	}
	if aliasRow.aliasStatus != "active" {
		return Resolved{}, ErrModelDisabled
	}

	modelRow, err := q.GetModelByID(ctx, dbregistry.GetModelByIDParams{
		ID:       aliasRow.modelID,
		TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// alias 指向了一个对该 tenant 不可见的 model 行。
			// 要么 model 确实缺失,要么它属于另一个 tenant
			//(由 tenant/scope 的 WHERE 子句防御)。
			// 两种情况解析器都返回 ErrModelDisabled 而非
			// ErrUnknownModel,使审计日志能显示 alias->model 的
			// 悬空状态,同时不泄露枚举信号。
			return Resolved{}, ErrModelDisabled
		}
		return Resolved{}, fmt.Errorf("%w: get model: %v", ErrRegistryBackend, err)
	}
	if modelRow.Status != "active" {
		return Resolved{}, ErrModelDisabled
	}

	caps, err := q.ListModelCapabilities(ctx, dbregistry.ListModelCapabilitiesParams{
		TenantID: tenantID,
		ModelID:  modelRow.ID,
	})
	if err != nil {
		return Resolved{}, fmt.Errorf("%w: list capabilities: %v", ErrRegistryBackend, err)
	}

	bindings, err := q.ListModelPoolBindings(ctx, dbregistry.ListModelPoolBindingsParams{
		TenantID: tenantID,
		ModelID:  modelRow.ID,
	})
	if err != nil {
		return Resolved{}, fmt.Errorf("%w: list bindings: %v", ErrRegistryBackend, err)
	}
	if len(bindings) == 0 {
		return Resolved{}, ErrTenantNoAccess
	}

	version, err := q.GetTenantSnapshotVersion(ctx, tenantID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return Resolved{}, fmt.Errorf("%w: snapshot: %v", ErrRegistryBackend, err)
		}
		// 缺少 snapshot 行 = 该 tenant 尚未有过任何 admin 写入;
		// 按 version 1 处理(与 schema 的 DEFAULT 一致)。
		version = 1
	}

	if err := tx.Commit(ctx); err != nil {
		return Resolved{}, fmt.Errorf("%w: commit: %v", ErrRegistryBackend, err)
	}

	out := Resolved{
		PublicAlias:            aliasRow.publicAliasDisplay,
		CanonicalModelID:       modelRow.CanonicalID,
		DefaultProviderModelID: modelRow.DefaultProviderModelID,
		ProviderModelID:        modelRow.DefaultProviderModelID,
		ContextWindow:          int(modelRow.DefaultContextWindow),
		PricingClass:           modelRow.PricingClass,
		ProtocolFamily:         modelRow.ProtocolFamily,
		RequestTimeoutMS:       int(modelRow.DefaultRequestTimeoutMs),
		Capabilities:           make([]string, 0, len(caps)),
		PoolCandidates:         make([]int64, 0, len(bindings)),
		BindingMetadata:        make([]BindingMetadata, 0, len(bindings)),
		SnapshotVersion:        fmt.Sprintf("registry:%d:%d", tenantID, version),
	}
	for _, c := range caps {
		out.Capabilities = append(out.Capabilities, c.Capability)
	}
	for _, b := range bindings {
		bodyParamStrips, err := decodeBindingBodyParamStrips(b.BodyParamStrips)
		if err != nil {
			return Resolved{}, fmt.Errorf("%w: decode body_param_strips: %v", ErrRegistryBackend, err)
		}
		sensitiveWords, err := decodeBindingBodyParamStrips(b.SensitiveWords)
		if err != nil {
			return Resolved{}, fmt.Errorf("%w: decode sensitive_words: %v", ErrRegistryBackend, err)
		}
		paramOverride, err := decodeBindingParamOverride(b.ParamOverride)
		if err != nil {
			return Resolved{}, fmt.Errorf("%w: decode param_override: %v", ErrRegistryBackend, err)
		}
		out.PoolCandidates = append(out.PoolCandidates, b.PoolGroupID)
		// binding 级的 provider model 重命名优先于 model 的默认值;
		// 对主候选项,第一个非 nil 的 override 生效。
		// 后续的 per-attempt override 可以再替换它。
		if b.ProviderModelIDOverride != nil && len(out.PoolCandidates) == 1 {
			out.ProviderModelID = *b.ProviderModelIDOverride
		}
		out.BindingMetadata = append(out.BindingMetadata, BindingMetadata{
			BindingID:               b.ID,
			PoolGroupID:             b.PoolGroupID,
			Priority:                b.Priority,
			Weight:                  b.Weight,
			SelectionMode:           b.SelectionMode,
			ProviderModelIDOverride: b.ProviderModelIDOverride,
			RPMLimit:                b.RpmLimit,
			TPMLimit:                b.TpmLimit,
			MaxParallelRequests:     b.MaxParallelRequests,
			FallbackClass:           b.FallbackClass,
			BodyParamStrips:         bodyParamStrips,
			SensitiveWords:          sensitiveWords,
			ParamOverride:           paramOverride,
		})
	}
	return out, nil
}

func decodeBindingBodyParamStrips(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, err
	}
	out := keys[:0]
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			out = append(out, key)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func decodeBindingParamOverride(raw string) (map[string]json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var override map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &override); err != nil {
		return nil, err
	}
	for key := range override {
		if strings.TrimSpace(key) == "" {
			delete(override, key)
		}
	}
	if len(override) == 0 {
		return nil, nil
	}
	return override, nil
}

// resolvedAliasRow 是 LookupTenantAlias / LookupGlobalAlias 行的
// 公共形态 —— 两者产出相同的五个字段。
type resolvedAliasRow struct {
	aliasID            int64
	modelID            int64
	aliasStatus        string
	disabledReason     *string
	publicAliasDisplay string
}

// lookupAlias 依据 D3 执行 tenant-then-global 的两步解析
// (显式拒绝不变量:tenant 被禁用会阻断 global 回退)。
// 所有读取都使用调用方提供的 Queries(它绑定到外层的
// REPEATABLE READ tx 以保证快照一致性)。
func (r *PostgresRegistry) lookupAlias(ctx context.Context, q *dbregistry.Queries, tenantID int64, aliasLower string) (resolvedAliasRow, error) {
	tenantRow, err := q.LookupTenantAlias(ctx, dbregistry.LookupTenantAliasParams{
		TenantID:   tenantID,
		AliasLower: aliasLower,
	})
	if err == nil {
		return resolvedAliasRow{
			aliasID:            tenantRow.AliasID,
			modelID:            tenantRow.ModelID,
			aliasStatus:        tenantRow.AliasStatus,
			disabledReason:     tenantRow.DisabledReason,
			publicAliasDisplay: tenantRow.PublicAliasDisplay,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return resolvedAliasRow{}, fmt.Errorf("%w: tenant alias: %v", ErrRegistryBackend, err)
	}

	// tenant 未命中 —— 检查继承策略。
	inherit, err := q.GetTenantInheritGlobal(ctx, tenantID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return resolvedAliasRow{}, fmt.Errorf("%w: tenant policy: %v", ErrRegistryBackend, err)
		}
		// 没有策略行 = 不继承。
		inherit = false
	}
	if !inherit {
		return resolvedAliasRow{}, ErrUnknownModel
	}

	globalRow, err := q.LookupGlobalAlias(ctx, aliasLower)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return resolvedAliasRow{}, ErrUnknownModel
		}
		return resolvedAliasRow{}, fmt.Errorf("%w: global alias: %v", ErrRegistryBackend, err)
	}
	return resolvedAliasRow{
		aliasID:            globalRow.AliasID,
		modelID:            globalRow.ModelID,
		aliasStatus:        globalRow.AliasStatus,
		disabledReason:     globalRow.DisabledReason,
		publicAliasDisplay: globalRow.PublicAliasDisplay,
	}, nil
}

// 编译期断言:PostgresRegistry 实现了 Registry。
var _ Registry = (*PostgresRegistry)(nil)
