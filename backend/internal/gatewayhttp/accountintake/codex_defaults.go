package accountintake

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

var codexDefaultCapabilities = []string{"stream", "tools", "json", "vision", "image_output"}

func isCodexIntake(in PlanInput) bool {
	if in.SourceKind == intake.SourceCLI || in.SourceKind == intake.SourceCodexAgent {
		return true
	}
	if in.DefaultVendor != credentialstore.VendorOpenAI {
		return false
	}
	switch in.DefaultAuthMode {
	case credentialstore.AuthModeCodexCLIOAuth,
		credentialstore.AuthModeCodexWebOAuth,
		credentialstore.AuthModeCodexAgent:
		return true
	default:
		return false
	}
}

func applyCodexAccountDefaults(in PlanInput) PlanInput {
	if in.Account.NamePrefix == "" && in.Account.ExactName == "" {
		in.Account.NamePrefix = "codex"
	}
	if in.Account.AccountType == "" {
		in.Account.AccountType = "oauth"
	}
	if in.Account.CapConcurrency == nil {
		value := int32(3)
		in.Account.CapConcurrency = &value
	}
	if in.Account.Priority == nil {
		value := int32(50)
		in.Account.Priority = &value
	}
	for _, capability := range codexDefaultCapabilities {
		in.Account.CapabilityFlags = appendUnique(in.Account.CapabilityFlags, capability)
	}
	return in
}

type codexLane struct {
	providerID int64
	channelID  int64
}

func (s *Service) resolveCodexLane(ctx context.Context, tenantID int64, account AccountDefaults) (AccountDefaults, error) {
	if account.ProviderID < 0 || account.ChannelID < 0 {
		return AccountDefaults{}, ErrInvalidInput
	}
	if tenantID <= 0 {
		return AccountDefaults{}, ErrInvalidInput
	}
	if s == nil || s.pool == nil {
		return AccountDefaults{}, ErrNotConfigured
	}

	rows, err := s.pool.Query(ctx, `
SELECT DISTINCT p.id, c.id
FROM providers p
JOIN channels c
  ON c.tenant_id = p.tenant_id
JOIN pool_groups pg
  ON pg.tenant_id = c.tenant_id
 AND pg.id = c.pool_group_id
JOIN model_pool_bindings mpb
  ON mpb.tenant_id = pg.tenant_id
 AND mpb.pool_group_id = pg.id
JOIN models m
  ON m.id = mpb.model_id
WHERE p.tenant_id = $1
  AND p.upstream_protocol = 'openai_codex'
  AND p.enabled = true
  AND p.deleted_at IS NULL
  AND c.enabled = true
  AND c.deleted_at IS NULL
  AND pg.enabled = true
  AND pg.deleted_at IS NULL
  AND mpb.enabled = true
  AND mpb.deleted_at IS NULL
  AND (mpb.effective_from IS NULL OR mpb.effective_from <= clock_timestamp())
  AND (mpb.effective_until IS NULL OR mpb.effective_until > clock_timestamp())
  AND m.protocol_family = 'openai_codex'
  AND m.status = 'active'
  AND m.deleted_at IS NULL
  AND ($2::bigint = 0 OR p.id = $2)
  AND ($3::bigint = 0 OR c.id = $3)
ORDER BY p.id, c.id
LIMIT 2`, tenantID, account.ProviderID, account.ChannelID)
	if err != nil {
		return AccountDefaults{}, fmt.Errorf("查询 Codex 可运行车道失败: %w", err)
	}
	defer rows.Close()

	lanes := make([]codexLane, 0, 2)
	for rows.Next() {
		var lane codexLane
		if err := rows.Scan(&lane.providerID, &lane.channelID); err != nil {
			return AccountDefaults{}, fmt.Errorf("读取 Codex 可运行车道失败: %w", err)
		}
		lanes = append(lanes, lane)
	}
	if err := rows.Err(); err != nil {
		return AccountDefaults{}, fmt.Errorf("遍历 Codex 可运行车道失败: %w", err)
	}
	switch len(lanes) {
	case 0:
		return AccountDefaults{}, ErrCodexLaneAbsent
	case 1:
		account.ProviderID = lanes[0].providerID
		account.ChannelID = lanes[0].channelID
		return account, nil
	default:
		return AccountDefaults{}, ErrCodexLaneMany
	}
}

