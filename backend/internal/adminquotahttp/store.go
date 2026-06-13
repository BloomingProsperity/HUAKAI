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

	// Postgres SQLSTATE codes surfaced by the live-policy unique index and the
	// quota_windows FK ON DELETE RESTRICT, normalized to 409 by the handlers.
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"

	liveScopeMetricIndex = "uq_quota_policies_live_scope_metric"
)

var (
	// errQuotaPolicyConflict marks a duplicate live (scope+metric+window+priority)
	// policy raised by the partial unique index.
	errQuotaPolicyConflict = errors.New("quota policy conflicts with an existing live policy")
	// errQuotaPolicyInUse marks a delete blocked by a referencing quota_windows
	// row (FK ON DELETE RESTRICT).
	errQuotaPolicyInUse = errors.New("quota policy has live windows and cannot be deleted")
	// errQuotaStorePoolUnset is returned when the adapter has no tx pool.
	errQuotaStorePoolUnset = errors.New("quota policy transaction pool unset")
)

// quotaPolicyCreateParams is the neutral create input the handler hands the
// store. It maps 1:1 onto dbquota.InsertQuotaPolicyParams except tenant scoping
// is fixed by the resolved identity.
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

// auditInput carries the admin_audit_events fields known before the row id is
// assigned; the adapter sets TargetID from the persisted policy id inside the
// transaction so the audit trail is atomic with the mutation.
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

// quotaPolicyStoreAdapter holds the *pgxpool.Pool and runs each mutation inside
// a single pgx.BeginFunc tx that writes the quota_policies row AND the
// admin_audit_events row atomically. Reads go straight to a pool-bound
// dbquota.Queries. The quota sqlc package has no admindb dependency, but both
// dbquota.New(tx) and admindb.New(tx) accept the same pgx.Tx (DBTX), so they
// compose cleanly in one transaction.
type quotaPolicyStoreAdapter struct {
	reads *dbquota.Queries
	pool  *pgxpool.Pool
}

// NewQuotaPolicyStoreAdapter wires the pool into the quota-policy admin store.
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

// normalizeQuotaPolicyDBError maps Postgres constraint failures to the package
// sentinels the handler translates to 409, mirroring the channel-catalog
// 23505->409 normalization. The live-policy unique index becomes
// quota_policy_conflict; the quota_windows FK RESTRICT (23503) becomes
// quota_policy_in_use.
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
		case pgForeignKeyViolation:
			return errQuotaPolicyInUse
		}
	}
	return err
}
