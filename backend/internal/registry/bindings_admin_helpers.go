package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func validateBindingAudit(actor, actorRole string) error {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(actorRole) == "" {
		return fmt.Errorf("%w: admin actor and role are required", ErrBindingInvalid)
	}
	return nil
}

func bindingChangedFields(in UpdateBindingInput) []string {
	fields := make([]string, 0, 13)
	if in.Priority.Set {
		fields = append(fields, "priority")
	}
	if in.Weight.Set {
		fields = append(fields, "weight")
	}
	if in.SelectionMode.Set {
		fields = append(fields, "selection_mode")
	}
	if in.ProviderModelIDOverride.Set {
		fields = append(fields, "provider_model_id_override")
	}
	if in.RPMLimit.Set {
		fields = append(fields, "rpm_limit")
	}
	if in.TPMLimit.Set {
		fields = append(fields, "tpm_limit")
	}
	if in.MaxParallelRequests.Set {
		fields = append(fields, "max_parallel_requests")
	}
	if in.FallbackClass.Set {
		fields = append(fields, "fallback_class")
	}
	if in.Enabled.Set {
		fields = append(fields, "enabled")
	}
	if in.DisabledReason.Set {
		fields = append(fields, "disabled_reason")
	}
	if in.EffectiveFrom.Set {
		fields = append(fields, "effective_from")
	}
	if in.EffectiveUntil.Set {
		fields = append(fields, "effective_until")
	}
	if in.Reason.Set {
		fields = append(fields, "reason")
	}
	return fields
}

func insertBindingMutationLog(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, bindingID int64,
	action, actor, actorRole, requestID, reason string,
	payload []byte,
) error {
	requestID = strings.TrimSpace(requestID)
	reason = strings.TrimSpace(reason)
	_, err := admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   &tenantID,
		ActorID:    strings.TrimSpace(actor),
		ActorRole:  strings.TrimSpace(actorRole),
		Action:     action,
		TargetType: "model_pool_binding",
		TargetID:   &bindingID,
		RequestID:  bindingOptionalText(requestID),
		Reason:     bindingOptionalText(reason),
		Payload:    payload,
	})
	if err != nil {
		return fmt.Errorf("%w: write binding log: %v", ErrRegistryBackend, err)
	}
	return nil
}

func bindingOptionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// checkModelBindable 校验 model 存在且该租户可绑：同租户的租户自有 model，或全局 model。
func checkModelBindable(ctx context.Context, tx pgx.Tx, modelID, tenantID int64) error {
	var one int
	err := tx.QueryRow(ctx, `
SELECT 1 FROM models m
WHERE m.id = $1 AND m.deleted_at IS NULL
  AND ((m.scope = 'tenant' AND m.tenant_id = $2)
       OR (m.scope = 'global' AND m.tenant_id IS NULL))`, modelID, tenantID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrModelNotBindable
	}
	if err != nil {
		return fmt.Errorf("%w: check model bindable: %v", ErrRegistryBackend, err)
	}
	return nil
}

// checkPoolGroupOwned 校验 pool_group 归属该租户，显式预检用于返回稳定 4xx。
func checkPoolGroupOwned(ctx context.Context, tx pgx.Tx, poolGroupID, tenantID int64) error {
	var one int
	err := tx.QueryRow(ctx, `
SELECT 1 FROM pool_groups
WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, poolGroupID, tenantID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPoolGroupNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: check pool group owned: %v", ErrRegistryBackend, err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func bindingReason(reason, op string) string {
	if reason == "" {
		return "admin binding " + op
	}
	return reason
}

func bindingActor(actor string) string {
	if actor == "" {
		return "admin"
	}
	return actor
}
