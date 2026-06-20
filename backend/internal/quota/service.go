package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

const (
	decisionCodeAllowed        = "quota_reserve_allowed"
	decisionCodeReused         = "quota_reservation_reused"
	decisionCodeSettledReplay  = "quota_reservation_settled_replay"
	decisionCodeInactiveClaim  = "quota_reservation_inactive"
	decisionCodeReplayConflict = "quota_reservation_replay_conflict"
	decisionCodeLimitExceeded  = "quota_limit_exceeded"
	decisionCodeFailClosed     = "quota_fail_closed"

	reserveTxRetryAttempts    = 3
	reserveTxRetryBaseBackoff = 5 * time.Millisecond
	reserveTxRetryMaxJitter   = 3 * time.Millisecond
)

var ErrReserveRequiresTransaction = errors.New("quota: reserve requires transaction support")

type quotaTxStore interface {
	WithTx(ctx context.Context, fn func(PGStore) error) error
}

// Service 编排 quota reserve 准入决策。
type Service struct {
	store PGStore
}

func NewService(store PGStore) *Service {
	return &Service{store: store}
}

type ReserveRequest struct {
	TenantID            int64
	ClaimID             int64
	RequestFingerprint  string
	Scopes              []Scope
	RequestedModel      string
	ReservedTokens      int64
	PredictedCost       decimal.Decimal
	NeedConcurrencySlot bool
	LeaseExpiresAt      time.Time
	At                  time.Time
}

type ReserveInput = ReserveRequest

type ReserveResult struct {
	Allowed        bool
	Decision       Decision
	Reservation    Reservation
	IdempotencyHit bool
}

func (s *Service) Reserve(ctx context.Context, req ReserveRequest) (ReserveResult, error) {
	req = normalizeReserveRequest(req)
	if s == nil || s.store == nil {
		result := denyResult(failClosedDecision(req, errors.New("quota store is not configured")))
		return result, &DenyError{Decision: result.Decision, Cause: ErrStoreNotConfigured}
	}

	var result ReserveResult
	var deny *DenyError
	var err error
	for attempt := 0; attempt < reserveTxRetryAttempts; attempt++ {
		result = ReserveResult{}
		deny = nil
		err = s.withStore(ctx, func(tx PGStore) error {
			existing, err := tx.GetReservationByClaimForUpdate(ctx, req.TenantID, req.ClaimID)
			if err == nil && existing.ID != 0 {
				if conflict := reservationReplayConflict(req, existing); conflict != nil {
					result = ReserveResult{Decision: conflict.Decision, Reservation: existing}
					deny = conflict
					return nil
				}
				if canReactivateReservation(existing.Status) {
					result, deny, err = reactivateExistingReservation(ctx, tx, req, existing)
					return err
				}
				result, deny = existingReservationResult(req, existing)
				return nil
			}
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				decision := failClosedDecision(req, err)
				result = denyResult(decision)
				deny = denyErr(decision, err)
				return nil
			}

			resolved, err := ResolvePolicies(ctx, tx, req.TenantID, req.Scopes, reserveMetrics(req), req.At)
			if err != nil {
				decision := failClosedDecision(req, err)
				result = denyResult(decision)
				deny = denyErr(decision, err)
				return nil
			}

			evaluated, err := evaluatePolicies(ctx, tx, req, resolved)
			if err != nil {
				return err
			}
			if evaluated.deny != nil {
				if err := insertDecisionAudit(ctx, tx, req, nil, "reserve_denied", evaluated.deny.Decision, evaluated.denyPayload); err != nil {
					return err
				}
				result = denyResult(evaluated.deny.Decision)
				deny = evaluated.deny
				return nil
			}

			for _, observation := range evaluated.observations {
				if err := insertDecisionAudit(ctx, tx, req, nil, "observe_exceeded", observation.decision, observation.payload); err != nil {
					return err
				}
			}

			reservation, err := tx.InsertReservation(ctx, ReservationInsert{
				TenantID:           req.TenantID,
				ClaimID:            req.ClaimID,
				RequestFingerprint: req.RequestFingerprint,
				Scopes:             req.Scopes,
				PolicySnapshot:     marshalReservationPolicySnapshot(resolved.Ordered, evaluated.enforceWindows),
				PredictedCost:      req.PredictedCost,
				ReservedUnits:      decimal.NewFromInt(1),
				LeaseExpiresAt:     req.LeaseExpiresAt,
			})
			if err != nil {
				if isQuotaReservationClaimUnique(err) {
					return &reservationClaimConflictError{cause: err}
				}
				return err
			}

			if err := applyEnforceReservations(ctx, tx, req, reservation, evaluated.enforceWindows); err != nil {
				if rollback, ok := err.(*rollbackDenyError); ok {
					deny = rollback.deny
					result = denyResult(rollback.deny.Decision)
				}
				return err
			}

			decision := Decision{
				Kind:   DecisionAllow,
				Code:   decisionCodeAllowed,
				Reason: "all enforce quota policies allowed",
			}
			resID := reservation.ID
			if err := insertDecisionAudit(ctx, tx, req, &resID, "reserve_allowed", decision, nil); err != nil {
				return err
			}
			result = ReserveResult{Allowed: true, Decision: decision, Reservation: reservation}
			return nil
		})
		if !isPgRetryableTxConflict(err) {
			break
		}
		if attempt == reserveTxRetryAttempts-1 {
			return ReserveResult{}, &RetryableError{Operation: "reserve transaction", Cause: err}
		}
		// 40001/40P01 是整笔 serializable 事务的瞬时冲突; rollback 后重跑完整 reserve, 不走 claim reread。
		if err := sleepBeforeReserveRetry(ctx, attempt); err != nil {
			return ReserveResult{}, err
		}
	}
	if isReservationClaimRetry(err) {
		return s.reuseReservationAfterClaimRace(ctx, req, err)
	}
	if err != nil {
		if rollback, ok := err.(*rollbackDenyError); ok {
			_ = s.writeDenyAuditBestEffort(ctx, req, rollback.deny.Decision, rollback.payload)
			return result, rollback.deny
		}
		decision := failClosedDecision(req, err)
		result = denyResult(decision)
		return result, denyErr(decision, err)
	}
	if deny != nil {
		return result, deny
	}
	return result, nil
}

