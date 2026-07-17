package provideraccountrecovery

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type accountStoreStub struct {
	mutation        AccountMutation
	recoverMutation AccountRecoverMutation
	result          admindb.AdminProviderAccountRow
	err             error
	order           *[]string
}

func (s *accountStoreStub) ClearRateLimitWithAudit(_ context.Context, mutation AccountMutation) (admindb.AdminProviderAccountRow, error) {
	s.mutation = mutation
	if s.order != nil {
		*s.order = append(*s.order, "account")
	}
	return s.result, s.err
}

func (s *accountStoreStub) RecoverAccountStateWithAudit(_ context.Context, mutation AccountRecoverMutation) (admindb.AdminProviderAccountRow, error) {
	s.recoverMutation = mutation
	if s.order != nil {
		*s.order = append(*s.order, "account")
	}
	return s.result, s.err
}

type channelControllerStub struct {
	tenantID          int64
	accountID         int64
	actorID           string
	result            channelhealth.Record
	changed           bool
	err               error
	calls             int
	forceActiveCalls  int
	forceActiveReason string
	order             *[]string
}

func (s *channelControllerStub) ClearRateLimitByProviderAccount(_ context.Context, tenantID, accountID int64, actorID string) (channelhealth.Record, bool, error) {
	s.calls++
	s.tenantID, s.accountID, s.actorID = tenantID, accountID, actorID
	if s.order != nil {
		*s.order = append(*s.order, "channel")
	}
	return s.result, s.changed, s.err
}

func (s *channelControllerStub) ForceActiveByProviderAccount(_ context.Context, tenantID, accountID int64, actorID, reason string) (channelhealth.Record, bool, error) {
	s.forceActiveCalls++
	s.tenantID, s.accountID, s.actorID, s.forceActiveReason = tenantID, accountID, actorID, reason
	if s.order != nil {
		*s.order = append(*s.order, "channel")
	}
	return s.result, s.changed, s.err
}

func TestClearRateLimitOrdersAccountAuditBeforeChannelRecovery(t *testing.T) {
	order := []string{}
	accounts := &accountStoreStub{
		result: admindb.AdminProviderAccountRow{ID: 77, TenantID: 7},
		order:  &order,
	}
	channels := &channelControllerStub{
		result:  channelhealth.Record{State: channelhealth.StateRamping},
		changed: true,
		order:   &order,
	}
	result, err := NewService(accounts, channels).ClearRateLimit(context.Background(), ClearRateLimitInput{
		TenantID: 7, AccountID: 77, ActorID: "admin:11", ActorRole: "tenant_operator", RequestID: "req-1",
	})
	if err != nil {
		t.Fatalf("ClearRateLimit: %v", err)
	}
	if len(order) != 2 || order[0] != "account" || order[1] != "channel" {
		t.Fatalf("mutation order=%v want [account channel]", order)
	}
	if accounts.mutation.Clear.ID != 77 || accounts.mutation.Clear.TenantID != 7 ||
		accounts.mutation.Audit.Action != clearRateLimitAuditAction ||
		accounts.mutation.Audit.TargetID == nil || *accounts.mutation.Audit.TargetID != 77 {
		t.Fatalf("account mutation mismatch: %+v", accounts.mutation)
	}
	if channels.tenantID != 7 || channels.accountID != 77 || channels.actorID != "admin:11" {
		t.Fatalf("channel input mismatch: %+v", channels)
	}
	if result.Account.ID != 77 || result.Channel == nil || !result.ChannelChanged ||
		result.Channel.State != channelhealth.StateRamping {
		t.Fatalf("result=%+v", result)
	}
}

func TestClearRateLimitAccountFailureDoesNotTouchChannel(t *testing.T) {
	accounts := &accountStoreStub{err: errors.New("audit rejected")}
	channels := &channelControllerStub{}
	_, err := NewService(accounts, channels).ClearRateLimit(context.Background(), ClearRateLimitInput{
		TenantID: 7, AccountID: 77, ActorID: "admin:11", ActorRole: "tenant_operator",
	})
	if err == nil {
		t.Fatal("account mutation failure must be returned")
	}
	if channels.calls != 0 {
		t.Fatalf("channel was called after account failure: %d", channels.calls)
	}
}

