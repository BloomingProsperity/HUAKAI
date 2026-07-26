package mediatask

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaenforce"
)

func (s *PostgresStore) createTaskWithUnifiedMoney(ctx context.Context, input CreateTaskInput) (Task, bool, error) {
	if existing, err := scanTask(s.pool.QueryRow(ctx, selectTaskSQL+` WHERE tenant_id=$1 AND request_id=$2`, input.TenantID, input.RequestID)); err == nil {
		if !sameIdempotentTask(existing, input) {
			return Task{}, false, ErrRequestIDConflict
		}
		return existing, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Task{}, false, err
	}

	apiKeyID, err := s.resolveTaskAPIKeyID(ctx, input)
	if err != nil {
		return Task{}, false, err
	}
	version := firstNonEmpty(input.BillingPolicyVersion, s.billingVersion, "mediatask-v1")
	requestClass := firstNonEmpty(input.RequestClass, s.requestClass, "standard")
	fingerprint := payloadHash(input.InputParams)
	predicted := centsToUSD(input.EstimatedCents)
	balanceMode := billing.DefaultBalanceEnforcementMode
	if s.balanceResolver != nil {
		balanceMode = s.balanceResolver.ResolveBalanceEnforcementMode(ctx, input.TenantID)
	}
	reserve, err := s.claimGate.Reserve(ctx, billing.ReserveRequest{
		TenantID: input.TenantID, APIKeyID: apiKeyID, UserID: input.UserID,
		LogicalRequestID: input.RequestID, EndpointFamily: "media_tasks",
		NormalizedPayloadHash: fingerprint, RequestedModel: firstNonEmpty(input.RequestedModel, input.TaskType),
		PoolingGroupID: input.PoolGroupID, BillingPolicyVersion: version,
		RequestClass: requestClass, PredictedCost: predicted,
		BalanceEnforcementMode: balanceMode,
	})
	if errors.Is(err, billing.ErrFingerprintConflict) {
		return Task{}, false, ErrRequestIDConflict
	}
	if err != nil {
		return Task{}, false, err
	}
	if reserve == nil || reserve.ClaimID <= 0 {
		return Task{}, false, ErrStoreNotConfigured
	}
	if reserve.IdempotencyHit {
		existing, lookupErr := scanTask(s.pool.QueryRow(ctx, selectTaskSQL+` WHERE tenant_id=$1 AND request_id=$2`, input.TenantID, input.RequestID))
		if lookupErr != nil {
			return Task{}, false, fmt.Errorf("mediatask: committed claim has no durable task: %w", lookupErr)
		}
		if !sameIdempotentTask(existing, input) {
			return Task{}, false, ErrRequestIDConflict
		}
		return existing, true, nil
	}

	if s.quotaReserver != nil {
		quotaResult, quotaErr := s.quotaReserver.Reserve(ctx, quotaenforce.BuildReserveRequest(quotaenforce.ReserveInput{
			TenantID: input.TenantID, UserID: input.UserID, APIKeyID: apiKeyID,
			ClaimID: reserve.ClaimID, PoolGroupID: input.PoolGroupID,
			RequestFingerprint: fingerprint, RequestedModel: firstNonEmpty(input.RequestedModel, input.TaskType),
			PredictedCost: predicted, At: time.Now().UTC(),
			LeaseExpiresAt: time.Now().UTC().Add(resolveClaimLeaseWindow(input.ClaimLeaseWindow)),
		}))
		if quotaenforce.IsDenied(quotaErr) || (quotaErr == nil && !quotaResult.Allowed) {
			abortErr := s.settler.Abort(ctx, input.TenantID, reserve.ClaimID, "quota_denied", input.RequestID, 0, nil)
			return Task{}, false, errors.Join(ErrQuotaDenied, quotaErr, abortErr)
		}
		// 配额后端基础设施错误沿用同步链的 fail-open 策略；最终结算仍会保留账务事实。
	}

	if s.beforeInsertTask != nil {
		if err := s.beforeInsertTask(); err != nil {
			abortErr := s.settler.Abort(ctx, input.TenantID, reserve.ClaimID, "media_task_insert_failed", input.RequestID, 0, nil)
			return Task{}, false, errors.Join(err, abortErr)
		}
	}
	input.APIKeyID = apiKeyID
	var created Task
	err = s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		existing, err := selectTaskByRequestForUpdate(ctx, tx, input.TenantID, input.RequestID)
		if err == nil {
			if !sameIdempotentTask(existing, input) {
				return ErrRequestIDConflict
			}
			created = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		created, err = scanTask(tx.QueryRow(ctx, insertTaskSQL,
			input.TenantID, input.UserID, apiKeyID, input.TaskType, StatusQueued, input.Provider,
			input.RequestID, input.InputParams, input.EstimatedCents, holdRef(reserve.ClaimID),
			input.ProviderAccountID, input.PoolGroupID, input.ProtocolFamily,
			input.RequestedModel, input.ProviderModelID, input.RouteID,
			input.BindingID, input.BindingRPMLimit, input.BindingTPMLimit, input.BindingMaxParallelRequests,
		))
		return err
	})
	if err != nil {
		abortErr := s.settler.Abort(ctx, input.TenantID, reserve.ClaimID, "media_task_insert_failed", input.RequestID, 0, nil)
		return Task{}, false, errors.Join(err, abortErr)
	}
	return created, false, nil
}

