package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

const (
	modelSyncSource         = "vendor_model_sync"
	modelSyncDisabledReason = "vendor_model_absent"
)

type vendorAliasState struct {
	AliasNormalized string
	Source          string
	Status          string
}

type vendorCatalogPlan struct {
	Upserts           []modelsync.Model
	DisableAliases    []string
	ReactivateAliases []string
}

// planVendorCatalogApply 只规划 auto-sync 管理的 alias。operator alias 不在
// 禁用范围内，避免上游目录漂移破坏人工别名、绑定或价格。
func planVendorCatalogApply(catalog modelsync.Catalog, current []vendorAliasState) (vendorCatalogPlan, error) {
	incoming := make(map[string]modelsync.Model, len(catalog.Models))
	out := vendorCatalogPlan{Upserts: make([]modelsync.Model, 0, len(catalog.Models))}
	for _, model := range catalog.Models {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" {
			return vendorCatalogPlan{}, fmt.Errorf("registry model sync: empty model id")
		}
		alias := AliasNormalize(model.ID)
		if alias == "" {
			return vendorCatalogPlan{}, fmt.Errorf("registry model sync: invalid model id %q", model.ID)
		}
		if _, exists := incoming[alias]; exists {
			return vendorCatalogPlan{}, fmt.Errorf("registry model sync: duplicate model id %q", model.ID)
		}
		incoming[alias] = model
		out.Upserts = append(out.Upserts, model)
	}

	activeAutoSynced := 0
	for _, state := range current {
		if state.Source != modelSyncSource {
			continue
		}
		alias := AliasNormalize(state.AliasNormalized)
		if alias == "" {
			continue
		}
		if state.Status == "active" {
			activeAutoSynced++
		}
		_, stillPresent := incoming[alias]
		if stillPresent {
			if state.Status != "active" {
				out.ReactivateAliases = append(out.ReactivateAliases, alias)
			}
			continue
		}
		if state.Status == "active" {
			out.DisableAliases = append(out.DisableAliases, alias)
		}
	}

	// S0 数据丢失护栏:上游空响应或截断(HTTP 200 但 models 为空 / 全被过滤)
	// 会把现有全部 auto-sync alias 判为"消失"→ 一次性误禁整目录。这里拒绝可疑
	// 同步(返回 error → 整事务回滚,保留现状),由调用方观测/重试,而非静默禁用。
	if len(out.DisableAliases) > 0 {
		// (1) 空目录但已有 active auto-sync alias = 几乎必然是上游异常。
		if len(catalog.Models) == 0 && activeAutoSynced > 0 {
			return vendorCatalogPlan{}, fmt.Errorf(
				"registry model sync: refusing empty %s catalog that would disable %d active alias(es) (likely upstream blip)",
				catalog.Vendor, len(out.DisableAliases))
		}
		// (2) 灾难性收缩:本次将禁用 >50% 的 active alias(且基数足够),疑似部分截断。
		if activeAutoSynced >= 4 && len(out.DisableAliases)*2 > activeAutoSynced {
			return vendorCatalogPlan{}, fmt.Errorf(
				"registry model sync: refusing %s catalog that would disable %d of %d active alias(es) (>50%%, catastrophic shrink; manual confirm required)",
				catalog.Vendor, len(out.DisableAliases), activeAutoSynced)
		}
	}
	return out, nil
}

// ApplyVendorCatalog 把某个 vendor 的完整 model-list 快照应用到全局 model
// catalog。它绝不会改动 tenant alias、pool binding 或定价。
func (r *PostgresRegistry) ApplyVendorCatalog(ctx context.Context, catalog modelsync.Catalog, opts modelsync.ApplyOptions) (modelsync.ApplyResult, error) {
	results, err := r.ApplyVendorCatalogs(ctx, []modelsync.Catalog{catalog}, opts)
	if err != nil {
		return modelsync.ApplyResult{}, err
	}
	if len(results) == 0 {
		return modelsync.ApplyResult{}, nil
	}
	return results[0], nil
}