func TestClearRateLimitChannelFailureIsRetryablePartial(t *testing.T) {
	accounts := &accountStoreStub{result: admindb.AdminProviderAccountRow{ID: 77, TenantID: 7}}
	channelErr := errors.New("channel transaction unavailable")
	channels := &channelControllerStub{err: channelErr}
	result, err := NewService(accounts, channels).ClearRateLimit(context.Background(), ClearRateLimitInput{
		TenantID: 7, AccountID: 77, ActorID: "admin:11", ActorRole: "tenant_operator",
	})
	if !errors.Is(err, ErrPartialRecovery) {
		t.Fatalf("err=%v want ErrPartialRecovery", err)
	}
	if !errors.Is(err, channelErr) {
		t.Fatalf("err=%v must retain the channel failure", err)
	}
	if result.Account.ID != 77 {
		t.Fatalf("partial result must retain committed account row: %+v", result)
	}
}

func TestClearRateLimitMissingChannelRecordIsIdempotentSuccess(t *testing.T) {
	accounts := &accountStoreStub{result: admindb.AdminProviderAccountRow{ID: 77, TenantID: 7}}
	channels := &channelControllerStub{err: channelhealth.ErrNotFound}
	result, err := NewService(accounts, channels).ClearRateLimit(context.Background(), ClearRateLimitInput{
		TenantID: 7, AccountID: 77, ActorID: "admin:11", ActorRole: "tenant_operator",
	})
	if err != nil {
		t.Fatalf("missing channel record should be a no-op success: %v", err)
	}
	if result.Channel != nil || result.ChannelChanged {
		t.Fatalf("missing channel result=%+v", result)
	}
}

// TestRecoverAccountStateResetsHealthAndForcesChannelActive 守卫「完整恢复」原语必须走
// 重置 health_state 的 recover 存储(而非只清限流的 clear)+ 渠道 force-active 满血(而非
// clear-rate-limit 落 ramping 渐进)。变异任一即转红:①RecoverAccountState 改调
// ClearRateLimitWithAudit → recoverMutation 空、审计动作不对;②渠道改调
// ClearRateLimitByProviderAccount → forceActiveCalls=0。
func TestRecoverAccountStateResetsHealthAndForcesChannelActive(t *testing.T) {
	order := []string{}
	accounts := &accountStoreStub{
		result: admindb.AdminProviderAccountRow{ID: 77, TenantID: 7, HealthState: "healthy"},
		order:  &order,
	}
	channels := &channelControllerStub{
		result:  channelhealth.Record{State: channelhealth.StateActive},
		changed: true,
		order:   &order,
	}
	result, err := NewService(accounts, channels).RecoverAccountState(context.Background(), RecoverAccountInput{
		TenantID: 7, AccountID: 77, ActorID: "admin:11", ActorRole: "platform_admin", RequestID: "req-2",
	})
	if err != nil {
		t.Fatalf("RecoverAccountState: %v", err)
	}
	if accounts.recoverMutation.Recover.ID != 77 || accounts.recoverMutation.Recover.TenantID != 7 ||
		accounts.recoverMutation.Audit.Action != recoverAccountAuditAction ||
		accounts.recoverMutation.Audit.TargetID == nil || *accounts.recoverMutation.Audit.TargetID != 77 {
		t.Fatalf("recover mutation mismatch: %+v", accounts.recoverMutation)
	}
	if channels.forceActiveCalls != 1 || channels.calls != 0 {
		t.Fatalf("channel force-active/clear calls=%d/%d want 1/0 (recover must force-active, not ramp)",
			channels.forceActiveCalls, channels.calls)
	}
	if channels.tenantID != 7 || channels.accountID != 77 || channels.actorID != "admin:11" {
		t.Fatalf("channel input mismatch: %+v", channels)
	}
	if result.Channel == nil || result.Channel.State != channelhealth.StateActive || !result.ChannelChanged {
		t.Fatalf("result channel=%+v want active+changed", result.Channel)
	}
	if len(order) != 2 || order[0] != "account" || order[1] != "channel" {
		t.Fatalf("mutation order=%v want [account channel]", order)
	}
}
