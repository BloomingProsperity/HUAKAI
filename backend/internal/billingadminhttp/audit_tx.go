package billingadminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

var errAdminBillingSettingsInvalidPersistedValue = errors.New("admin billing settings: invalid persisted value")

type adminBillingSettingsTxPhase string

const (
	adminBillingSettingsTxPhaseBegin  adminBillingSettingsTxPhase = "begin"
	adminBillingSettingsTxPhaseRead   adminBillingSettingsTxPhase = "read"
	adminBillingSettingsTxPhaseUpdate adminBillingSettingsTxPhase = "update"
	adminBillingSettingsTxPhaseAudit  adminBillingSettingsTxPhase = "audit"
	adminBillingSettingsTxPhaseCommit adminBillingSettingsTxPhase = "commit"
)

type adminBillingSettingsTxPhaseError struct {
	phase adminBillingSettingsTxPhase
	err   error
}

func (e adminBillingSettingsTxPhaseError) Error() string {
	if e.err == nil {
		return string(e.phase)
	}
	return e.err.Error()
}

func (e adminBillingSettingsTxPhaseError) Unwrap() error {
	return e.err
}

func adminBillingSettingsPhaseError(phase adminBillingSettingsTxPhase, err error) error {
	if err == nil {
		return nil
	}
	return adminBillingSettingsTxPhaseError{phase: phase, err: err}
}

func adminBillingSettingsErrorPhase(err error) (adminBillingSettingsTxPhase, bool) {
	var phaseErr adminBillingSettingsTxPhaseError
	if errors.As(err, &phaseErr) {
		return phaseErr.phase, true
	}
	return "", false
}

// AdminBillingSettingsAuditUpdate 是管理端 billing setting PUT 的事务输入。
type AdminBillingSettingsAuditUpdate struct {
	TenantID  int64
	Policy    billing.StreamInputOnlyInterruptedPolicy
	UpdatedBy string
	ActorID   string
	ActorRole string
	Reason    string
	RequestID string
}

// AdminBillingSettingsAuditUpdateResult 返回事务提交后的设置行。
type AdminBillingSettingsAuditUpdateResult struct {
	Previous      billing.StoredBillingSetting
	PreviousFound bool
	Updated       billing.StoredBillingSetting
}

// AdminBillingSettingsAuditUpdater 把设置更新和管理审计绑定成一个原子入口。
type AdminBillingSettingsAuditUpdater interface {
	UpsertStreamInputOnlyInterruptedPolicyWithAudit(context.Context, AdminBillingSettingsAuditUpdate) (AdminBillingSettingsAuditUpdateResult, error)
}

type adminBillingSettingsBillingQueries interface {
	AcquireBillingSettingLock(context.Context, dbbilling.AcquireBillingSettingLockParams) error
	GetBillingSettingForUpdate(context.Context, dbbilling.GetBillingSettingForUpdateParams) (dbbilling.BillingSetting, error)
	UpsertBillingSetting(context.Context, dbbilling.UpsertBillingSettingParams) (dbbilling.BillingSetting, error)
}

