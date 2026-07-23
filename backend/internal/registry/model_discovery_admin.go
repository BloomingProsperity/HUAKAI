package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
)

type modelDiscoveryScanner interface {
	Scan(...any) error
}

func (r *PostgresRegistry) ListModelDiscoveries(ctx context.Context, params ModelDiscoveryListParams) (ModelDiscoveryPage, error) {
	if r == nil || r.pool == nil {
		return ModelDiscoveryPage{}, ErrRegistryBackend
	}
	if err := normalizeModelDiscoveryList(&params); err != nil {
		return ModelDiscoveryPage{}, err
	}
	rows, err := r.pool.Query(ctx, `
SELECT id, vendor, model_id_normalized, provider_model_id, display_name, owned_by,
       protocol_family, context_window, model_created_at, capabilities, status,
       first_seen_at, last_seen_at, last_absent_at, decided_at, decided_by_actor,
       decision_reason, promoted_model_id, created_at, updated_at
FROM model_discovery_inbox
WHERE ($1 = '' OR vendor = $1)
  AND ($2 = '' OR status = $2)
  AND ($3 = '' OR strpos(lower(provider_model_id), lower($3)) > 0
               OR strpos(lower(display_name), lower($3)) > 0)
  AND ($4::bigint = 0 OR id < $4)
ORDER BY id DESC
LIMIT $5
`, string(params.Vendor), params.Status, params.Search, params.BeforeID, params.Limit+1)
	if err != nil {
		return ModelDiscoveryPage{}, fmt.Errorf("%w: list model discoveries: %w", ErrRegistryBackend, err)
	}
	defer rows.Close()
	items := make([]ModelDiscovery, 0, params.Limit+1)
	for rows.Next() {
		item, err := scanModelDiscovery(rows)
		if err != nil {
			return ModelDiscoveryPage{}, fmt.Errorf("%w: scan model discovery: %w", ErrRegistryBackend, err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ModelDiscoveryPage{}, fmt.Errorf("%w: model discovery rows: %w", ErrRegistryBackend, err)
	}
	page := ModelDiscoveryPage{Items: items}
	if len(items) > params.Limit {
		page.Items = items[:params.Limit]
		next := page.Items[len(page.Items)-1].ID
		page.NextBeforeID = &next
	}
	return page, nil
}

func (r *PostgresRegistry) PromoteModelDiscovery(ctx context.Context, in ModelDiscoveryDecision) (ModelDiscovery, error) {
	if r == nil || r.pool == nil {
		return ModelDiscovery{}, ErrRegistryBackend
	}
	if err := normalizeModelDiscoveryDecision(&in); err != nil {
		return ModelDiscovery{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return retryModelDiscoveryTx(ctx, func() (ModelDiscovery, error) {
		return r.promoteModelDiscoveryOnce(ctx, in)
	})
}

func (r *PostgresRegistry) promoteModelDiscoveryOnce(ctx context.Context, in ModelDiscoveryDecision) (ModelDiscovery, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ModelDiscovery{}, fmt.Errorf("%w: begin model discovery promotion: %w", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := lockModelDiscoveryTx(ctx, tx, in.ID)
	if err != nil {
		return ModelDiscovery{}, err
	}
	if current.Status == ModelDiscoveryPromoted {
		return current, nil
	}
	if current.Status != ModelDiscoveryPending {
		return ModelDiscovery{}, ErrModelDiscoveryConflict
	}
	if err := ensureModelDiscoveryPromotionAvailable(ctx, tx, current); err != nil {
		return ModelDiscovery{}, err
	}
	model := modelsync.Model{
		ID:             current.ProviderModelID,
		DisplayName:    current.DisplayName,
		OwnedBy:        current.OwnedBy,
		ProtocolFamily: current.ProtocolFamily,
		ContextWindow:  current.ContextWindow,
		Capabilities:   append([]string(nil), current.Capabilities...),
	}
	if current.ModelCreatedAt != nil {
		model.CreatedAt = current.ModelCreatedAt.UTC()
	}
	modelID, outcome, err := upsertVendorModel(ctx, tx, current.Vendor, model)
	if err != nil {
		if errors.Is(err, ErrModelDiscoveryConflict) || isUniqueViolation(err) {
			return ModelDiscovery{}, ErrModelDiscoveryConflict
		}
		return ModelDiscovery{}, err
	}
	if outcome != "added" {
		return ModelDiscovery{}, ErrModelDiscoveryConflict
	}
	if _, err := syncVendorCapabilities(ctx, tx, modelID, current.Capabilities); err != nil {
		return ModelDiscovery{}, err
	}
	updated, err := updatePromotedModelDiscoveryTx(ctx, tx, current.ID, modelID, in)
	if err != nil {
		return ModelDiscovery{}, err
	}
	if _, err := bumpAffectedSnapshots(ctx, tx, []int64{modelID}, "model discovery promotion: "+in.Reason, in.Access.Actor); err != nil {
		return ModelDiscovery{}, err
	}
	// 上架管道第 4 关:上架成功即自动绑到能提供它的全部 pool_group,让手动上架也一步到位
	// (自动挡复用同一 promote 路径,因此两挡上架后都自动可用)。无合格账号则不建空绑定。
	if _, err := r.AutoBindModelToEligiblePoolsTx(ctx, tx, modelID, current.ProtocolFamily, current.ProviderModelID, in.Access.Actor, "model discovery promotion: "+in.Reason); err != nil {
		return ModelDiscovery{}, err
	}
	if err := insertModelDiscoveryLogTx(ctx, tx, "promote_model_discovery", current, updated, in); err != nil {
		return ModelDiscovery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModelDiscovery{}, fmt.Errorf("%w: commit model discovery promotion: %w", ErrRegistryBackend, err)
	}
	return updated, nil
}

func (r *PostgresRegistry) IgnoreModelDiscovery(ctx context.Context, in ModelDiscoveryDecision) (ModelDiscovery, error) {
	if r == nil || r.pool == nil {
		return ModelDiscovery{}, ErrRegistryBackend
	}
	if err := normalizeModelDiscoveryDecision(&in); err != nil {
		return ModelDiscovery{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return retryModelDiscoveryTx(ctx, func() (ModelDiscovery, error) {
		return r.ignoreModelDiscoveryOnce(ctx, in)
	})
}

func (r *PostgresRegistry) ignoreModelDiscoveryOnce(ctx context.Context, in ModelDiscoveryDecision) (ModelDiscovery, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ModelDiscovery{}, fmt.Errorf("%w: begin model discovery ignore: %w", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := lockModelDiscoveryTx(ctx, tx, in.ID)
	if err != nil {
		return ModelDiscovery{}, err
	}
	if current.Status == ModelDiscoveryIgnored {
		return current, nil
	}
	if current.Status != ModelDiscoveryPending && current.Status != ModelDiscoveryAbsent {
		return ModelDiscovery{}, ErrModelDiscoveryConflict
	}
	updated, err := updateIgnoredModelDiscoveryTx(ctx, tx, current.ID, in)
	if err != nil {
		return ModelDiscovery{}, err
	}
	if err := insertModelDiscoveryLogTx(ctx, tx, "ignore_model_discovery", current, updated, in); err != nil {
		return ModelDiscovery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ModelDiscovery{}, fmt.Errorf("%w: commit model discovery ignore: %w", ErrRegistryBackend, err)
	}
	return updated, nil
}

func lockModelDiscoveryTx(ctx context.Context, tx pgx.Tx, id int64) (ModelDiscovery, error) {
	item, err := scanModelDiscovery(tx.QueryRow(ctx, `
SELECT id, vendor, model_id_normalized, provider_model_id, display_name, owned_by,
       protocol_family, context_window, model_created_at, capabilities, status,
       first_seen_at, last_seen_at, last_absent_at, decided_at, decided_by_actor,
       decision_reason, promoted_model_id, created_at, updated_at
FROM model_discovery_inbox
WHERE id = $1
FOR UPDATE
`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelDiscovery{}, ErrModelDiscoveryNotFound
	}
	if err != nil {
		return ModelDiscovery{}, fmt.Errorf("%w: lock model discovery: %w", ErrRegistryBackend, err)
	}
	return item, nil
}

func ensureModelDiscoveryPromotionAvailable(ctx context.Context, tx pgx.Tx, item ModelDiscovery) error {
	var modelExists, aliasExists bool
	err := tx.QueryRow(ctx, `
SELECT EXISTS (
           SELECT 1 FROM models
           WHERE scope = 'global' AND tenant_id IS NULL AND deleted_at IS NULL
             AND canonical_id = $1
       ),
       EXISTS (
           SELECT 1 FROM model_aliases
           WHERE scope = 'global' AND tenant_id IS NULL AND deleted_at IS NULL
             AND public_alias_normalized = $2
       )
`, vendorCanonicalID(item.Vendor, item.ProviderModelID), item.ModelIDNormalized).Scan(&modelExists, &aliasExists)
	if err != nil {
		return fmt.Errorf("%w: check model discovery promotion conflicts: %w", ErrRegistryBackend, err)
	}
	if modelExists || aliasExists {
		return ErrModelDiscoveryConflict
	}
	return nil
}

func updatePromotedModelDiscoveryTx(ctx context.Context, tx pgx.Tx, id, modelID int64, in ModelDiscoveryDecision) (ModelDiscovery, error) {
	item, err := scanModelDiscovery(tx.QueryRow(ctx, `
UPDATE model_discovery_inbox
SET status = 'promoted', decided_at = now(), decided_by_actor = $2,
    decision_reason = $3, promoted_model_id = $4, updated_at = now()
WHERE id = $1 AND status = 'pending'
RETURNING id, vendor, model_id_normalized, provider_model_id, display_name, owned_by,
          protocol_family, context_window, model_created_at, capabilities, status,
          first_seen_at, last_seen_at, last_absent_at, decided_at, decided_by_actor,
          decision_reason, promoted_model_id, created_at, updated_at
`, id, in.Access.Actor, in.Reason, modelID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelDiscovery{}, ErrModelDiscoveryConflict
	}
	if err != nil {
		return ModelDiscovery{}, fmt.Errorf("%w: mark model discovery promoted: %w", ErrRegistryBackend, err)
	}
	return item, nil
}

func updateIgnoredModelDiscoveryTx(ctx context.Context, tx pgx.Tx, id int64, in ModelDiscoveryDecision) (ModelDiscovery, error) {
	item, err := scanModelDiscovery(tx.QueryRow(ctx, `
UPDATE model_discovery_inbox
SET status = 'ignored', decided_at = now(), decided_by_actor = $2,
    decision_reason = $3, promoted_model_id = NULL, updated_at = now()
WHERE id = $1 AND status IN ('pending', 'absent')
RETURNING id, vendor, model_id_normalized, provider_model_id, display_name, owned_by,
          protocol_family, context_window, model_created_at, capabilities, status,
          first_seen_at, last_seen_at, last_absent_at, decided_at, decided_by_actor,
          decision_reason, promoted_model_id, created_at, updated_at
`, id, in.Access.Actor, in.Reason))
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelDiscovery{}, ErrModelDiscoveryConflict
	}
	if err != nil {
		return ModelDiscovery{}, fmt.Errorf("%w: mark model discovery ignored: %w", ErrRegistryBackend, err)
	}
	return item, nil
}

func insertModelDiscoveryLogTx(ctx context.Context, tx pgx.Tx, action string, before, after ModelDiscovery, in ModelDiscoveryDecision) error {
	payload, err := json.Marshal(map[string]any{
		"vendor":              after.Vendor,
		"provider_model_id":   after.ProviderModelID,
		"previous_status":     before.Status,
		"new_status":          after.Status,
		"promoted_model_id":   after.PromotedModelID,
		"model_id_normalized": after.ModelIDNormalized,
	})
	if err != nil {
		return fmt.Errorf("%w: encode model discovery log: %v", ErrRegistryBackend, err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO admin_audit_events (
    tenant_id, actor_id, actor_role, action, target_type, target_id,
    request_id, reason, payload, log_category
) VALUES (
    NULL, $1, 'platform_admin', $2, 'model_discovery', $3,
    NULLIF($4, ''), $5, $6::jsonb, 'operation'
)
`, in.Access.Actor, action, after.ID, in.Access.RequestID, in.Reason, payload)
	if err != nil {
		return fmt.Errorf("%w: insert model discovery log: %w", ErrRegistryBackend, err)
	}
	return nil
}

func scanModelDiscovery(scanner modelDiscoveryScanner) (ModelDiscovery, error) {
	var (
		item            ModelDiscovery
		vendor          string
		modelCreatedAt  pgtype.Timestamptz
		lastAbsentAt    pgtype.Timestamptz
		decidedAt       pgtype.Timestamptz
		decidedByActor  pgtype.Text
		decisionReason  pgtype.Text
		promotedModelID pgtype.Int8
	)
	err := scanner.Scan(
		&item.ID, &vendor, &item.ModelIDNormalized, &item.ProviderModelID,
		&item.DisplayName, &item.OwnedBy, &item.ProtocolFamily, &item.ContextWindow,
		&modelCreatedAt, &item.Capabilities, &item.Status, &item.FirstSeenAt,
		&item.LastSeenAt, &lastAbsentAt, &decidedAt, &decidedByActor,
		&decisionReason, &promotedModelID, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return ModelDiscovery{}, err
	}
	item.Vendor = modelsync.Vendor(vendor)
	if !validModelDiscoveryVendor(item.Vendor) || !validModelDiscoveryStatus(item.Status) {
		return ModelDiscovery{}, fmt.Errorf("%w: invalid persisted model discovery enum", ErrRegistryBackend)
	}
	if item.Capabilities == nil {
		item.Capabilities = []string{}
	}
	if modelCreatedAt.Valid {
		value := modelCreatedAt.Time.UTC()
		item.ModelCreatedAt = &value
	}
	if lastAbsentAt.Valid {
		value := lastAbsentAt.Time.UTC()
		item.LastAbsentAt = &value
	}
	if decidedAt.Valid {
		value := decidedAt.Time.UTC()
		item.DecidedAt = &value
	}
	if decidedByActor.Valid {
		value := decidedByActor.String
		item.DecidedByActor = &value
	}
	if decisionReason.Valid {
		value := decisionReason.String
		item.DecisionReason = &value
	}
	if promotedModelID.Valid {
		value := promotedModelID.Int64
		item.PromotedModelID = &value
	}
	item.FirstSeenAt = item.FirstSeenAt.UTC()
	item.LastSeenAt = item.LastSeenAt.UTC()
	item.CreatedAt = item.CreatedAt.UTC()
	item.UpdatedAt = item.UpdatedAt.UTC()
	return item, nil
}
