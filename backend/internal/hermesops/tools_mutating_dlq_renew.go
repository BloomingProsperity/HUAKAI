package hermesops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// 本文件声明死信重放与凭据轮换两个改动工具。死信重放包含外部投递副作用，使用独立恢复合同；
// 凭据轮换则加入编排器事务，使凭据、凭据日志和 Hermes 日志同成同败。

// ---------------------------------------------------------------------------
// dlq_replay (仅限 platform_admin)
// ---------------------------------------------------------------------------

// DLQReplayDeps 把 dlq_replay 工具接上依赖。Lookup 是 Resolve 用来读取目标记录
// (按租户限定)以供预览 + 租户复检的读取。Replay 是已有的 dlq.Service.Replay,
// 它按 id 重新 claim 该记录(用其 IdempotencyKey 去重)并重新运行投递 handler。
type DLQReplayDeps struct {
	Lookup func(ctx context.Context, id, tenantID int64) (dlq.Record, error)
	Replay func(ctx context.Context, id int64, actorID string) (*dlq.Record, error)
}

// DLQReplaySpec 构建 dlq_replay 改动型工具。仅限 platform_admin —— 禁止 tenant_operator
// (RBAC 底线是 RolePlatformAdmin,与 admin DLQ handler 的 platform-admin 门一致)。Args: { "id": <int64> }。
func DLQReplaySpec(deps DLQReplayDeps) ToolSpec {
	return ToolSpec{
		Name:                 ToolDLQReplay,
		Category:             CategoryMutating,
		Description:          "Re-deliver a dead-lettered event by id (idempotent on the record's idempotency key). platform_admin ONLY. MUTATING — dry-run + confirm required.",
		ReadOnly:             false,
		Mutating:             true,
		RequiresConfirmation: true,
		RequiredRole:         RolePlatformAdmin,
		InputSchema: ObjectSchema(map[string]any{
			"id": PositiveIntegerSchema("要重放的死信记录 ID"),
		}, "id"),
		Resolve: func(ctx context.Context, req ToolRequest) (MutationPlan, error) {
			if deps.Lookup == nil || deps.Replay == nil {
				return MutationPlan{}, ErrDependencyUnwired
			}
			id, err := ArgInt(req.Args, "id")
			if err != nil {
				return MutationPlan{}, err
			}
			rec, err := deps.Lookup(ctx, id, req.TenantID)
			if err != nil {
				return MutationPlan{}, fmt.Errorf("%w: dlq record %d not found for tenant %d", ErrTargetResolution, id, req.TenantID)
			}
			if rec.TenantID != req.TenantID {
				return MutationPlan{}, fmt.Errorf("%w: dlq record tenant mismatch", ErrTargetResolution)
			}
			return MutationPlan{
				TargetType: "dlq_event",
				TargetID:   id,
				LockKey:    fmt.Sprintf("hermes:dlq_replay:%d:%d", req.TenantID, id),
				Preview: map[string]any{
					"target_type":     "dlq_event",
					"dlq_id":          id,
					"event_kind":      string(rec.EventKind),
					"lane":            string(rec.Lane),
					"current_status":  string(rec.Status),
					"replay_attempts": rec.ReplayAttempts,
					"intended_action": "re_deliver",
					// already_delivered 让预览如实反映:这次 replay 会被幂等守卫变成 no-op。
					"already_delivered": rec.Status == dlq.StatusDelivered,
				},
			}, nil
		},
		Mutate: func(ctx context.Context, req ToolRequest, plan MutationPlan) (ToolResult, error) {
			if deps.Replay == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			// L5 幂等:Replay 按 id 重新 claim,记录的 IdempotencyKey 负责去重 ——
			// 已投递的记录不会被重新处理(ClaimByID 会拒绝一个 active/closed 的 claim)。
			// actorID 是传入的 operator 用户 id。
			actorID := fmt.Sprintf("%s:%d", req.ActorSource, req.ActorID)
			rec, err := deps.Replay(ctx, plan.TargetID, actorID)
			if err != nil {
				if errors.Is(err, dlq.ErrNotFound) {
					return ToolResult{Summary: map[string]any{
						"dlq_id":          plan.TargetID,
						"previous_status": plan.Preview["current_status"],
						"status":          "already_processed",
						"idempotent":      true,
						"message":         "DLQ 记录已处理或已由其他副本投递,无需重复 replay",
					}}, nil
				}
				return ToolResult{}, err
			}
			summary := map[string]any{
				"dlq_id":          plan.TargetID,
				"previous_status": plan.Preview["current_status"],
			}
			if rec != nil {
				summary["status"] = string(rec.Status)
				summary["replay_attempts"] = rec.ReplayAttempts
			}
			return ToolResult{Summary: summary}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// renew_trigger (platform_admin 或租户内的 tenant_operator)
// ---------------------------------------------------------------------------

// RenewTriggerDeps 把 renew_trigger 工具接上依赖。ListByAccount 是 Resolve 用来读取凭证
// 当前版本 + 校验租户归属的读取。Rotate 是已有的 credentialstore.Store.Rotate,
// 它原子地取代上一版凭证(乐观的 credential_version 匹配 -> version+1),且只返回元数据
// (绝不返回轮换后的 payload)。
type RenewTriggerDeps struct {
	ListByAccount func(ctx context.Context, tenantID, accountID int64) ([]credentialstore.CredentialMetadata, error)
	RotateTx      func(ctx context.Context, tx pgx.Tx, in credentialstore.RotateCredentialInput) (credentialstore.CredentialMetadata, error)
}

// RenewTriggerSpec 构建 renew_trigger 改动型工具:它把某个 provider account 的凭证轮换到
// 新 payload,使上一版失效。scope:platform_admin 或目标租户内的 tenant_operator。Args:
// { "account_id": <int64>, "credential_id": <int64>, "credentials": <object> }。
// 隐私:新凭证材料("credentials")会被接收,但它是敏感参数(在审计行里被脱敏);
// 轮换后的材料绝不返回 —— 只返回结果版本 + state。
func RenewTriggerSpec(deps RenewTriggerDeps) ToolSpec {
	return ToolSpec{
		Name:                 ToolRenewTrigger,
		Category:             CategoryMutating,
		Description:          "Rotate a provider account credential to a new payload, invalidating the prior version. MUTATING — dry-run + confirm required. Rotated material is never returned.",
		ReadOnly:             false,
		Mutating:             true,
		RequiresConfirmation: true,
		RequiredRole:         RoleTenantOperator,
		InputSchema: ObjectSchema(map[string]any{
			"account_id":    PositiveIntegerSchema("凭据所属的上游账号 ID"),
			"credential_id": PositiveIntegerSchema("要轮换的凭据 ID"),
			"credentials":   ObjectValueSchema("新凭据载荷，日志中会脱敏"),
		}, "account_id", "credential_id", "credentials"),
		Resolve: func(ctx context.Context, req ToolRequest) (MutationPlan, error) {
			if deps.ListByAccount == nil || deps.RotateTx == nil {
				return MutationPlan{}, ErrDependencyUnwired
			}
			accountID, err := ArgInt(req.Args, "account_id")
			if err != nil {
				return MutationPlan{}, err
			}
			credentialID, err := ArgInt(req.Args, "credential_id")
			if err != nil {
				return MutationPlan{}, err
			}
			rows, err := deps.ListByAccount(ctx, req.TenantID, accountID)
			if err != nil {
				return MutationPlan{}, fmt.Errorf("%w: account %d credentials not readable for tenant %d", ErrTargetResolution, accountID, req.TenantID)
			}
			var target *credentialstore.CredentialMetadata
			for i := range rows {
				if rows[i].ID == credentialID {
					target = &rows[i]
					break
				}
			}
			if target == nil {
				return MutationPlan{}, fmt.Errorf("%w: credential %d not found on account %d", ErrTargetResolution, credentialID, accountID)
			}
			if target.TenantID != req.TenantID {
				return MutationPlan{}, fmt.Errorf("%w: credential tenant mismatch", ErrTargetResolution)
			}
			return MutationPlan{
				TargetType: "account_credential",
				TargetID:   credentialID,
				LockKey:    fmt.Sprintf("hermes:renew_trigger:%d:%d", req.TenantID, credentialID),
				Preview: map[string]any{
					"target_type":     "account_credential",
					"account_id":      accountID,
					"credential_id":   credentialID,
					"vendor":          target.Vendor,
					"current_version": target.Version,
					"current_state":   target.State,
					"next_version":    target.Version + 1,
					"intended_action": "rotate",
				},
			}, nil
		},
		Mutate: func(ctx context.Context, req ToolRequest, plan MutationPlan) (ToolResult, error) {
			if deps.RotateTx == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			tx := txFromContext(ctx)
			if tx == nil {
				return ToolResult{}, ErrDependencyUnwired
			}
			accountID, err := ArgInt(req.Args, "account_id")
			if err != nil {
				return ToolResult{}, err
			}
			payload, err := rotatePayload(req.Args)
			if err != nil {
				return ToolResult{}, err
			}
			meta, err := deps.RotateTx(ctx, tx, credentialstore.RotateCredentialInput{
				TenantID:          req.TenantID,
				ProviderAccountID: accountID,
				CredentialID:      plan.TargetID,
				Payload:           payload,
				ActorID:           fmt.Sprintf("%s:%d", req.ActorSource, req.ActorID),
			})
			if err != nil {
				return ToolResult{}, err
			}
			// 隐私:只露出结果版本 + state —— 绝不露轮换后的 payload
			// (CredentialMetadata 本身就不带 payload 字段)。
			return ToolResult{Summary: map[string]any{
				"credential_id":    plan.TargetID,
				"previous_version": plan.Preview["current_version"],
				"new_version":      meta.Version,
				"state":            meta.State,
			}}, nil
		},
	}
}

// rotatePayload 从 args 中提取新凭证 payload,并重新编码为 JSON 供 Rotate 使用。该 payload
// 绝不以原始形式持久化 —— 它只流入轮换;审计行会对 "credentials" 键脱敏。
func rotatePayload(args map[string]any) ([]byte, error) {
	raw, ok := args["credentials"]
	if !ok {
		return nil, fmt.Errorf("%w: credentials payload required", ErrInvalidArgs)
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil, fmt.Errorf("%w: credentials payload empty", ErrInvalidArgs)
		}
		return []byte(v), nil
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("%w: credentials payload not encodable", ErrInvalidArgs)
		}
		return encoded, nil
	}
}
