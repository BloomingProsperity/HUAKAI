package payment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestAdminAdjustBalanceRejectsBlankReasonBeforeStore(t *testing.T) {
	store := &adminBalanceStoreStub{}
	svc := NewService(store)

	_, err := svc.AdminAdjustBalance(context.Background(), AdminBalanceAdjustmentInput{
		TenantID:        7,
		UserID:          3,
		Amount:          decimal.RequireFromString("10.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          " ",
		ExternalTradeNo: "admin-no-reason",
		Now:             time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("AdminAdjustBalance err=%v want ErrInvalidInput for blank reason", err)
	}
	if store.called {
		t.Fatalf("blank reason reached store: %+v", store.got)
	}
}

func TestAdminAdjustBalanceRejectsOversizedIdempotencyKeyBeforeStore(t *testing.T) {
	store := &adminBalanceStoreStub{}
	svc := NewService(store)

	_, err := svc.AdminAdjustBalance(context.Background(), AdminBalanceAdjustmentInput{
		TenantID:        7,
		UserID:          3,
		Amount:          decimal.RequireFromString("10.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          "manual correction",
		ExternalTradeNo: strings.Repeat("a", 129),
		Now:             time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("AdminAdjustBalance err=%v want ErrInvalidInput for oversized idempotency key", err)
	}
	if store.called {
		t.Fatalf("oversized idempotency key reached store: %+v", store.got)
	}
}

// TestParseAdminActorIDNumericVsPoisonedString 把 P2b-1 回归的根因钉成回归测试:
// balance_credit 的 ActorID 是「字符串接口、实则 int64 归属 sink」——admin_credit.go 用
// parseAdminActorID(strconv.ParseInt 吞错) 把它解回数字后写进 payment_orders.created_by_admin_id /
// confirmed_by_admin_id 与 payment_audit_events.actor_id(均 bigint)。
//
//   - 传纯数字 TokenID("11") → 得 11,归属正确落库;
//   - 传 AuditActor() 形态的串("admin_token:5")→ ParseInt 失败静默得 0 → 下游 nullableInt64(0)=NULL
//     → 充值归属被抹掉(记成 admin 0 / NULL)。
//
// 这条守的就是「未来若有人再把 AuditActor() 接进 balance_credit,归属会丢」的跨层坑。
// 变异:把 parseAdminActorID 改成 return 一个固定非零值 / 或让它对 "admin_token:5" 也返回非 0,
// 下面任一分支断言即变红。
func TestParseAdminActorIDNumericVsPoisonedString(t *testing.T) {
	// 纯数字 TokenID:必须精确解回、归属不丢。
	if got := parseAdminActorID("11"); got != 11 {
		t.Fatalf("parseAdminActorID(%q)=%d want 11 (纯数字 TokenID 归属必须落到正确数字)", "11", got)
	}
	// 前后空白容错(handler/中间件可能带空格)——仍须解回数字,不能因空白丢归属。
	if got := parseAdminActorID("  42  "); got != 42 {
		t.Fatalf("parseAdminActorID(%q)=%d want 42 (带空白的数字仍须解回)", "  42  ", got)
	}
	// P2b-1 回归本体:AuditActor() 形态的串解不出数字 → 必须得 0(归属丢失的信号)。
	// 断言「确实是 0」而非「非 11」,把「传字符串会丢归属」这个跨层事实钉死成回归。
	if got := parseAdminActorID("admin_token:5"); got != 0 {
		t.Fatalf("parseAdminActorID(%q)=%d want 0 (非数字 actor 串必须丢归属→0,这是 P2b-1 回归的根因)", "admin_token:5", got)
	}
	// 纯字母 / 空串同样丢归属得 0。
	if got := parseAdminActorID("session:alice"); got != 0 {
		t.Fatalf("parseAdminActorID(%q)=%d want 0", "session:alice", got)
	}
	if got := parseAdminActorID(""); got != 0 {
		t.Fatalf("parseAdminActorID(%q)=%d want 0", "", got)
	}
}

// TestNullableInt64ZeroBecomesNULL 守 parseAdminActorID 得 0 之后「归属为何真的丢」的下游机制:
// 归属列(created_by_admin_id / confirmed_by_admin_id / actor_id 均 nullable bigint)由
// nullableInt64 写入——0(含负数)→ nil(NULL),正数原样写。这是「actor 串被 ParseInt 成 0 →
// 归属列变 NULL」这一跨层链路的最后一环。
// 变异:把 nullableInt64 的 `v <= 0` 改成 `v < 0`(让 0 也原样写)→ 下面 0 分支断言变红。
func TestNullableInt64ZeroBecomesNULL(t *testing.T) {
	if v := nullableInt64(0); v != nil {
		t.Fatalf("nullableInt64(0)=%v want nil (0 归属必须落 NULL,这是充值归属被抹的下游机制)", v)
	}
	if v := nullableInt64(-1); v != nil {
		t.Fatalf("nullableInt64(-1)=%v want nil", v)
	}
	if v := nullableInt64(11); v != int64(11) {
		t.Fatalf("nullableInt64(11)=%v want int64(11) (正数 TokenID 必须原样落库、不丢归属)", v)
	}
}

type adminBalanceStoreStub struct {
	*MemoryStore
	called bool
	got    AdminBalanceAdjustmentInput
}

func (s *adminBalanceStoreStub) ApplyAdminBalanceAdjustment(_ context.Context, input AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, error) {
	s.called = true
	s.got = input
	return AdminBalanceAdjustmentResult{}, nil
}