type adminBillingSettingsAuditQueries interface {
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type adminBillingSettingsTxRunner interface {
	RunAdminBillingSettingsTx(context.Context, func(context.Context, adminBillingSettingsBillingQueries, adminBillingSettingsAuditQueries) error) error
}

type adminBillingSettingsTxUpdater struct {
	runner adminBillingSettingsTxRunner
}

// NewAdminBillingSettingsAuditUpdater 构造生产用事务化更新入口。
func NewAdminBillingSettingsAuditUpdater(pool *pgxpool.Pool) AdminBillingSettingsAuditUpdater {
	return &adminBillingSettingsTxUpdater{
		runner: adminBillingSettingsPostgresTxRunner{pool: pool},
	}
}

func (u *adminBillingSettingsTxUpdater) UpsertStreamInputOnlyInterruptedPolicyWithAudit(ctx context.Context, in AdminBillingSettingsAuditUpdate) (AdminBillingSettingsAuditUpdateResult, error) {
	if u == nil || u.runner == nil {
		return AdminBillingSettingsAuditUpdateResult{}, billing.ErrPoolNotConfigured
	}
	if in.TenantID <= 0 {
		return AdminBillingSettingsAuditUpdateResult{}, fmt.Errorf("%w: tenant_id", billing.ErrBillingSettingInvalid)
	}
	canonical, err := billing.ParseStreamInputOnlyInterruptedPolicy(in.Policy.String())
	if err != nil {
		return AdminBillingSettingsAuditUpdateResult{}, err
	}
	updatedBy := strings.TrimSpace(in.UpdatedBy)
	if updatedBy == "" {
		updatedBy = "system"
	}
	actorID := strings.TrimSpace(in.ActorID)
	if actorID == "" {
		actorID = updatedBy
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return AdminBillingSettingsAuditUpdateResult{}, fmt.Errorf("%w: reason", billing.ErrBillingSettingInvalid)
	}

	var result AdminBillingSettingsAuditUpdateResult
	err = u.runner.RunAdminBillingSettingsTx(ctx, func(ctx context.Context, bq adminBillingSettingsBillingQueries, aq adminBillingSettingsAuditQueries) error {
		if err := bq.AcquireBillingSettingLock(ctx, dbbilling.AcquireBillingSettingLockParams{
			TenantID:   in.TenantID,
			SettingKey: billing.StreamInputOnlyInterruptedPolicyKey,
		}); err != nil {
			return adminBillingSettingsPhaseError(adminBillingSettingsTxPhaseRead, err)
		}

		previousRow, err := bq.GetBillingSettingForUpdate(ctx, dbbilling.GetBillingSettingForUpdateParams{
			TenantID:   in.TenantID,
			SettingKey: billing.StreamInputOnlyInterruptedPolicyKey,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			result.PreviousFound = false
		} else if err != nil {
			return adminBillingSettingsPhaseError(adminBillingSettingsTxPhaseRead, err)
		} else {
			result.Previous = adminBillingSettingsStoredSettingFromDB(previousRow)
			result.PreviousFound = true
			if _, err := billing.ParseStreamInputOnlyInterruptedPolicy(result.Previous.Value); err != nil {
				return adminBillingSettingsPhaseError(adminBillingSettingsTxPhaseRead, errAdminBillingSettingsInvalidPersistedValue)
			}
		}

		updatedRow, err := bq.UpsertBillingSetting(ctx, dbbilling.UpsertBillingSettingParams{
			TenantID:     in.TenantID,
			SettingKey:   billing.StreamInputOnlyInterruptedPolicyKey,
			SettingValue: canonical.String(),
			UpdatedBy:    updatedBy,
		})
		if err != nil {
			return adminBillingSettingsPhaseError(adminBillingSettingsTxPhaseUpdate, err)
		}
		result.Updated = adminBillingSettingsStoredSettingFromDB(updatedRow)

		payload, err := adminBillingSettingsAuditPayload(result.Previous, result.PreviousFound, result.Updated, reason)
		if err != nil {
			return adminBillingSettingsPhaseError(adminBillingSettingsTxPhaseAudit, err)
		}
		targetID := result.Updated.ID
		tenantID := in.TenantID
		params := admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantID,
			ActorID:    actorID,
			ActorRole:  in.ActorRole,
			Action:     adminBillingSettingsActionUpdate,
			TargetType: "billing_setting",
			TargetID:   &targetID,
			Reason:     &reason,
			Payload:    payload,
		}
		if reqID := strings.TrimSpace(in.RequestID); reqID != "" {
			params.RequestID = &reqID
		}
		if _, err := aq.InsertAdminAuditEvent(ctx, params); err != nil {
			return adminBillingSettingsPhaseError(adminBillingSettingsTxPhaseAudit, err)
		}
		return nil
	})
	if err != nil {
		return AdminBillingSettingsAuditUpdateResult{}, err
	}
	return result, nil
}

type adminBillingSettingsPostgresTxRunner struct {
	pool *pgxpool.Pool
}

func (r adminBillingSettingsPostgresTxRunner) RunAdminBillingSettingsTx(ctx context.Context, fn func(context.Context, adminBillingSettingsBillingQueries, adminBillingSettingsAuditQueries) error) error {
	if r.pool == nil {
		return billing.ErrPoolNotConfigured
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return adminBillingSettingsPhaseError(adminBillingSettingsTxPhaseBegin, err)
	}
	committed := false
	defer func() {
		if !committed {
			// 回滚不能复用已取消的请求 ctx, 否则 ctx 取消路径可能留下未关闭事务。
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(rollbackCtx)
		}
	}()

	if err := fn(ctx, dbbilling.New(tx), admindb.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return adminBillingSettingsPhaseError(adminBillingSettingsTxPhaseCommit, err)
	}
	committed = true
	return nil
}

func adminBillingSettingsAuditPayload(previous billing.StoredBillingSetting, previousFound bool, next billing.StoredBillingSetting, reason string) ([]byte, error) {
	previousValue := billing.DefaultStreamInputOnlyInterruptedPolicy.String()
	previousSource := "default"
	if previousFound {
		policy, err := billing.ParseStreamInputOnlyInterruptedPolicy(previous.Value)
		if err != nil {
			return nil, errAdminBillingSettingsInvalidPersistedValue
		}
		previousValue = policy.String()
		previousSource = "tenant"
	}
	nextPolicy, err := billing.ParseStreamInputOnlyInterruptedPolicy(next.Value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"setting_key":     billing.StreamInputOnlyInterruptedPolicyKey,
		"previous_value":  previousValue,
		"previous_source": previousSource,
		"new_value":       nextPolicy.String(),
		"new_source":      "tenant",
		"reason":          reason,
	})
}

func adminBillingSettingsStoredSettingFromDB(row dbbilling.BillingSetting) billing.StoredBillingSetting {
	return billing.StoredBillingSetting{
		ID:        row.ID,
		TenantID:  row.TenantID,
		Key:       row.SettingKey,
		Value:     row.SettingValue,
		UpdatedAt: adminBillingSettingsTime(row.UpdatedAt.Time, row.UpdatedAt.Valid),
		UpdatedBy: row.UpdatedBy,
	}
}

func adminBillingSettingsTime(value time.Time, valid bool) time.Time {
	if !valid {
		return time.Time{}
	}
	return value.UTC()
}

var _ AdminBillingSettingsAuditUpdater = (*adminBillingSettingsTxUpdater)(nil)
var _ adminBillingSettingsTxRunner = adminBillingSettingsPostgresTxRunner{}
var _ adminBillingSettingsAuditQueries = (*admindb.Queries)(nil)
var _ adminBillingSettingsBillingQueries = (*dbbilling.Queries)(nil)
