package hermesrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

var (
	ErrStoreUnavailable = errors.New("hermes 变更恢复存储未配置")
	ErrStaleLease       = errors.New("hermes 变更恢复租约已失效")
	ErrPrepared         = errors.New("hermes 变更尚未产生可记录结果")
)

type Store struct {
	pool *pgxpool.Pool
}

type Entry struct {
	OperationID      uuid.UUID
	TenantID         int64
	ActorSource      string
	ActorID          int64
	ActorRole        string
	ToolName         string
	TargetID         int64
	ResultStatus     hermesops.ResultStatus
	RecoveryAttempts int32
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Prepare 在真实独立事务变更之前持久化已确认意图。原始敏感参数不会进入恢复表。
func (s *Store) Prepare(ctx context.Context, rec hermesops.MutationAuditRecord) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	if rec.OperationID == uuid.Nil {
		return fmt.Errorf("%w：缺少操作号", ErrStoreUnavailable)
	}
	calledAt := rec.CalledAt
	if calledAt.IsZero() {
		calledAt = time.Now().UTC()
	}
	args, err := marshalSanitized(rec.Args)
	if err != nil {
		return fmt.Errorf("编码变更参数：%w", err)
	}
	payload, err := marshalSanitized(rec.AuditPayload)
	if err != nil {
		return fmt.Errorf("编码管理员日志载荷：%w", err)
	}
	return hermestoolsdb.New(s.pool).InsertHermesMutationRecovery(ctx, hermestoolsdb.InsertHermesMutationRecoveryParams{
		OperationID:   toPGUUID(rec.OperationID),
		TenantID:      rec.TenantID,
		ActorSource:   rec.ActorSource,
		ActorID:       rec.ActorID,
		ActorRole:     rec.ActorRole,
		ToolName:      rec.ToolName,
		RequestedArgs: args,
		AdminAction:   rec.AdminAction,
		TargetType:    rec.TargetType,
		TargetID:      rec.TargetID,
		AuditPayload:  payload,
		CorrelationID: nilIfEmpty(rec.CorrelationID),
		RequestID:     nilIfEmpty(rec.RequestID),
		CalledAt:      pgtype.Timestamptz{Time: calledAt.UTC(), Valid: true},
	})
}

func (s *Store) RecordOutcome(
	ctx context.Context,
	operationID uuid.UUID,
	status hermesops.ResultStatus,
	summary map[string]any,
	errorClass string,
	returnedAt time.Time,
) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	params, err := outcomeParams(operationID, status, summary, errorClass, returnedAt)
	if err != nil {
		return err
	}
	rows, err := hermestoolsdb.New(s.pool).SetHermesMutationRecoveryOutcome(ctx, params)
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w：操作记录不存在或已经完成", ErrStoreUnavailable)
	}
	return nil
}

func (s *Store) RecordClaimedOutcome(
	ctx context.Context,
	entry Entry,
	leaseOwner string,
	status hermesops.ResultStatus,
	summary map[string]any,
	errorClass string,
	returnedAt time.Time,
) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	base, err := outcomeParams(entry.OperationID, status, summary, errorClass, returnedAt)
	if err != nil {
		return err
	}
	rows, err := hermestoolsdb.New(s.pool).SetClaimedHermesMutationRecoveryOutcome(ctx, hermestoolsdb.SetClaimedHermesMutationRecoveryOutcomeParams{
		ResultStatus:  base.ResultStatus,
		ResultSummary: base.ResultSummary,
		ErrorClass:    base.ErrorClass,
		ReturnedAt:    base.ReturnedAt,
		OperationID:   base.OperationID,
		LeaseOwner:    leaseOwner,
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrStaleLease
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, leaseOwner string, leaseTTL time.Duration) (Entry, error) {
	if s == nil || s.pool == nil {
		return Entry{}, ErrStoreUnavailable
	}
	if leaseTTL <= 0 {
		leaseTTL = 2 * time.Minute
	}
	row, err := hermestoolsdb.New(s.pool).ClaimHermesMutationRecovery(ctx, hermestoolsdb.ClaimHermesMutationRecoveryParams{
		LeaseOwner: leaseOwner,
		LeaseTtl:   durationInterval(leaseTTL),
	})
	if err != nil {
		return Entry{}, err
	}
	operationID, err := fromPGUUID(row.OperationID)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		OperationID:      operationID,
		TenantID:         row.TenantID,
		ActorSource:      row.ActorSource,
		ActorID:          row.ActorID,
		ActorRole:        row.ActorRole,
		ToolName:         row.ToolName,
		TargetID:         row.TargetID,
		ResultStatus:     hermesops.ResultStatus(row.ResultStatus),
		RecoveryAttempts: row.RecoveryAttempts,
	}, nil
}

func (s *Store) Release(ctx context.Context, operationID uuid.UUID, leaseOwner string, retryAfter time.Duration) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	if retryAfter <= 0 {
		retryAfter = 30 * time.Second
	}
	rows, err := hermestoolsdb.New(s.pool).ReleaseHermesMutationRecovery(ctx, hermestoolsdb.ReleaseHermesMutationRecoveryParams{
		RetryAfter:  durationInterval(retryAfter),
		OperationID: toPGUUID(operationID),
		LeaseOwner:  leaseOwner,
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrStaleLease
	}
	return nil
}

