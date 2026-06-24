package budgetenforce

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// TestReserverFailOpenByDefault 守护默认行为不变:budget 后端【基础设施故障】
// (非额度拒绝)时,默认 fail-open 放行,不阻断业务。
// 变异证伪:把 budgetFailClosed 默认改成 true → 本用例(期望放行)转红。
func TestReserverFailOpenByDefault(t *testing.T) {
	t.Setenv("HUAKAI_BUDGET_FAIL_CLOSED", "")
	b := &budgetStub{reserveErr: errors.New("budget backend down")}
	q := &quotaStub{}
	r := NewReserver(b, q)

	res, err := r.Reserve(context.Background(), quota.ReserveRequest{TenantID: 1})
	if err != nil {
		t.Fatalf("默认 fail-open 不应返回 err,got %v", err)
	}
	if !res.Allowed || res.Decision.Code != "budget_fail_open" {
		t.Fatalf("默认应放行 budget_fail_open,got allowed=%v code=%q", res.Allowed, res.Decision.Code)
	}
}

// TestReserverFailClosedWhenEnvSet 守护 fail-close 开关:HUAKAI_BUDGET_FAIL_CLOSED=true
// 时,budget 后端故障即【拒绝】而非放行,且不再委派 quota(已拒)。
// 变异证伪:删掉 Reserve 里 `if r.failClosed {...}` 分支 → 故障仍放行,本用例
// (期望拒绝)转红;断言 quota 未被调用则守住"拒绝即短路"。
func TestReserverFailClosedWhenEnvSet(t *testing.T) {
	t.Setenv("HUAKAI_BUDGET_FAIL_CLOSED", "true")
	b := &budgetStub{reserveErr: errors.New("budget backend down")}
	q := &quotaStub{}
	r := NewReserver(b, q)

	res, err := r.Reserve(context.Background(), quota.ReserveRequest{TenantID: 1})
	if err == nil {
		t.Fatal("fail-close 时 budget 故障应返回 DenyError,got nil")
	}
	if res.Allowed || res.Decision.Code != "budget_fail_closed" {
		t.Fatalf("fail-close 应拒绝 budget_fail_closed,got allowed=%v code=%q", res.Allowed, res.Decision.Code)
	}
	if !quota.IsDenied(err) {
		t.Fatalf("返回的 err 应是 quota deny,got %v", err)
	}
	if q.calls != 0 {
		t.Fatalf("fail-close 拒绝后不应再委派 quota,calls=%d", q.calls)
	}
}