func (s *Service) withStore(ctx context.Context, fn func(PGStore) error) error {
	if txStore, ok := s.store.(quotaTxStore); ok {
		return txStore.WithTx(ctx, fn)
	}
	return ErrReserveRequiresTransaction
}

type evaluatedPolicy struct {
	policy Policy
	window WindowCounter
	amount decimal.Decimal
	metric Metric
}

type policyObservation struct {
	decision Decision
	payload  []byte
}

type policyEvaluation struct {
	enforceWindows []evaluatedPolicy
	observations   []policyObservation
	deny           *DenyError
	denyPayload    []byte
}

func marshalReservationPolicySnapshot(policies []Policy, evaluated []evaluatedPolicy) []byte {
	concreteWindows := make(map[policyMetricKey]Window, len(evaluated))
	reservedAmounts := make(map[policyMetricKey]decimal.Decimal, len(evaluated))
	for _, item := range evaluated {
		if item.policy.Mode != ModeEnforce {
			continue
		}
		if !metricHasWindowReservation(item.metric) {
			continue
		}
		key := policyMetricKey{policyID: item.policy.ID, metric: item.metric}
		concreteWindows[key] = item.window.Window
		reservedAmounts[key] = item.amount
	}

	snapshotPolicies := make([]Policy, len(policies))
	copy(snapshotPolicies, policies)
	for i := range snapshotPolicies {
		policy := snapshotPolicies[i]
		if policy.Mode != ModeEnforce {
			continue
		}
		if !metricHasWindowReservation(policy.Metric) {
			continue
		}
		if window, ok := concreteWindows[policyMetricKey{policyID: policy.ID, metric: policy.Metric}]; ok {
			snapshotPolicies[i].Window.Start = window.Start
			snapshotPolicies[i].Window.End = window.End
		}
	}
	return marshalPolicySnapshot(snapshotPolicies, reservedAmounts)
}

type policyMetricKey struct {
	policyID int64
	metric   Metric
}

