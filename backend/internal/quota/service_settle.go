package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

var (
	// ErrReservationNotFound 表示 billing claim 没有对应 quota reservation。
	ErrReservationNotFound = errors.New("quota: reservation not found")
	ErrReservationMismatch = errors.New("quota: reservation identity mismatch")
	ErrInvalidFinalization = errors.New("quota: invalid reservation finalization")
)

const (
	quotaAuditOperationSettleCommitted = "settle_committed"
	quotaAuditOperationReleaseAborted  = "release_aborted"
	quotaAuditOperationCacheHit        = "cache_hit"

	quotaReconcileKindSettleFailed   = "quota_settle_failed"
	quotaReconcileKindReleaseFailed  = "quota_release_failed"
	quotaReconcileKindCacheHitFailed = "quota_cache_hit_failed"
)

type SettleRequest struct {
	TenantID      int64
	ClaimID       int64
	ReservationID int64
	ActualCost    decimal.Decimal
	SettledAt     time.Time
	Actor         *string
}

type SettleResult struct {
	Reservation          Reservation
	IdempotencyHit       bool
	OverageValue         decimal.Decimal
	ReconciliationQueued bool
}

type ReleaseRequest struct {
	TenantID      int64
	ClaimID       int64
	ReservationID int64
	Reason        string
	ReleasedAt    time.Time
	Actor         *string
}

type ReleaseResult struct {
	Reservation          Reservation
	IdempotencyHit       bool
	ReconciliationQueued bool
}

type CacheHitRequest struct {
	TenantID      int64
	ClaimID       int64
	ReservationID int64
	CommittedAt   time.Time
	Actor         *string
	CacheKey      string
	CacheSource   string
}

type CacheHitResult struct {
	Reservation          Reservation
	IdempotencyHit       bool
	ReconciliationQueued bool
}

func (s *Service) Settle(ctx context.Context, req SettleRequest) (SettleResult, error) {
	req = normalizeSettleRequest(req)
	if err := validateSettleRequest(req); err != nil {
		return SettleResult{}, err
	}

	var result SettleResult
	err := s.runQuotaFinalizationWithRetry(ctx, "settle", defaultFinalizationRetryPolicy, func(tx PGStore) error {
		result = SettleResult{}
		reservation, err := getFinalizationReservation(ctx, tx, finalizationReservationInput{
			TenantID:      req.TenantID,
			ClaimID:       req.ClaimID,
			ReservationID: req.ReservationID,
			Operation:     quotaAuditOperationSettleCommitted,
			Actor:         req.Actor,
		})
		if err != nil {
			return err
		}
		result.Reservation = reservation
		switch reservation.Status {
		case ReservationSettled:
			result.IdempotencyHit = true
			return nil
		case ReservationReleased:
			return reconciliationStateError(req.TenantID, req.ClaimID, req.ReservationID, quotaReconcileKindSettleFailed, reservation.Status)
		case ReservationExpired:
			return reconciliationStateError(req.TenantID, req.ClaimID, req.ReservationID, quotaReconcileKindSettleFailed, reservation.Status)
		case ReservationReserved, ReservationReconciliationNeeded:
			// 写操作一律用解析出的 reservation.ID,不用 req.ReservationID:结算调用方
			// (quotaenforce)只按 claim 定位、令 req.ReservationID=0,直接用会 WHERE id=0
			// 命中 0 行、requireAffected 抛裸 no rows,导致 quota 窗口永不结算。
			overage, err := applySettlementWindows(ctx, tx, reservation, req.ActualCost, false)
			if err != nil {
				return err
			}
			if err := tx.ReleaseConcurrencySlots(ctx, req.TenantID, reservation.ID, "settled"); err != nil {
				return err
			}
			if err := tx.SettleReservation(ctx, Settlement{
				TenantID:      req.TenantID,
				ReservationID: reservation.ID,
				ClaimID:       req.ClaimID,
				ActualCost:    req.ActualCost,
				SettledUnits:  reservation.ReservedUnits,
				OverageUnits:  overage,
				SettledAt:     req.SettledAt,
			}); err != nil {
				return err
			}
			if err := insertQuotaFinalizationAudit(ctx, tx, quotaFinalizationAudit{
				Reservation:    reservation,
				Operation:      quotaAuditOperationSettleCommitted,
				EventType:      "settled",
				DecisionCode:   "quota_settle_committed",
				Metric:         MetricCostUSD,
				AmountReserved: reservation.PredictedCost,
				AmountSettled:  req.ActualCost,
				Actor:          req.Actor,
				OverageValue:   overage,
				ExtraPayload: map[string]any{
					"actual_cost":    req.ActualCost.String(),
					"predicted_cost": reservation.PredictedCost.String(),
				},
			}); err != nil {
				return err
			}
			reservation.Status = ReservationSettled
			result.Reservation = reservation
			result.OverageValue = overage
			return nil
		default:
			return invalidReservationStatusError("settle", reservation.Status)
		}
	})
	if err != nil {
		if shouldQueueQuotaFinalizationFailure(err) {
			queued, queueErr := s.enqueueFinalizationReconciliation(ctx, req.TenantID, req.ClaimID, req.ReservationID, quotaReconcileKindSettleFailed, err)
			result.ReconciliationQueued = queued
			if queueErr != nil {
				return result, errors.Join(err, queueErr)
			}
		}
		return result, err
	}
	return result, nil
}

