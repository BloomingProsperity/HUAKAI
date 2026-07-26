package modelroutingadminhttp

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

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbmodelroutingadmin "github.com/BloomingProsperity/HUAKAI/internal/db/modelroutingadmin"
)

var (
	ErrNotConfigured    = errors.New("模型路由强制 pin 服务未配置")
	ErrInvalid          = errors.New("模型路由强制 pin 输入无效")
	ErrNotFound         = errors.New("模型路由强制 pin 不存在")
	ErrConflict         = errors.New("同一池和模型已存在强制 pin")
	ErrPoolNotOwned     = errors.New("池组不属于目标租户")
	ErrAccountsNotOwned = errors.New("账号集合不属于目标租户和池组")
)

type Override struct {
	ID                 int64
	TenantID           int64
	PoolGroupID        int64
	Model              string
	ProviderAccountIDs []int64
	Enabled            bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type CreateInput struct {
	TenantID           int64
	PoolGroupID        int64
	Model              string
	ProviderAccountIDs []int64
	Enabled            bool
	Audit              MutationAudit
}

type UpdateInput struct {
	ID                 int64
	TenantID           int64
	ProviderAccountIDs *[]int64
	Enabled            *bool
	Audit              MutationAudit
}

type MutationAudit struct {
	ActorID   string
	ActorRole string
	RequestID string
}

type PostgresService struct {
	pool    *pgxpool.Pool
	queries *dbmodelroutingadmin.Queries
}

func NewPostgresService(pool *pgxpool.Pool, queries *dbmodelroutingadmin.Queries) *PostgresService {
	if pool == nil || queries == nil {
		return nil
	}
	return &PostgresService{pool: pool, queries: queries}
}

func (s *PostgresService) List(ctx context.Context, tenantID int64) ([]Override, error) {
	if s == nil || s.queries == nil {
		return nil, ErrNotConfigured
	}
	if tenantID <= 0 {
		return nil, ErrInvalid
	}
	rows, err := s.queries.ListModelRoutingOverridesAdmin(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("列举模型路由强制 pin：%w", err)
	}
	items := make([]Override, 0, len(rows))
	for _, row := range rows {
		items = append(items, overrideFromDB(row))
	}
	return items, nil
}

func (s *PostgresService) Create(ctx context.Context, input CreateInput) (Override, error) {
	if s == nil || s.pool == nil || s.queries == nil {
		return Override{}, ErrNotConfigured
	}
	model := strings.TrimSpace(input.Model)
	accountIDs, err := normalizeProviderAccountIDs(input.ProviderAccountIDs)
	if input.TenantID <= 0 || input.PoolGroupID <= 0 || model == "" || err != nil {
		return Override{}, ErrInvalid
	}
	if err := validateMutationAudit(input.Audit); err != nil {
		return Override{}, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Override{}, fmt.Errorf("开始创建模型路由强制 pin 事务：%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	if err := validateRoutingTargets(ctx, queries, input.TenantID, input.PoolGroupID, accountIDs); err != nil {
		return Override{}, err
	}
	row, err := queries.CreateModelRoutingOverrideAdmin(ctx, dbmodelroutingadmin.CreateModelRoutingOverrideAdminParams{
		TenantID: input.TenantID, PoolGroupID: input.PoolGroupID, Model: model,
		ProviderAccountIDs: accountIDs, Enabled: input.Enabled,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return Override{}, ErrConflict
		}
		return Override{}, fmt.Errorf("创建模型路由强制 pin：%w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"pool_group_id": input.PoolGroupID,
		"model":         model,
		"account_count": len(accountIDs),
		"enabled":       input.Enabled,
	})
	if err != nil {
		return Override{}, fmt.Errorf("编码创建强制 pin 日志：%w", err)
	}
	if err := insertRoutingMutationLog(ctx, tx, input.TenantID, row.ID, "create_model_routing_override", payload, input.Audit); err != nil {
		return Override{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Override{}, fmt.Errorf("提交创建模型路由强制 pin：%w", err)
	}
	return overrideFromDB(row), nil
}

func (s *PostgresService) Update(ctx context.Context, input UpdateInput) (Override, error) {
	if s == nil || s.pool == nil || s.queries == nil {
		return Override{}, ErrNotConfigured
	}
	if input.ID <= 0 || input.TenantID <= 0 || (input.ProviderAccountIDs == nil && input.Enabled == nil) {
		return Override{}, ErrInvalid
	}
	if err := validateMutationAudit(input.Audit); err != nil {
		return Override{}, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Override{}, fmt.Errorf("开始更新模型路由强制 pin 事务：%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := s.queries.WithTx(tx)
	current, err := queries.GetModelRoutingOverrideAdminForUpdate(ctx, dbmodelroutingadmin.GetModelRoutingOverrideAdminForUpdateParams{
		TenantID: input.TenantID,
		ID:       input.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Override{}, ErrNotFound
	}
	if err != nil {
		return Override{}, fmt.Errorf("读取待更新模型路由强制 pin：%w", err)
	}

	accountIDs := current.ProviderAccountIDs
	if input.ProviderAccountIDs != nil {
		accountIDs, err = normalizeProviderAccountIDs(*input.ProviderAccountIDs)
		if err != nil {
			return Override{}, ErrInvalid
		}
	}
	if err := validateRoutingTargets(ctx, queries, input.TenantID, current.PoolGroupID, accountIDs); err != nil {
		return Override{}, err
	}
	enabled := current.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	row, err := queries.UpdateModelRoutingOverrideAdmin(ctx, dbmodelroutingadmin.UpdateModelRoutingOverrideAdminParams{
		ProviderAccountIDs: accountIDs,
		Enabled:            enabled,
		TenantID:           input.TenantID,
		ID:                 input.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Override{}, ErrNotFound
	}
	if err != nil {
		return Override{}, fmt.Errorf("更新模型路由强制 pin：%w", err)
	}
	changedFields := make([]string, 0, 2)
	if input.ProviderAccountIDs != nil {
		changedFields = append(changedFields, "provider_account_ids")
	}
	if input.Enabled != nil {
		changedFields = append(changedFields, "enabled")
	}
	payload, err := json.Marshal(map[string]any{
		"changed_fields": changedFields,
		"account_count":  len(accountIDs),
	})
	if err != nil {
		return Override{}, fmt.Errorf("编码更新强制 pin 日志：%w", err)
	}
	if err := insertRoutingMutationLog(ctx, tx, input.TenantID, row.ID, "update_model_routing_override", payload, input.Audit); err != nil {
		return Override{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Override{}, fmt.Errorf("提交更新模型路由强制 pin：%w", err)
	}
	return overrideFromDB(row), nil
}

func (s *PostgresService) Delete(ctx context.Context, id, tenantID int64, audit MutationAudit) error {
	if s == nil || s.pool == nil || s.queries == nil {
		return ErrNotConfigured
	}
	if id <= 0 || tenantID <= 0 {
		return ErrInvalid
	}
	if err := validateMutationAudit(audit); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("开始删除模型路由强制 pin 事务：%w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.queries.WithTx(tx).DeleteModelRoutingOverrideAdmin(ctx, dbmodelroutingadmin.DeleteModelRoutingOverrideAdminParams{
		TenantID: tenantID,
		ID:       id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("删除模型路由强制 pin：%w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"pool_group_id": row.PoolGroupID,
		"model":         row.Model,
		"account_count": len(row.ProviderAccountIDs),
	})
	if err != nil {
		return fmt.Errorf("编码删除强制 pin 日志：%w", err)
	}
	if err := insertRoutingMutationLog(ctx, tx, tenantID, row.ID, "delete_model_routing_override", payload, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交删除模型路由强制 pin：%w", err)
	}
	return nil
}

func validateMutationAudit(audit MutationAudit) error {
	if strings.TrimSpace(audit.ActorID) == "" || strings.TrimSpace(audit.ActorRole) == "" {
		return ErrInvalid
	}
	return nil
}

func insertRoutingMutationLog(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, overrideID int64,
	action string,
	payload []byte,
	audit MutationAudit,
) error {
	requestID := strings.TrimSpace(audit.RequestID)
	_, err := admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   &tenantID,
		ActorID:    strings.TrimSpace(audit.ActorID),
		ActorRole:  strings.TrimSpace(audit.ActorRole),
		Action:     action,
		TargetType: "model_routing_override",
		TargetID:   &overrideID,
		RequestID:  routingOptionalText(requestID),
		Payload:    payload,
	})
	if err != nil {
		return fmt.Errorf("写入模型路由强制 pin 操作日志：%w", err)
	}
	return nil
}

func routingOptionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func validateRoutingTargets(ctx context.Context, queries *dbmodelroutingadmin.Queries, tenantID, poolGroupID int64, accountIDs []int64) error {
	if _, err := queries.LockModelRoutingPoolForTenant(ctx, dbmodelroutingadmin.LockModelRoutingPoolForTenantParams{
		TenantID: tenantID,
		ID:       poolGroupID,
	}); errors.Is(err, pgx.ErrNoRows) {
		return ErrPoolNotOwned
	} else if err != nil {
		return fmt.Errorf("核验模型路由池组：%w", err)
	}

	matched, err := queries.LockModelRoutingAccountsForPool(ctx, dbmodelroutingadmin.LockModelRoutingAccountsForPoolParams{
		TenantID:           tenantID,
		PoolGroupID:        poolGroupID,
		ProviderAccountIDs: accountIDs,
	})
	if err != nil {
		return fmt.Errorf("核验模型路由账号：%w", err)
	}
	return validateMatchedAccountIDs(accountIDs, matched)
}

func normalizeProviderAccountIDs(input []int64) ([]int64, error) {
	if len(input) == 0 {
		return nil, ErrInvalid
	}
	seen := make(map[int64]struct{}, len(input))
	result := make([]int64, 0, len(input))
	for _, id := range input {
		if id <= 0 {
			return nil, ErrInvalid
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func validateMatchedAccountIDs(want, got []int64) error {
	if len(want) != len(got) {
		return ErrAccountsNotOwned
	}
	set := make(map[int64]struct{}, len(got))
	for _, id := range got {
		set[id] = struct{}{}
	}
	for _, id := range want {
		if _, ok := set[id]; !ok {
			return ErrAccountsNotOwned
		}
	}
	return nil
}

func overrideFromDB(row dbmodelroutingadmin.ModelRoutingOverride) Override {
	return Override{
		ID: row.ID, TenantID: row.TenantID, PoolGroupID: row.PoolGroupID, Model: row.Model,
		ProviderAccountIDs: append([]int64(nil), row.ProviderAccountIDs...), Enabled: row.Enabled,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