// metricHasWindowReservation 列举需要窗口预留账本的 metric(单点真相,避免散落
// 硬编码 != MetricRequests && != MetricCostUSD)。tokens_estimated 加入后,
// token-per-window 配额从 observe-only 变为真实预留/拦截。
func metricHasWindowReservation(metric Metric) bool {
	return metric == MetricRequests || metric == MetricCostUSD || metric == MetricTokensEstimated
}

func evaluatePolicies(ctx context.Context, store PGStore, req ReserveRequest, resolved ResolvedPolicies) (policyEvaluation, error) {
	var out policyEvaluation
	for _, policy := range resolved.Ordered {
		if policy.Metric == MetricConcurrency && !req.NeedConcurrencySlot {
			continue
		}
		assessment, err := assessPolicy(ctx, store, req, policy)
		if err != nil {
			return policyEvaluation{}, err
		}
		if assessment.skipped {
			continue
		}
		switch policy.Mode {
		case ModeEnforce:
			if assessment.exceeded {
				decision := exceededDecision(req, policy, assessment)
				return policyEvaluation{
					deny:        denyErr(decision, ErrDenied),
					denyPayload: assessment.payload(policy),
				}, nil
			}
			out.enforceWindows = append(out.enforceWindows, evaluatedPolicy{
				policy: policy,
				window: assessment.window,
				amount: assessment.amount,
				metric: policy.Metric,
			})
		case ModeObserve, ModeManualFirst:
			if assessment.exceeded {
				decision := exceededDecision(req, policy, assessment)
				if policy.Mode == ModeManualFirst {
					decision.Reason = "manual_first=限额已配但需运营手动激活才 enforce,暂不阻断"
				}
				out.observations = append(out.observations, policyObservation{
					decision: decision,
					payload:  assessment.payload(policy),
				})
			}
		}
	}
	return out, nil
}

func applyEnforceReservations(ctx context.Context, store PGStore, req ReserveRequest, reservation Reservation, evaluated []evaluatedPolicy) error {
	for _, item := range evaluated {
		switch item.metric {
		case MetricRequests:
			if _, err := store.IncrementWindowReserved(ctx, WindowReserve{
				TenantID:          req.TenantID,
				WindowID:          item.window.ID,
				ReserveDelta:      item.amount,
				RequestCountDelta: 1,
				LimitValue:        item.policy.LimitValue,
			}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					decision := exceededDecision(req, item.policy, policyAssessment{
						amount:       decimal.NewFromInt(1),
						limit:        item.policy.LimitValue,
						retryAfter:   retryAfter(req.At, item.policy),
						requestCount: item.window.RequestCount,
					})
					return &rollbackDenyError{
						deny:    denyErr(decision, ErrDenied),
						payload: assessmentPayload(item.policy, item.window.ReservedValue.Add(item.window.SettledValue), item.amount, item.policy.LimitValue, item.window.RequestCount),
					}
				}
				return err
			}
		case MetricCostUSD:
			if _, err := store.IncrementWindowReserved(ctx, WindowReserve{
				TenantID:          req.TenantID,
				WindowID:          item.window.ID,
				ReserveDelta:      req.PredictedCost,
				RequestCountDelta: 0,
				LimitValue:        item.policy.LimitValue,
			}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					decision := exceededDecision(req, item.policy, policyAssessment{
						amount:     req.PredictedCost,
						limit:      item.policy.LimitValue,
						retryAfter: retryAfter(req.At, item.policy),
					})
					return &rollbackDenyError{
						deny:    denyErr(decision, ErrDenied),
						payload: assessmentPayload(item.policy, item.window.ReservedValue.Add(item.window.SettledValue), req.PredictedCost, item.policy.LimitValue, item.window.RequestCount),
					}
				}
				return err
			}
		case MetricTokensEstimated:
			if _, err := store.IncrementWindowReserved(ctx, WindowReserve{
				TenantID:          req.TenantID,
				WindowID:          item.window.ID,
				ReserveDelta:      item.amount,
				RequestCountDelta: 0,
				LimitValue:        item.policy.LimitValue,
			}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					decision := exceededDecision(req, item.policy, policyAssessment{
						amount:     item.amount,
						limit:      item.policy.LimitValue,
						retryAfter: retryAfter(req.At, item.policy),
					})
					return &rollbackDenyError{
						deny:    denyErr(decision, ErrDenied),
						payload: assessmentPayload(item.policy, item.window.ReservedValue.Add(item.window.SettledValue), item.amount, item.policy.LimitValue, item.window.RequestCount),
					}
				}
				return err
			}
		case MetricConcurrency:
			slot, err := store.AcquireConcurrencySlot(ctx, ConcurrencyAcquire{
				TenantID:       req.TenantID,
				ReservationID:  reservation.ID,
				ClaimID:        req.ClaimID,
				Scope:          item.policy.Scope,
				SlotLimit:      item.policy.LimitValue.IntPart(),
				At:             req.At,
				LeaseExpiresAt: req.LeaseExpiresAt,
			})
			if err != nil {
				return err
			}
			if slot.ID == 0 {
				decision := exceededDecision(req, item.policy, policyAssessment{
					amount:     decimal.NewFromInt(1),
					limit:      item.policy.LimitValue,
					retryAfter: retryAfter(req.At, item.policy),
				})
				return &rollbackDenyError{
					deny:    denyErr(decision, ErrDenied),
					payload: concurrencySaturatedPayload(item.policy, decimal.NewFromInt(1), item.policy.LimitValue),
				}
			}
		}
	}
	return nil
}