func (s *Service) Release(ctx context.Context, req ReleaseRequest) (ReleaseResult, error) {
	req = normalizeReleaseRequest(req)
	if err := validateReleaseRequest(req); err != nil {
		return ReleaseResult{}, err
	}

	var result ReleaseResult
	err := s.runQuotaFinalizationWithRetry(ctx, "release", releaseFinalizationRetryPolicy, func(tx PGStore) error {
		result = ReleaseResult{}
		reservation, err := getFinalizationReservation(ctx, tx, finalizationReservationInput{
			TenantID:      req.TenantID,
			ClaimID:       req.ClaimID,
			ReservationID: req.ReservationID,
			Operation:     quotaAuditOperationReleaseAborted,
			Actor:         req.Actor,
		})
		if err != nil {
			return err
		}
		result.Reservation = reservation
		switch reservation.Status {
		case ReservationReleased:
			result.IdempotencyHit = true
			return nil
		case ReservationSettled:
			return invalidReservationStatusError("release", reservation.Status)
		case ReservationExpired:
			return reconciliationStateError(req.TenantID, req.ClaimID, req.ReservationID, quotaReconcileKindReleaseFailed, reservation.Status)
		case ReservationReserved, ReservationReconciliationNeeded:
			if err := applyReleaseWindows(ctx, tx, reservation); err != nil {
				return err
			}
			if err := tx.ReleaseConcurrencySlots(ctx, req.TenantID, reservation.ID, req.Reason); err != nil {
				return err
			}
			if err := tx.ReleaseReservation(ctx, ReservationRelease{
				TenantID:      req.TenantID,
				ReservationID: reservation.ID,
				ClaimID:       req.ClaimID,
				Reason:        req.Reason,
			}); err != nil {
				return err
			}
			if err := insertQuotaFinalizationAudit(ctx, tx, quotaFinalizationAudit{
				Reservation:    reservation,
				Operation:      quotaAuditOperationReleaseAborted,
				EventType:      "released",
				DecisionCode:   "quota_release_aborted",
				Metric:         MetricRequests,
				AmountReserved: reservation.ReservedUnits,
				AmountSettled:  decimal.Zero,
				Actor:          req.Actor,
				ExtraPayload: map[string]any{
					"reason": req.Reason,
				},
			}); err != nil {
				return err
			}
			reservation.Status = ReservationReleased
			result.Reservation = reservation
			return nil
		default:
			return invalidReservationStatusError("release", reservation.Status)
		}
	})
	if err != nil {
		if shouldQueueQuotaFinalizationFailure(err) {
			cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), quotaCleanupTimeout)
			queued, queueErr := s.handoffReleaseRecovery(cleanupCtx, req.TenantID, req.ClaimID, req.ReservationID, err)
			cancelCleanup()
			result.ReconciliationQueued = queued
			if queueErr != nil {
				return result, errors.Join(err, queueErr)
			}
		}
		return result, err
	}
	return result, nil
}

