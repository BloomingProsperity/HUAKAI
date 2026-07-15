package proxyadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// 现有审计表对 action 有数据库白名单；本操作属于租户级平台设置更新，
	// payload 再用 setting 精确标识列名，避免在禁止迁移的切片里伪造新 action。
	tenantDefaultAuditAction = "update_platform_settings"
	tenantDefaultAuditTarget = "tenant"
)

const lockTenantDefaultSQL = `
SELECT default_proxy_id
FROM tenants
WHERE id = $1
FOR UPDATE`

const validateTenantProxySQL = `
SELECT id
FROM proxies
WHERE id = $1
  AND tenant_id = $2
  AND deleted_at IS NULL
FOR UPDATE`

const updateTenantDefaultSQL = `
UPDATE tenants
SET default_proxy_id = $2
WHERE id = $1`

const insertTenantDefaultAuditSQL = `
INSERT INTO admin_audit_events (
    tenant_id, actor_id, actor_role, action, target_type,
    target_id, request_id, reason, payload
) VALUES (
    $1, $2, $3, $4, $5,
    $6, NULLIF($7, ''), NULL, $8::jsonb
)`

type tenantDefaultReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type tenantDefaultTx interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}

type tenantDefaultBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (tenantDefaultTx, error)
}

type tenantDefaultPoolBeginner struct{ pool *pgxpool.Pool }

func (b tenantDefaultPoolBeginner) BeginTx(ctx context.Context, opts pgx.TxOptions) (tenantDefaultTx, error) {
	return b.pool.BeginTx(ctx, opts)
}

// PostgresTenantDefaultStore 原子维护 tenants.default_proxy_id 与对应管理员审计。
// 代理存在性校验、列更新和审计都在同一事务中，避免并发软删或审计失败留下半状态。
type PostgresTenantDefaultStore struct {
	reader   tenantDefaultReader
	beginner tenantDefaultBeginner
}

func NewPostgresTenantDefaultStore(pool *pgxpool.Pool) *PostgresTenantDefaultStore {
	if pool == nil {
		return &PostgresTenantDefaultStore{}
	}
	return &PostgresTenantDefaultStore{
		reader:   pool,
		beginner: tenantDefaultPoolBeginner{pool: pool},
	}
}

// Get 读取目标租户当前的默认出口；SQL NULL 稳定映射为 nil。
func (s *PostgresTenantDefaultStore) Get(ctx context.Context, tenantID int64) (TenantDefaultProxy, error) {
	if s == nil || s.reader == nil {
		return TenantDefaultProxy{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 {
		return TenantDefaultProxy{}, ErrInvalidInput
	}
	var proxyID *int64
	err := s.reader.QueryRow(ctx, `SELECT default_proxy_id FROM tenants WHERE id = $1`, tenantID).Scan(&proxyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TenantDefaultProxy{}, ErrTenantNotFound
	}
	if err != nil {
		return TenantDefaultProxy{}, tenantDefaultBackendError("read tenant", err)
	}
	return TenantDefaultProxy{ProxyID: cloneProxyID(proxyID)}, nil
}

// Set 设置或清除租户默认出口。非空代理只要求属于同一租户且未软删；生命周期
// 状态由消费端继续执行既有 fail-closed 判定，管理写口不把 inactive 偷换成直连。
func (s *PostgresTenantDefaultStore) Set(ctx context.Context, tenantID int64, proxyID *int64, audit TenantDefaultAudit) (TenantDefaultProxy, error) {
	if s == nil || s.beginner == nil {
		return TenantDefaultProxy{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || (proxyID != nil && *proxyID <= 0) || strings.TrimSpace(audit.ActorID) == "" || strings.TrimSpace(audit.ActorRole) == "" {
		return TenantDefaultProxy{}, ErrInvalidInput
	}
	tx, err := s.beginner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return TenantDefaultProxy{}, tenantDefaultBackendError("begin transaction", err)
	}
	committed := false
	defer rollbackTenantDefault(tx, &committed)

	var beforeProxyID *int64
	if err := tx.QueryRow(ctx, lockTenantDefaultSQL, tenantID).Scan(&beforeProxyID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TenantDefaultProxy{}, ErrTenantNotFound
		}
		return TenantDefaultProxy{}, tenantDefaultBackendError("lock tenant", err)
	}
	if proxyID != nil {
		var validatedID int64
		if err := tx.QueryRow(ctx, validateTenantProxySQL, *proxyID, tenantID).Scan(&validatedID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return TenantDefaultProxy{}, ErrNotFound
			}
			return TenantDefaultProxy{}, tenantDefaultBackendError("validate proxy", err)
		}
	}

	updateTag, err := tx.Exec(ctx, updateTenantDefaultSQL, tenantID, nullableProxyID(proxyID))
	if err != nil {
		return TenantDefaultProxy{}, tenantDefaultBackendError("update tenant", err)
	}
	if updateTag.RowsAffected() != 1 {
		return TenantDefaultProxy{}, tenantDefaultBackendError("update tenant", fmt.Errorf("rows affected %d", updateTag.RowsAffected()))
	}
	payload, err := json.Marshal(struct {
		Setting       string `json:"setting"`
		BeforeProxyID *int64 `json:"before_proxy_id"`
		AfterProxyID  *int64 `json:"after_proxy_id"`
	}{
		Setting:       "default_proxy_id",
		BeforeProxyID: cloneProxyID(beforeProxyID),
		AfterProxyID:  cloneProxyID(proxyID),
	})
	if err != nil {
		return TenantDefaultProxy{}, tenantDefaultBackendError("marshal audit", err)
	}
	auditTag, err := tx.Exec(ctx, insertTenantDefaultAuditSQL,
		tenantID,
		strings.TrimSpace(audit.ActorID),
		strings.TrimSpace(audit.ActorRole),
		tenantDefaultAuditAction,
		tenantDefaultAuditTarget,
		tenantID,
		strings.TrimSpace(audit.RequestID),
		payload,
	)
	if err != nil {
		return TenantDefaultProxy{}, tenantDefaultBackendError("write audit", err)
	}
	if auditTag.RowsAffected() != 1 {
		return TenantDefaultProxy{}, tenantDefaultBackendError("write audit", fmt.Errorf("rows affected %d", auditTag.RowsAffected()))
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantDefaultProxy{}, tenantDefaultBackendError("commit transaction", err)
	}
	committed = true
	return TenantDefaultProxy{ProxyID: cloneProxyID(proxyID)}, nil
}

func nullableProxyID(proxyID *int64) any {
	if proxyID == nil {
		return nil
	}
	return *proxyID
}

func cloneProxyID(proxyID *int64) *int64 {
	if proxyID == nil {
		return nil
	}
	copy := *proxyID
	return &copy
}

func tenantDefaultBackendError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrBackend, operation, err)
}

func rollbackTenantDefault(tx tenantDefaultTx, committed *bool) {
	if *committed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
