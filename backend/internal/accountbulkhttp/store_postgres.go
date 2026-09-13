package accountbulkhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
)

// FieldUpdate 是一次逐项字段写入(启停 / 优先级 / 权重)的参数。
type FieldUpdate struct {
	TenantID     int64
	AccountID    int64
	ActorID      string
	ActorRole    string
	RequestID    string
	Reason       string
	Enabled      *bool
	Priority     *int32
	StaticWeight *int32
	DryRun       bool
}

// ChannelMove 是一次逐项改渠道的参数。
type ChannelMove struct {
	TenantID         int64
	AccountID        int64
	TargetChannelID  int64
	ConfirmMixedRisk bool
	ActorID          string
	ActorRole        string
	RequestID        string
	Reason           string
	DryRun           bool
}

// BatchAudit 是批次级日志。
type BatchAudit struct {
	TenantID  int64
	ActorID   string
	ActorRole string
	RequestID string
	Reason    string
	Action    Action
	Summary   map[string]any
}

// Store 是批量运维需要的写入面;每个方法都是一个独立的短事务。
type Store interface {
	ApplyFieldUpdate(ctx context.Context, arg FieldUpdate) (ItemResult, error)
	MoveChannel(ctx context.Context, arg ChannelMove) (ItemResult, error)
	InsertBatchAudit(ctx context.Context, arg BatchAudit) (int64, error)
}

// PostgresStore 以 provider_accounts / channels / admin_audit_events 为真相实现 Store。
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore 构造 PostgreSQL 写入面。
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	if pool == nil {
		return nil
	}
	return &PostgresStore{pool: pool}
}

// ApplyFieldUpdate 在一个事务内锁定账号、判定是否已达期望态、写字段并落逐项审计。
// 他租户 / 已删除 / 不存在 → not_found;dry-run 只读不写。
func (s *PostgresStore) ApplyFieldUpdate(ctx context.Context, arg FieldUpdate) (ItemResult, error) {
	if s == nil || s.pool == nil {
		return ItemResult{}, errors.New("accountbulk: postgres store not configured")
	}
	result := ItemResult{ID: arg.AccountID}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		current, err := q.LockProviderAccountForBulk(ctx, admindb.LockProviderAccountForBulkParams{ID: arg.AccountID, TenantID: arg.TenantID})
		if errors.Is(err, pgx.ErrNoRows) {
			result = notFound(arg.AccountID)
			return nil
		}
		if err != nil {
			return err
		}
		if (arg.Enabled == nil || *arg.Enabled == current.Enabled) &&
			(arg.Priority == nil || *arg.Priority == current.Priority) &&
			(arg.StaticWeight == nil || *arg.StaticWeight == current.StaticWeight) {
			result = ItemResult{ID: arg.AccountID, Status: StatusSkipped, Code: CodeAlreadyInDesiredState, Message: "account already in the desired state"}
			return nil
		}
		if arg.DryRun {
			result = ItemResult{ID: arg.AccountID, Status: StatusWouldApply, Detail: map[string]any{
				"before": fieldSnapshot(current.Enabled, current.Priority, current.StaticWeight),
				"after":  fieldSnapshot(pick(arg.Enabled, current.Enabled), pick(arg.Priority, current.Priority), pick(arg.StaticWeight, current.StaticWeight)),
			}}
			return nil
		}
		actorID := arg.ActorID
		if _, err := q.UpdateAdminProviderAccount(ctx, admindb.UpdateAdminProviderAccountParams{
			ID: arg.AccountID, TenantID: arg.TenantID, ActorID: &actorID,
			Enabled: arg.Enabled, Priority: arg.Priority, StaticWeight: arg.StaticWeight,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				result = notFound(arg.AccountID)
				return nil
			}
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"tenant_id": arg.TenantID, "id": arg.AccountID, "selection": "ids",
			"before": fieldSnapshot(current.Enabled, current.Priority, current.StaticWeight),
			"after":  fieldSnapshot(pick(arg.Enabled, current.Enabled), pick(arg.Priority, current.Priority), pick(arg.StaticWeight, current.StaticWeight)),
		})
		if err != nil {
			return err
		}
		if err := insertItemAudit(ctx, q, arg.TenantID, arg.AccountID, arg.ActorID, arg.ActorRole, arg.RequestID, arg.Reason, "update_provider_account", payload); err != nil {
			return err
		}
		result = ItemResult{ID: arg.AccountID, Status: StatusSucceeded}
		return nil
	})
	if err != nil {
		return ItemResult{}, fmt.Errorf("accountbulk: apply field update: %w", err)
	}
	return result, nil
}

