package voucher

import (
	"bytes"
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu               sync.Mutex
	nextVoucherID    int64
	nextBatchID      int64
	nextRedemptionID int64
	nextBillingID    int64
	vouchers         map[int64]Voucher
	vouchersByCode   map[string]int64
	batches          map[int64]Batch
	batchVouchers    map[int64][]int64
	redemptions      map[int64]Redemption
	redemptionsByKey map[string]int64
	billingEvents    []BillingEvent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		vouchers:         map[int64]Voucher{},
		vouchersByCode:   map[string]int64{},
		batches:          map[int64]Batch{},
		batchVouchers:    map[int64][]int64{},
		redemptions:      map[int64]Redemption{},
		redemptionsByKey: map[string]int64{},
	}
}

func (s *MemoryStore) CreateVoucher(_ context.Context, rec createVoucherRecord) (Voucher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vouchersByCode[codeKey(rec.TenantID, rec.CodeHash)]; ok {
		return Voucher{}, ErrVoucherDuplicate
	}
	v := voucherFromCreate(rec, s.nextIDLocked("voucher"))
	s.vouchers[v.ID] = v
	s.vouchersByCode[codeKey(v.TenantID, v.CodeHash)] = v.ID
	if v.BatchID != nil {
		s.batchVouchers[*v.BatchID] = append(s.batchVouchers[*v.BatchID], v.ID)
	}
	return cloneVoucher(v), nil
}

func (s *MemoryStore) CreateBatch(_ context.Context, batchRec createBatchRecord, voucherRecs []createVoucherRecord) (Batch, []Voucher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]struct{}{}
	for _, rec := range voucherRecs {
		key := codeKey(rec.TenantID, rec.CodeHash)
		if _, ok := seen[key]; ok {
			return Batch{}, nil, ErrVoucherDuplicate
		}
		if _, ok := s.vouchersByCode[key]; ok {
			return Batch{}, nil, ErrVoucherDuplicate
		}
		seen[key] = struct{}{}
	}
	now := batchRec.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	b := Batch{
		ID: batchRecID(s), TenantID: batchRec.TenantID, CreatedByAdminID: batchRec.AdminID,
		RequestedCount: batchRec.RequestedCount, CreatedCount: len(voucherRecs),
		AmountCents: batchRec.AmountCents, CurrencyCode: batchRec.CurrencyCode,
		ValidFrom: batchRec.ValidFrom.UTC(), ValidUntil: batchRec.ValidUntil.UTC(),
		MaxRedemptions: batchRec.MaxRedemptions, SingleUsePerUser: batchRec.SingleUsePerUser,
		Status: BatchStatusCompleted, CreatedAt: now,
	}
	s.batches[b.ID] = b
	out := make([]Voucher, 0, len(voucherRecs))
	for _, rec := range voucherRecs {
		batchID := b.ID
		rec.BatchID = &batchID
		v := voucherFromCreate(rec, s.nextIDLocked("voucher"))
		s.vouchers[v.ID] = v
		s.vouchersByCode[codeKey(v.TenantID, v.CodeHash)] = v.ID
		s.batchVouchers[b.ID] = append(s.batchVouchers[b.ID], v.ID)
		out = append(out, cloneVoucher(v))
	}
	return b, out, nil
}