func (s *Service) CommitCacheHit(ctx context.Context, req CacheHitRequest) (CacheHitResult, error) {
	req = normalizeCacheHitRequest(req)
	if err := validateCacheHitRequest(req); err != nil {
		return CacheHitResult{}, err
	}

	var result CacheHitResult
	err := s.runQuotaFinalizationWithRetry(ctx, "cache_hit", defaultFinalizationRetryPolicy, func(tx PGStore) error {
		result = CacheHitResult{}
		reservation, err := getFinalizationReservation(ctx, tx, finalizationReservationInput{
			TenantID:      req.TenantID,
			ClaimID:       req.ClaimID,
			ReservationID: req.ReservationID,
			Operation:     quotaAuditOperationCacheHit,
			Actor:         req.Actor,
		})
		if err != nil {
			return err
		}
		result.Reservation = reservation
		switch reservation.Status {
		case ReservationSettled:
			result.IdempotencyHit = true
			return nil
		case ReservationReleased:
			return reconciliationStateError(req.TenantID, req.ClaimID, req.ReservationID, quotaReconcileKindCacheHitFailed, reservation.Status)
		case ReservationExpired:
			return reconciliationStateError(req.TenantID, req.ClaimID, req.ReservationID, quotaReconcileKindCacheHitFailed, reservation.Status)
		case ReservationReserved, ReservationReconciliationNeeded:
			if _, err := applySettlementWindows(ctx, tx, reservation, decimal.Zero, true); err != nil {
				return err
			}
			if err := tx.ReleaseConcurrencySlots(ctx, req.TenantID, reservation.ID, "cache_hit"); err != nil {
				return err
			}
			if err := tx.SettleReservation(ctx, Settlement{
				TenantID:      req.TenantID,
				ReservationID: reservation.ID,
				ClaimID:       req.ClaimID,
				ActualCost:    decimal.Zero,
				SettledUnits:  reservation.ReservedUnits,
				OverageUnits:  decimal.Zero,
				SettledAt:     req.CommittedAt,
			}); err != nil {
				return err
			}
			extra := map[string]any{}
			if req.CacheKey != "" {
				extra["cache_key"] = req.CacheKey
			}
			if req.CacheSource != "" {
				extra["cache_source"] = req.CacheSource
			}
			if err := insertQuotaFinalizationAudit(ctx, tx, quotaFinalizationAudit{
				Reservation:    reservation,
				Operation:      quotaAuditOperationCacheHit,
				EventType:      "settled",
				DecisionCode:   "quota_cache_hit",
				Metric:         MetricRequests,
				AmountReserved: reservation.ReservedUnits,
				AmountSettled:  reservation.ReservedUnits,
				Actor:          req.Actor,
				ExtraPayload:   extra,
			}); err != nil {
				return err
			}
			reservation.Status = ReservationSettled
			result.Reservation = reservation
			return nil
		default:
			return invalidReservationStatusError("cache_hit", reservation.Status)
		}
	})
	if err != nil {
		if shouldQueueQuotaFinalizationFailure(err) {
			queued, queueErr := s.enqueueFinalizationReconciliation(ctx, req.TenantID, req.ClaimID, req.ReservationID, quotaReconcileKindCacheHitFailed, err)
			result.ReconciliationQueued = queued
			if queueErr != nil {
				return result, errors.Join(err, queueErr)
			}
		}
		return result, err
	}
	return result, nil
}

type finalizationReservationInput struct {
	TenantID      int64
	ClaimID       int64
	ReservationID int64
	Operation     string
	Actor         *string
}

