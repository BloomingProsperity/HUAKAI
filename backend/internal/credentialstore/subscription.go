package credentialstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

type subscriptionStateRow struct {
	ObservationID int64
	Observation   subscriptionprofile.Observation
	FirstObserved time.Time
	ObservedAt    time.Time
	ChangedAt     time.Time
}

// resolveSubscriptionObservation 优先使用获取链已经标明来源的观测，回退时只从
// 凭据载荷提取展示事实。该结果绝不能参与授权、计费或配额判断。
func resolveSubscriptionObservation(vendor, authMode string, payload []byte, supplied subscriptionprofile.Observation) subscriptionprofile.Observation {
	if !supplied.Empty() {
		return supplied
	}
	return subscriptionprofile.DetectPayload(vendor, authMode, payload)
}

func (s *Store) persistSubscriptionProjection(
	ctx context.Context,
	tenantID, providerAccountID, credentialID int64,
	credentialVersion int32,
	vendor, authMode string,
	payload []byte,
	supplied subscriptionprofile.Observation,
) (*subscriptionprofile.Observation, error) {
	observation := resolveSubscriptionObservation(vendor, authMode, payload, supplied)
	if observation.Empty() {
		return nil, nil
	}
	projected, err := s.recordSubscriptionObservation(
		ctx, tenantID, providerAccountID, credentialID, credentialVersion, observation,
	)
	if err != nil {
		return nil, err
	}
	return &projected, nil
}

func credentialSubscriptionAuditPayload(observation *subscriptionprofile.Observation) map[string]any {
	payload := map[string]any{"credentials_present": true}
	if observation == nil {
		return payload
	}
	if label := observation.Label(); label != "" {
		payload["subscription_label"] = label
		payload["subscription_status"] = observation.Status
	}
	return payload
}

// freshSubscriptionRefreshObservation 只提取本次刷新真正新增或改变的套餐证据。
// 刷新适配器会保留旧字段，因此必须先对比新旧 payload，再按证据
// 来源独立解析，避免旧 id_token 遮住刷新响应直接返回的新套餐。
func freshSubscriptionRefreshObservation(vendor, authMode string, previousPayload, nextPayload []byte) (subscriptionprofile.Observation, bool) {
	var previous, next map[string]any
	if json.Unmarshal(previousPayload, &previous) != nil {
		previous = nil
	}
	if json.Unmarshal(nextPayload, &next) != nil || next == nil {
		return subscriptionprofile.Observation{}, false
	}
	if token := changedStringField(previous, next, "id_token", "idToken"); token != "" {
		observation := subscriptionObservationFromFields(vendor, authMode, next, "id_token", token)
		if subscriptionRefreshObservationUsable(observation) {
			return observation, true
		}
	}
	explicitNames := []string{"chatgpt_plan_type", "plan_type", "subscription_plan", "subscription_tier", "subscription_tier_raw", "tier_id"}
	if rawPlan := changedStringField(previous, next, explicitNames...); rawPlan != "" {
		observation := subscriptionObservationFromFields(vendor, authMode, next, "subscription_plan", rawPlan)
		if !observation.Empty() {
			if antigravityRefreshMetadataVerified(vendor, authMode, next) {
				observation.Source = subscriptionprofile.SourceProviderAPI
				observation.Trust = subscriptionprofile.TrustVerifiedAPI
				observation.Verification = subscriptionprofile.VerificationVerified
			} else {
				observation.Source = subscriptionprofile.SourceCredentialRefresh
				observation.Trust = subscriptionprofile.TrustIssuerResponse
				observation.Verification = subscriptionprofile.VerificationIssuerResponse
			}
			return observation, true
		}
	}
	if token := changedStringField(previous, next, "access_token", "accessToken", "session_token"); token != "" {
		observation := subscriptionObservationFromFields(vendor, authMode, next, "access_token", token)
		if subscriptionRefreshObservationUsable(observation) {
			return observation, true
		}
	}
	return subscriptionprofile.Observation{}, false
}

func antigravityRefreshMetadataVerified(vendor, authMode string, fields map[string]any) bool {
	vendor = Normalize(vendor)
	authMode = Normalize(authMode)
	isAntigravity := vendor == VendorAntigravity && authMode == AuthModeOAuth
	isCompatibilityMode := vendor == VendorGemini && authMode == AuthModeAntigravity
	return (isAntigravity || isCompatibilityMode) && firstMapString(fields, "subscription_metadata_status") == "resolved"
}