type reservationClaimConflictError struct {
	cause error
}

func (e *reservationClaimConflictError) Error() string {
	if e == nil || e.cause == nil {
		return "quota reservation claim conflict"
	}
	return "quota reservation claim conflict: " + e.cause.Error()
}

func (e *reservationClaimConflictError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type rollbackDenyError struct {
	deny    *DenyError
	payload []byte
}

func (e *rollbackDenyError) Error() string {
	if e == nil || e.deny == nil {
		return ErrDenied.Error()
	}
	return e.deny.Error()
}

func normalizeReserveRequest(req ReserveRequest) ReserveRequest {
	if req.At.IsZero() {
		req.At = time.Now().UTC()
	} else {
		req.At = req.At.UTC()
	}
	req.LeaseExpiresAt = req.LeaseExpiresAt.UTC()
	if req.LeaseExpiresAt.IsZero() {
		req.LeaseExpiresAt = req.At.Add(5 * time.Minute)
	}
	if req.Scopes == nil {
		req.Scopes = []Scope{}
	}
	req.Scopes = normalizeScopesForResolution(req.TenantID, req.Scopes)
	return req
}

func normalizeScopesForResolution(tenantID int64, scopes []Scope) []Scope {
	normalized := normalizeScopes(tenantID, scopes)
	for _, scope := range normalized {
		if scope.Kind == ScopeGlobal && scope.ID == "*" {
			return normalized
		}
	}
	out := make([]Scope, 0, len(normalized)+1)
	out = append(out, Scope{TenantID: tenantID, Kind: ScopeGlobal, ID: "*"})
	out = append(out, normalized...)
	return out
}

func reserveMetrics(req ReserveRequest) []Metric {
	metrics := []Metric{MetricRequests, MetricCostUSD, MetricTokensEstimated}
	if req.NeedConcurrencySlot {
		metrics = append(metrics, MetricConcurrency)
	}
	return metrics
}

func serviceWindowForPolicy(policy Policy, at time.Time) (Window, error) {
	switch policy.Window.Kind {
	case WindowNone:
		start := policy.ValidFrom.UTC()
		if start.IsZero() {
			start = manualWindowStart
		}
		return Window{
			Kind:    policy.Window.Kind,
			Seconds: policy.Window.Seconds,
			Start:   start,
			End:     manualWindowEnd,
		}, nil
	case WindowManual:
		start := policy.ValidFrom.UTC()
		if start.IsZero() {
			start = manualWindowStart
		}
		return Window{
			Kind:    policy.Window.Kind,
			Seconds: policy.Window.Seconds,
			Start:   start,
			End:     manualWindowEnd,
		}, nil
	default:
		start, end, ok := ComputeWindow(policy.Window.Kind, policy.Window.Seconds, at)
		if !ok {
			return Window{}, fmt.Errorf("quota: invalid policy window kind=%s seconds=%d policy=%d", policy.Window.Kind, policy.Window.Seconds, policy.ID)
		}
		return Window{
			Kind:    policy.Window.Kind,
			Seconds: policy.Window.Seconds,
			Start:   start,
			End:     end,
		}, nil
	}
}

func exceededDecision(req ReserveRequest, policy Policy, assessment policyAssessment) Decision {
	return Decision{
		Kind:       DecisionDeny,
		Code:       decisionCodeLimitExceeded,
		Reason:     fmt.Sprintf("quota %s limit exceeded", policy.Metric),
		RetryAfter: assessment.retryAfter,
		Scope:      policy.Scope,
		Metric:     policy.Metric,
		Amount:     assessment.amount,
		// 透出命中的窗口种类,供拒绝响应让客户端区分日/周/月额超限。policy 已在手,直接取其窗口。
		WindowKind: policy.Window.Kind,
	}
}

func existingReservationResult(req ReserveRequest, existing Reservation) (ReserveResult, *DenyError) {
	if conflict := reservationReplayConflict(req, existing); conflict != nil {
		return ReserveResult{
			Decision:    conflict.Decision,
			Reservation: existing,
		}, conflict
	}
	switch {
	case existing.Status == ReservationReserved && existing.LeaseExpiresAt.After(req.At):
		return ReserveResult{
			Allowed:        true,
			IdempotencyHit: true,
			Decision: Decision{
				Kind:   DecisionAllow,
				Code:   decisionCodeReused,
				Reason: "reservation already exists for claim",
			},
			Reservation: existing,
		}, nil
	case existing.Status == ReservationSettled:
		return ReserveResult{
			Allowed:        true,
			IdempotencyHit: true,
			Decision: Decision{
				Kind:   DecisionAllow,
				Code:   decisionCodeSettledReplay,
				Reason: "reservation already settled for claim",
			},
			Reservation: existing,
		}, nil
	default:
		decision := inactiveReservationDecision(existing)
		return ReserveResult{
			Decision:       decision,
			Reservation:    existing,
			IdempotencyHit: true,
		}, denyErr(decision, ErrDenied)
	}
}

func reservationReplayConflict(req ReserveRequest, existing Reservation) *DenyError {
	if strings.TrimSpace(existing.RequestFingerprint) != strings.TrimSpace(req.RequestFingerprint) {
		return reservationReplayConflictError()
	}
	if !existing.PredictedCost.Equal(req.PredictedCost) {
		return reservationReplayConflictError()
	}
	if !sameScopes(existing.Scopes, req.Scopes, req.TenantID) {
		return reservationReplayConflictError()
	}
	return nil
}

func reservationReplayConflictError() *DenyError {
	decision := Decision{
		Kind:   DecisionDeny,
		Code:   decisionCodeReplayConflict,
		Reason: "reservation replay identity does not match original claim",
	}
	return denyErr(decision, ErrReservationReplayConflict)
}

func sameScopes(left, right []Scope, tenantID int64) bool {
	left = normalizeScopesForResolution(tenantID, left)
	right = normalizeScopesForResolution(tenantID, right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].TenantID != right[i].TenantID || left[i].Kind != right[i].Kind || left[i].ID != right[i].ID {
			return false
		}
	}
	return true
}

