package userkey

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PatchRequest 是 KEY-026 的部分更新请求。字段使用指针区分省略与显式设置。
// ExpiresAt、ClearExpiry 分别表达保持、设置和清除截止时间三种状态。
type PatchRequest struct {
	TenantID    int64
	UserID      int64
	APIKeyID    int64
	Name        *string
	Status      *string
	ExpiresAt   *time.Time
	ClearExpiry bool
	RequestID   string
}

type PatchResult struct {
	APIKeyID  int64
	Name      string
	Status    string
	ExpiresAt *time.Time
}

// Patch 部分更新当前用户自己的 Key；归属、活跃租户和活跃终端用户条件均在事务内复核。
func (s *Service) Patch(ctx context.Context, req PatchRequest) (PatchResult, error) {
	if s == nil || s.pool == nil {
		return PatchResult{}, fmt.Errorf("%w: pool unset", ErrServiceMisconfig)
	}
	if req.TenantID <= 0 || req.UserID <= 0 || req.APIKeyID <= 0 {
		return PatchResult{}, ErrNotFound
	}
	if req.Name == nil && req.Status == nil && req.ExpiresAt == nil && !req.ClearExpiry {
		row, err := s.Get(ctx, req.TenantID, req.UserID, req.APIKeyID)
		if err != nil {
			return PatchResult{}, err
		}
		return PatchResult{
			APIKeyID:  row.APIKeyID,
			Name:      row.Name,
			Status:    row.Status,
			ExpiresAt: row.ExpiresAt,
		}, nil
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(s.now().UTC()) {
		return PatchResult{}, ErrInvalidExpiry
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if len(name) == 0 || len(name) > MaxNameLen {
			return PatchResult{}, ErrInvalidName
		}
		req.Name = &name
	}
	if req.Status != nil {
		switch *req.Status {
		case "active", "revoked":
		default:
			return PatchResult{}, ErrInvalidStatus
		}
	}

	out := PatchResult{APIKeyID: req.APIKeyID}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		currentStatus, err := lockPatchCurrentStatus(ctx, tx, req)
		if err != nil {
			return err
		}
		if req.Status != nil && *req.Status == "active" && currentStatus != "active" {
			if currentStatus == "revoked" {
				return ErrAlreadyRevoked
			}
			// disabled/expired 由风控或系统状态机控制；用户自助面只能主动撤销，
			// 不能绕过管理员恢复流程把受控状态直接翻回 active。
			return ErrStatusManaged
		}

		var (
			setClauses []string
			args       []any
			argIdx     = 1
		)
		if req.Name != nil {
			setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
			args = append(args, *req.Name)
			argIdx++
		}
		if req.Status != nil && *req.Status != currentStatus {
			setClauses = append(setClauses, fmt.Sprintf("status = $%d", argIdx))
			args = append(args, *req.Status)
			argIdx++
			setClauses = append(setClauses, fmt.Sprintf(
				"revoked_at = CASE WHEN $%d = 'revoked' THEN NOW() ELSE revoked_at END", argIdx))
			args = append(args, *req.Status)
			argIdx++
		}
		if req.ClearExpiry {
			setClauses = append(setClauses, "expires_at = NULL")
		} else if req.ExpiresAt != nil {
			setClauses = append(setClauses, fmt.Sprintf("expires_at = $%d", argIdx))
			args = append(args, *req.ExpiresAt)
			argIdx++
		}
		setClauses = append(setClauses, "updated_at = NOW()")

		query := fmt.Sprintf(
			`UPDATE api_keys
			    SET %s
			  WHERE id = $%d
			    AND tenant_id = $%d
			    AND user_id = $%d
			    AND purpose = 'user'
			    AND deleted_at IS NULL
			    AND EXISTS (
			        SELECT 1 FROM tenants t
			         WHERE t.id = api_keys.tenant_id
			           AND t.status = 'active' AND t.deleted_at IS NULL)
			    AND EXISTS (
			        SELECT 1 FROM users u
			         WHERE u.id = api_keys.user_id AND u.tenant_id = api_keys.tenant_id
			           AND u.principal_kind = 'human' AND u.role = 'user'
			           AND u.status = 'active' AND u.deleted_at IS NULL)
			RETURNING name, status, expires_at`,
			strings.Join(setClauses, ", "), argIdx, argIdx+1, argIdx+2,
		)
		allArgs := append(args, req.APIKeyID, req.TenantID, req.UserID)
		var expiresAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, query, allArgs...).Scan(
			&out.Name, &out.Status, &expiresAt,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("%w: patch: %v", ErrBackend, err)
		}
		if expiresAt.Valid {
			value := expiresAt.Time
			out.ExpiresAt = &value
		}
		return nil
	})
	if err != nil {
		return PatchResult{}, err
	}
	return out, nil
}
