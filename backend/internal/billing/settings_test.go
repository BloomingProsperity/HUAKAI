package billing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPolicyResolver_DefaultNoRow(t *testing.T) {
	store := newFakePolicyStore()
	resolver := NewPolicyResolver(store, time.Minute)

	got := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 101)

	if got != StreamInputOnlyInterruptedPolicyNoBill {
		t.Fatalf("policy=%q want %q", got, StreamInputOnlyInterruptedPolicyNoBill)
	}
	if calls := store.getCallsForTenant(101); calls != 1 {
		t.Fatalf("tenant 101 get calls=%d want 1", calls)
	}
}

func TestParseStreamInputOnlyInterruptedPolicyValidation(t *testing.T) {
	for _, value := range []string{"no_bill", " no_bill_record "} {
		if _, err := ParseStreamInputOnlyInterruptedPolicy(value); err != nil {
			t.Fatalf("ParseStreamInputOnlyInterruptedPolicy(%q) err=%v", value, err)
		}
	}
	if _, err := ParseStreamInputOnlyInterruptedPolicy("bill_input"); !errors.Is(err, ErrBillingPolicyRoadmap) {
		t.Fatalf("bill_input err=%v want %v", err, ErrBillingPolicyRoadmap)
	}
	if _, err := ParseStreamInputOnlyInterruptedPolicy("charge_everything"); !errors.Is(err, ErrBillingSettingInvalid) {
		t.Fatalf("invalid err=%v want %v", err, ErrBillingSettingInvalid)
	}
}

func TestSQLPolicyStore_UpsertCanonicalizesStreamInputOnlyInterruptedPolicy(t *testing.T) {
	q := newFakeBillingSettingsQueries()
	store := &SQLPolicyStore{q: q}

	got, err := store.UpsertStreamInputOnlyInterruptedPolicy(context.Background(), 701, StreamInputOnlyInterruptedPolicy(" no_bill "), "owner")
	if err != nil {
		t.Fatalf("UpsertStreamInputOnlyInterruptedPolicy: %v", err)
	}

	want := StreamInputOnlyInterruptedPolicyNoBill.String()
	if got.Value != want {
		t.Fatalf("stored value=%q want %q", got.Value, want)
	}
	if q.lastUpsert.SettingValue != want {
		t.Fatalf("upsert setting_value=%q want %q", q.lastUpsert.SettingValue, want)
	}
	row := q.rows[701][StreamInputOnlyInterruptedPolicyKey]
	if row.SettingValue != want {
		t.Fatalf("persisted setting_value=%q want %q", row.SettingValue, want)
	}
}

func TestPolicyResolver_TenantIsolation(t *testing.T) {
	store := newFakePolicyStore()
	store.set(201, StreamInputOnlyInterruptedPolicyNoBillRecord)
	resolver := NewPolicyResolver(store, time.Minute)

	gotA := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 201)
	gotB := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 202)

	if gotA != StreamInputOnlyInterruptedPolicyNoBillRecord {
		t.Fatalf("tenant A policy=%q want %q", gotA, StreamInputOnlyInterruptedPolicyNoBillRecord)
	}
	if gotB != StreamInputOnlyInterruptedPolicyNoBill {
		t.Fatalf("tenant B policy=%q want %q", gotB, StreamInputOnlyInterruptedPolicyNoBill)
	}
}

func TestPolicyResolver_CacheHitDoesNotQueryStore(t *testing.T) {
	now := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	store := newFakePolicyStore()
	store.set(301, StreamInputOnlyInterruptedPolicyNoBillRecord)
	resolver := NewPolicyResolver(store, time.Minute)
	resolver.now = func() time.Time { return now }

	first := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 301)
	store.set(301, StreamInputOnlyInterruptedPolicyNoBill)
	second := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 301)

	if first != StreamInputOnlyInterruptedPolicyNoBillRecord || second != StreamInputOnlyInterruptedPolicyNoBillRecord {
		t.Fatalf("cache policies first=%q second=%q want cached %q", first, second, StreamInputOnlyInterruptedPolicyNoBillRecord)
	}
	if calls := store.getCallsForTenant(301); calls != 1 {
		t.Fatalf("tenant 301 get calls=%d want 1", calls)
	}
}