func canReactivateReservation(status ReservationStatus) bool {
	return status == ReservationReleased || status == ReservationExpired
}

func reactivateExistingReservation(ctx context.Context, store PGStore, req ReserveRequest, existing Reservation) (ReserveResult, *DenyError, error) {
	resolved, err := ResolvePolicies(ctx, store, req.TenantID, req.Scopes, reserveMetrics(req), req.At)
	if err != nil {
		decision := failClosedDecision(req, err)
		return denyResult(decision), denyErr(decision, err), nil
	}

	evaluated, err := evaluatePolicies(ctx, store, req, resolved)
	if err != nil {
		return ReserveResult{}, nil, err
	}
	resID := existing.ID
	if evaluated.deny != nil {
		if err := insertDecisionAudit(ctx, store, req, &resID, "reserve_denied", evaluated.deny.Decision, evaluated.denyPayload); err != nil {
			return ReserveResult{}, nil, err
		}
		return ReserveResult{
			Decision:       evaluated.deny.Decision,
			Reservation:    existing,
			IdempotencyHit: true,
		}, evaluated.deny, nil
	}

	for _, observation := range evaluated.observations {
		if err := insertDecisionAudit(ctx, store, req, &resID, "observe_exceeded", observation.decision, observation.payload); err != nil {
			return ReserveResult{}, nil, err
		}
	}

	reservation, err := store.ReactivateReservation(ctx, ReservationReactivate{
		TenantID:           req.TenantID,
		ReservationID:      existing.ID,
		ClaimID:            req.ClaimID,
		RequestFingerprint: req.RequestFingerprint,
		Scopes:             req.Scopes,
		PolicySnapshot:     marshalReservationPolicySnapshot(resolved.Ordered, evaluated.enforceWindows),
		PredictedCost:      req.PredictedCost,
		ReservedUnits:      decimal.NewFromInt(1),
		LeaseExpiresAt:     req.LeaseExpiresAt,
	})
	if err != nil {
		return ReserveResult{}, nil, err
	}

	if err := applyEnforceReservations(ctx, store, req, reservation, evaluated.enforceWindows); err != nil {
		if rollback, ok := err.(*rollbackDenyError); ok {
			return denyResult(rollback.deny.Decision), rollback.deny, err
		}
		return ReserveResult{}, nil, err
	}

	decision := Decision{
		Kind:   DecisionAllow,
		Code:   decisionCodeAllowed,
		Reason: "all enforce quota policies allowed",
	}
	if err := insertDecisionAudit(ctx, store, req, &resID, "reserve_allowed", decision, nil); err != nil {
		return ReserveResult{}, nil, err
	}
	return ReserveResult{
		Allowed:        true,
		Decision:       decision,
		Reservation:    reservation,
		IdempotencyHit: true,
	}, nil, nil
}