// ApplyVendorCatalogs 在一个事务内应用多 vendor 快照。只要任一 vendor 写入
// 失败，整次同步回滚，避免 admin 看到失败但 registry 已部分改变。
func (r *PostgresRegistry) ApplyVendorCatalogs(ctx context.Context, catalogs []modelsync.Catalog, opts modelsync.ApplyOptions) ([]modelsync.ApplyResult, error) {
	if r == nil || r.pool == nil {
		return nil, ErrRegistryBackend
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("%w: begin model sync: %v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	results := make([]modelsync.ApplyResult, 0, len(catalogs))
	for _, catalog := range catalogs {
		result, err := applyVendorCatalogTx(ctx, tx, catalog, opts)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("%w: commit model sync: %v", ErrRegistryBackend, err)
	}
	return results, nil
}

func applyVendorCatalogTx(ctx context.Context, tx pgx.Tx, catalog modelsync.Catalog, opts modelsync.ApplyOptions) (modelsync.ApplyResult, error) {
	current, err := loadVendorAliasStates(ctx, tx, catalog.Vendor)
	if err != nil {
		return modelsync.ApplyResult{}, err
	}
	plan, err := planVendorCatalogApply(catalog, current)
	if err != nil {
		return modelsync.ApplyResult{}, fmt.Errorf("%w: plan model sync: %v", ErrRegistryBackend, err)
	}

	result := modelsync.ApplyResult{Vendor: catalog.Vendor}
	changedModelIDs := make([]int64, 0, len(plan.Upserts)+len(plan.DisableAliases))
	reactivating := make(map[string]struct{}, len(plan.ReactivateAliases))
	for _, alias := range plan.ReactivateAliases {
		reactivating[alias] = struct{}{}
	}
	for _, model := range plan.Upserts {
		modelID, outcome, err := upsertVendorModel(ctx, tx, catalog.Vendor, model)
		if err != nil {
			return modelsync.ApplyResult{}, err
		}
		capChanged, err := syncVendorCapabilities(ctx, tx, modelID, model.Capabilities)
		if err != nil {
			return modelsync.ApplyResult{}, err
		}
		if outcome == "unchanged" && capChanged {
			outcome = "updated"
		}
		modelRef := strings.TrimSpace(model.ID)
		if _, ok := reactivating[AliasNormalize(model.ID)]; ok && outcome != "added" {
			outcome = "reactivated"
		}
		switch outcome {
		case "added":
			result.Added++
			result.Detected = append(result.Detected, modelRef)
			changedModelIDs = append(changedModelIDs, modelID)
		case "updated":
			result.Updated++
			result.Detected = append(result.Detected, modelRef)
			changedModelIDs = append(changedModelIDs, modelID)
		case "reactivated":
			result.Reactivated++
			result.Detected = append(result.Detected, modelRef)
			changedModelIDs = append(changedModelIDs, modelID)
		default:
			result.Unchanged++
			result.Ignored = append(result.Ignored, modelRef)
		}
	}

	disabledModelIDs, disabled, err := disableMissingVendorAliases(ctx, tx, catalog.Vendor, plan.DisableAliases)
	if err != nil {
		return modelsync.ApplyResult{}, err
	}
	result.Disabled = disabled
	if disabled > 0 {
		result.Removed = append(result.Removed, plan.DisableAliases...)
	}
	changedModelIDs = append(changedModelIDs, disabledModelIDs...)

	if len(changedModelIDs) > 0 {
		bumps, err := bumpAffectedSnapshots(ctx, tx, changedModelIDs, snapshotReason(catalog.Vendor, opts), snapshotActor(opts))
		if err != nil {
			return modelsync.ApplyResult{}, err
		}
		result.SnapshotBumps = bumps
	}

	if err := updateProviderAccountsModelSyncTracking(ctx, tx, catalog.Vendor, result); err != nil {
		return modelsync.ApplyResult{}, err
	}
	return result, nil
}

func updateProviderAccountsModelSyncTracking(ctx context.Context, tx pgx.Tx, vendor modelsync.Vendor, result modelsync.ApplyResult) error {
	detected, err := jsonModelList(result.Detected)
	if err != nil {
		return fmt.Errorf("%w: encode model_update_detected: %v", ErrRegistryBackend, err)
	}
	ignored, err := jsonModelList(result.Ignored)
	if err != nil {
		return fmt.Errorf("%w: encode model_update_ignored: %v", ErrRegistryBackend, err)
	}
	removed, err := jsonModelList(result.Removed)
	if err != nil {
		return fmt.Errorf("%w: encode model_update_removed: %v", ErrRegistryBackend, err)
	}
	_, err = tx.Exec(ctx, `
UPDATE provider_accounts pa
SET model_sync_last_check_at = now(),
    model_update_detected = $2::jsonb,
    model_update_ignored = $3::jsonb,
    model_update_removed = $4::jsonb,
    updated_at = now()
FROM providers p
WHERE p.id = pa.provider_id
  AND p.tenant_id = pa.tenant_id
  AND p.code = $1
  AND p.deleted_at IS NULL
  AND pa.deleted_at IS NULL
`, string(vendor), detected, ignored, removed)
	if err != nil {
		return fmt.Errorf("%w: update provider account model sync tracking: %v", ErrRegistryBackend, err)
	}
	return nil
}

func jsonModelList(items []string) ([]byte, error) {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return json.Marshal(out)
}

func loadVendorAliasStates(ctx context.Context, tx pgx.Tx, vendor modelsync.Vendor) ([]vendorAliasState, error) {
	rows, err := tx.Query(ctx, `
SELECT a.public_alias_normalized, a.source, a.status
FROM model_aliases a
INNER JOIN models m ON m.id = a.model_id
WHERE a.scope = 'global'
  AND a.tenant_id IS NULL
  AND a.deleted_at IS NULL
  AND m.scope = 'global'
  AND m.tenant_id IS NULL
  AND m.deleted_at IS NULL
  AND m.canonical_id LIKE $1
`, vendorCanonicalLike(vendor))
	if err != nil {
		return nil, fmt.Errorf("%w: list vendor aliases: %v", ErrRegistryBackend, err)
	}
	defer rows.Close()
	var out []vendorAliasState
	for rows.Next() {
		var item vendorAliasState
		if err := rows.Scan(&item.AliasNormalized, &item.Source, &item.Status); err != nil {
			return nil, fmt.Errorf("%w: scan vendor alias: %v", ErrRegistryBackend, err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: vendor alias rows: %v", ErrRegistryBackend, err)
	}
	return out, nil
}

func upsertVendorModel(ctx context.Context, tx pgx.Tx, vendor modelsync.Vendor, model modelsync.Model) (int64, string, error) {
	canonicalID := vendorCanonicalID(vendor, model.ID)
	protocol := strings.TrimSpace(model.ProtocolFamily)
	if protocol == "" {
		protocol = defaultProtocolForVendor(vendor)
	}
	protocol = normalizeSyncedProtocolFamily(protocol)
	owner := strings.TrimSpace(model.OwnedBy)
	if owner == "" {
		owner = defaultOwnerForVendor(vendor)
	}
	contextWindow := model.ContextWindow
	if contextWindow < 0 {
		contextWindow = 0
	}
	createdAt := nullableTime(model.CreatedAt)

	var existingID int64
	var autoManaged bool
	err := tx.QueryRow(ctx, `
SELECT m.id,
       EXISTS (
           SELECT 1 FROM model_aliases a
           WHERE a.model_id = m.id
             AND a.scope = 'global'
             AND a.tenant_id IS NULL
             AND a.deleted_at IS NULL
             AND a.source = $2
       ) AS auto_managed
FROM models m
WHERE m.scope = 'global'
  AND m.tenant_id IS NULL
  AND m.deleted_at IS NULL
  AND m.canonical_id = $1
LIMIT 1
`, canonicalID, modelSyncSource).Scan(&existingID, &autoManaged)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, "", fmt.Errorf("%w: find vendor model: %v", ErrRegistryBackend, err)
	}
	outcome := "unchanged"
	modelID := existingID
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
INSERT INTO models (
    tenant_id, scope, canonical_id, protocol_family, default_provider_model_id,
    default_context_window, model_owner, model_created_at, status
) VALUES (
    NULL, 'global', $1, $2, $3, $4, $5, $6, 'active'
) RETURNING id
`, canonicalID, protocol, model.ID, contextWindow, owner, createdAt).Scan(&modelID)
		if err != nil {
			return 0, "", fmt.Errorf("%w: insert vendor model: %v", ErrRegistryBackend, err)
		}
		outcome = "added"
	} else if autoManaged {
		tag, err := tx.Exec(ctx, `
UPDATE models
SET protocol_family = $2,
    default_provider_model_id = $3,
    default_context_window = $4,
    model_owner = $5,
    model_created_at = COALESCE($6::timestamptz, model_created_at),
    status = 'active',
    updated_at = now()
WHERE id = $1
  AND (
    protocol_family IS DISTINCT FROM $2
    OR default_provider_model_id IS DISTINCT FROM $3
    OR default_context_window IS DISTINCT FROM $4
    OR model_owner IS DISTINCT FROM $5
    OR ($6::timestamptz IS NOT NULL AND model_created_at IS DISTINCT FROM $6::timestamptz)
    OR status IS DISTINCT FROM 'active'
  )
`, modelID, protocol, model.ID, contextWindow, owner, createdAt)
		if err != nil {
			return 0, "", fmt.Errorf("%w: update vendor model: %v", ErrRegistryBackend, err)
		}
		if tag.RowsAffected() > 0 {
			outcome = "updated"
		}
	}

	aliasChanged, err := upsertVendorAlias(ctx, tx, modelID, model)
	if err != nil {
		return 0, "", err
	}
	if outcome == "unchanged" && aliasChanged {
		outcome = "updated"
	}
	return modelID, outcome, nil
}

func upsertVendorAlias(ctx context.Context, tx pgx.Tx, modelID int64, model modelsync.Model) (bool, error) {
	alias := AliasNormalize(model.ID)
	display := strings.TrimSpace(model.DisplayName)
	if display == "" {
		display = model.ID
	}
	tag, err := tx.Exec(ctx, `
INSERT INTO model_aliases (
    tenant_id, scope, model_id, public_alias_normalized, public_alias_display,
    status, disabled_reason, source
) VALUES (
    NULL, 'global', $1, $2, $3, 'active', NULL, $4
)
ON CONFLICT (public_alias_normalized)
WHERE deleted_at IS NULL AND scope = 'global'
DO UPDATE SET
    model_id = EXCLUDED.model_id,
    public_alias_display = EXCLUDED.public_alias_display,
    status = 'active',
    disabled_reason = NULL,
    updated_at = now()
WHERE model_aliases.source = $4
  AND (
    model_aliases.model_id IS DISTINCT FROM EXCLUDED.model_id
    OR model_aliases.public_alias_display IS DISTINCT FROM EXCLUDED.public_alias_display
    OR model_aliases.status IS DISTINCT FROM 'active'
    OR model_aliases.disabled_reason IS NOT NULL
  )
`, modelID, alias, display, modelSyncSource)
	if err != nil {
		return false, fmt.Errorf("%w: upsert vendor alias: %v", ErrRegistryBackend, err)
	}
	return tag.RowsAffected() > 0, nil
}

func syncVendorCapabilities(ctx context.Context, tx pgx.Tx, modelID int64, capabilities []string) (bool, error) {
	changed := false
	want := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		rawCapability := capability
		capability, err := normalizeKnownModelCapability(capability)
		if err != nil {
			if strings.TrimSpace(rawCapability) == "" {
				continue
			}
			return false, err
		}
		if capability == "" {
			continue
		}
		want[capability] = struct{}{}
		tag, err := tx.Exec(ctx, `
INSERT INTO model_registry_capabilities (
    tenant_id, scope, model_id, capability, enabled, source
) VALUES (
    NULL, 'global', $1, $2, true, $3
)
ON CONFLICT (model_id, capability)
WHERE deleted_at IS NULL AND scope = 'global'
DO UPDATE SET
    enabled = true,
    updated_at = now()
WHERE model_registry_capabilities.source = $3
  AND model_registry_capabilities.enabled IS DISTINCT FROM true
`, modelID, capability, modelSyncSource)
		if err != nil {
			return false, fmt.Errorf("%w: upsert vendor capability: %v", ErrRegistryBackend, err)
		}
		if tag.RowsAffected() > 0 {
			changed = true
		}
	}

	rows, err := tx.Query(ctx, `
SELECT capability
FROM model_registry_capabilities
WHERE model_id = $1
  AND scope = 'global'
  AND tenant_id IS NULL
  AND deleted_at IS NULL
  AND source = $2
  AND enabled = true
`, modelID, modelSyncSource)
	if err != nil {
		return false, fmt.Errorf("%w: list vendor capabilities: %v", ErrRegistryBackend, err)
	}
	staleCapabilities := make([]string, 0)
	for rows.Next() {
		var capability string
		if err := rows.Scan(&capability); err != nil {
			rows.Close()
			return false, fmt.Errorf("%w: scan vendor capability: %v", ErrRegistryBackend, err)
		}
		if _, ok := want[capability]; ok {
			continue
		}
		staleCapabilities = append(staleCapabilities, capability)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return false, fmt.Errorf("%w: vendor capability rows: %v", ErrRegistryBackend, rowsErr)
	}
	for _, capability := range staleCapabilities {
		tag, err := tx.Exec(ctx, `
UPDATE model_registry_capabilities
SET enabled = false, updated_at = now()
WHERE model_id = $1
  AND scope = 'global'
  AND tenant_id IS NULL
  AND source = $2
  AND capability = $3
`, modelID, modelSyncSource, capability)
		if err != nil {
			return false, fmt.Errorf("%w: disable vendor capability: %v", ErrRegistryBackend, err)
		}
		if tag.RowsAffected() > 0 {
			changed = true
		}
	}
	return changed, nil
}

func disableMissingVendorAliases(ctx context.Context, tx pgx.Tx, vendor modelsync.Vendor, aliases []string) ([]int64, int, error) {
	if len(aliases) == 0 {
		return nil, 0, nil
	}
	modelIDs := make([]int64, 0, len(aliases))
	disabled := 0
	for _, alias := range aliases {
		rows, err := tx.Query(ctx, `
UPDATE model_aliases a
SET status = 'disabled',
    disabled_reason = $4,
    updated_at = now()
FROM models m
WHERE a.model_id = m.id
  AND a.scope = 'global'
  AND a.tenant_id IS NULL
  AND a.deleted_at IS NULL
  AND a.source = $1
  AND a.public_alias_normalized = $2
  AND a.status = 'active'
  AND m.scope = 'global'
  AND m.tenant_id IS NULL
  AND m.deleted_at IS NULL
  AND m.canonical_id LIKE $3
RETURNING a.model_id
`, modelSyncSource, alias, vendorCanonicalLike(vendor), modelSyncDisabledReason)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: disable missing vendor alias: %v", ErrRegistryBackend, err)
		}
		for rows.Next() {
			var modelID int64
			if err := rows.Scan(&modelID); err != nil {
				rows.Close()
				return nil, 0, fmt.Errorf("%w: scan disabled vendor alias: %v", ErrRegistryBackend, err)
			}
			modelIDs = append(modelIDs, modelID)
			disabled++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("%w: disabled vendor alias rows: %v", ErrRegistryBackend, err)
		}
		rows.Close()
	}
	return modelIDs, disabled, nil
}

func bumpAffectedSnapshots(ctx context.Context, tx pgx.Tx, modelIDs []int64, reason, actor string) (int, error) {
	if len(modelIDs) == 0 {
		return 0, nil
	}
	tag, err := tx.Exec(ctx, `
WITH affected_tenants AS (
    SELECT tenant_id
    FROM model_registry_tenant_policies
    WHERE inherit_global_catalog = true
    UNION
    SELECT DISTINCT tenant_id
    FROM model_pool_bindings
    WHERE model_id = ANY($1::bigint[])
      AND deleted_at IS NULL
)
INSERT INTO model_registry_snapshots (tenant_id, version, reason, updated_by_actor)
SELECT tenant_id, 2, $2, $3
FROM affected_tenants
ON CONFLICT (tenant_id) DO UPDATE SET
    version = model_registry_snapshots.version + 1,
    reason = EXCLUDED.reason,
    updated_by_actor = EXCLUDED.updated_by_actor,
    updated_at = now()
`, modelIDs, reason, actor)
	if err != nil {
		return 0, fmt.Errorf("%w: bump model sync snapshots: %v", ErrRegistryBackend, err)
	}
	return int(tag.RowsAffected()), nil
}

func snapshotReason(vendor modelsync.Vendor, opts modelsync.ApplyOptions) string {
	base := "model sync " + string(vendor)
	reason := strings.TrimSpace(opts.Reason)
	if reason == "" {
		return base
	}
	return base + ": " + reason
}

func snapshotActor(opts modelsync.ApplyOptions) string {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		return modelSyncSource
	}
	return actor
}

func vendorCanonicalID(vendor modelsync.Vendor, modelID string) string {
	return string(vendor) + "/" + strings.TrimSpace(modelID)
}

func vendorCanonicalLike(vendor modelsync.Vendor) string {
	return string(vendor) + "/%"
}

func defaultProtocolForVendor(vendor modelsync.Vendor) string {
	switch vendor {
	case modelsync.VendorAnthropic:
		return registrydefault.ProtocolAnthropicMessages
	case modelsync.VendorGemini:
		return registrydefault.ProtocolGeminiMessages
	default:
		return registrydefault.ProtocolOpenAIChat
	}
}

func normalizeSyncedProtocolFamily(protocol string) string {
	switch strings.TrimSpace(protocol) {
	case "gemini":
		return registrydefault.ProtocolGeminiMessages
	default:
		return strings.TrimSpace(protocol)
	}
}

func defaultOwnerForVendor(vendor modelsync.Vendor) string {
	switch vendor {
	case modelsync.VendorAnthropic:
		return "anthropic"
	case modelsync.VendorGemini:
		return "google"
	default:
		return "openai"
	}
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