func changedStringField(previous, next map[string]any, names ...string) string {
	before := firstMapString(previous, names...)
	after := firstMapString(next, names...)
	if after == "" || before == after {
		return ""
	}
	return after
}

func subscriptionObservationFromFields(vendor, authMode string, fields map[string]any, evidenceKey, evidenceValue string) subscriptionprofile.Observation {
	payload := map[string]any{evidenceKey: evidenceValue}
	for _, key := range []string{
		"external_subject_id", "chatgpt_user_id", "user_id",
		"external_account_id", "chatgpt_account_id", "account_id", "workspace_id", "organization_id",
	} {
		if value := firstMapString(fields, key); value != "" {
			payload[key] = value
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return subscriptionprofile.Observation{}
	}
	return subscriptionprofile.DetectPayload(vendor, authMode, raw)
}

func subscriptionRefreshObservationUsable(observation subscriptionprofile.Observation) bool {
	return !observation.Empty() && observation.Status != subscriptionprofile.StatusMissing &&
		observation.Status != subscriptionprofile.StatusParseFailed
}

func firstMapString(fields map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := fields[name].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func (s *Store) recordSubscriptionObservation(
	ctx context.Context,
	tenantID, providerAccountID, credentialID int64,
	credentialVersion int32,
	observation subscriptionprofile.Observation,
) (subscriptionprofile.Observation, error) {
	if observation.Empty() {
		return subscriptionprofile.Observation{}, nil
	}
	const insertObservation = `
INSERT INTO provider_account_subscription_observations (
    tenant_id, provider_account_id, account_credential_id, credential_version,
    vendor, normalized_plan, raw_plan, scope_kind, subject_ref, workspace_ref,
    source_type, trust_level, verification_status, observation_status,
    mapping_version, error_class
) VALUES (
    $1, $2, $3, $4,
    $5, $6, NULLIF($7, ''), $8, NULLIF($9, ''), NULLIF($10, ''),
    $11, $12, $13, $14,
    $15, NULLIF($16, '')
)
RETURNING id, observed_at`
	var observationID int64
	var observedAt time.Time
	if err := s.db.QueryRow(ctx, insertObservation,
		tenantID, providerAccountID, credentialID, credentialVersion,
		observation.Vendor, observation.Plan, observation.RawPlan, observation.Scope,
		observation.SubjectRef, observation.WorkspaceRef,
		observation.Source, observation.Trust, observation.Verification, observation.Status,
		observation.MappingVersion, observation.ErrorClass,
	).Scan(&observationID, &observedAt); err != nil {
		return subscriptionprofile.Observation{}, err
	}

	current, err := s.loadSubscriptionStateForUpdate(ctx, tenantID, providerAccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		var inserted bool
		inserted, err = s.insertSubscriptionState(ctx, tenantID, providerAccountID, observationID, observation, observedAt)
		if err != nil {
			return subscriptionprofile.Observation{}, err
		}
		if inserted {
			return observation, nil
		}
		// 并发事务刚建立了同一账号的首个投影；唯一键冲突等待结束后重读并走统一仲裁。
		current, err = s.loadSubscriptionStateForUpdate(ctx, tenantID, providerAccountID)
	}
	if err != nil {
		return subscriptionprofile.Observation{}, err
	}

	next, changedAt, replaceObservedAt, replaceSource := mergeSubscriptionState(current, observation, observedAt)
	if err := s.updateSubscriptionState(ctx, tenantID, providerAccountID, observationID, next, current, observedAt, changedAt, replaceObservedAt, replaceSource); err != nil {
		return subscriptionprofile.Observation{}, err
	}
	return next, nil
}

// RecordSubscriptionObservation 把后台只读探测得到的套餐事实绑定到探测时实际使用的凭据版本。
func (s *Store) RecordSubscriptionObservation(
	ctx context.Context,
	tenantID, providerAccountID, credentialID int64,
	credentialVersion int32,
	observation subscriptionprofile.Observation,
) (subscriptionprofile.Observation, error) {
	if tenantID <= 0 || providerAccountID <= 0 || credentialID <= 0 || credentialVersion <= 0 || observation.Empty() {
		return subscriptionprofile.Observation{}, ErrInvalidPayload
	}
	var projected subscriptionprofile.Observation
	err := s.WithTransaction(ctx, func(txStore *Store, _ db.DBTX) error {
		const lockCredential = `
SELECT 1
FROM account_credentials
WHERE id = $1
  AND tenant_id = $2
  AND provider_account_id = $3
  AND credential_version = $4
  AND deleted_at IS NULL
FOR SHARE`
		var marker int
		if err := txStore.db.QueryRow(ctx, lockCredential, credentialID, tenantID, providerAccountID, credentialVersion).Scan(&marker); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrCredentialVersionConflict
			}
			return err
		}
		var err error
		projected, err = txStore.recordSubscriptionObservation(
			ctx, tenantID, providerAccountID, credentialID, credentialVersion, observation,
		)
		return err
	})
	return projected, err
}