func (s *PostgresStore) resolveTaskAPIKeyID(ctx context.Context, input CreateTaskInput) (int64, error) {
	query := `
	SELECT id
	FROM api_keys
	WHERE tenant_id=$1 AND user_id=$2 AND status='active' AND deleted_at IS NULL
	  AND (expires_at IS NULL OR expires_at > $3)`
	args := []any{input.TenantID, input.UserID, time.Now().UTC()}
	if input.APIKeyID > 0 {
		query += ` AND id=$4`
		args = append(args, input.APIKeyID)
	}
	query += ` ORDER BY id ASC LIMIT 2`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	ids := make([]int64, 0, 2)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, ErrNoActiveAPIKey
	}
	if input.APIKeyID <= 0 && len(ids) > 1 {
		return 0, ErrAPIKeyAmbiguous
	}
	return ids[0], nil
}

func (s *PostgresStore) CompleteSuccess(ctx context.Context, task Task, owner string, result PollResult, now time.Time) (bool, error) {
	if s == nil || s.pool == nil {
		return false, ErrStoreNotConfigured
	}
	if s.settler != nil && isDurablyBoundVideoProvider(task.Provider) {
		return s.completeSuccessWithUnifiedMoney(ctx, task, owner, result, now)
	}
	var settled bool
	err := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		locked, ok, err := lockTerminalCandidate(ctx, tx, task.ID, owner)
		if err != nil || !ok {
			return err
		}
		if result.ActualCents < 0 {
			return fmt.Errorf("%w: actual_cents", ErrInvalidInput)
		}
		// 保守止血:上游真把任务跑成功、但实际成本超过预扣的预估时,不再回滚整事务
		// (旧逻辑返回 ErrActualExceedsEstimate),因为那会让成功任务卡死在 in_progress
		// 反复轮询,直到 TaskTimeout→ExpireTask 把【全额】预扣释放、平台白吃真实上游成本。
		// 改为把【结算金额】clamp 到预扣的预估上限:成功任务正常推进到终态 succeeded,
		// 按预估结算(收费不超过预扣,不超收客户),平台只吸收"超出预估"这部分有界差额。
		// 注:此处仅 clamp 计费金额;media_tasks.actual_cents 仍记录真实上游成本(见
		// updateTaskSuccess),供运维核对平台吸收了多少。超收客户 / 转 failure 的更激进
		// 策略属 money-policy,留待 Owner 拍板,本处只做保守版。
		billedCents := result.ActualCents
		// 下限锚定(bug ② 修复):上游 Poll 未回实际用量时 ActualCents 保持 0(图像/视频等
		// 任务创建型上游普遍只回任务 ID/状态、不回 token 用量),若按 0 结算 = 任务成功却白吃
		// 真实上游成本、等同给客户做了 $0 全额退式结算。此处锚定到预扣的预估额度
		// locked.EstimatedCents(绝不归零;免费模型 EstimatedCents=0 时仍正确结 $0),维持
		// "无可用用量时保持预扣估算、非正差额不动账"的保守口径。
		if billedCents <= 0 {
			billedCents = locked.EstimatedCents
		}
		if billedCents > locked.EstimatedCents {
			billedCents = locked.EstimatedCents
		}
		claimID, err := claimIDFromHoldRef(locked.HoldRef)
		if err != nil {
			return err
		}
		actual := centsToUSD(billedCents)
		qtx := dbbilling.New(tx)
		if rows, err := qtx.UpdateClaimCommitted(ctx, dbbilling.UpdateClaimCommittedParams{
			ID: claimID, ActualCost: decimal.NullDecimal{Decimal: actual, Valid: true}, TenantID: locked.TenantID,
		}); err != nil {
			return err
		} else if rows == 0 {
			var claimStatus string
			if err := tx.QueryRow(ctx, `
SELECT status
FROM billing_ledger_claims
WHERE tenant_id=$1 AND id=$2
FOR UPDATE`, locked.TenantID, claimID).Scan(&claimStatus); err != nil {
				return err
			}
			if claimStatus != "aborted" {
				return billing.ErrClaimNotReserving
			}
			// claim 已被 billing LeaseSweeper 抢先 abort (预扣已释放退回用户)。回滚整事务
			// 会让任务卡 in_progress 每 ~30s 重试死循环且永远结算不了。任务在上游真实跑
			// 成功: 强推终态 succeeded (用户拿到产物), 落孤儿对账线索 (admin Manual-First
			// 追扣/核销), 跳过 claim/billing 写 (sweeper 已写平 abort 账, 再写 committed 是假账)。
			if err := updateTaskSuccess(ctx, tx, locked.ID, result, now); err != nil {
				return err
			}
			if err := persistOrphanTx(ctx, tx, locked, owner, now); err != nil {
				return err
			}
			settled = true
			return nil
		}
		if err := insertBillingEvent(ctx, qtx, locked, claimID, "claim_committed", actual, "stream_end_graceful", billing.StreamStatePartial); err != nil {
			return err
		}
		if _, err := billing.Capture(ctx, tx, claimID, actual); err != nil {
			return err
		}
		if err := insertExternalMediaUsage(ctx, tx, locked, claimID, billedCents, result, now); err != nil {
			return err
		}
		if err := updateTaskSuccess(ctx, tx, locked.ID, result, now); err != nil {
			return err
		}
		settled = true
		return nil
	})
	return settled, err
}