func getFinalizationReservation(ctx context.Context, store PGStore, input finalizationReservationInput) (Reservation, error) {
	reservation, err := store.GetReservationByClaimForUpdate(ctx, input.TenantID, input.ClaimID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, &reservationNotFoundError{tenantID: input.TenantID, claimID: input.ClaimID, reservationID: input.ReservationID}
	}
	if err != nil {
		return Reservation{}, err
	}
	if input.ReservationID != 0 && reservation.ID != input.ReservationID {
		auditErr := insertQuotaFinalizationAudit(ctx, store, quotaFinalizationAudit{
			Reservation:  reservation,
			Operation:    "reservation_mismatch",
			EventType:    "reconciliation_enqueued",
			DecisionCode: "quota_reservation_mismatch",
			Metric:       MetricRequests,
			Actor:        input.Actor,
			ExtraPayload: map[string]any{
				"requested_operation":  input.Operation,
				"expected_reservation": input.ReservationID,
				"actual_reservation":   reservation.ID,
				"status":               reservation.Status,
			},
		})
		if auditErr != nil {
			return Reservation{}, auditErr
		}
		return Reservation{}, &reservationMismatchError{
			tenantID:     input.TenantID,
			claimID:      input.ClaimID,
			expectedID:   input.ReservationID,
			actualID:     reservation.ID,
			operation:    input.Operation,
			actualStatus: reservation.Status,
		}
	}
	return reservation, nil
}

func applySettlementWindows(ctx context.Context, store PGStore, reservation Reservation, actualCost decimal.Decimal, cacheHit bool) (decimal.Decimal, error) {
	policies, err := snapshotFinalizationPolicies(reservation)
	if err != nil {
		return decimal.Zero, err
	}
	totalOverage := decimal.Zero
	for _, policy := range policies {
		counter, err := snapshotWindowForUpdate(ctx, store, reservation.TenantID, policy)
		if err != nil {
			return decimal.Zero, err
		}
		switch policy.Metric {
		case MetricRequests:
			if _, err := store.ApplyWindowSettlement(ctx, WindowSettlement{
				TenantID:             reservation.TenantID,
				WindowID:             counter.ID,
				ReservedReleaseValue: policy.ReservedAmount,
				SettledAddValue:      decimal.NewFromInt(1),
				OverageAddValue:      decimal.Zero,
			}); err != nil {
				return decimal.Zero, err
			}
		case MetricCostUSD:
			settledAdd := actualCost
			overageAdd := decimal.Zero
			if cacheHit {
				settledAdd = decimal.Zero
			} else {
				overageAdd = marginalBudgetOverage(counter, policy.Policy, reservation.PredictedCost, actualCost)
			}
			if _, err := store.ApplyWindowSettlement(ctx, WindowSettlement{
				TenantID:             reservation.TenantID,
				WindowID:             counter.ID,
				ReservedReleaseValue: policy.ReservedAmount,
				SettledAddValue:      settledAdd,
				OverageAddValue:      overageAdd,
			}); err != nil {
				return decimal.Zero, err
			}
			totalOverage = totalOverage.Add(overageAdd)
		case MetricTokensEstimated:
			if _, err := store.ApplyWindowSettlement(ctx, WindowSettlement{
				TenantID:             reservation.TenantID,
				WindowID:             counter.ID,
				ReservedReleaseValue: policy.ReservedAmount,
				SettledAddValue:      policy.ReservedAmount,
				OverageAddValue:      decimal.Zero,
			}); err != nil {
				return decimal.Zero, err
			}
		}
	}
	return totalOverage, nil
}

func applyReleaseWindows(ctx context.Context, store PGStore, reservation Reservation) error {
	policies, err := snapshotFinalizationPolicies(reservation)
	if err != nil {
		return err
	}
	for _, policy := range policies {
		counter, err := snapshotWindowForUpdate(ctx, store, reservation.TenantID, policy)
		if err != nil {
			return err
		}
		releaseValue := decimal.Zero
		switch policy.Metric {
		case MetricRequests:
			releaseValue = policy.ReservedAmount
		case MetricCostUSD:
			releaseValue = policy.ReservedAmount
		case MetricTokensEstimated:
			releaseValue = policy.ReservedAmount
		default:
			continue
		}
		if _, err := store.ApplyWindowSettlement(ctx, WindowSettlement{
			TenantID:             reservation.TenantID,
			WindowID:             counter.ID,
			ReservedReleaseValue: releaseValue,
			SettledAddValue:      decimal.Zero,
			OverageAddValue:      decimal.Zero,
		}); err != nil {
			return err
		}
	}
	return nil
}