func inactiveReservationDecision(existing Reservation) Decision {
	status := strings.TrimSpace(string(existing.Status))
	if status == "" {
		status = "unknown"
	}
	return Decision{
		Kind:   DecisionDeny,
		Code:   decisionCodeInactiveClaim,
		Reason: fmt.Sprintf("reservation status %s is not active for claim", status),
	}
}

func (s *Service) reuseReservationAfterClaimRace(ctx context.Context, req ReserveRequest, cause error) (ReserveResult, error) {
	var result ReserveResult
	var deny *DenyError
	err := s.withStore(ctx, func(tx PGStore) error {
		existing, err := tx.GetReservationByClaimForUpdate(ctx, req.TenantID, req.ClaimID)
		if err != nil {
			return err
		}
		result, deny = existingReservationResult(req, existing)
		return nil
	})
	if err != nil {
		decision := failClosedDecision(req, fmt.Errorf("quota reservation claim race retry failed: %w", cause))
		result = denyResult(decision)
		return result, denyErr(decision, err)
	}
	if deny != nil {
		return result, deny
	}
	return result, nil
}

func isReservationClaimRetry(err error) bool {
	if err == nil {
		return false
	}
	var conflict *reservationClaimConflictError
	return errors.As(err, &conflict)
}

func isQuotaReservationClaimUnique(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	if pgErr.ConstraintName == "uq_quota_reservations_tenant_claim" {
		return true
	}
	return pgErr.ConstraintName == "" && strings.Contains(pgErr.Message, "uq_quota_reservations_tenant_claim")
}

func isPgRetryableTxConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}

func sleepBeforeReserveRetry(ctx context.Context, attempt int) error {
	delay := reserveRetryBackoff(attempt)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func reserveRetryBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	var jitter time.Duration
	if reserveTxRetryMaxJitter > 0 {
		jitter = time.Duration(time.Now().UnixNano() % int64(reserveTxRetryMaxJitter))
	}
	return time.Duration(attempt+1)*reserveTxRetryBaseBackoff + jitter
}

