package credentialworker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// TestDefaultProviderAccountHealthPolicyMapsAuditOutcomes 守卫终态 / 瞬态冷却 /
// healthy 三分类法。该表自带区分力——终态类(auth_expired、risk_control_triggered、
// account_disabled)必须携带 nil HealthStateUntil,使 eligibility SQL
// (health_state_until IS NOT NULL)与 router gate(until.IsZero)都拒绝自动恢复它们;
// 而真正瞬态的 rate_limit_exceeded 必须保留一个有限的未来截止时间,以便它确实会
// 自动恢复。
//
// Mutation check:把 auth_expired(或 risk_control_triggered)改回 now+RevokedCooldown,
// 其 wantUntil:nil 断言就会转红;反过来,如果某个修复把每个 outcome 一律置 nil,
// rate_limit_exceeded 的 wantUntil:+3m 那一行就会转红——证明本测试区分终态与瞬态,
// 而非对两个极端任一盖章放行。
func TestDefaultProviderAccountHealthPolicyMapsAuditOutcomes(t *testing.T) {
	fixed := time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC)
	policy := DefaultProviderAccountHealthPolicy()

	for _, tc := range []struct {
		name      string
		outcome   auth.Outcome
		wantState string
		wantUntil *time.Time
		wantAlert bool
	}{
		{
			name:      "auth expired is terminal revoked with operator alert",
			outcome:   auth.RefreshAuditOutcome("auth_expired"),
			wantState: "revoked",
			wantUntil: nil,
			wantAlert: true,
		},
		{
			name:      "rate limit throttled cooldown",
			outcome:   auth.RefreshAuditOutcome("rate_limit_exceeded"),
			wantState: "throttled",
			wantUntil: timePtr(fixed.Add(3 * time.Minute)),
		},
		{
			name:      "risk control is terminal revoked with alert",
			outcome:   auth.RefreshAuditOutcome("risk_control_triggered"),
			wantState: "revoked",
			wantUntil: nil,
			wantAlert: true,
		},
		{
			name:      "account disabled permanent revoked",
			outcome:   auth.RefreshAuditOutcome("account_disabled"),
			wantState: "revoked",
			wantUntil: nil,
		},
		{
			name:      "refresh success resets healthy",
			outcome:   auth.OutcomeRefreshSucceeded,
			wantState: "healthy",
			wantUntil: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := policy.Transition(tc.outcome, fixed)
			if !ok {
				t.Fatalf("Transition(%q) returned no-op", tc.outcome)
			}
			if got.HealthState != tc.wantState {
				t.Fatalf("state=%q, want %q", got.HealthState, tc.wantState)
			}
			if !sameOptionalTime(got.HealthStateUntil, tc.wantUntil) {
				t.Fatalf("until=%v, want %v", got.HealthStateUntil, tc.wantUntil)
			}
			if got.Alert != tc.wantAlert {
				t.Fatalf("alert=%v, want %v", got.Alert, tc.wantAlert)
			}
		})
	}
}

func TestSchedulerAuthExpiredMarksProviderAccountRevoked(t *testing.T) {
	// 修掉的回归:scheduler 的 audit outcome 还必须改写
	// provider_accounts.health_state。Mutation 自检:删掉 health 更新会让
	// health.entries 为空,本测试转红。
	fixed := time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC)
	health := &healthStateStoreSpy{}
	audit := &auditSpy{}
	ref := &refresherSpy{errs: []error{
		auth.WithRefreshAuditOutcome(nonRetryableRefreshErr{}, "auth_expired"),
	}}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccount(31)}, &stormSpy{}, ref,
		withNow(func() time.Time { return fixed }),
		withProviderAccountHealthStore(health),
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
	)

	err := s.RunOnce(context.Background())
	if err == nil {
		t.Fatalf("RunOnce must return classified refresh error")
	}
	if got := audit.lastOutcome(); got != auth.RefreshAuditOutcome("auth_expired") {
		t.Fatalf("audit outcome=%q, want auth_expired", got)
	}
	if len(health.entries) != 1 {
		t.Fatalf("health updates=%d, want 1", len(health.entries))
	}
	got := health.entries[0]
	if got.TenantID != 7 || got.ProviderAccountID != 31 {
		t.Fatalf("health update target=(tenant=%d account=%d), want (7,31)", got.TenantID, got.ProviderAccountID)
	}
	if got.HealthState != "revoked" {
		t.Fatalf("health state=%q, want revoked", got.HealthState)
	}
	// auth_expired 是终态——HealthStateUntil 必须为 nil,使 eligibility SQL 与 router
	// gate 都不会靠定时器自动恢复该账号。Mutation check:还原 now+cooldown 截止时间,
	// 本断言就会转红。
	if got.HealthStateUntil != nil {
		t.Fatalf("health until=%v, want nil (terminal)", got.HealthStateUntil)
	}
	if !got.Alert {
		t.Fatalf("auth_expired must raise an operator alert")
	}
}