type snapshotFinalizationPolicy struct {
	Policy
	ReservedAmount decimal.Decimal
}

func snapshotFinalizationPolicies(reservation Reservation) ([]snapshotFinalizationPolicy, error) {
	var records []policySnapshotRecord
	if len(reservation.PolicySnapshot) == 0 {
		return nil, fmt.Errorf("quota: reservation %d policy snapshot is missing", reservation.ID)
	}
	if err := json.Unmarshal(reservation.PolicySnapshot, &records); err != nil {
		return nil, fmt.Errorf("quota: reservation %d policy snapshot is invalid: %w", reservation.ID, err)
	}

	policies := make([]snapshotFinalizationPolicy, 0, len(records))
	for _, record := range records {
		if Mode(record.Mode) != ModeEnforce {
			continue
		}
		metric := Metric(record.Metric)
		if !metricHasWindowReservation(metric) {
			continue
		}
		window, err := snapshotRecordWindow(reservation.ID, record)
		if err != nil {
			return nil, err
		}
		limit, err := decimal.NewFromString(record.LimitValue)
		if err != nil {
			return nil, fmt.Errorf("quota: reservation %d policy %d snapshot limit is invalid: %w", reservation.ID, record.ID, err)
		}
		reservedAmount, err := snapshotReservedAmount(reservation, record, metric)
		if err != nil {
			return nil, err
		}
		policies = append(policies, snapshotFinalizationPolicy{
			ReservedAmount: reservedAmount,
			Policy: Policy{
				TenantID:   reservation.TenantID,
				ID:         record.ID,
				Scope:      Scope{TenantID: reservation.TenantID, Kind: ScopeKind(record.ScopeKind), ID: normalizeScopeID(ScopeKind(record.ScopeKind), record.ScopeID)},
				Metric:     metric,
				Window:     window,
				LimitValue: limit,
				Mode:       Mode(record.Mode),
			}})
	}
	return policies, nil
}

func snapshotReservedAmount(reservation Reservation, record policySnapshotRecord, metric Metric) (decimal.Decimal, error) {
	if strings.TrimSpace(record.ReservedAmount) != "" {
		amount, err := decimal.NewFromString(record.ReservedAmount)
		if err != nil {
			return decimal.Zero, fmt.Errorf("quota: reservation %d policy %d snapshot reserved_amount is invalid: %w", reservation.ID, record.ID, err)
		}
		if amount.IsNegative() {
			return decimal.Zero, fmt.Errorf("quota: reservation %d policy %d snapshot reserved_amount must be non-negative", reservation.ID, record.ID)
		}
		return amount, nil
	}
	switch metric {
	case MetricCostUSD:
		return reservation.PredictedCost, nil
	default:
		return reservation.ReservedUnits, nil
	}
}

func snapshotRecordWindow(reservationID int64, record policySnapshotRecord) (Window, error) {
	if strings.TrimSpace(record.WindowStart) == "" || strings.TrimSpace(record.WindowEnd) == "" {
		return Window{}, fmt.Errorf("quota: reservation %d policy %d snapshot is missing concrete window for %s", reservationID, record.ID, record.Metric)
	}
	start, err := time.Parse(time.RFC3339Nano, record.WindowStart)
	if err != nil {
		return Window{}, fmt.Errorf("quota: reservation %d policy %d snapshot window_start is invalid: %w", reservationID, record.ID, err)
	}
	end, err := time.Parse(time.RFC3339Nano, record.WindowEnd)
	if err != nil {
		return Window{}, fmt.Errorf("quota: reservation %d policy %d snapshot window_end is invalid: %w", reservationID, record.ID, err)
	}
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) {
		return Window{}, fmt.Errorf("quota: reservation %d policy %d snapshot window_end must be after window_start", reservationID, record.ID)
	}
	return Window{
		Kind:  WindowKind(record.WindowKind),
		Start: start,
		End:   end,
	}, nil
}

