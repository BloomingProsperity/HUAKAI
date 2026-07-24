// KeyRevoker 为 admin 端点处理 api_keys 的吊销。仅软吊销 —— billing 表
// 以 ON DELETE RESTRICT 的 FK 指回 api_keys(见 migration 0009),因此
// 只要资金或用量历史仍引用该 Key，数据库就拒绝硬删除；这是预期的不变量。

package admin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// RevokeRequest 捕获运维的吊销调用。
type RevokeRequest struct {
	Caller    AdminIdentity
	APIKeyID  int64
	TenantID  int64 // key 所归属的 tenant(RBAC scope 检查)
	Reason    string
	RequestID string
}

// RevokeResult 告诉 handler 发生了什么。当吊销为幂等时
//(status 原本不是 'active'),AlreadyRevoked=true。
type RevokeResult struct {
	APIKeyID       int64
	AlreadyRevoked bool
}

// KeyRevoker 与 KeyIssuer 的 TX 形态对应。通过 NewKeyRevoker 构造。
type KeyRevoker struct {
	pool *pgxpool.Pool
}

func NewKeyRevoker(pool *pgxpool.Pool) *KeyRevoker {
	return &KeyRevoker{pool: pool}
}

// Revoke 把某一把 key 的 api_keys.status 翻转为 'revoked'。RBAC:与 Issue
// 规则相同 —— platform_admin 全局、tenant_operator 仅限自身 tenant。
// 幂等:吊销一把已吊销的 key 返回 AlreadyRevoked=true。
func (r *KeyRevoker) Revoke(ctx context.Context, req RevokeRequest) (RevokeResult, error) {
	if r == nil || r.pool == nil {
		return RevokeResult{}, fmt.Errorf("%w: revoker not configured", ErrAdminBackend)
	}
	if req.APIKeyID == 0 || req.TenantID == 0 {
		return RevokeResult{}, fmt.Errorf("%w: api_key_id and tenant_id required", ErrAdminBadRequest)
	}
	if err := req.Caller.CanIssueForTenant(req.TenantID); err != nil {
		// 被拒绝的吊销尝试必须进入审计轨迹。
		// best-effort 写入;即便 audit 插入失败,调用方仍会得到 403。
		_ = r.auditDeny(ctx, req, "rbac_violation")
		return RevokeResult{}, err
	}

	out := RevokeResult{APIKeyID: req.APIKeyID}
	err := r.tx(ctx, func(qtx *admindb.Queries) error {
		// 先核实该 key 存在于此 tenant 中;对缺失或 tenant 不符的 key,
		// AdminGetAPIKeyByID 返回 NoRows(D7 风格的 404)。
		row, err := qtx.AdminGetAPIKeyByID(ctx, admindb.AdminGetAPIKeyByIDParams{
			ID:       req.APIKeyID,
			TenantID: req.TenantID,
		})
		if err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("%w: api_key %d in tenant %d", ErrAdminNotFound, req.APIKeyID, req.TenantID)
			}
			return fmt.Errorf("%w: get api_key: %v", ErrAdminBackend, err)
		}
		// 只有一行已经是 revoked 才走幂等路径。disabled/expired 的行仍会
		// 被翻转为 revoked,这样运维不会被误导,以为一把 disabled 的 key
		// 已安全退役,而其实它仍可被吊销。
		if row.Status == "revoked" {
			out.AlreadyRevoked = true
		} else {
			rows, err := qtx.AdminRevokeAPIKey(ctx, admindb.AdminRevokeAPIKeyParams{
				ID:       req.APIKeyID,
				TenantID: req.TenantID,
				Reason:   req.Reason,
			})
			if err != nil {
				return fmt.Errorf("%w: revoke api_key: %v", ErrAdminBackend, err)
			}
			if rows == 0 {
				// 竞态:该行在 SELECT 与 UPDATE 之间被翻转为
				// 'revoked'。按幂等处理。
				out.AlreadyRevoked = true
			}
		}

		// Audit(始终写,即便是幂等情况)。
		payloadBytes, _ := json.Marshal(map[string]any{
			"api_key_id":      req.APIKeyID,
			"tenant_id":       req.TenantID,
			"already_revoked": out.AlreadyRevoked,
		})
		actorRole := req.Caller.Role
		if actorRole == "" {
			actorRole = RoleTenantOperator
		}
		if _, err := qtx.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID:   nullableInt64(req.TenantID),
			ActorID:    req.Caller.AuditActor(),
			ActorRole:  actorRole,
			Action:     "revoke_api_key",
			TargetType: "api_key",
			TargetID:   nullableInt64(req.APIKeyID),
			RequestID:  nullableString(req.RequestID),
			Reason:     nullableString(req.Reason),
			Payload:    payloadBytes,
		}); err != nil {
			return fmt.Errorf("%w: insert audit: %v", ErrAdminBackend, err)
		}
		return nil
	})
	if err != nil {
		return RevokeResult{}, err
	}
	return out, nil
}

// auditDeny 在任何 TX 之外写一条被拒绝的 'revoke_api_key' audit 行。
// 从 RBAC 拒绝路径调用,这样被拒绝的吊销尝试仍会出现在事故复盘中。
func (r *KeyRevoker) auditDeny(ctx context.Context, req RevokeRequest, reason string) error {
	q := admindb.New(r.pool)
	payload, _ := json.Marshal(map[string]any{
		"outcome":    "denied",
		"reason":     reason,
		"api_key_id": req.APIKeyID,
		"tenant_id":  req.TenantID,
	})
	actorRole := req.Caller.Role
	if actorRole == "" {
		actorRole = RoleTenantOperator
	}
	// deny-audit【绝不可】以攻击者提供的 tenant_id 写入,否则一个探测
	// 其他 tenant 的 tenant_operator 会污染那些 tenant 的审计轨迹。使用
	// NULL 的 tenant scope;被尝试的 tenant_id 留在 payload jsonb 中供
	// 取证审查。
	_, err := q.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   nil,
		ActorID:    req.Caller.AuditActor(),
		ActorRole:  actorRole,
		Action:     "revoke_api_key",
		TargetType: "api_key",
		TargetID:   nullableInt64(req.APIKeyID),
		RequestID:  nullableString(req.RequestID),
		Reason:     nullableString(reason),
		Payload:    payload,
	})
	return err
}

func (r *KeyRevoker) tx(ctx context.Context, fn func(*admindb.Queries) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("%w: begin: %v", ErrAdminBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(admindb.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit: %v", ErrAdminBackend, err)
	}
	return nil
}