func insertExternalMediaUsage(
	ctx context.Context,
	tx pgx.Tx,
	task Task,
	claimID int64,
	billedCents int64,
	result PollResult,
	now time.Time,
) error {
	usageSource := string(gateway.UsageSourceReported)
	var confidence any
	if result.ActualCents <= 0 {
		usageSource = string(gateway.UsageSourceInferred)
		confidence = 0.5
	}
	requestedAt := task.CreatedAt.UTC()
	if task.CreatedAt.IsZero() {
		requestedAt = now.UTC()
	}
	var attemptSeq int32
	if err := tx.QueryRow(ctx, `
SELECT attempt_seq
FROM billing_ledger_claims
WHERE tenant_id=$1 AND id=$2`, task.TenantID, claimID).Scan(&attemptSeq); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
INSERT INTO usage_records (
    tenant_id, claim_id, api_key_id, user_id, provider_account_id, acquisition_token,
    settlement_source, attempt_seq, actual_cost, end_class, usage_source,
    confidence_score, routing_reason, requested_at, requested_model, upstream_model,
    stream, stream_state
) VALUES (
    $1,$2,$3,$4,NULL,NULL,$5,$6,$7,'non_streaming',$8,$9,
    COALESCE(NULLIF($10::jsonb, 'null'::jsonb), '{}'::jsonb),$11,$12,NULLIF($13,''),false,$14
)`,
		task.TenantID, claimID, task.APIKeyID, task.UserID,
		billing.SettlementSourceExternalMediaRelay, attemptSeq, centsToUSD(billedCents),
		usageSource, confidence, jsonOrNull(result.RoutingReason), requestedAt,
		firstNonEmpty(task.RequestedModel, task.TaskType), firstNonEmpty(task.ProviderModelID, task.Provider),
		billing.StreamStatePartial,
	)
	if err != nil {
		return fmt.Errorf("mediatask: insert external relay usage: %w", err)
	}
	return nil
}

func (s *PostgresStore) CompleteFailure(ctx context.Context, task Task, owner, errorClass string, now time.Time) (bool, error) {
	if s != nil && s.settler != nil && isDurablyBoundVideoProvider(task.Provider) {
		return s.abortTaskWithUnifiedMoney(ctx, task, owner, StatusFailed, firstNonEmpty(errorClass, "provider_failed"), now)
	}
	return s.abortTask(ctx, task.ID, owner, StatusFailed, firstNonEmpty(errorClass, "provider_failed"), now)
}

func (s *PostgresStore) ExpireTask(ctx context.Context, task Task, owner string, now time.Time) (bool, error) {
	if s != nil && s.settler != nil && isDurablyBoundVideoProvider(task.Provider) {
		return s.abortTaskWithUnifiedMoney(ctx, task, owner, StatusExpired, "timeout", now)
	}
	return s.abortTask(ctx, task.ID, owner, StatusExpired, "timeout", now)
}

func (s *PostgresStore) abortTaskWithUnifiedMoney(ctx context.Context, task Task, owner string, status Status, errorClass string, now time.Time) (bool, error) {
	current, err := scanTask(s.pool.QueryRow(ctx, selectTaskSQL+` WHERE id=$1`, task.ID))
	if err != nil {
		return false, err
	}
	if current.LeaseOwner != owner || IsTerminal(current.Status) {
		return false, ErrLeaseLost
	}
	if current.Status == StatusSubmissionUnknown {
		return false, ErrSubmissionRecoveryActionRequired
	}
	if current.Status == StatusSettlementPending {
		return false, ErrSettlementPending
	}
	claimID, err := claimIDFromHoldRef(task.HoldRef)
	if err != nil {
		return false, err
	}
	err = s.settler.Abort(ctx, task.TenantID, claimID, errorClass, task.RequestID, 0, nil)
	if err != nil && !errors.Is(err, billing.ErrClaimNotReserving) {
		return false, err
	}
	claimStatus, _, stateErr := s.claimSettlementState(ctx, task.TenantID, claimID)
	if stateErr != nil {
		return false, stateErr
	}
	if claimStatus == "committed" {
		return false, billing.ErrClaimNotReserving
	}
	var completed bool
	updateErr := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
UPDATE media_tasks
SET status=$2, error_class=$3, lease_owner=NULL, lease_expires_at=NULL,
    updated_at=$4, finished_at=$4
WHERE id=$1 AND lease_owner=$5
  AND status IN ('queued','submitting','submission_releasing','in_progress')`,
			task.ID, status, errorClass, now.UTC(), owner)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrLeaseLost
		}
		if err := finalizeUnknownSubmissionRelease(ctx, tx, task.ID, now); err != nil {
			return err
		}
		completed = true
		return nil
	})
	if updateErr != nil {
		return false, updateErr
	}
	return completed, nil
}