type healthStateStoreSpy struct {
	entries []ProviderAccountHealthChange
	err     error
}

func (s *healthStateStoreSpy) UpdateProviderAccountHealth(_ context.Context, change ProviderAccountHealthChange) error {
	if s.err != nil {
		return s.err
	}
	s.entries = append(s.entries, change)
	return nil
}

func TestSchedulerHealthStateUpdateFailureFailsClosed(t *testing.T) {
	// 修掉的回归:health_state 改写失败绝不能被一次成功的 audit 写入掩盖。
	// Mutation 自检:吞掉 health store 错误会让 RunOnce 只返回已分类的 refresh
	// 错误,而不带下方的 sentinel。
	healthErr := errors.New("health update rejected")
	health := &healthStateStoreSpy{err: healthErr}
	ref := &refresherSpy{errs: []error{
		auth.WithRefreshAuditOutcome(nonRetryableRefreshErr{}, "auth_expired"),
	}}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccount(32)}, &stormSpy{}, ref,
		withProviderAccountHealthStore(health),
		WithAuditLedger(&ledgerSpy{}),
	)

	err := s.RunOnce(context.Background())
	if !errors.Is(err, healthErr) {
		t.Fatalf("RunOnce err=%v, want health update error", err)
	}
}

// providerAccountDownSpy 记录 scheduler 尝试的每一次告警投递,使测试可以断言
// (tenant, account, state, outcome) 元组及调用次数。err 让测试可以模拟一个失败的
// 通知管线。
type providerAccountDownSpy struct {
	deliveries []providerAccountDownDelivery
	err        error
}

type providerAccountDownDelivery struct {
	TenantID    int64
	AccountID   int64
	HealthState string
	Outcome     auth.Outcome
}

func (s *providerAccountDownSpy) DeliverProviderAccountDown(_ context.Context, change ProviderAccountHealthChange, outcome auth.Outcome) error {
	s.deliveries = append(s.deliveries, providerAccountDownDelivery{
		TenantID:    change.TenantID,
		AccountID:   change.ProviderAccountID,
		HealthState: change.HealthState,
		Outcome:     outcome,
	})
	return s.err
}

func syncAlertRunner(fn func()) { fn() }

// TestSchedulerProviderAccountDownDeliveredOnAuthExpired 证明:当一次刷新被分类为
// auth_expired(Alert=true)时,告警 deliverer 恰好触发一次,且携带正确的
// (tenant, account, state) 元组。
//
// Mutation check:删掉 maybeLogProviderAccountHealthAlert 内部的
// s.deliverProviderAccountDown 调用,spy 就会保持为空 -> 转红。spy 记录具体的
// (tenant=7, account=31, state=revoked) 元组,因此一次 no-op 或投递到错误目标也会
// 被抓住,而不只是"触发了某个东西"。
func TestSchedulerProviderAccountDownDeliveredOnAuthExpired(t *testing.T) {
	fixed := time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC)
	alertSpy := &providerAccountDownSpy{}
	ref := &refresherSpy{errs: []error{
		auth.WithRefreshAuditOutcome(nonRetryableRefreshErr{}, "auth_expired"),
	}}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(31, "anthropic")}, &stormSpy{}, ref,
		withNow(func() time.Time { return fixed }),
		withProviderAccountHealthStore(&healthStateStoreSpy{}),
		WithProviderAccountDownDeliverer(alertSpy),
		withAlertAsync(syncAlertRunner),
	)

	_ = s.RunOnce(context.Background())

	if len(alertSpy.deliveries) != 1 {
		t.Fatalf("alert deliveries=%d, want 1; MUTATION: dropping the deliverer call leaves this 0", len(alertSpy.deliveries))
	}
	got := alertSpy.deliveries[0]
	if got.TenantID != 7 || got.AccountID != 31 {
		t.Fatalf("alert target=(tenant=%d account=%d), want (7,31)", got.TenantID, got.AccountID)
	}
	if got.HealthState != "revoked" {
		t.Fatalf("alert health state=%q, want revoked", got.HealthState)
	}
	if got.Outcome != auth.RefreshAuditOutcome("auth_expired") {
		t.Fatalf("alert outcome=%q, want auth_expired", got.Outcome)
	}
}