func TestPolicyResolver_CacheExpiryReloadsStore(t *testing.T) {
	now := time.Date(2026, 5, 20, 11, 0, 0, 0, time.UTC)
	store := newFakePolicyStore()
	store.set(401, StreamInputOnlyInterruptedPolicyNoBillRecord)
	resolver := NewPolicyResolver(store, 10*time.Millisecond)
	resolver.now = func() time.Time { return now }

	first := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 401)
	store.set(401, StreamInputOnlyInterruptedPolicyNoBill)
	now = now.Add(11 * time.Millisecond)
	second := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 401)

	if first != StreamInputOnlyInterruptedPolicyNoBillRecord || second != StreamInputOnlyInterruptedPolicyNoBill {
		t.Fatalf("policies first=%q second=%q want reload to %q", first, second, StreamInputOnlyInterruptedPolicyNoBill)
	}
	if calls := store.getCallsForTenant(401); calls != 2 {
		t.Fatalf("tenant 401 get calls=%d want 2", calls)
	}
}

func TestPolicyResolver_WriteInvalidatesCache(t *testing.T) {
	store := newFakePolicyStore()
	resolver := NewPolicyResolver(store, time.Minute)
	ctx := context.Background()

	first := resolver.ResolveStreamInputOnlyInterruptedPolicy(ctx, 501)
	if first != StreamInputOnlyInterruptedPolicyNoBill {
		t.Fatalf("initial policy=%q want %q", first, StreamInputOnlyInterruptedPolicyNoBill)
	}
	if err := resolver.SetStreamInputOnlyInterruptedPolicy(ctx, 501, StreamInputOnlyInterruptedPolicyNoBillRecord, "owner"); err != nil {
		t.Fatalf("SetStreamInputOnlyInterruptedPolicy: %v", err)
	}
	second := resolver.ResolveStreamInputOnlyInterruptedPolicy(ctx, 501)

	if second != StreamInputOnlyInterruptedPolicyNoBillRecord {
		t.Fatalf("post-write policy=%q want %q", second, StreamInputOnlyInterruptedPolicyNoBillRecord)
	}
	if calls := store.getCallsForTenant(501); calls != 2 {
		t.Fatalf("tenant 501 get calls=%d want 2", calls)
	}
	if store.upsertCalls != 1 {
		t.Fatalf("upsert calls=%d want 1", store.upsertCalls)
	}
}

func TestPolicyResolver_InvalidationDuringColdReadDoesNotCacheStalePolicy(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	store := newFakePolicyStore()
	store.set(551, StreamInputOnlyInterruptedPolicyNoBill)
	resolver := NewPolicyResolver(store, time.Minute)
	resolver.now = func() time.Time { return now }
	store.onGet = func(tenantID int64, key string) {
		if tenantID != 551 || key != StreamInputOnlyInterruptedPolicyKey {
			return
		}
		store.set(551, StreamInputOnlyInterruptedPolicyNoBillRecord)
		resolver.Invalidate(551)
	}

	first := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 551)
	if first != StreamInputOnlyInterruptedPolicyNoBill {
		t.Fatalf("race resolve policy=%q want read value %q", first, StreamInputOnlyInterruptedPolicyNoBill)
	}
	if cached, ok := resolver.cacheGet(551); ok {
		t.Fatalf("stale cache entry policy=%q want no cache entry after invalidation during read", cached.policy)
	}
	second := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 551)
	if second != StreamInputOnlyInterruptedPolicyNoBillRecord {
		t.Fatalf("post-race policy=%q want %q", second, StreamInputOnlyInterruptedPolicyNoBillRecord)
	}
	if calls := store.getCallsForTenant(551); calls != 2 {
		t.Fatalf("tenant 551 get calls=%d want 2", calls)
	}
}

func TestPolicyResolver_ColdReadFailureFallsBackNoBill(t *testing.T) {
	store := newFakePolicyStore()
	store.err = errors.New("database unavailable")
	resolver := NewPolicyResolver(store, time.Minute)

	got := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 601)

	if got != StreamInputOnlyInterruptedPolicyNoBill {
		t.Fatalf("policy=%q want fallback %q", got, StreamInputOnlyInterruptedPolicyNoBill)
	}
	if calls := store.getCallsForTenant(601); calls != 1 {
		t.Fatalf("tenant 601 get calls=%d want 1", calls)
	}
	store.err = nil
	store.set(601, StreamInputOnlyInterruptedPolicyNoBillRecord)
	got = resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 601)
	if got != StreamInputOnlyInterruptedPolicyNoBillRecord {
		t.Fatalf("post-recovery policy=%q want %q", got, StreamInputOnlyInterruptedPolicyNoBillRecord)
	}
}