func (s *PostgresStore) insertReservedTask(ctx context.Context, tx pgx.Tx, input CreateTaskInput) (Task, error) {
	apiKeyID := input.APIKeyID
	if apiKeyID > 0 {
		if err := requireActiveAPIKey(ctx, tx, input.TenantID, input.UserID, apiKeyID, time.Now().UTC()); err != nil {
			return Task{}, err
		}
	} else {
		var err error
		apiKeyID, err = activeAPIKeyID(ctx, tx, input.TenantID, input.UserID, time.Now().UTC())
		if err != nil {
			return Task{}, err
		}
	}
	version := firstNonEmpty(input.BillingPolicyVersion, s.billingVersion)
	if version == "" {
		version = "mediatask-v1"
	}
	requestClass := firstNonEmpty(input.RequestClass, s.requestClass, "standard")
	fingerprint := payloadHash(input.InputParams)
	qtx := dbbilling.New(tx)
	claim, err := qtx.InsertClaim(ctx, dbbilling.InsertClaimParams{
		TenantID: input.TenantID, IdempotencyKey: mediaClaimKey(input, apiKeyID, version, requestClass),
		RequestFingerprint: fingerprint, APIKeyID: apiKeyID, UserID: input.UserID,
		LogicalRequestID: input.RequestID, EndpointFamily: "media_tasks", RequestedModel: firstNonEmpty(input.RequestedModel, input.TaskType),
		PoolingGroupID:       nullablePositiveInt64(input.PoolGroupID),
		BillingPolicyVersion: version, RequestClass: requestClass, PredictedCost: centsToUSD(input.EstimatedCents),
		// claim 孤儿回收租约必须 > 媒体任务最大生命周期(TaskTimeout)。原硬编码 90s 远
		// 短于 TaskTimeout(默认 15min),会让跑得久的合法任务 claim 被 billing LeaseSweeper
		// 提前 abort、完成时无法计费致亏钱。改用 resolveClaimLeaseWindow(覆盖任务生命周期)。
		CurrencyCode: "USD", LeaseExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(resolveClaimLeaseWindow(input.ClaimLeaseWindow)), Valid: true},
	})
	if err != nil {
		return Task{}, err
	}
	if _, err := billing.Reserve(ctx, tx, billing.ReserveParams{
		TenantID: input.TenantID, UserID: input.UserID, ClaimID: claim.ID,
		Cost: centsToUSD(input.EstimatedCents), EnforcementMode: s.holdMode(ctx, input.TenantID),
	}); err != nil {
		if errors.Is(err, billing.ErrBalanceHoldInsufficientBalance) {
			return Task{}, billing.ErrInsufficientBalance
		}
		return Task{}, err
	}
	if s.beforeInsertTask != nil {
		if err := s.beforeInsertTask(); err != nil {
			return Task{}, err
		}
	}
	return scanTask(tx.QueryRow(ctx, insertTaskSQL,
		input.TenantID, input.UserID, apiKeyID, input.TaskType, StatusQueued, input.Provider,
		input.RequestID, input.InputParams, input.EstimatedCents, holdRef(claim.ID),
		input.ProviderAccountID, input.PoolGroupID, input.ProtocolFamily,
		input.RequestedModel, input.ProviderModelID, input.RouteID,
		input.BindingID, input.BindingRPMLimit, input.BindingTPMLimit, input.BindingMaxParallelRequests,
	))
}