func (s *Store) loadSubscriptionStateForUpdate(ctx context.Context, tenantID, providerAccountID int64) (subscriptionStateRow, error) {
	const query = `
SELECT current_observation_id, vendor, normalized_plan, COALESCE(raw_plan, ''),
       scope_kind, COALESCE(subject_ref, ''), COALESCE(workspace_ref, ''),
       source_type, trust_level, verification_status, state_status,
       mapping_version, COALESCE(error_class, ''),
       first_observed_at, observed_at, changed_at
FROM provider_account_subscription_states
WHERE tenant_id = $1 AND provider_account_id = $2
FOR UPDATE`
	var row subscriptionStateRow
	err := s.db.QueryRow(ctx, query, tenantID, providerAccountID).Scan(
		&row.ObservationID,
		&row.Observation.Vendor, &row.Observation.Plan, &row.Observation.RawPlan,
		&row.Observation.Scope, &row.Observation.SubjectRef, &row.Observation.WorkspaceRef,
		&row.Observation.Source, &row.Observation.Trust, &row.Observation.Verification,
		&row.Observation.Status, &row.Observation.MappingVersion, &row.Observation.ErrorClass,
		&row.FirstObserved, &row.ObservedAt, &row.ChangedAt,
	)
	return row, err
}

func (s *Store) insertSubscriptionState(ctx context.Context, tenantID, providerAccountID, observationID int64, observation subscriptionprofile.Observation, observedAt time.Time) (bool, error) {
	const query = `
INSERT INTO provider_account_subscription_states (
    tenant_id, provider_account_id, current_observation_id,
    vendor, normalized_plan, raw_plan, scope_kind, subject_ref, workspace_ref,
    source_type, trust_level, verification_status, state_status,
    mapping_version, error_class,
    first_observed_at, observed_at, changed_at
) VALUES (
    $1, $2, $3,
    $4, $5, NULLIF($6, ''), $7, NULLIF($8, ''), NULLIF($9, ''),
    $10, $11, $12, $13,
    $14, NULLIF($15, ''),
    $16, $16, $16
) ON CONFLICT (tenant_id, provider_account_id) DO NOTHING`
	tag, err := s.db.Exec(ctx, query,
		tenantID, providerAccountID, observationID,
		observation.Vendor, observation.Plan, observation.RawPlan, observation.Scope,
		observation.SubjectRef, observation.WorkspaceRef,
		observation.Source, observation.Trust, observation.Verification, observation.Status,
		observation.MappingVersion, observation.ErrorClass, observedAt,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) updateSubscriptionState(
	ctx context.Context,
	tenantID, providerAccountID, observationID int64,
	next subscriptionprofile.Observation,
	current subscriptionStateRow,
	acceptedObservedAt time.Time,
	changedAt time.Time,
	replaceObservedAt, replaceSource bool,
) error {
	observedAt := current.ObservedAt
	if replaceObservedAt {
		observedAt = acceptedObservedAt
	}
	firstObserved := current.FirstObserved
	if firstObserved.IsZero() {
		firstObserved = observedAt
	}
	if changedAt.IsZero() {
		changedAt = current.ChangedAt
	}
	if !replaceSource {
		next.Source = current.Observation.Source
		next.Trust = current.Observation.Trust
		next.Verification = current.Observation.Verification
	}
	const query = `
UPDATE provider_account_subscription_states
SET current_observation_id = $3,
    vendor = $4,
    normalized_plan = $5,
    raw_plan = NULLIF($6, ''),
    scope_kind = $7,
    subject_ref = NULLIF($8, ''),
    workspace_ref = NULLIF($9, ''),
    source_type = $10,
    trust_level = $11,
    verification_status = $12,
    state_status = $13,
    mapping_version = $14,
    error_class = NULLIF($15, ''),
    first_observed_at = $16,
    observed_at = $17,
    changed_at = $18,
    updated_at = NOW()
WHERE tenant_id = $1 AND provider_account_id = $2`
	tag, err := s.db.Exec(ctx, query,
		tenantID, providerAccountID, observationID,
		next.Vendor, next.Plan, next.RawPlan, next.Scope, next.SubjectRef, next.WorkspaceRef,
		next.Source, next.Trust, next.Verification, next.Status, next.MappingVersion,
		next.ErrorClass, firstObserved, observedAt, changedAt,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("credentialstore: 套餐当前投影更新丢失")
	}
	return nil
}

func mergeSubscriptionState(current subscriptionStateRow, incoming subscriptionprofile.Observation, observedAt time.Time) (subscriptionprofile.Observation, time.Time, bool, bool) {
	previous := current.Observation
	if incoming.Status == subscriptionprofile.StatusConflict {
		if previous.Status == subscriptionprofile.StatusObserved || previous.Status == subscriptionprofile.StatusUnknownValue ||
			previous.Status == subscriptionprofile.StatusStale || previous.Status == subscriptionprofile.StatusConflict {
			next := previous
			next.Status = subscriptionprofile.StatusConflict
			next.ErrorClass = firstNonEmptyString(incoming.ErrorClass, "subscription_evidence_conflict")
			return next, subscriptionStateChangedAt(current, next, observedAt), false, false
		}
		return incoming, observedAt, true, true
	}
	if incoming.Status == subscriptionprofile.StatusMissing || incoming.Status == subscriptionprofile.StatusParseFailed {
		if previous.Status == subscriptionprofile.StatusObserved || previous.Status == subscriptionprofile.StatusUnknownValue || previous.Status == subscriptionprofile.StatusStale {
			next := previous
			next.Status = subscriptionprofile.StatusStale
			next.ErrorClass = firstNonEmptyString(incoming.ErrorClass, "subscription_evidence_missing")
			return next, subscriptionStateChangedAt(current, next, observedAt), false, false
		}
		return incoming, observedAt, true, true
	}
	if subscriptionScopeConflicts(previous, incoming) {
		next := previous
		next.Status = subscriptionprofile.StatusConflict
		next.ErrorClass = "subscription_scope_conflict"
		return next, subscriptionStateChangedAt(current, next, observedAt), false, false
	}
	if subscriptionprofile.TrustRank(incoming.Trust) < subscriptionprofile.TrustRank(previous.Trust) {
		if previous.Plan != incoming.Plan {
			next := previous
			next.Status = subscriptionprofile.StatusConflict
			next.ErrorClass = "weaker_subscription_evidence_conflict"
			return next, subscriptionStateChangedAt(current, next, observedAt), false, false
		}
		// 同值弱证据只追加历史，不得降低当前投影的信任等级。
		// 否则下一条弱冲突证据就可能覆盖原来的强证据。
		return previous, current.ChangedAt, false, false
	}
	changedAt := current.ChangedAt
	if subscriptionStateChanged(previous, incoming) {
		changedAt = observedAt
	}
	return incoming, changedAt, true, true
}

func subscriptionStateChangedAt(current subscriptionStateRow, next subscriptionprofile.Observation, observedAt time.Time) time.Time {
	if subscriptionStateChanged(current.Observation, next) {
		return observedAt
	}
	return current.ChangedAt
}

func subscriptionStateChanged(current, incoming subscriptionprofile.Observation) bool {
	return subscriptionMeaningChanged(current, incoming) || current.Status != incoming.Status ||
		current.ErrorClass != incoming.ErrorClass
}

func subscriptionScopeConflicts(current, incoming subscriptionprofile.Observation) bool {
	if current.Vendor != incoming.Vendor {
		return true
	}
	if current.Scope != subscriptionprofile.ScopeUnknown && incoming.Scope != subscriptionprofile.ScopeUnknown && current.Scope != incoming.Scope {
		return true
	}
	if distinctNonEmpty(current.SubjectRef, incoming.SubjectRef) {
		return true
	}
	return distinctNonEmpty(current.WorkspaceRef, incoming.WorkspaceRef)
}

func subscriptionMeaningChanged(current, incoming subscriptionprofile.Observation) bool {
	return current.Plan != incoming.Plan || current.RawPlan != incoming.RawPlan ||
		current.Scope != incoming.Scope || current.SubjectRef != incoming.SubjectRef ||
		current.WorkspaceRef != incoming.WorkspaceRef
}

func distinctNonEmpty(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	return left != "" && right != "" && left != right
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
