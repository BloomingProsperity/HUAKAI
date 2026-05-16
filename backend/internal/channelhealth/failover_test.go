package channelhealth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/pool"
)

type staticAccounts []*pool.AccountSnapshot

func (s staticAccounts) ListAccounts(context.Context, pool.SelectionRequest) ([]*pool.AccountSnapshot, error) {
	return []*pool.AccountSnapshot(s), nil
}

type acceptingSlots struct{}

func (acceptingSlots) Acquire(context.Context, *pool.AccountSnapshot, pool.SelectionRequest) (*pool.AcquireResult, error) {
	return &pool.AcquireResult{AcquisitionToken: uuid.New()}, nil
}

func TestChannelHealth_AT006_AT012_PoolGateSkipsCooledAndSurfacesAllUnhealthy(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	gate := NewPoolGate(store, nil)
	gates := pool.DefaultGateChain()
	gates.Health = gate
	sel := pool.NewDefaultSelector(staticAccounts{
		{ID: 101, TenantID: 7, Priority: 1, MaxConcurrency: 1},
		{ID: 202, TenantID: 7, Priority: 2, MaxConcurrency: 1},
	}, pool.WithGateChain(gates), pool.WithSlotManager(acceptingSlots{}))

	key1 := testKey()
	key1.ProviderAccountID = 101
	key2 := testKey()
	key2.ProviderAccountID = 202
	key2.AccountCredentialID = 9002
	svc := NewService(store, testPolicy(), nil)
	rec1, _ := svc.EnsureDefaultActive(ctx, key1)
	rec1.State = StateCoolingDown
	_, _ = store.UpsertRecord(ctx, rec1)
	_, _ = svc.EnsureDefaultActive(ctx, key2)

	res, err := sel.Select(ctx, pool.SelectionRequest{TenantID: 7, PoolGroupID: 1, RequestedModel: "gpt-test"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 202 {
		t.Fatalf("selected account=%d want healthy secondary 202", res.AccountID)
	}

	rec2, _ := store.Get(ctx, key2)
	rec2.State = StateDisabled
	_, _ = store.UpsertRecord(ctx, rec2)
	_, err = sel.Select(ctx, pool.SelectionRequest{TenantID: 7, PoolGroupID: 1, RequestedModel: "gpt-test"})
	if !errors.Is(err, pool.ErrAllChannelsDegraded) {
		t.Fatalf("err=%v want ErrAllChannelsDegraded", err)
	}
}

func TestChannelHealth_PoolGateLazyCooldownExpiryStartsRamp(t *testing.T) {
	ctx, svc, store, clock := testService()
	key := testKey()
	rec, _ := svc.EnsureDefaultActive(ctx, key)
	expired := clock.Now().Add(-time.Second)
	rec.State = StateCoolingDown
	rec.CooldownUntil = &expired
	_, _ = store.UpsertRecord(ctx, rec)

	gate := NewServicePoolGate(svc, clock)
	req := rampAdmittedRequest(t, key.ProviderAccountID)
	ok, why, err := gate.Allow(ctx, &pool.AccountSnapshot{ID: key.ProviderAccountID, TenantID: key.TenantID}, req)
	if err != nil || !ok {
		t.Fatalf("Allow ok=%v why=%s err=%v, want lazy ramp admission", ok, why, err)
	}
	rec, _ = store.Get(ctx, key)
	if rec.State != StateRamping || rec.RampStagePct != 1 || rec.CooldownUntil != nil {
		t.Fatalf("lazy ramp rec=%+v, want ramping 1%% with cleared cooldown", rec)
	}
	if !hasAudit(auditTypes(store.Audits()), EventRampStarted) {
		t.Fatalf("ramp-start audit missing: %+v", store.Audits())
	}
}

func TestChannelHealth_AT012_SampleFloorPreventsSingleFailureCooldown(t *testing.T) {
	ctx, svc, store, _ := testService()
	key := testKey()
	if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError}); err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	rec, _ := store.Get(ctx, key)
	if rec.State != StateActive {
		t.Fatalf("single failure below sample floor state=%s want active", rec.State)
	}
}

func rampAdmittedRequest(t *testing.T, accountID int64) pool.SelectionRequest {
	t.Helper()
	for i := 0; i < 10000; i++ {
		req := pool.SelectionRequest{
			TenantID:       7,
			RequestedModel: fmt.Sprintf("ramp-%d", i),
		}
		if AdmitRamp(RampAdmissionKey(req, accountID), 1) {
			return req
		}
	}
	t.Fatal("could not find 1% ramp admission key")
	return pool.SelectionRequest{}
}
