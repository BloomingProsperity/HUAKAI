package registry

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
)

type modelDiscoverySyncOutcome string

const (
	discoverySyncNone       modelDiscoverySyncOutcome = "none"
	discoverySyncDiscovered modelDiscoverySyncOutcome = "discovered"
	discoverySyncUpdated    modelDiscoverySyncOutcome = "updated"
	discoverySyncIgnored    modelDiscoverySyncOutcome = "ignored"
	discoverySyncUnchanged  modelDiscoverySyncOutcome = "unchanged"
)

func loadVendorDiscoveryStates(ctx context.Context, tx pgx.Tx, vendor modelsync.Vendor) ([]vendorDiscoveryState, error) {
	rows, err := tx.Query(ctx, `
SELECT model_id_normalized, status
FROM model_discovery_inbox
WHERE vendor = $1
`, string(vendor))
	if err != nil {
		return nil, fmt.Errorf("%w: list vendor model discoveries: %w", ErrRegistryBackend, err)
	}
	defer rows.Close()
	states := make([]vendorDiscoveryState, 0)
	for rows.Next() {
		var state vendorDiscoveryState
		if err := rows.Scan(&state.ModelIDNormalized, &state.Status); err != nil {
			return nil, fmt.Errorf("%w: scan vendor model discovery: %w", ErrRegistryBackend, err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: vendor model discovery rows: %w", ErrRegistryBackend, err)
	}
	return states, nil
}

func syncModelDiscoveryTx(ctx context.Context, tx pgx.Tx, vendor modelsync.Vendor, model modelsync.Model, allowInsert bool) (modelDiscoverySyncOutcome, string, error) {
	normalized, err := normalizeDiscoveredModel(vendor, model)
	if err != nil {
		return discoverySyncNone, "", err
	}
	var (
		id                  int64
		status              string
		providerModelID     string
		displayName         string
		ownedBy             string
		protocolFamily      string
		contextWindow       int
		modelCreatedAt      pgtype.Timestamptz
		currentCapabilities []string
	)
	err = tx.QueryRow(ctx, `
SELECT id, status, provider_model_id, display_name, owned_by, protocol_family,
       context_window, model_created_at, capabilities
FROM model_discovery_inbox
WHERE vendor = $1 AND model_id_normalized = $2
FOR UPDATE
`, string(vendor), normalized.ModelIDNormalized).Scan(
		&id, &status, &providerModelID, &displayName, &ownedBy, &protocolFamily,
		&contextWindow, &modelCreatedAt, &currentCapabilities,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if !allowInsert {
			return discoverySyncNone, normalized.ProviderModelID, nil
		}
		_, err = tx.Exec(ctx, `
INSERT INTO model_discovery_inbox (
    vendor, model_id_normalized, provider_model_id, display_name, owned_by,
    protocol_family, context_window, model_created_at, capabilities, status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending')
`, string(vendor), normalized.ModelIDNormalized, normalized.ProviderModelID,
			normalized.DisplayName, normalized.OwnedBy, normalized.ProtocolFamily,
			normalized.ContextWindow, normalized.ModelCreatedAt, normalized.Capabilities)
		if err != nil {
			return discoverySyncNone, "", fmt.Errorf("%w: insert model discovery: %w", ErrRegistryBackend, err)
		}
		return discoverySyncDiscovered, normalized.ProviderModelID, nil
	}
	if err != nil {
		return discoverySyncNone, "", fmt.Errorf("%w: lock model discovery: %w", ErrRegistryBackend, err)
	}

	metadataChanged := providerModelID != normalized.ProviderModelID ||
		displayName != normalized.DisplayName || ownedBy != normalized.OwnedBy ||
		protocolFamily != normalized.ProtocolFamily || contextWindow != normalized.ContextWindow ||
		!sameNullableTime(modelCreatedAt, normalized.ModelCreatedAt) ||
		!slices.Equal(currentCapabilities, normalized.Capabilities)
	_, err = tx.Exec(ctx, `
UPDATE model_discovery_inbox
SET provider_model_id = $2,
    display_name = $3,
    owned_by = $4,
    protocol_family = $5,
    context_window = $6,
    model_created_at = $7,
    capabilities = $8,
    status = CASE WHEN status = 'absent' THEN 'pending' ELSE status END,
    last_seen_at = now(),
    updated_at = now()
WHERE id = $1
`, id, normalized.ProviderModelID, normalized.DisplayName, normalized.OwnedBy,
		normalized.ProtocolFamily, normalized.ContextWindow, normalized.ModelCreatedAt,
		normalized.Capabilities)
	if err != nil {
		return discoverySyncNone, "", fmt.Errorf("%w: refresh model discovery: %w", ErrRegistryBackend, err)
	}
	switch status {
	case ModelDiscoveryAbsent:
		return discoverySyncDiscovered, normalized.ProviderModelID, nil
	case ModelDiscoveryIgnored:
		return discoverySyncIgnored, normalized.ProviderModelID, nil
	case ModelDiscoveryPending:
		if metadataChanged {
			return discoverySyncUpdated, normalized.ProviderModelID, nil
		}
		return discoverySyncUnchanged, normalized.ProviderModelID, nil
	case ModelDiscoveryPromoted:
		return discoverySyncUnchanged, normalized.ProviderModelID, nil
	default:
		return discoverySyncNone, "", fmt.Errorf("%w: unknown model discovery status %q", ErrRegistryBackend, status)
	}
}

// InvestAccountModelDiscoveriesTx 把账号级发现的模型投进发现箱(上架管道第 2 关)。
// 复用 vendor 级同步的写入原语,在调用方提供的事务内执行,与账号白名单写入保持原子:
// 白名单回滚则投箱一并回滚。单个模型 id 非法(规范化失败)只跳过该项,不拖垮其余模型
// 与白名单提交;后端错误照常上抛触发回滚。vendor 无发现箱合同(如自定义 upstream_static
// 归一化出的非枚举 vendor)时静默跳过,只走白名单。返回本次真正新入箱或元数据更新的条数。
func (r *PostgresRegistry) InvestAccountModelDiscoveriesTx(ctx context.Context, tx pgx.Tx, vendor modelsync.Vendor, models []modelsync.Model) (int, error) {
	if tx == nil {
		return 0, ErrRegistryBackend
	}
	if !validModelDiscoveryVendor(vendor) {
		return 0, nil
	}
	invested := 0
	for _, model := range models {
		outcome, _, err := syncModelDiscoveryTx(ctx, tx, vendor, model, true)
		if err != nil {
			if errors.Is(err, ErrModelDiscoveryInvalid) {
				continue
			}
			return invested, err
		}
		switch outcome {
		case discoverySyncDiscovered, discoverySyncUpdated:
			invested++
		}
	}
	return invested, nil
}

func markModelDiscoveriesAbsentTx(ctx context.Context, tx pgx.Tx, vendor modelsync.Vendor, normalizedIDs []string) ([]string, error) {
	if len(normalizedIDs) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
UPDATE model_discovery_inbox
SET status = 'absent',
    last_absent_at = now(),
    updated_at = now()
WHERE vendor = $1
  AND status = 'pending'
  AND model_id_normalized = ANY($2::text[])
RETURNING provider_model_id
`, string(vendor), normalizedIDs)
	if err != nil {
		return nil, fmt.Errorf("%w: mark model discoveries absent: %w", ErrRegistryBackend, err)
	}
	defer rows.Close()
	removed := make([]string, 0, len(normalizedIDs))
	for rows.Next() {
		var modelID string
		if err := rows.Scan(&modelID); err != nil {
			return nil, fmt.Errorf("%w: scan absent model discovery: %w", ErrRegistryBackend, err)
		}
		removed = append(removed, modelID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: absent model discovery rows: %w", ErrRegistryBackend, err)
	}
	sort.Strings(removed)
	return removed, nil
}

func sameNullableTime(current pgtype.Timestamptz, expected *time.Time) bool {
	if !current.Valid {
		return expected == nil
	}
	return expected != nil && current.Time.Equal(expected.UTC())
}