func TestPolicyResolver_ExpiredCacheServesStaleOnRefreshFailure(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC)
	store := newFakePolicyStore()
	store.set(651, StreamInputOnlyInterruptedPolicyNoBillRecord)
	resolver := NewPolicyResolver(store, 10*time.Millisecond)
	resolver.now = func() time.Time { return now }

	first := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 651)
	if first != StreamInputOnlyInterruptedPolicyNoBillRecord {
		t.Fatalf("initial policy=%q want %q", first, StreamInputOnlyInterruptedPolicyNoBillRecord)
	}
	cachedBefore, ok := resolver.cacheGet(651)
	if !ok {
		t.Fatal("want cached policy before refresh failure")
	}
	store.err = errors.New("database unavailable")
	now = now.Add(11 * time.Millisecond)

	second := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 651)

	if second != StreamInputOnlyInterruptedPolicyNoBillRecord {
		t.Fatalf("stale policy=%q want %q", second, StreamInputOnlyInterruptedPolicyNoBillRecord)
	}
	cachedAfter, ok := resolver.cacheGet(651)
	if !ok {
		t.Fatal("want stale entry retained during bounded grace window")
	}
	if want := now.Add(policyResolverRefreshRetryInterval); !cachedAfter.expiresAt.Equal(want) {
		t.Fatalf("expiresAt=%s want retry throttle deadline %s", cachedAfter.expiresAt, want)
	}
	if !cachedAfter.staleDeadline.Equal(cachedBefore.staleDeadline) {
		t.Fatalf("staleDeadline=%s want unchanged %s", cachedAfter.staleDeadline, cachedBefore.staleDeadline)
	}
	if calls := store.getCallsForTenant(651); calls != 2 {
		t.Fatalf("tenant 651 get calls=%d want 2", calls)
	}
}

func TestPolicyResolver_ExpiredCacheBeyondStaleGraceFallsBack(t *testing.T) {
	now := time.Date(2026, 5, 20, 14, 0, 0, 0, time.UTC)
	store := newFakePolicyStore()
	store.set(652, StreamInputOnlyInterruptedPolicyNoBillRecord)
	resolver := NewPolicyResolver(store, 10*time.Millisecond)
	resolver.now = func() time.Time { return now }

	first := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 652)
	if first != StreamInputOnlyInterruptedPolicyNoBillRecord {
		t.Fatalf("initial policy=%q want %q", first, StreamInputOnlyInterruptedPolicyNoBillRecord)
	}
	store.err = errors.New("database unavailable")
	now = now.Add(111 * time.Millisecond)

	second := resolver.ResolveStreamInputOnlyInterruptedPolicy(context.Background(), 652)

	if second != StreamInputOnlyInterruptedPolicyNoBill {
		t.Fatalf("expired stale policy=%q want fallback %q", second, StreamInputOnlyInterruptedPolicyNoBill)
	}
	if _, ok := resolver.cacheGet(652); ok {
		t.Fatal("want expired stale entry removed after refresh failure beyond grace")
	}
	if calls := store.getCallsForTenant(652); calls != 2 {
		t.Fatalf("tenant 652 get calls=%d want 2", calls)
	}
}

type fakePolicyStore struct {
	mu          sync.Mutex
	rows        map[int64]map[string]StoredBillingSetting
	getCalls    map[int64]int
	upsertCalls int
	err         error
	onGet       func(tenantID int64, key string)
}

func newFakePolicyStore() *fakePolicyStore {
	return &fakePolicyStore{
		rows:     make(map[int64]map[string]StoredBillingSetting),
		getCalls: make(map[int64]int),
	}
}

func (s *fakePolicyStore) Get(_ context.Context, tenantID int64, key string) (StoredBillingSetting, bool, error) {
	s.mu.Lock()
	s.getCalls[tenantID]++
	err := s.err
	row, ok := s.rows[tenantID][key]
	onGet := s.onGet
	s.onGet = nil
	s.mu.Unlock()

	if onGet != nil {
		onGet(tenantID, key)
	}
	if err != nil {
		return StoredBillingSetting{}, false, err
	}
	return row, ok, nil
}

