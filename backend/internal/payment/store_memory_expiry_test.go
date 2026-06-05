package payment

import (
	"context"
	"testing"
	"time"
)

// 守:过期未付 pending 单必须排除出 pending 数与日额上限,否则废单永久堵正常充值。
// Mutation: 去掉 expires 过滤 → 过期单被计入 → 本断言红。
func TestMemoryStore_ExpiredPendingExcludedFromCaps(t *testing.T) {
	s := NewMemoryStore()
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	s.orders[1] = &Order{ID: 1, TenantID: 7, UserID: 13, Status: StatusPending, ExpiresAt: &past, AmountCents: 500, CreatedAt: time.Now().Add(-2 * time.Hour)}
	s.orders[2] = &Order{ID: 2, TenantID: 7, UserID: 13, Status: StatusPending, ExpiresAt: &future, AmountCents: 700, CreatedAt: time.Now().Add(-30 * time.Minute)}
	if n, err := s.CountPendingOrders(context.Background(), 7, 13, time.Now()); err != nil || n != 1 {
		t.Fatalf("CountPendingOrders=(%d,%v) want (1,nil) (expired pending excluded)", n, err)
	}
	if sum, err := s.SumRechargeAmountSince(context.Background(), 7, 13, time.Now().Add(-3*time.Hour), time.Now()); err != nil || sum != 700 {
		t.Fatalf("SumRechargeAmountSince=(%d,%v) want (700,nil) (expired pending 500 excluded)", sum, err)
	}
}