func failClosedDecision(req ReserveRequest, cause error) Decision {
	reason := "quota reserve failed closed"
	if cause != nil {
		reason = reason + ": " + cause.Error()
	}
	return Decision{
		Kind:   DecisionDeny,
		Code:   decisionCodeFailClosed,
		Reason: reason,
	}
}

func denyResult(decision Decision) ReserveResult {
	return ReserveResult{Decision: decision}
}

func denyErr(decision Decision, cause error) *DenyError {
	if decision.Kind == "" {
		decision.Kind = DecisionDeny
	}
	return &DenyError{Decision: decision, Cause: cause}
}

func retryAfter(at time.Time, policy Policy) time.Duration {
	if policy.Window.Kind == WindowNone || policy.Window.Kind == WindowManual || policy.Window.End.IsZero() || policy.Window.End.Equal(manualWindowEnd) {
		return 0
	}
	d := policy.Window.End.Sub(at.UTC())
	if d < 0 {
		return 0
	}
	return d
}

func insertDecisionAudit(ctx context.Context, store PGStore, req ReserveRequest, reservationID *int64, eventType string, decision Decision, payload []byte) error {
	retryAfterSeconds := retryAfterSecondsPtr(decision.RetryAfter)
	claimID := req.ClaimID
	scope := decision.Scope
	if scope.Kind == "" {
		scope = Scope{TenantID: req.TenantID, Kind: ScopeGlobal, ID: "*"}
	}
	metric := decision.Metric
	if metric == "" {
		metric = MetricRequests
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	_, err := store.InsertAuditEvent(ctx, AuditEvent{
		TenantID:          req.TenantID,
		ReservationID:     reservationID,
		ClaimID:           &claimID,
		EventType:         eventType,
		DecisionCode:      decision.Code,
		Scope:             scope,
		Metric:            metric,
		AmountReserved:    decision.Amount,
		AmountSettled:     decimal.Zero,
		RetryAfterSeconds: retryAfterSeconds,
		Payload:           payload,
	})
	return err
}

func (s *Service) writeDenyAuditBestEffort(ctx context.Context, req ReserveRequest, decision Decision, payload []byte) error {
	if txStore, ok := s.store.(quotaTxStore); ok {
		return txStore.WithTx(ctx, func(tx PGStore) error {
			return insertDecisionAudit(ctx, tx, req, nil, "reserve_denied", decision, payload)
		})
	}
	return insertDecisionAudit(ctx, s.store, req, nil, "reserve_denied", decision, payload)
}

func retryAfterSecondsPtr(d time.Duration) *int {
	if d <= 0 {
		return nil
	}
	seconds := int(math.Ceil(d.Seconds()))
	if seconds > math.MaxInt32 {
		seconds = math.MaxInt32
	}
	return &seconds
}

func (a policyAssessment) payload(policy Policy) []byte {
	return assessmentPayload(policy, a.current, a.amount, a.limit, a.requestCount)
}

func assessmentPayload(policy Policy, current decimal.Decimal, amount decimal.Decimal, limit decimal.Decimal, requestCount int64) []byte {
	data, err := json.Marshal(map[string]any{
		"policy_id":     policy.ID,
		"scope_kind":    policy.Scope.Kind,
		"scope_id":      normalizeScopeID(policy.Scope.Kind, policy.Scope.ID),
		"metric":        policy.Metric,
		"mode":          policy.Mode,
		"window_kind":   policy.Window.Kind,
		"current":       current.String(),
		"amount":        amount.String(),
		"limit":         limit.String(),
		"request_count": requestCount,
	})
	if err != nil {
		return []byte("{}")
	}
	return data
}

func concurrencySaturatedPayload(policy Policy, amount decimal.Decimal, limit decimal.Decimal) []byte {
	data, err := json.Marshal(map[string]any{
		"policy_id":  policy.ID,
		"scope_kind": policy.Scope.Kind,
		"scope_id":   normalizeScopeID(policy.Scope.Kind, policy.Scope.ID),
		"metric":     policy.Metric,
		"mode":       policy.Mode,
		"reason":     "concurrency_cap_saturated",
		"amount":     amount.String(),
		"limit":      limit.String(),
	})
	if err != nil {
		return []byte("{}")
	}
	return data
}