func snapshotWindowForUpdate(ctx context.Context, store PGStore, tenantID int64, policy snapshotFinalizationPolicy) (WindowCounter, error) {
	upserted, err := store.UpsertWindow(ctx, WindowUpsert{
		TenantID: tenantID,
		PolicyID: policy.ID,
		Window:   policy.Window,
	})
	if err != nil {
		return WindowCounter{}, err
	}
	counter, err := store.GetWindowForUpdate(ctx, tenantID, upserted.ID)
	if err != nil {
		return WindowCounter{}, err
	}
	counter.Window = policy.Window
	return counter, nil
}

func marginalBudgetOverage(counter WindowCounter, policy Policy, predictedCost decimal.Decimal, actualCost decimal.Decimal) decimal.Decimal {
	committedBefore := counter.ReservedValue.Add(counter.SettledValue)
	committedAfter := committedBefore.Sub(predictedCost).Add(actualCost)
	beforeOverage := positiveDecimal(committedBefore.Sub(policy.LimitValue))
	afterOverage := positiveDecimal(committedAfter.Sub(policy.LimitValue))
	return positiveDecimal(afterOverage.Sub(beforeOverage))
}

func positiveDecimal(value decimal.Decimal) decimal.Decimal {
	if value.LessThan(decimal.Zero) {
		return decimal.Zero
	}
	return value
}

type quotaFinalizationAudit struct {
	Reservation    Reservation
	Operation      string
	EventType      string
	DecisionCode   string
	Metric         Metric
	AmountReserved decimal.Decimal
	AmountSettled  decimal.Decimal
	OverageValue   decimal.Decimal
	Actor          *string
	ExtraPayload   map[string]any
}

func insertQuotaFinalizationAudit(ctx context.Context, store PGStore, audit quotaFinalizationAudit) error {
	payload := make(map[string]any, len(audit.ExtraPayload)+7)
	payload["operation"] = audit.Operation
	payload["reservation_status"] = audit.Reservation.Status
	payload["predicted_cost"] = audit.Reservation.PredictedCost.String()
	payload["reserved_units"] = audit.Reservation.ReservedUnits.String()
	if !audit.OverageValue.IsZero() {
		payload["overage"] = audit.OverageValue.String()
	}
	for key, value := range audit.ExtraPayload {
		payload[key] = value
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	claimID := audit.Reservation.ClaimID
	reservationID := audit.Reservation.ID
	scope := Scope{TenantID: audit.Reservation.TenantID, Kind: ScopeGlobal, ID: "*"}
	metric := audit.Metric
	if metric == "" {
		metric = MetricRequests
	}
	_, err = store.InsertAuditEvent(ctx, AuditEvent{
		TenantID:       audit.Reservation.TenantID,
		ReservationID:  &reservationID,
		ClaimID:        &claimID,
		EventType:      audit.EventType,
		DecisionCode:   audit.DecisionCode,
		Scope:          scope,
		Metric:         metric,
		AmountReserved: audit.AmountReserved,
		AmountSettled:  audit.AmountSettled,
		Payload:        raw,
		Actor:          audit.Actor,
	})
	return err
}

func normalizeSettleRequest(req SettleRequest) SettleRequest {
	if req.SettledAt.IsZero() {
		req.SettledAt = time.Now().UTC()
	} else {
		req.SettledAt = req.SettledAt.UTC()
	}
	return req
}

func normalizeReleaseRequest(req ReleaseRequest) ReleaseRequest {
	if req.ReleasedAt.IsZero() {
		req.ReleasedAt = time.Now().UTC()
	} else {
		req.ReleasedAt = req.ReleasedAt.UTC()
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		req.Reason = "abort"
	}
	return req
}

func normalizeCacheHitRequest(req CacheHitRequest) CacheHitRequest {
	if req.CommittedAt.IsZero() {
		req.CommittedAt = time.Now().UTC()
	} else {
		req.CommittedAt = req.CommittedAt.UTC()
	}
	req.CacheKey = strings.TrimSpace(req.CacheKey)
	req.CacheSource = strings.TrimSpace(req.CacheSource)
	return req
}

func validateSettleRequest(req SettleRequest) error {
	if err := validateFinalizationIDs(req.TenantID, req.ClaimID, req.ReservationID); err != nil {
		return err
	}
	if req.ActualCost.LessThan(decimal.Zero) {
		return fmt.Errorf("%w: actual cost must be non-negative", ErrInvalidFinalization)
	}
	return nil
}

func validateReleaseRequest(req ReleaseRequest) error {
	if err := validateFinalizationIDs(req.TenantID, req.ClaimID, req.ReservationID); err != nil {
		return err
	}
	switch req.Reason {
	case "abort", "upstream_error", "caller_cancelled", "pre_billing_failure":
		return nil
	default:
		return fmt.Errorf("%w: unsupported release reason %q", ErrInvalidFinalization, req.Reason)
	}
}

func validateCacheHitRequest(req CacheHitRequest) error {
	return validateFinalizationIDs(req.TenantID, req.ClaimID, req.ReservationID)
}

func validateFinalizationIDs(tenantID int64, claimID int64, reservationID int64) error {
	if tenantID <= 0 || claimID <= 0 {
		return fmt.Errorf("%w: tenant_id and claim_id are required", ErrInvalidFinalization)
	}
	if reservationID < 0 {
		return fmt.Errorf("%w: reservation_id must be non-negative", ErrInvalidFinalization)
	}
	return nil
}

func shouldQueueQuotaFinalizationFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrReservationNotFound) || errors.Is(err, ErrReservationMismatch) {
		return false
	}
	var invalid *invalidReservationStatus
	if errors.As(err, &invalid) {
		return false
	}
	return true
}