// MoveChannel 在一个事务内锁定账号、校验目标渠道属于同一租户、按目标渠道现有账号做混合风险判定
// (高风险且未确认 → 该项拒绝并附报告)、写 channel_id 并落逐项审计。与建号一样用 (tenant, channel)
// 咨询锁串行化同一目标渠道上的并发写,避免两个"空渠道"并发绕过风险门。
func (s *PostgresStore) MoveChannel(ctx context.Context, arg ChannelMove) (ItemResult, error) {
	if s == nil || s.pool == nil {
		return ItemResult{}, errors.New("accountbulk: postgres store not configured")
	}
	result := ItemResult{ID: arg.AccountID}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		current, err := q.LockProviderAccountForBulk(ctx, admindb.LockProviderAccountForBulkParams{ID: arg.AccountID, TenantID: arg.TenantID})
		if errors.Is(err, pgx.ErrNoRows) {
			result = notFound(arg.AccountID)
			return nil
		}
		if err != nil {
			return err
		}
		if current.ChannelID == arg.TargetChannelID {
			result = ItemResult{ID: arg.AccountID, Status: StatusSkipped, Code: CodeInvalidTargetSameChannel, Message: "account already belongs to the target channel"}
			return nil
		}
		target, err := q.GetChannelForBulkMove(ctx, admindb.GetChannelForBulkMoveParams{ID: arg.TargetChannelID, TenantID: arg.TenantID})
		if errors.Is(err, pgx.ErrNoRows) {
			result = ItemResult{ID: arg.AccountID, Status: StatusFailed, Code: CodeChannelNotFound, Message: "target channel is not available in this tenant"}
			return nil
		}
		if err != nil {
			return err
		}
		lockKey := fmt.Sprintf("provider-account-mixed-risk:%d:%d", arg.TenantID, arg.TargetChannelID)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey); err != nil {
			return err
		}
		peers, err := q.ListProviderAccountRiskPeers(ctx, admindb.ListProviderAccountRiskPeersParams{TenantID: arg.TenantID, ChannelID: arg.TargetChannelID})
		if err != nil {
			return err
		}
		self, err := q.ListProviderAccountRiskPeers(ctx, admindb.ListProviderAccountRiskPeersParams{TenantID: arg.TenantID, ChannelID: current.ChannelID})
		if err != nil {
			return err
		}
		candidate := mixedchannelrisk.Account{ID: arg.AccountID, ProviderID: current.ProviderID, ChannelID: arg.TargetChannelID, AccountType: current.AccountType}
		for _, row := range self {
			if row.ID == arg.AccountID {
				candidate.Vendor, candidate.AuthMode = row.CredentialVendor, row.CredentialAuthMode
			}
		}
		report := mixedchannelrisk.Evaluate(candidate, riskPeers(peers))
		if report.HighRisk && !arg.ConfirmMixedRisk {
			result = ItemResult{ID: arg.AccountID, Status: StatusFailed, Code: CodeMixedRiskConfirmRequired,
				Message: "moving this account into the target channel mixes sources/vendors/modes; resend with confirm_mixed_risk=true",
				Detail:  map[string]any{"mixed_risk": report, "target_channel": channelSnapshot(target)}}
			if arg.DryRun {
				result.Status = StatusWouldReject
			}
			return nil
		}
		if arg.DryRun {
			result = ItemResult{ID: arg.AccountID, Status: StatusWouldApply, Detail: map[string]any{
				"from_channel_id": current.ChannelID, "target_channel": channelSnapshot(target), "mixed_risk": report,
			}}
			return nil
		}
		actorID := arg.ActorID
		n, err := q.UpdateProviderAccountChannel(ctx, admindb.UpdateProviderAccountChannelParams{
			ID: arg.AccountID, TenantID: arg.TenantID, ChannelID: arg.TargetChannelID, ActorID: &actorID,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			result = notFound(arg.AccountID)
			return nil
		}
		payload, err := json.Marshal(map[string]any{
			"tenant_id": arg.TenantID, "id": arg.AccountID, "selection": "ids",
			"before":          map[string]any{"channel_id": current.ChannelID},
			"after":           map[string]any{"channel_id": arg.TargetChannelID, "pool_group_id": target.PoolGroupID},
			"mixed_risk_high": report.HighRisk, "mixed_risk_confirmed": arg.ConfirmMixedRisk,
		})
		if err != nil {
			return err
		}
		if err := insertItemAudit(ctx, q, arg.TenantID, arg.AccountID, arg.ActorID, arg.ActorRole, arg.RequestID, arg.Reason, "move_provider_account_channel", payload); err != nil {
			return err
		}
		result = ItemResult{ID: arg.AccountID, Status: StatusSucceeded, Detail: map[string]any{"from_channel_id": current.ChannelID, "to_channel_id": arg.TargetChannelID}}
		return nil
	})
	if err != nil {
		return ItemResult{}, fmt.Errorf("accountbulk: move channel: %w", err)
	}
	return result, nil
}

