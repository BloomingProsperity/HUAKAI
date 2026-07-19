package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	dbregistry "github.com/BloomingProsperity/HUAKAI/internal/db/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

var (
	ErrModelAdminInvalid   = errors.New("registry: invalid admin model input")
	ErrModelAdminNotFound  = errors.New("registry: admin model not found")
	ErrModelAdminForbidden = errors.New("registry: admin model scope forbidden")
	ErrConflict            = errors.New("registry: admin model conflict")
)

const (
	ModelScopeTenant = "tenant"
	ModelScopeGlobal = "global"

	modelAdminRolePlatform = "platform_admin"
	modelAdminRoleTenant   = "tenant_operator"
)

// AdminModelAccess 只携带模型主体服务完成第二道权限校验所需的认证结果。
// Role、ScopeTenantID 与 Actor 必须由 handler 从已认证身份构造，不能取自请求体。
type AdminModelAccess struct {
	Role          string
	ScopeTenantID int64
	Actor         string
}

// AdminModelTarget 描述本次操作命中的模型命名域。global 的 TenantID 必须为零；
// tenant 的 TenantID 必须为正整数。
type AdminModelTarget struct {
	Scope    string
	TenantID int64
}

// AdminModel 是 models 主体表的运维投影，不包含任何供应商凭证或客户密钥。
type AdminModel struct {
	ID                      int64
	TenantID                *int64
	Scope                   string
	CanonicalID             string
	ProtocolFamily          string
	DefaultProviderModelID  string
	DefaultContextWindow    int32
	DefaultRequestTimeoutMS int32
	PricingClass            string
	ModelOwner              string
	ModelCreatedAt          *time.Time
	Capabilities            map[string]bool
	MaxOutputTokens         *int32
	ModelMode               *string
	Status                  string
	CreatedAt               time.Time
	UpdatedAt               time.Time
	DeletedAt               *time.Time
}

type CreateAdminModelInput struct {
	Access                  AdminModelAccess
	Target                  AdminModelTarget
	CanonicalID             string
	ProtocolFamily          string
	DefaultProviderModelID  string
	DefaultContextWindow    int32
	DefaultRequestTimeoutMS int32
	PricingClass            string
	ModelOwner              string
	Status                  string
}

type UpdateAdminModelInput struct {
	Access                  AdminModelAccess
	Target                  AdminModelTarget
	ID                      int64
	DefaultProviderModelID  *string
	DefaultContextWindow    *int32
	DefaultRequestTimeoutMS *int32
	PricingClass            *string
	ProtocolFamily          *string
	ModelOwner              *string
	Status                  *string
}