// TestSchedulerProviderAccountDownNotDeliveredWhenAlertFalse 证明 Alert 标志即为
// 闸门:account_disabled 与 rate_limit_exceeded 都会让账号发生转换,但目前都携带
// Alert=false,因此绝不能投递任何告警。
//
// Mutation check:把 maybeLogProviderAccountHealthAlert 里的
// `if !change.Alert { return }` 翻成无条件投递,spy 就会增加投递记录 -> 转红。
// 这是针对闸门本身的区分性 fixture,而不仅仅是针对"投递发生"。
func TestSchedulerProviderAccountDownNotDeliveredWhenAlertFalse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome string
	}{
		{name: "account_disabled", outcome: "account_disabled"},
		{name: "rate_limit_exceeded", outcome: "rate_limit_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixed := time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC)
			alertSpy := &providerAccountDownSpy{}
			ref := &refresherSpy{errs: []error{
				auth.WithRefreshAuditOutcome(nonRetryableRefreshErr{}, tc.outcome),
			}}
			s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(41, "anthropic")}, &stormSpy{}, ref,
				withNow(func() time.Time { return fixed }),
				withProviderAccountHealthStore(&healthStateStoreSpy{}),
				WithProviderAccountDownDeliverer(alertSpy),
				withAlertAsync(syncAlertRunner),
			)

			_ = s.RunOnce(context.Background())

			if len(alertSpy.deliveries) != 0 {
				t.Fatalf("alert deliveries=%d, want 0 for Alert=false outcome %q; MUTATION: removing the Alert gate makes this >0", len(alertSpy.deliveries), tc.outcome)
			}
		})
	}
}

// TestSchedulerProviderAccountDownDeliveryFailureNonFatal 证明一次告警发送失败
// 绝不会破坏 credential worker:RunOnce 返回的错误必须恰好是已分类的 refresh 错误,
// 且绝不能 wrap deliverer 错误。
//
// Mutation check:把 deliverer 错误传播进 recordAudit 的返回值,RunOnce 就会 wrap
// deliverErr -> errors.Is(err, deliverErr) 变为 true -> 转红。证明非致命隔离
// (核心安全属性)。
func TestSchedulerProviderAccountDownDeliveryFailureNonFatal(t *testing.T) {
	fixed := time.Date(2026, 5, 25, 9, 30, 0, 0, time.UTC)
	deliverErr := errors.New("notification pipeline unavailable")
	alertSpy := &providerAccountDownSpy{err: deliverErr}
	health := &healthStateStoreSpy{}
	audit := &auditSpy{}
	ref := &refresherSpy{errs: []error{
		auth.WithRefreshAuditOutcome(nonRetryableRefreshErr{}, "auth_expired"),
	}}
	s := newTestScheduler([]dbbilling.ListAccountsForRefreshRow{testAccountWithVendor(51, "anthropic")}, &stormSpy{}, ref,
		withNow(func() time.Time { return fixed }),
		withProviderAccountHealthStore(health),
		withAuditWriter(audit),
		WithAuditLedger(&ledgerSpy{}),
		WithProviderAccountDownDeliverer(alertSpy),
		withAlertAsync(syncAlertRunner),
	)

	err := s.RunOnce(context.Background())

	if errors.Is(err, deliverErr) {
		t.Fatalf("RunOnce wrapped the alert delivery error %v; MUTATION: propagating the deliverer error up makes this fail", err)
	}
	// 告警仍然被尝试(投递执行了,并返回了它的错误),而 audit/health 路径仍然
	// 正常提交。
	if len(alertSpy.deliveries) != 1 {
		t.Fatalf("alert deliveries=%d, want 1 (attempted-then-failed)", len(alertSpy.deliveries))
	}
	if got := audit.lastOutcome(); got != auth.RefreshAuditOutcome("auth_expired") {
		t.Fatalf("audit outcome=%q, want auth_expired (audit path still succeeded)", got)
	}
	if len(health.entries) != 1 || health.entries[0].HealthState != "revoked" {
		t.Fatalf("health path did not commit revoked transition: %+v", health.entries)
	}
}

func timePtr(v time.Time) *time.Time {
	return &v
}

func sameOptionalTime(a, b *time.Time) bool {
	switch {
	case a == nil || b == nil:
		return a == nil && b == nil
	default:
		return a.Equal(*b)
	}
}