func reconciliationStateError(tenantID int64, claimID int64, reservationID int64, kind string, status ReservationStatus) error {
	return &ReconciliationNeededError{
		TenantID:      tenantID,
		ClaimID:       claimID,
		ReservationID: reservationID,
		Kind:          kind,
		Cause:         fmt.Errorf("quota reservation status %s requires reconciliation", status),
	}
}

type reservationNotFoundError struct {
	tenantID      int64
	claimID       int64
	reservationID int64
}

func (e *reservationNotFoundError) Error() string {
	return fmt.Sprintf("quota: reservation not found tenant=%d claim=%d reservation=%d", e.tenantID, e.claimID, e.reservationID)
}

func (e *reservationNotFoundError) Unwrap() error {
	return ErrReservationNotFound
}

func (e *reservationNotFoundError) Is(target error) bool {
	return target == ErrReservationNotFound
}

type reservationMismatchError struct {
	tenantID     int64
	claimID      int64
	expectedID   int64
	actualID     int64
	operation    string
	actualStatus ReservationStatus
}

func (e *reservationMismatchError) Error() string {
	return fmt.Sprintf("quota: reservation identity mismatch tenant=%d claim=%d expected=%d actual=%d operation=%s status=%s",
		e.tenantID, e.claimID, e.expectedID, e.actualID, e.operation, e.actualStatus)
}

func (e *reservationMismatchError) Unwrap() error {
	return ErrReservationMismatch
}

func (e *reservationMismatchError) Is(target error) bool {
	return target == ErrReservationMismatch
}

type invalidReservationStatus struct {
	operation string
	status    ReservationStatus
}

func invalidReservationStatusError(operation string, status ReservationStatus) error {
	return &invalidReservationStatus{operation: operation, status: status}
}

func (e *invalidReservationStatus) Error() string {
	return fmt.Sprintf("quota: invalid %s for reservation status %s", e.operation, e.status)
}

func (e *invalidReservationStatus) Unwrap() error {
	return ErrInvalidFinalization
}
