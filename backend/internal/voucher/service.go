package voucher

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	store   Store
	audit   AuditSink
	limiter BurstLimiter
}

const supportedVoucherBalanceCurrency = "USD"

type Option func(*Service)

func WithAuditSink(sink AuditSink) Option {
	return func(s *Service) { s.audit = sink }
}

func WithBurstLimiter(limiter BurstLimiter) Option {
	return func(s *Service) { s.limiter = limiter }
}

func NewService(store Store, opts ...Option) *Service {
	s := &Service{
		store:   store,
		audit:   NoopAuditSink{},
		limiter: NewMemoryBurstLimiter(DefaultBurstPolicy()),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.audit == nil {
		s.audit = NoopAuditSink{}
	}
	return s
}

func (s *Service) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	if s == nil || s.store == nil {
		return CreateResult{}, ErrStoreNotConfigured
	}
	input = normalizeCreateInput(input)
	if err := validateCreateInput(input); err != nil {
		return CreateResult{}, err
	}
	code := NormalizeCode(input.Code)
	if code == "" {
		generated, err := GenerateCode()
		if err != nil {
			return CreateResult{}, fmt.Errorf("voucher: generate code: %w", err)
		}
		code = NormalizeCode(generated)
	}
	hash, fp := CodeHash(input.TenantID, code)
	v, err := s.store.CreateVoucher(ctx, createVoucherRecord{
		TenantID: input.TenantID, AdminID: input.AdminID, CodeHash: hash, CodeFingerprint: fp,
		AmountCents: input.AmountCents, CurrencyCode: input.CurrencyCode,
		ValidFrom: input.ValidFrom, ValidUntil: input.ValidUntil,
		MaxRedemptions: input.MaxRedemptions, SingleUsePerUser: input.SingleUsePerUser,
		EligibleUserID:     input.EligibleUserID,
		GrantKind:          input.GrantKind,
		SubscriptionPlanID: input.SubscriptionPlanID,
		Now:                input.Now,
	})
	if err != nil {
		return CreateResult{}, err
	}
	payload := map[string]any{
		"amount_cents": v.AmountCents, "currency_code": v.CurrencyCode,
		"max_redemptions": v.MaxRedemptions, "single_use_per_user": v.SingleUsePerUser,
		"grant_kind": v.GrantKind,
	}
	if v.SubscriptionPlanID != nil {
		payload["subscription_plan_id"] = *v.SubscriptionPlanID
	}
	_ = s.emit(ctx, AuditEvent{
		EventType: AuditVoucherCreated, TenantID: v.TenantID, VoucherID: v.ID,
		ActorID: strconv.FormatInt(input.AdminID, 10), CodeFingerprint: v.CodeFingerprint,
		Payload:    payload,
		OccurredAt: input.Now,
	})
	return CreateResult{Voucher: v, Code: code}, nil
}