func (s *MemoryStore) ListVouchers(_ context.Context, input ListInput) ([]Voucher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Voucher
	for _, v := range s.vouchers {
		if v.TenantID == input.TenantID {
			out = append(out, cloneVoucher(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if input.Limit > 0 && len(out) > input.Limit {
		out = out[:input.Limit]
	}
	return out, nil
}

func (s *MemoryStore) GetBatch(_ context.Context, tenantID, id int64) (GetBatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.batches[id]
	if !ok || b.TenantID != tenantID {
		return GetBatchResult{}, ErrVoucherNotFound
	}
	result := GetBatchResult{Batch: b}
	for _, voucherID := range s.batchVouchers[id] {
		if v, ok := s.vouchers[voucherID]; ok {
			result.Vouchers = append(result.Vouchers, cloneVoucher(v))
		}
	}
	return result, nil
}

func (s *MemoryStore) RevokeVoucher(_ context.Context, input RevokeInput) (Voucher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vouchers[input.ID]
	if !ok || v.TenantID != input.TenantID {
		return Voucher{}, ErrVoucherNotFound
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.Status = StatusRevoked
	v.RevokedByAdminID = input.AdminID
	v.RevokedReason = input.Reason
	v.UpdatedAt = now
	v.RevokedAt = &now
	s.vouchers[v.ID] = v
	return cloneVoucher(v), nil
}

func (s *MemoryStore) Redeem(_ context.Context, rec redeemRecord) (RedeemResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := rec.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if rec.IdempotencyKey != "" {
		if existingID, ok := s.redemptionsByKey[idempotencyKey(rec.TenantID, rec.UserID, rec.IdempotencyKey)]; ok {
			red := s.redemptions[existingID]
			v := s.vouchers[red.VoucherID]
			if !bytes.Equal(v.CodeHash, rec.CodeHash) {
				return RedeemResult{}, ErrIdempotencyConflict
			}
			red.CodeFingerprint = v.CodeFingerprint
			return RedeemResult{Voucher: cloneVoucher(v), Redemption: red, BalanceCents: s.balanceLocked(rec.TenantID, rec.UserID), Idempotent: true}, nil
		}
	}
	voucherID, ok := s.vouchersByCode[codeKey(rec.TenantID, rec.CodeHash)]
	if !ok {
		return RedeemResult{}, ErrVoucherNotFound
	}
	v := s.vouchers[voucherID]
	if v.GrantKind == GrantKindSubscription {
		// 订阅券激活依赖真订阅/配额表, 内存 store 不镜像 (见 P3b-3 计划 §5 D3); 真路径用 PG store。
		return RedeemResult{}, ErrSubscriptionVoucherUnsupported
	}
	if err := evaluateVoucher(v, rec.UserID, now, s.userRedeemedLocked); err != nil {
		if err == ErrVoucherExpired && v.Status == StatusActive {
			v.Status = StatusExpired
			v.UpdatedAt = now
			s.vouchers[v.ID] = v
		}
		return RedeemResult{}, err
	}
	s.nextRedemptionID++
	red := Redemption{
		ID: s.nextRedemptionID, TenantID: rec.TenantID, VoucherID: v.ID, UserID: rec.UserID,
		IdempotencyKey: rec.IdempotencyKey, AmountCents: v.AmountCents, CurrencyCode: v.CurrencyCode,
		SingleUsePerUser: v.SingleUsePerUser, SourceIPHash: rec.SourceIPHash, RequestID: rec.RequestID,
		RedeemedAt: now, CodeFingerprint: v.CodeFingerprint,
	}
	s.nextBillingID++
	event := BillingEvent{
		ID: s.nextBillingID, TenantID: rec.TenantID, EventType: "voucher_redeemed",
		RedemptionID: red.ID, VoucherID: v.ID, UserID: rec.UserID, AmountCents: v.AmountCents,
		Fingerprint: v.CodeFingerprint, OccurredAt: now,
	}
	red.BillingEventID = event.ID
	s.redemptions[red.ID] = red
	if rec.IdempotencyKey != "" {
		s.redemptionsByKey[idempotencyKey(rec.TenantID, rec.UserID, rec.IdempotencyKey)] = red.ID
	}
	s.billingEvents = append(s.billingEvents, event)
	v.RedeemedCount++
	if v.RedeemedCount >= v.MaxRedemptions {
		v.Status = StatusExhausted
	}
	v.UpdatedAt = now
	s.vouchers[v.ID] = v
	return RedeemResult{Voucher: cloneVoucher(v), Redemption: red, BalanceCents: s.balanceLocked(rec.TenantID, rec.UserID)}, nil
}

func (s *MemoryStore) BillingEvents(_ context.Context, tenantID, userID int64) ([]BillingEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []BillingEvent
	for _, event := range s.billingEvents {
		if event.TenantID == tenantID && (userID == 0 || event.UserID == userID) {
			out = append(out, event)
		}
	}
	return out, nil
}

func (s *MemoryStore) nextIDLocked(kind string) int64 {
	switch kind {
	case "voucher":
		s.nextVoucherID++
		return s.nextVoucherID
	default:
		return 0
	}
}

func batchRecID(s *MemoryStore) int64 {
	s.nextBatchID++
	return s.nextBatchID
}

func voucherFromCreate(rec createVoucherRecord, id int64) Voucher {
	now := rec.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	status := StatusActive
	if !rec.ValidUntil.After(now) {
		status = StatusExpired
	}
	return Voucher{
		ID: id, TenantID: rec.TenantID, BatchID: rec.BatchID, CodeHash: append([]byte(nil), rec.CodeHash...),
		CodeFingerprint: rec.CodeFingerprint, AmountCents: rec.AmountCents, CurrencyCode: rec.CurrencyCode,
		ValidFrom: rec.ValidFrom.UTC(), ValidUntil: rec.ValidUntil.UTC(), MaxRedemptions: rec.MaxRedemptions,
		SingleUsePerUser: rec.SingleUsePerUser, EligibleUserID: cloneInt64Ptr(rec.EligibleUserID),
		GrantKind: grantKindOrDefault(rec.GrantKind), SubscriptionPlanID: cloneInt64Ptr(rec.SubscriptionPlanID),
		Status: status, CreatedByAdminID: rec.AdminID,
		CreatedAt: now, UpdatedAt: now,
	}
}

func evaluateVoucher(v Voucher, userID int64, now time.Time, redeemed func(Voucher, int64) bool) error {
	switch v.Status {
	case StatusRevoked:
		return ErrVoucherRevoked
	case StatusExpired:
		return ErrVoucherExpired
	case StatusExhausted:
		return ErrVoucherExhausted
	case StatusActive:
	default:
		return ErrVoucherInactive
	}
	if now.Before(v.ValidFrom) {
		return ErrVoucherNotYetValid
	}
	if !now.Before(v.ValidUntil) {
		return ErrVoucherExpired
	}
	if v.RedeemedCount >= v.MaxRedemptions {
		return ErrVoucherExhausted
	}
	if v.EligibleUserID != nil && *v.EligibleUserID != userID {
		return ErrVoucherWrongUser
	}
	if v.SingleUsePerUser && redeemed(v, userID) {
		return ErrAlreadyRedeemed
	}
	return nil
}

func (s *MemoryStore) userRedeemedLocked(v Voucher, userID int64) bool {
	for _, red := range s.redemptions {
		if red.TenantID == v.TenantID && red.VoucherID == v.ID && red.UserID == userID {
			return true
		}
	}
	return false
}

func (s *MemoryStore) balanceLocked(tenantID, userID int64) int64 {
	var total int64
	for _, red := range s.redemptions {
		v := s.vouchers[red.VoucherID]
		if red.TenantID == tenantID && red.UserID == userID && v.GrantKind != GrantKindSubscription {
			total += red.AmountCents
		}
	}
	return total
}

func codeKey(tenantID int64, hash []byte) string {
	return stringKey(tenantID, string(hash))
}

func idempotencyKey(tenantID, userID int64, key string) string {
	return stringKey(tenantID, userID, key)
}

func cloneVoucher(v Voucher) Voucher {
	v.CodeHash = append([]byte(nil), v.CodeHash...)
	v.EligibleUserID = cloneInt64Ptr(v.EligibleUserID)
	v.SubscriptionPlanID = cloneInt64Ptr(v.SubscriptionPlanID)
	return v
}

func cloneInt64Ptr(v *int64) *int64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