// InsertBatchAudit 落一条批次级日志(动作、选择形状、计数),不含任何账号秘密。
func (s *PostgresStore) InsertBatchAudit(ctx context.Context, arg BatchAudit) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, errors.New("accountbulk: postgres store not configured")
	}
	summary := make(map[string]any, len(arg.Summary)+1)
	for k, v := range arg.Summary {
		summary[k] = v
	}
	summary["action"] = string(arg.Action)
	payload, err := json.Marshal(summary)
	if err != nil {
		return 0, err
	}
	tenant := arg.TenantID
	var requestID, reason *string
	if arg.RequestID != "" {
		requestID = &arg.RequestID
	}
	if arg.Reason != "" {
		reason = &arg.Reason
	}
	row, err := admindb.New(s.pool).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &tenant, ActorID: arg.ActorID, ActorRole: arg.ActorRole,
		Action: "bulk_provider_accounts", TargetType: "provider_account_batch",
		RequestID: requestID, Reason: reason, Payload: payload,
	})
	if err != nil {
		return 0, fmt.Errorf("accountbulk: insert batch audit: %w", err)
	}
	return row.ID, nil
}

func insertItemAudit(ctx context.Context, q *admindb.Queries, tenantID, accountID int64, actorID, actorRole, requestID, reason, action string, payload []byte) error {
	tenant, target := tenantID, accountID
	var reqID, why *string
	if requestID != "" {
		reqID = &requestID
	}
	if reason != "" {
		why = &reason
	}
	_, err := q.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID: &tenant, ActorID: actorID, ActorRole: actorRole, Action: action,
		TargetType: "provider_account", TargetID: &target, RequestID: reqID, Reason: why, Payload: payload,
	})
	return err
}

func notFound(id int64) ItemResult {
	return ItemResult{ID: id, Status: StatusFailed, Code: CodeNotFound, Message: "provider account not found in this tenant"}
}

func fieldSnapshot(enabled bool, priority, weight int32) map[string]any {
	return map[string]any{"enabled": enabled, "priority": priority, "static_weight": weight}
}

func channelSnapshot(c admindb.GetChannelForBulkMoveRow) map[string]any {
	return map[string]any{"id": c.ID, "name": c.Name, "pool_group_id": c.PoolGroupID, "enabled": c.Enabled}
}

func pick[T any](v *T, current T) T {
	if v == nil {
		return current
	}
	return *v
}

func riskPeers(rows []admindb.ProviderAccountRiskPeerRow) []mixedchannelrisk.Account {
	out := make([]mixedchannelrisk.Account, 0, len(rows))
	for _, row := range rows {
		out = append(out, mixedchannelrisk.Account{
			ID: row.ID, ProviderID: row.ProviderID, ChannelID: row.ChannelID,
			AccountType: row.AccountType, Vendor: row.CredentialVendor, AuthMode: row.CredentialAuthMode,
		})
	}
	return out
}