func (s *Service) CreateBatch(ctx context.Context, input BatchCreateInput) (BatchCreateResult, error) {
	if s == nil || s.store == nil {
		return BatchCreateResult{}, ErrStoreNotConfigured
	}
	input = normalizeBatchInput(input)
	if err := validateBatchInput(input); err != nil {
		return BatchCreateResult{}, err
	}
	batchRec := createBatchRecord{
		TenantID: input.TenantID, AdminID: input.AdminID, RequestedCount: input.Count,
		AmountCents: input.AmountCents, CurrencyCode: input.CurrencyCode,
		ValidFrom: input.ValidFrom, ValidUntil: input.ValidUntil,
		MaxRedemptions: input.MaxRedemptions, SingleUsePerUser: input.SingleUsePerUser,
		EligibleUserID: input.EligibleUserID,
		Now:            input.Now,
	}
	records := make([]createVoucherRecord, 0, input.Count)
	codes := make([]CreatedCode, 0, input.Count)
	seen := map[string]struct{}{}
	for len(records) < input.Count {
		code, err := GenerateCode()
		if err != nil {
			return BatchCreateResult{}, fmt.Errorf("voucher: generate batch code: %w", err)
		}
		code = NormalizeCode(code)
		hash, fp := CodeHash(input.TenantID, code)
		key := string(hash)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		records = append(records, createVoucherRecord{
			TenantID: input.TenantID, AdminID: input.AdminID, CodeHash: hash, CodeFingerprint: fp,
			AmountCents: input.AmountCents, CurrencyCode: input.CurrencyCode,
			ValidFrom: input.ValidFrom, ValidUntil: input.ValidUntil,
			MaxRedemptions: input.MaxRedemptions, SingleUsePerUser: input.SingleUsePerUser,
			EligibleUserID: input.EligibleUserID,
			Now:            input.Now,
		})
		codes = append(codes, CreatedCode{Code: code, CodeFingerprint: fp})
	}
	batch, vouchers, err := s.store.CreateBatch(ctx, batchRec, records)
	if err != nil {
		return BatchCreateResult{}, err
	}
	for i := range vouchers {
		codes[i].VoucherID = vouchers[i].ID
		_ = s.emit(ctx, AuditEvent{
			EventType: AuditVoucherCreated, TenantID: input.TenantID, BatchID: batch.ID,
			VoucherID: vouchers[i].ID, ActorID: strconv.FormatInt(input.AdminID, 10),
			CodeFingerprint: vouchers[i].CodeFingerprint, OccurredAt: input.Now,
			Payload: map[string]any{"amount_cents": vouchers[i].AmountCents, "batch_id": batch.ID},
		})
	}
	return BatchCreateResult{Batch: batch, Vouchers: vouchers, Codes: codes}, nil
}

func (s *Service) Redeem(ctx context.Context, input RedeemInput) (RedeemResult, error) {
	if s == nil || s.store == nil {
		return RedeemResult{}, ErrStoreNotConfigured
	}
	input = normalizeRedeemInput(input)
	if err := validateRedeemInput(input); err != nil {
		return RedeemResult{}, err
	}
	hash, fp := CodeHash(input.TenantID, NormalizeCode(input.Code))
	ipHash := SourceIPHash(input.SourceIP)
	if s.limiter != nil {
		decision, err := s.limiter.AllowVoucherAttempt(ctx, BurstAttempt{
			TenantID: input.TenantID, UserID: input.UserID, SourceIPHash: ipHash,
			CodeFingerprint: fp, RequestID: input.RequestID, Now: input.Now,
		})
		if err != nil {
			return RedeemResult{}, err
		}
		if !decision.Allowed {
			_ = s.emit(ctx, AuditEvent{
				EventType: AuditVoucherRedeemBurstAlert, TenantID: input.TenantID,
				UserID: input.UserID, RequestID: input.RequestID, ReasonClass: "attempt_burst",
				CodeFingerprint: fp, OccurredAt: input.Now,
				Payload: map[string]any{"attempts": decision.Attempts, "source_ip_hash": ipHash},
			})
			return RedeemResult{}, ErrBurstLimited
		}
	}
	result, err := s.store.Redeem(ctx, redeemRecord{
		TenantID: input.TenantID, UserID: input.UserID, CodeHash: hash, CodeFingerprint: fp,
		IdempotencyKey: input.IdempotencyKey, SourceIPHash: ipHash, RequestID: input.RequestID,
		Now: input.Now,
	})
	if err != nil {
		reason := redeemFailureReason(err)
		_ = s.emit(ctx, AuditEvent{
			EventType: AuditVoucherRedeemFailed, TenantID: input.TenantID, UserID: input.UserID,
			RequestID: input.RequestID, ReasonClass: reason, CodeFingerprint: fp, OccurredAt: input.Now,
		})
		if errors.Is(err, ErrVoucherExpired) {
			_ = s.emit(ctx, AuditEvent{
				EventType: AuditVoucherExpired, TenantID: input.TenantID, UserID: input.UserID,
				RequestID: input.RequestID, ReasonClass: "expired", CodeFingerprint: fp, OccurredAt: input.Now,
			})
		}
		return RedeemResult{}, err
	}
	payload := map[string]any{"source_ip_hash": ipHash}
	if result.Subscription != nil {
		// 订阅券: 记订阅维度 (套餐/结果/到期), 不记 balance/billing (订阅零入账)。
		// 键后缀须落在 privacy allowlist (_kind/_id/_at); validity 天数后缀 _days 不在白名单,
		// 且 new_expires_at 已表达效果, 审计不再单列。
		payload["grant_kind"] = GrantKindSubscription
		payload["subscription_plan_id"] = result.Subscription.PlanID
		payload["result_kind"] = result.Subscription.ResultKind
		payload["new_expires_at"] = result.Subscription.NewExpiresAt.UTC()
	} else {
		payload["grant_kind"] = GrantKindBalance
		payload["amount_cents"] = result.Redemption.AmountCents
		payload["balance_cents"] = result.BalanceCents
		payload["billing_event_id"] = result.Redemption.BillingEventID
	}
	_ = s.emit(ctx, AuditEvent{
		EventType: AuditVoucherRedeemed, TenantID: result.Voucher.TenantID, VoucherID: result.Voucher.ID,
		RedemptionID: result.Redemption.ID, UserID: result.Redemption.UserID, RequestID: input.RequestID,
		CodeFingerprint: result.Voucher.CodeFingerprint, OccurredAt: input.Now,
		Payload: payload,
	})
	return result, nil
}