func (r *PostgresRegistry) ListAdminModels(ctx context.Context, access AdminModelAccess, target AdminModelTarget) ([]AdminModel, error) {
	if r == nil || r.pool == nil {
		return nil, ErrRegistryBackend
	}
	if err := authorizeAdminModel(access, target, false); err != nil {
		return nil, err
	}
	if err := r.authorizeGlobalModelRead(ctx, access, target); err != nil {
		return nil, err
	}
	rows, err := dbregistry.New(r.pool).ListAdminModels(ctx, dbregistry.ListAdminModelsParams{
		Scope: target.Scope, TenantID: targetTenantID(target),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: 列举模型主体：%v", ErrRegistryBackend, err)
	}
	models := make([]AdminModel, 0, len(rows))
	for _, row := range rows {
		model, err := adminModelFromDB(row)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, nil
}

func (r *PostgresRegistry) GetAdminModel(ctx context.Context, access AdminModelAccess, target AdminModelTarget, id int64) (AdminModel, error) {
	if r == nil || r.pool == nil {
		return AdminModel{}, ErrRegistryBackend
	}
	if id <= 0 {
		return AdminModel{}, ErrModelAdminInvalid
	}
	if err := authorizeAdminModel(access, target, false); err != nil {
		return AdminModel{}, err
	}
	if err := r.authorizeGlobalModelRead(ctx, access, target); err != nil {
		return AdminModel{}, err
	}
	row, err := dbregistry.New(r.pool).GetAdminModel(ctx, dbregistry.GetAdminModelParams{
		ID: id, Scope: target.Scope, TenantID: targetTenantID(target),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminModel{}, ErrModelAdminNotFound
	}
	if err != nil {
		return AdminModel{}, fmt.Errorf("%w: 读取模型主体：%v", ErrRegistryBackend, err)
	}
	return adminModelFromDB(row)
}

func (r *PostgresRegistry) CreateAdminModel(ctx context.Context, in CreateAdminModelInput) (AdminModel, error) {
	if r == nil || r.pool == nil {
		return AdminModel{}, ErrRegistryBackend
	}
	if err := authorizeAdminModel(in.Access, in.Target, true); err != nil {
		return AdminModel{}, err
	}
	if err := normalizeCreateAdminModel(&in); err != nil {
		return AdminModel{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AdminModel{}, fmt.Errorf("%w: 开始创建模型主体事务：%v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := dbregistry.New(tx).CreateAdminModel(ctx, dbregistry.CreateAdminModelParams{
		TenantID: targetTenantID(in.Target), Scope: in.Target.Scope,
		CanonicalID: in.CanonicalID, ProtocolFamily: in.ProtocolFamily,
		DefaultProviderModelID: in.DefaultProviderModelID, DefaultContextWindow: in.DefaultContextWindow,
		DefaultRequestTimeoutMs: in.DefaultRequestTimeoutMS, PricingClass: in.PricingClass,
		ModelOwner: in.ModelOwner, Status: in.Status,
	})
	if isUniqueViolation(err) {
		return AdminModel{}, ErrConflict
	}
	if isForeignKeyViolation(err) || isCheckViolation(err) {
		return AdminModel{}, ErrModelAdminInvalid
	}
	if err != nil {
		return AdminModel{}, fmt.Errorf("%w: 创建模型主体：%v", ErrRegistryBackend, err)
	}
	if err := bumpAdminModelSnapshots(ctx, tx, row.ID, in.Target, in.Access, "create"); err != nil {
		return AdminModel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminModel{}, fmt.Errorf("%w: 提交创建模型主体：%v", ErrRegistryBackend, err)
	}
	return adminModelFromDB(row)
}

func (r *PostgresRegistry) UpdateAdminModel(ctx context.Context, in UpdateAdminModelInput) (AdminModel, error) {
	if r == nil || r.pool == nil {
		return AdminModel{}, ErrRegistryBackend
	}
	if in.ID <= 0 || !hasAdminModelUpdate(in) {
		return AdminModel{}, ErrModelAdminInvalid
	}
	if err := authorizeAdminModel(in.Access, in.Target, true); err != nil {
		return AdminModel{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AdminModel{}, fmt.Errorf("%w: 开始更新模型主体事务：%v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbregistry.New(tx)
	current, err := q.LockAdminModelForUpdate(ctx, dbregistry.LockAdminModelForUpdateParams{
		ID: in.ID, Scope: in.Target.Scope, TenantID: targetTenantID(in.Target),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminModel{}, ErrModelAdminNotFound
	}
	if err != nil {
		return AdminModel{}, fmt.Errorf("%w: 锁定待更新模型主体：%v", ErrRegistryBackend, err)
	}
	params, err := mergeAdminModelUpdate(current, in)
	if err != nil {
		return AdminModel{}, err
	}
	row, err := q.UpdateAdminModel(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminModel{}, ErrModelAdminNotFound
	}
	if isCheckViolation(err) {
		return AdminModel{}, ErrModelAdminInvalid
	}
	if err != nil {
		return AdminModel{}, fmt.Errorf("%w: 更新模型主体：%v", ErrRegistryBackend, err)
	}
	if err := bumpAdminModelSnapshots(ctx, tx, row.ID, in.Target, in.Access, "update"); err != nil {
		return AdminModel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminModel{}, fmt.Errorf("%w: 提交更新模型主体：%v", ErrRegistryBackend, err)
	}
	return adminModelFromDB(row)
}

func (r *PostgresRegistry) SoftDeleteAdminModel(ctx context.Context, access AdminModelAccess, target AdminModelTarget, id int64) error {
	if r == nil || r.pool == nil {
		return ErrRegistryBackend
	}
	if id <= 0 {
		return ErrModelAdminInvalid
	}
	if err := authorizeAdminModel(access, target, true); err != nil {
		return err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("%w: 开始软删模型主体事务：%v", ErrRegistryBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := dbregistry.New(tx).SoftDeleteAdminModel(ctx, dbregistry.SoftDeleteAdminModelParams{
		ID: id, Scope: target.Scope, TenantID: targetTenantID(target),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrModelAdminNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: 软删模型主体：%v", ErrRegistryBackend, err)
	}
	if err := bumpAdminModelSnapshots(ctx, tx, row.ID, target, access, "delete"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: 提交软删模型主体：%v", ErrRegistryBackend, err)
	}
	return nil
}

func authorizeAdminModel(access AdminModelAccess, target AdminModelTarget, write bool) error {
	switch target.Scope {
	case ModelScopeGlobal:
		if target.TenantID != 0 {
			return ErrModelAdminInvalid
		}
		if access.Role != modelAdminRolePlatform && access.Role != modelAdminRoleTenant {
			return ErrModelAdminForbidden
		}
		if write && access.Role != modelAdminRolePlatform {
			return ErrModelAdminForbidden
		}
		return nil
	case ModelScopeTenant:
		if target.TenantID <= 0 {
			return ErrModelAdminInvalid
		}
		switch access.Role {
		case modelAdminRolePlatform:
			return nil
		case modelAdminRoleTenant:
			if access.ScopeTenantID == target.TenantID && access.ScopeTenantID > 0 {
				return nil
			}
		}
		return ErrModelAdminForbidden
	default:
		return ErrModelAdminInvalid
	}
}

// authorizeGlobalModelRead 确保租户操作员只能读取其租户已显式继承的全局目录。
// 平台管理员不受租户继承策略限制，写权限仍由 authorizeAdminModel 单独控制。
func (r *PostgresRegistry) authorizeGlobalModelRead(ctx context.Context, access AdminModelAccess, target AdminModelTarget) error {
	if target.Scope != ModelScopeGlobal || access.Role == modelAdminRolePlatform {
		return nil
	}
	if access.Role != modelAdminRoleTenant || access.ScopeTenantID <= 0 {
		return ErrModelAdminForbidden
	}
	inherit, err := dbregistry.New(r.pool).GetTenantInheritGlobal(ctx, access.ScopeTenantID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !inherit) {
		return ErrModelAdminForbidden
	}
	if err != nil {
		return fmt.Errorf("%w: 读取全局目录继承策略：%v", ErrRegistryBackend, err)
	}
	return nil
}

func normalizeCreateAdminModel(in *CreateAdminModelInput) error {
	in.CanonicalID = strings.TrimSpace(in.CanonicalID)
	in.ProtocolFamily = strings.TrimSpace(in.ProtocolFamily)
	in.DefaultProviderModelID = strings.TrimSpace(in.DefaultProviderModelID)
	in.PricingClass = strings.TrimSpace(in.PricingClass)
	in.ModelOwner = strings.TrimSpace(in.ModelOwner)
	in.Status = strings.TrimSpace(in.Status)
	if in.CanonicalID == "" || in.DefaultProviderModelID == "" || in.PricingClass == "" || in.ModelOwner == "" {
		return ErrModelAdminInvalid
	}
	if !registrydefault.IsSupportedProtocolFamily(in.ProtocolFamily) || in.DefaultContextWindow < 0 || in.DefaultRequestTimeoutMS <= 0 {
		return ErrModelAdminInvalid
	}
	if in.Status != "active" && in.Status != "disabled" {
		return ErrModelAdminInvalid
	}
	return nil
}

func hasAdminModelUpdate(in UpdateAdminModelInput) bool {
	return in.DefaultProviderModelID != nil || in.DefaultContextWindow != nil ||
		in.DefaultRequestTimeoutMS != nil || in.PricingClass != nil || in.ProtocolFamily != nil ||
		in.ModelOwner != nil || in.Status != nil
}

func mergeAdminModelUpdate(current dbregistry.LockAdminModelForUpdateRow, in UpdateAdminModelInput) (dbregistry.UpdateAdminModelParams, error) {
	params := dbregistry.UpdateAdminModelParams{
		ID: current.ID, Scope: current.Scope, TenantID: current.TenantID,
		DefaultProviderModelID:  current.DefaultProviderModelID,
		DefaultContextWindow:    current.DefaultContextWindow,
		DefaultRequestTimeoutMs: current.DefaultRequestTimeoutMs,
		PricingClass:            current.PricingClass, ProtocolFamily: current.ProtocolFamily,
		ModelOwner: current.ModelOwner, Status: current.Status,
	}
	if in.DefaultProviderModelID != nil {
		params.DefaultProviderModelID = strings.TrimSpace(*in.DefaultProviderModelID)
	}
	if in.DefaultContextWindow != nil {
		params.DefaultContextWindow = *in.DefaultContextWindow
	}
	if in.DefaultRequestTimeoutMS != nil {
		params.DefaultRequestTimeoutMs = *in.DefaultRequestTimeoutMS
	}
	if in.PricingClass != nil {
		params.PricingClass = strings.TrimSpace(*in.PricingClass)
	}
	if in.ProtocolFamily != nil {
		params.ProtocolFamily = strings.TrimSpace(*in.ProtocolFamily)
	}
	if in.ModelOwner != nil {
		params.ModelOwner = strings.TrimSpace(*in.ModelOwner)
	}
	if in.Status != nil {
		params.Status = strings.TrimSpace(*in.Status)
	}
	if params.DefaultProviderModelID == "" || params.PricingClass == "" || params.ModelOwner == "" ||
		!registrydefault.IsSupportedProtocolFamily(params.ProtocolFamily) || params.DefaultContextWindow < 0 ||
		params.DefaultRequestTimeoutMs <= 0 || (params.Status != "active" && params.Status != "disabled") {
		return dbregistry.UpdateAdminModelParams{}, ErrModelAdminInvalid
	}
	return params, nil
}

func bumpAdminModelSnapshots(ctx context.Context, tx pgx.Tx, modelID int64, target AdminModelTarget, access AdminModelAccess, operation string) error {
	reason := "admin model " + operation
	actor := strings.TrimSpace(access.Actor)
	if actor == "" {
		actor = "admin"
	}
	if target.Scope == ModelScopeGlobal {
		_, err := bumpAffectedSnapshots(ctx, tx, []int64{modelID}, reason, actor)
		return err
	}
	_, err := tx.Exec(ctx, `
INSERT INTO model_registry_snapshots (tenant_id, version, reason, updated_by_actor)
VALUES ($1, 2, $2, $3)
ON CONFLICT (tenant_id) DO UPDATE SET
    version = model_registry_snapshots.version + 1,
    reason = EXCLUDED.reason,
    updated_by_actor = EXCLUDED.updated_by_actor,
    updated_at = now()`, target.TenantID, reason, actor)
	if err != nil {
		return fmt.Errorf("%w: 递增模型主体快照：%v", ErrRegistryBackend, err)
	}
	return nil
}

func targetTenantID(target AdminModelTarget) *int64 {
	if target.Scope == ModelScopeGlobal {
		return nil
	}
	tenantID := target.TenantID
	return &tenantID
}

type adminModelDBRow dbregistry.GetAdminModelRow

func adminModelFromDB(source any) (AdminModel, error) {
	var row adminModelDBRow
	switch value := source.(type) {
	case dbregistry.ListAdminModelsRow:
		row = adminModelDBRow(value)
	case dbregistry.GetAdminModelRow:
		row = adminModelDBRow(value)
	case dbregistry.CreateAdminModelRow:
		row = adminModelDBRow(value)
	case dbregistry.UpdateAdminModelRow:
		row = adminModelDBRow(value)
	default:
		return AdminModel{}, fmt.Errorf("%w: 未支持的模型主体数据库投影 %T", ErrRegistryBackend, source)
	}
	capabilities := make(map[string]bool)
	if len(row.Capabilities) > 0 {
		if err := json.Unmarshal(row.Capabilities, &capabilities); err != nil {
			return AdminModel{}, fmt.Errorf("%w: 解析模型主体 capabilities：%v", ErrRegistryBackend, err)
		}
	}
	model := AdminModel{
		ID: row.ID, TenantID: row.TenantID, Scope: row.Scope, CanonicalID: row.CanonicalID,
		ProtocolFamily: row.ProtocolFamily, DefaultProviderModelID: row.DefaultProviderModelID,
		DefaultContextWindow: row.DefaultContextWindow, DefaultRequestTimeoutMS: row.DefaultRequestTimeoutMs,
		PricingClass: row.PricingClass, ModelOwner: row.ModelOwner, Capabilities: capabilities,
		MaxOutputTokens: row.MaxOutputTokens, ModelMode: row.ModelMode, Status: row.Status,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.ModelCreatedAt.Valid {
		value := row.ModelCreatedAt.Time
		model.ModelCreatedAt = &value
	}
	if row.DeletedAt.Valid {
		value := row.DeletedAt.Time
		model.DeletedAt = &value
	}
	return model, nil
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}