// FinalizeAudit 把两类日志和完成标记放进同一个事务。行锁让多副本恢复不会重复补记。
func (s *Store) FinalizeAudit(ctx context.Context, operationID uuid.UUID) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	queries := hermestoolsdb.New(tx)
	row, err := queries.GetHermesMutationRecoveryForUpdate(ctx, toPGUUID(operationID))
	if err != nil {
		return err
	}
	if row.AuditCommittedAt.Valid {
		return tx.Commit(ctx)
	}
	if row.ResultStatus == "prepared" {
		return ErrPrepared
	}
	rec, err := recoveryAuditRecord(row)
	if err != nil {
		return err
	}
	if err := hermesops.InsertMutationAuditRows(ctx, tx, rec); err != nil {
		return fmt.Errorf("补写 Hermes 变更日志：%w", err)
	}
	rows, err := queries.MarkHermesMutationRecoveryAudited(ctx, row.OperationID)
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("标记 Hermes 变更日志完成：%w", ErrStaleLease)
	}
	return tx.Commit(ctx)
}

func recoveryAuditRecord(row hermestoolsdb.HermesMutationRecovery) (hermesops.MutationAuditRecord, error) {
	operationID, err := fromPGUUID(row.OperationID)
	if err != nil {
		return hermesops.MutationAuditRecord{}, err
	}
	args := map[string]any{}
	if len(row.RequestedArgs) > 0 {
		if err := json.Unmarshal(row.RequestedArgs, &args); err != nil {
			return hermesops.MutationAuditRecord{}, fmt.Errorf("解析恢复参数：%w", err)
		}
	}
	summary := map[string]any{}
	if len(row.ResultSummary) > 0 {
		if err := json.Unmarshal(row.ResultSummary, &summary); err != nil {
			return hermesops.MutationAuditRecord{}, fmt.Errorf("解析恢复结果：%w", err)
		}
	}
	payload := map[string]any{}
	if len(row.AuditPayload) > 0 {
		if err := json.Unmarshal(row.AuditPayload, &payload); err != nil {
			return hermesops.MutationAuditRecord{}, fmt.Errorf("解析恢复日志载荷：%w", err)
		}
	}
	payload["result_status"] = row.ResultStatus
	if row.ErrorClass != nil {
		payload["error_class"] = *row.ErrorClass
	}
	return hermesops.MutationAuditRecord{
		OperationID:   operationID,
		TenantID:      row.TenantID,
		ActorSource:   row.ActorSource,
		ActorID:       row.ActorID,
		ActorRole:     row.ActorRole,
		ToolName:      row.ToolName,
		Args:          args,
		ResultSummary: summary,
		Status:        hermesops.ResultStatus(row.ResultStatus),
		ErrorClass:    stringValue(row.ErrorClass),
		CorrelationID: stringValue(row.CorrelationID),
		RequestID:     stringValue(row.RequestID),
		CalledAt:      row.CalledAt.Time,
		ReturnedAt:    row.ReturnedAt.Time,
		AdminAction:   row.AdminAction,
		TargetType:    row.TargetType,
		TargetID:      row.TargetID,
		AuditPayload:  payload,
	}, nil
}

func outcomeParams(
	operationID uuid.UUID,
	status hermesops.ResultStatus,
	summary map[string]any,
	errorClass string,
	returnedAt time.Time,
) (hermestoolsdb.SetHermesMutationRecoveryOutcomeParams, error) {
	if operationID == uuid.Nil {
		return hermestoolsdb.SetHermesMutationRecoveryOutcomeParams{}, fmt.Errorf("缺少操作号")
	}
	if returnedAt.IsZero() {
		returnedAt = time.Now().UTC()
	}
	switch status {
	case hermesops.ResultOK:
		errorClass = ""
	case hermesops.ResultError:
		if errorClass == "" {
			errorClass = "mutation_failed"
		}
	default:
		return hermestoolsdb.SetHermesMutationRecoveryOutcomeParams{}, fmt.Errorf("无效变更结果：%s", status)
	}
	encoded, err := marshalSanitized(summary)
	if err != nil {
		return hermestoolsdb.SetHermesMutationRecoveryOutcomeParams{}, err
	}
	return hermestoolsdb.SetHermesMutationRecoveryOutcomeParams{
		ResultStatus:  string(status),
		ResultSummary: encoded,
		ErrorClass:    nilIfEmpty(errorClass),
		ReturnedAt:    pgtype.Timestamptz{Time: returnedAt.UTC(), Valid: true},
		OperationID:   toPGUUID(operationID),
	}, nil
}

func marshalSanitized(value map[string]any) ([]byte, error) {
	if value == nil {
		value = map[string]any{}
	}
	return json.Marshal(hermes.SanitizeArgs(value))
}

func toPGUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: value, Valid: value != uuid.Nil}
}

func fromPGUUID(value pgtype.UUID) (uuid.UUID, error) {
	if !value.Valid {
		return uuid.Nil, fmt.Errorf("恢复记录缺少操作号")
	}
	return uuid.UUID(value.Bytes), nil
}

func durationInterval(value time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: value.Microseconds(), Valid: true}
}

func nilIfEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
