package adminquotahttp

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
)

const (
	auditTargetType = "quota_policy"

	// 由 live-policy 唯一索引以及 quota_windows 的 FK ON DELETE RESTRICT 抛出的
	// Postgres SQLSTATE 码,handler 会将其归一化为 409。不同约束动作可能分别
	// 返回 foreign_key_violation 或 restrict_violation。
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgRestrictViolation   = "23001"

	liveScopeMetricIndex = "uq_quota_policies_live_scope_metric"
)

var (
	// errQuotaPolicyConflict 标记由部分唯一索引抛出的、重复的 live
	//(scope+metric+window+priority)策略。
	errQuotaPolicyConflict = errors.New("quota policy conflicts with an existing live policy")
	// errQuotaPolicyInUse 标记因被某个引用它的 quota_windows 行
	//(FK ON DELETE RESTRICT)挡住而失败的删除。
	errQuotaPolicyInUse = errors.New("quota policy has live windows and cannot be deleted")
	// errQuotaStorePoolUnset 在 adapter 没有事务连接池时返回。
	errQuotaStorePoolUnset = errors.New("quota policy transaction pool unset")
)

// quotaPolicyCreateParams 是 handler 交给 store 的中立 create 输入。它与
// dbquota.InsertQuotaPolicyParams 一一对应,只是租户作用域由已解析的身份固定。
type quotaPolicyCreateParams struct {
	insert dbquota.InsertQuotaPolicyParams
}

type quotaPolicyUpdateParams struct {
	update dbquota.UpdateQuotaPolicyParams
}

type quotaPolicyDeleteParams struct {
	TenantID int64
	ID       int64
}

// auditInput 携带在行 id 分配之前就已知的 admin_audit_events 字段;adapter
// 在事务内部用持久化后的 policy id 设置 TargetID,使审计轨迹与变更操作原子一致。
type auditInput struct {
	TenantID  int64
	ActorID   string
	ActorRole string
	Action    string
	RequestID *string
	Reason    *string
	Payload   []byte
}

func (a auditInput) toParams(targetID int64) admindb.InsertAdminAuditEventParams {
	tenantID := a.TenantID
	id := targetID
	return admindb.InsertAdminAuditEventParams{
		TenantID:   &tenantID,
		ActorID:    a.ActorID,
		ActorRole:  a.ActorRole,
		Action:     a.Action,
		TargetType: auditTargetType,
		TargetID:   &id,
		RequestID:  a.RequestID,
		Reason:     a.Reason,
		Payload:    a.Payload,
	}
}

// quotaPolicyStoreAdapter 持有 *pgxpool.Pool,并在单个 pgx.BeginFunc 事务里
// 运行每次变更,原子地写入 quota_policies 行和 admin_audit_events 行。读取直接
// 走绑定到连接池的 dbquota.Queries。quota 的 sqlc 包不依赖 admindb,但
// dbquota.New(tx) 与 admindb.New(tx) 接受同一个 pgx.Tx(DBTX),所以二者可以
// 干净地组合进同一个事务。
type quotaPolicyStoreAdapter struct {
	reads *dbquota.Queries
	pool  *pgxpool.Pool
}

// NewQuotaPolicyStoreAdapter 把连接池接入 quota-policy 的 admin store。
func NewQuotaPolicyStoreAdapter(pool *pgxpool.Pool) quotaPolicyStore {
	if pool == nil {
		return nil
	}
	return quotaPolicyStoreAdapter{reads: dbquota.New(pool), pool: pool}
}

func (s quotaPolicyStoreAdapter) ListQuotaPoliciesForAdmin(ctx context.Context, arg dbquota.ListQuotaPoliciesForAdminParams) ([]dbquota.QuotaPolicy, error) {
	if s.reads == nil {
		return nil, errQuotaStorePoolUnset
	}
	return s.reads.ListQuotaPoliciesForAdmin(ctx, arg)
}

func (s quotaPolicyStoreAdapter) GetQuotaPolicyByID(ctx context.Context, arg dbquota.GetQuotaPolicyByIDParams) (dbquota.QuotaPolicy, error) {
	if s.reads == nil {
		return dbquota.QuotaPolicy{}, errQuotaStorePoolUnset
	}
	return s.reads.GetQuotaPolicyByID(ctx, arg)
}

func (s quotaPolicyStoreAdapter) CreateQuotaPolicyWithAudit(ctx context.Context, arg quotaPolicyCreateParams, audit auditInput) (dbquota.QuotaPolicy, error) {
	if s.pool == nil {
		return dbquota.QuotaPolicy{}, errQuotaStorePoolUnset
	}
	var policy dbquota.QuotaPolicy
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row, err := dbquota.New(tx).InsertQuotaPolicy(ctx, arg.insert)
		if err != nil {
			return normalizeQuotaPolicyDBError(err)
		}
		if _, err := admindb.New(tx).InsertAdminAuditEvent(ctx, audit.toParams(row.ID)); err != nil {
			return err
		}
		policy = row
		return nil
	})
	return policy, err
}

func (s quotaPolicyStoreAdapter) UpdateQuotaPolicyWithAudit(ctx context.Context, arg quotaPolicyUpdateParams, audit auditInput) (dbquota.QuotaPolicy, error) {
	if s.pool == nil {
		return dbquota.QuotaPolicy{}, errQuotaStorePoolUnset
	}
	var policy dbquota.QuotaPolicy
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row, err := dbquota.New(tx).UpdateQuotaPolicy(ctx, arg.update)
		if err != nil {
			return normalizeQuotaPolicyDBError(err)
		}
		if _, err := admindb.New(tx).InsertAdminAuditEvent(ctx, audit.toParams(row.ID)); err != nil {
			return err
		}
		policy = row
		return nil
	})
	return policy, err
}

func (s quotaPolicyStoreAdapter) DeleteQuotaPolicyWithAudit(ctx context.Context, arg quotaPolicyDeleteParams, audit auditInput) (int64, error) {
	if s.pool == nil {
		return 0, errQuotaStorePoolUnset
	}
	var deletedID int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		id, err := dbquota.New(tx).DeleteQuotaPolicy(ctx, dbquota.DeleteQuotaPolicyParams{
			TenantID: arg.TenantID, ID: arg.ID,
		})
		if err != nil {
			return normalizeQuotaPolicyDBError(err)
		}
		if _, err := admindb.New(tx).InsertAdminAuditEvent(ctx, audit.toParams(id)); err != nil {
			return err
		}
		deletedID = id
		return nil
	})
	return deletedID, err
}

// normalizeQuotaPolicyDBError 把 Postgres 约束失败映射到本包的哨兵错误,handler
// 再把它们翻译成 409,与 channel-catalog 的 23505->409 归一化方式一致。live-policy
// 唯一索引变成 quota_policy_conflict;quota_windows 的 FK RESTRICT 约束失败变成
// quota_policy_in_use。
func normalizeQuotaPolicyDBError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			if pgErr.ConstraintName == liveScopeMetricIndex {
				return errQuotaPolicyConflict
			}
		case pgForeignKeyViolation, pgRestrictViolation:
			return errQuotaPolicyInUse
		}
	}
	return err
}