func (s *Service) Revoke(ctx context.Context, input RevokeInput) (Voucher, error) {
	if s == nil || s.store == nil {
		return Voucher{}, ErrStoreNotConfigured
	}
	if input.TenantID <= 0 || input.ID <= 0 || input.AdminID <= 0 {
		return Voucher{}, ErrInvalidInput
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	v, err := s.store.RevokeVoucher(ctx, input)
	if err != nil {
		return Voucher{}, err
	}
	_ = s.emit(ctx, AuditEvent{
		EventType: AuditVoucherRevoked, TenantID: v.TenantID, VoucherID: v.ID,
		ActorID: strconv.FormatInt(input.AdminID, 10), ReasonClass: "admin_revoked",
		CodeFingerprint: v.CodeFingerprint, OccurredAt: input.Now,
		Payload: map[string]any{"unredeemed_capacity": v.MaxRedemptions - v.RedeemedCount},
	})
	return v, nil
}

func (s *Service) List(ctx context.Context, input ListInput) ([]Voucher, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if input.TenantID <= 0 {
		return nil, ErrInvalidInput
	}
	if input.Limit <= 0 {
		input.Limit = 50
	}
	if input.Limit > 200 {
		input.Limit = 200
	}
	return s.store.ListVouchers(ctx, input)
}

func (s *Service) GetBatch(ctx context.Context, tenantID, batchID int64) (GetBatchResult, error) {
	if s == nil || s.store == nil {
		return GetBatchResult{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || batchID <= 0 {
		return GetBatchResult{}, ErrInvalidInput
	}
	return s.store.GetBatch(ctx, tenantID, batchID)
}

func (s *Service) BillingEvents(ctx context.Context, tenantID, userID int64) ([]BillingEvent, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	return s.store.BillingEvents(ctx, tenantID, userID)
}

func (s *Service) ListRedemptionsByUser(ctx context.Context, tenantID, userID int64, limit int) ([]Redemption, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return nil, ErrInvalidInput
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return s.store.ListRedemptionsByUser(ctx, tenantID, userID, limit)
}

func (s *Service) emit(ctx context.Context, event AuditEvent) error {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	event.Payload = sanitizeAuditPayload(ctx, event.Payload)
	return s.audit.EmitVoucherAudit(ctx, event)
}

func normalizeCreateInput(input CreateInput) CreateInput {
	input.CurrencyCode = normalizeCurrency(input.CurrencyCode)
	input.Code = NormalizeCode(input.Code)
	input.GrantKind = normalizeGrantKind(input.GrantKind)
	input.ValidFrom = input.ValidFrom.UTC()
	input.ValidUntil = input.ValidUntil.UTC()
	if input.MaxRedemptions == 0 {
		input.MaxRedemptions = 1
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	return input
}

// normalizeGrantKind 空值默认 balance, 其余 trim+lower (与 GrantKind* 常量对齐)。
func normalizeGrantKind(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	if k == "" {
		return GrantKindBalance
	}
	return k
}

func validateCreateInput(input CreateInput) error {
	// AmountCents 对两种券都要求为正: 余额券即面额; 订阅券为名义价 (信息性, 兑换时不入余额)。
	if input.TenantID <= 0 || input.AdminID <= 0 || input.AmountCents <= 0 || input.MaxRedemptions <= 0 {
		return ErrInvalidInput
	}
	if input.CurrencyCode != supportedVoucherBalanceCurrency {
		return ErrInvalidInput
	}
	if input.ValidFrom.IsZero() || input.ValidUntil.IsZero() || !input.ValidUntil.After(input.ValidFrom) {
		return ErrInvalidInput
	}
	// grant_kind 与套餐指针一致性 (镜像 DB voucher_subscription_kind_check, 在写库前拦截):
	// 订阅券必须指向有效套餐; 余额券不得携带套餐指针 (防误配)。
	switch input.GrantKind {
	case GrantKindBalance:
		if input.SubscriptionPlanID != nil {
			return ErrInvalidInput
		}
	case GrantKindSubscription:
		if input.SubscriptionPlanID == nil || *input.SubscriptionPlanID <= 0 {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func normalizeBatchInput(input BatchCreateInput) BatchCreateInput {
	input.CurrencyCode = normalizeCurrency(input.CurrencyCode)
	input.ValidFrom = input.ValidFrom.UTC()
	input.ValidUntil = input.ValidUntil.UTC()
	if input.MaxRedemptions == 0 {
		input.MaxRedemptions = 1
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	return input
}

func validateBatchInput(input BatchCreateInput) error {
	if input.TenantID <= 0 || input.AdminID <= 0 || input.Count <= 0 || input.Count > 1000 ||
		input.AmountCents <= 0 || input.MaxRedemptions <= 0 {
		return ErrInvalidInput
	}
	if input.CurrencyCode != supportedVoucherBalanceCurrency {
		return ErrInvalidInput
	}
	if input.ValidFrom.IsZero() || input.ValidUntil.IsZero() || !input.ValidUntil.After(input.ValidFrom) {
		return ErrInvalidInput
	}
	return nil
}

func normalizeRedeemInput(input RedeemInput) RedeemInput {
	input.Code = NormalizeCode(input.Code)
	input.IdempotencyKey = NormalizeIdempotencyKey(input.IdempotencyKey)
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	return input
}

func validateRedeemInput(input RedeemInput) error {
	if input.TenantID <= 0 || input.UserID <= 0 || input.Code == "" {
		return ErrInvalidInput
	}
	return nil
}

func normalizeCurrency(code string) string {
	code = strings.TrimSpace(strings.ToUpper(code))
	if code == "" {
		return supportedVoucherBalanceCurrency
	}
	return code
}

func redeemFailureReason(err error) string {
	switch {
	case errors.Is(err, ErrVoucherNotFound):
		return "not_found"
	case errors.Is(err, ErrVoucherNotYetValid):
		return "not_yet_valid"
	case errors.Is(err, ErrVoucherExpired):
		return "expired"
	case errors.Is(err, ErrVoucherExhausted):
		return "exhausted"
	case errors.Is(err, ErrVoucherRevoked):
		return "revoked"
	case errors.Is(err, ErrVoucherWrongUser):
		return "wrong_user"
	case errors.Is(err, ErrAlreadyRedeemed):
		return "single_use_per_user"
	case errors.Is(err, ErrIdempotencyConflict):
		return "idempotency_conflict"
	case errors.Is(err, ErrBurstLimited):
		return "burst_limited"
	default:
		return "rejected"
	}
}