func requireActiveAPIKey(ctx context.Context, tx pgx.Tx, tenantID, userID, apiKeyID int64, now time.Time) error {
	var found int64
	err := tx.QueryRow(ctx, `
	SELECT id
	FROM api_keys
	WHERE id=$1 AND tenant_id=$2 AND user_id=$3 AND status='active' AND deleted_at IS NULL
	  AND (expires_at IS NULL OR expires_at > $4)
	FOR KEY SHARE`, apiKeyID, tenantID, userID, now.UTC()).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoActiveAPIKey
	}
	return err
}

func nullablePositiveInt64(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func (s *PostgresStore) abortTask(ctx context.Context, taskID int64, owner string, status Status, errorClass string, now time.Time) (bool, error) {
	if s == nil || s.pool == nil {
		return false, ErrStoreNotConfigured
	}
	var completed bool
	err := s.withSerializableRetry(ctx, func(tx pgx.Tx) error {
		locked, ok, err := lockTerminalCandidate(ctx, tx, taskID, owner)
		if err != nil || !ok {
			return err
		}
		if locked.Status == StatusSubmissionUnknown {
			return ErrSubmissionRecoveryActionRequired
		}
		if locked.Status == StatusSettlementPending {
			return ErrSettlementPending
		}
		claimID, err := claimIDFromHoldRef(locked.HoldRef)
		if err != nil {
			return err
		}
		qtx := dbbilling.New(tx)
		reason := errorClass
		if rows, err := qtx.UpdateClaimAbortedWithReason(ctx, dbbilling.UpdateClaimAbortedWithReasonParams{
			ID: claimID, TenantID: locked.TenantID, AbortedReason: &reason,
		}); err != nil {
			return err
		} else if rows == 0 {
			var claimStatus string
			if err := tx.QueryRow(ctx, `
SELECT status
FROM billing_ledger_claims
WHERE tenant_id=$1 AND id=$2
FOR UPDATE`, locked.TenantID, claimID).Scan(&claimStatus); err != nil {
				return err
			}
			if claimStatus != "aborted" {
				return billing.ErrClaimNotReserving
			}
			// claim 已被清扫器抢先 abort，账务已释放。任务必须收敛为终态，
			// 但不得把 committed 等其他 0 行原因冒充成“已退款”。
			if _, err := tx.Exec(ctx, `
		UPDATE media_tasks
		SET status=$2, error_class='claim_swept', lease_owner=NULL, lease_expires_at=NULL,
		    updated_at=$3, finished_at=$3
		WHERE id=$1 AND status IN ('queued','submitting','submission_releasing','in_progress')`,
				locked.ID, status, now.UTC(),
			); err != nil {
				return err
			}
			if err := finalizeUnknownSubmissionRelease(ctx, tx, locked.ID, now); err != nil {
				return err
			}
			if err := persistOrphanTx(ctx, tx, locked, owner, now); err != nil {
				return err
			}
			completed = true
			return nil
		}
		if err := insertBillingEvent(ctx, qtx, locked, claimID, "claim_aborted", decimal.Zero, reason, billing.StreamStateFailed); err != nil {
			return err
		}
		if _, err := billing.Release(ctx, tx, claimID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
		UPDATE media_tasks
		SET status=$2, error_class=$3, lease_owner=NULL, lease_expires_at=NULL,
		    updated_at=$4, finished_at=$4
		WHERE id=$1 AND status IN ('queued','submitting','submission_releasing','in_progress')`,
			locked.ID, status, errorClass, now.UTC(),
		); err != nil {
			return err
		}
		if err := finalizeUnknownSubmissionRelease(ctx, tx, locked.ID, now); err != nil {
			return err
		}
		if err := persistOrphanTx(ctx, tx, locked, owner, now); err != nil {
			return err
		}
		completed = true
		return nil
	})
	return completed, err
}

func (s *PostgresStore) holdMode(ctx context.Context, tenantID int64) billing.EnforcementMode {
	mode := billing.DefaultBalanceEnforcementMode
	if s.balanceResolver != nil {
		mode = s.balanceResolver.ResolveBalanceEnforcementMode(ctx, tenantID)
	}
	if mode == billing.BalanceEnforcementModeOptIn {
		return billing.EnforcementModeOptIn
	}
	return billing.EnforcementModeMandatory
}

func activeAPIKeyID(ctx context.Context, tx pgx.Tx, tenantID, userID int64, now time.Time) (int64, error) {
	rows, err := tx.Query(ctx, `
	SELECT id
	FROM api_keys
	WHERE tenant_id=$1 AND user_id=$2 AND status='active' AND deleted_at IS NULL
	  AND (expires_at IS NULL OR expires_at > $3)
	ORDER BY id ASC
	LIMIT 2
	FOR KEY SHARE`, tenantID, userID, now.UTC())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	ids := make([]int64, 0, 2)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, ErrNoActiveAPIKey
	}
	if len(ids) > 1 {
		return 0, ErrAPIKeyAmbiguous
	}
	return ids[0], nil
}