func (s *fakePolicyStore) UpsertStreamInputOnlyInterruptedPolicy(_ context.Context, tenantID int64, policy StreamInputOnlyInterruptedPolicy, updatedBy string) (StoredBillingSetting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertCalls++
	if s.err != nil {
		return StoredBillingSetting{}, s.err
	}
	canonical, err := ParseStreamInputOnlyInterruptedPolicy(policy.String())
	if err != nil {
		return StoredBillingSetting{}, err
	}
	settingValue := canonical.String()
	if !validStreamInputOnlyInterruptedSettingValue(settingValue) {
		return StoredBillingSetting{}, errors.New("billing_settings setting_value check violation")
	}
	if s.rows[tenantID] == nil {
		s.rows[tenantID] = make(map[string]StoredBillingSetting)
	}
	row := StoredBillingSetting{
		ID:        int64(len(s.rows[tenantID]) + 1),
		TenantID:  tenantID,
		Key:       StreamInputOnlyInterruptedPolicyKey,
		Value:     settingValue,
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: updatedBy,
	}
	s.rows[tenantID][StreamInputOnlyInterruptedPolicyKey] = row
	return row, nil
}

func (s *fakePolicyStore) List(_ context.Context, tenantID int64) ([]StoredBillingSetting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	rows := s.rows[tenantID]
	out := make([]StoredBillingSetting, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return out, nil
}

func (s *fakePolicyStore) set(tenantID int64, policy StreamInputOnlyInterruptedPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settingValue := policy.String()
	if !validStreamInputOnlyInterruptedSettingValue(settingValue) {
		panic("invalid stream input-only interrupted policy fixture")
	}
	if s.rows[tenantID] == nil {
		s.rows[tenantID] = make(map[string]StoredBillingSetting)
	}
	s.rows[tenantID][StreamInputOnlyInterruptedPolicyKey] = StoredBillingSetting{
		ID:        int64(len(s.rows[tenantID]) + 1),
		TenantID:  tenantID,
		Key:       StreamInputOnlyInterruptedPolicyKey,
		Value:     settingValue,
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: "test",
	}
}

func (s *fakePolicyStore) getCallsForTenant(tenantID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getCalls[tenantID]
}

var _ PolicyStore = (*fakePolicyStore)(nil)

type fakeBillingSettingsQueries struct {
	rows       map[int64]map[string]dbbilling.BillingSetting
	lastUpsert dbbilling.UpsertBillingSettingParams
}

func newFakeBillingSettingsQueries() *fakeBillingSettingsQueries {
	return &fakeBillingSettingsQueries{
		rows: make(map[int64]map[string]dbbilling.BillingSetting),
	}
}

func (q *fakeBillingSettingsQueries) GetBillingSetting(_ context.Context, arg dbbilling.GetBillingSettingParams) (dbbilling.BillingSetting, error) {
	row, ok := q.rows[arg.TenantID][arg.SettingKey]
	if !ok {
		return dbbilling.BillingSetting{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *fakeBillingSettingsQueries) UpsertBillingSetting(_ context.Context, arg dbbilling.UpsertBillingSettingParams) (dbbilling.BillingSetting, error) {
	q.lastUpsert = arg
	if arg.SettingKey == StreamInputOnlyInterruptedPolicyKey && !validStreamInputOnlyInterruptedSettingValue(arg.SettingValue) {
		return dbbilling.BillingSetting{}, errors.New("billing_settings setting_value check violation")
	}
	if q.rows[arg.TenantID] == nil {
		q.rows[arg.TenantID] = make(map[string]dbbilling.BillingSetting)
	}
	id := int64(len(q.rows[arg.TenantID]) + 1)
	if existing, ok := q.rows[arg.TenantID][arg.SettingKey]; ok {
		id = existing.ID
	}
	row := dbbilling.BillingSetting{
		ID:           id,
		TenantID:     arg.TenantID,
		SettingKey:   arg.SettingKey,
		SettingValue: arg.SettingValue,
		UpdatedAt: pgtype.Timestamptz{
			Time:  time.Now().UTC(),
			Valid: true,
		},
		UpdatedBy: arg.UpdatedBy,
	}
	q.rows[arg.TenantID][arg.SettingKey] = row
	return row, nil
}

func (q *fakeBillingSettingsQueries) ListBillingSettingsByTenant(_ context.Context, tenantID int64) ([]dbbilling.BillingSetting, error) {
	rows := q.rows[tenantID]
	out := make([]dbbilling.BillingSetting, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return out, nil
}

func validStreamInputOnlyInterruptedSettingValue(value string) bool {
	switch StreamInputOnlyInterruptedPolicy(value) {
	case StreamInputOnlyInterruptedPolicyNoBill, StreamInputOnlyInterruptedPolicyNoBillRecord:
		return true
	default:
		return false
	}
}

var _ billingSettingsQueries = (*fakeBillingSettingsQueries)(nil)