func (s *Service) rejectUnrunnableCodexUpdates(ctx context.Context, q *admindb.Queries, tenantID int64, plan *intake.Plan) error {
	if plan == nil {
		return nil
	}
	for index := range plan.Items {
		item := &plan.Items[index]
		if item.Action != intake.ActionUpdate {
			continue
		}
		account, err := q.GetAdminProviderAccount(ctx, admindb.GetAdminProviderAccountParams{
			ID: item.ExistingAccountID, TenantID: tenantID,
		})
		if err != nil {
			return fmt.Errorf("查询待更新 Codex 账号失败: %w", err)
		}
		_, err = s.resolveCodexLane(ctx, tenantID, AccountDefaults{
			ProviderID: account.ProviderID, ChannelID: account.ChannelID,
		})
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrCodexLaneAbsent) {
			return err
		}
		item.Action = intake.ActionFail
		item.Code = "existing_codex_lane_not_runnable"
		item.Message = "已有 Codex 账号所在的路由车道当前不可运行"
		item.FieldChanges = nil
		item.RequiredConfirmations = nil
	}
	recountPlan(plan)
	return nil
}

func lockRunnableCodexLane(ctx context.Context, tx pgx.Tx, tenantID, providerID, channelID int64) error {
	if tx == nil || tenantID <= 0 || providerID <= 0 || channelID <= 0 {
		return ErrInvalidInput
	}
	var lockedProviderID, lockedChannelID int64
	err := tx.QueryRow(ctx, `
SELECT p.id, c.id
FROM providers p
JOIN channels c
  ON c.tenant_id = p.tenant_id
JOIN pool_groups pg
  ON pg.tenant_id = c.tenant_id
 AND pg.id = c.pool_group_id
JOIN model_pool_bindings mpb
  ON mpb.tenant_id = pg.tenant_id
 AND mpb.pool_group_id = pg.id
JOIN models m
  ON m.id = mpb.model_id
WHERE p.tenant_id = $1
  AND p.id = $2
  AND c.id = $3
  AND p.upstream_protocol = 'openai_codex'
  AND p.enabled = true
  AND p.deleted_at IS NULL
  AND c.enabled = true
  AND c.deleted_at IS NULL
  AND pg.enabled = true
  AND pg.deleted_at IS NULL
  AND mpb.enabled = true
  AND mpb.deleted_at IS NULL
  AND (mpb.effective_from IS NULL OR mpb.effective_from <= clock_timestamp())
  AND (mpb.effective_until IS NULL OR mpb.effective_until > clock_timestamp())
  AND m.protocol_family = 'openai_codex'
  AND m.status = 'active'
  AND m.deleted_at IS NULL
ORDER BY mpb.id, m.id
LIMIT 1
FOR SHARE OF p, c, pg, mpb, m`, tenantID, providerID, channelID).Scan(&lockedProviderID, &lockedChannelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCodexLaneAbsent
	}
	if err != nil {
		return fmt.Errorf("锁定 Codex 可运行车道失败: %w", err)
	}
	return nil
}

func ensureCodexCapabilities(ctx context.Context, tx pgx.Tx, tenantID, accountID int64) error {
	if tx == nil || tenantID <= 0 || accountID <= 0 {
		return ErrInvalidInput
	}
	tag, err := tx.Exec(ctx, `
UPDATE provider_accounts
SET capability_flags = ARRAY(
        SELECT DISTINCT capability
        FROM unnest(capability_flags || $3::text[]) AS capability
        ORDER BY capability
    ),
    updated_at = clock_timestamp()
WHERE tenant_id = $1
  AND id = $2
  AND deleted_at IS NULL`, tenantID, accountID, codexDefaultCapabilities)
	if err != nil {
		return fmt.Errorf("补齐 Codex 账号能力失败: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrExecutionStale
	}
	return nil
}
