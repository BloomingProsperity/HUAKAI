//go:build integration_pg

package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

// TestReserveConcurrentSameUser_NoSerializationLeakToClient 证明 Serializable 重试配套
// 修复了「同用户并发预扣抛 40001 直接暴露给调用方(→HTTP 500)」的配合缺口
//(2026-07-02 P0 细粒度 E2E 实测发现)。N 个 goroutine 同时对同一 user 预扣(各自
// 唯一 logical_request_id + payload,真争抢 user_balances 行,而非幂等重放),断言:
//   - 没有任何一个返回原始 40001/40P01 序列化冲突(那正是修复前泄漏成 500 的根因);
//   - 每个结果要么成功、要么是可重试的 ErrClaimRace(重试耗尽降级)或余额门 ErrInsufficientBalance;
//   - 余额不为负(预扣防超支不被破坏)、held == 成功 claim 数 × 单价(hold 不泄漏不重复)。
//
// 变异判据:把 DefaultClaimGate.Reserve 改成直接调 reserveOnce(去掉 retryReserve 包裹)
// → 并发下必现原始 40001 泄漏 → 本测试 RED。这是单模块测不到、只有并发配合链才暴露的缺陷。
func TestReserveConcurrentSameUser_NoSerializationLeakToClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPool(t, ctx)
	tenantID, apiKeyID, userID := seedTenant(t, ctx, pool, "conc")
	// 充足余额:每笔预扣 0.01、并发 N 笔共 « 余额,全应过余额门(隔离出「序列化冲突」这一变量)。
	if _, err := pool.Exec(ctx,
		`UPDATE user_balances SET balance=100, held=0 WHERE user_id=$1 AND tenant_id=$2`,
		userID, tenantID); err != nil {
		t.Fatalf("充值: %v", err)
	}
	gate := NewClaimGate(pool)

	const n = 16
	var wg sync.WaitGroup
	results := make([]error, n)
	claimIDs := make([]int64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := baseRequest(tenantID, apiKeyID, userID)
			// 每 goroutine 唯一幂等键 → 各建独立 claim、真争抢同一余额行(非幂等命中重放)。
			req.LogicalRequestID = fmt.Sprintf("conc-%d-%d", tenantID, idx)
			req.NormalizedPayloadHash = fmt.Sprintf("hash-%d", idx)
			res, err := gate.Reserve(ctx, req)
			results[idx] = err
			if err == nil && res != nil {
				claimIDs[idx] = res.ClaimID
			}
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for i, err := range results {
		switch {
		case err == nil:
			succeeded++
			if claimIDs[i] == 0 {
				t.Errorf("goroutine %d 成功但无 ClaimID", i)
			}
		case errors.Is(err, ErrClaimRace):
			// 可接受:重试耗尽降级成可重试的 409(客户端自动退避重试)。
			// 注意:本例余额充足(100 » 16×0.01),ErrInsufficientBalance 不该出现——
			// 刻意不纳入可接受集合,否则会静默吞掉「并发下 hold 计算错误导致误拒」的缺陷。
		default:
			// 关键判别:绝不允许原始序列化冲突泄漏给调用方——那就是修复前的 500 根因。
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
				t.Fatalf("goroutine %d 泄漏原始序列化冲突 %s(修复前的 500 根因,重试未生效)", i, pgErr.Code)
			}
			t.Fatalf("goroutine %d 非预期错误: %v", i, err)
		}
	}
	if succeeded == 0 {
		t.Fatalf("并发全失败,预期绝大多数应成功")
	}

	// 余额/held 守恒(判别性):预扣只增 held、不动 balance(HUAKAI 模型:reserve 建 hold,
	// balance 到 settle/capture 才实扣)。故并发 N 笔后 balance 必恒等于初始 100(充足余额下),
	// held 必恰等成功数×单价。用「精确守恒」而非「非负」——非负在 balance=100 下永真、抓不到
	// 「reserve 误扣 balance / hold 泄漏 / 双重 hold」这些并发下的真实缺陷。
	var balance, held decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT balance, held FROM user_balances WHERE user_id=$1 AND tenant_id=$2`,
		userID, tenantID).Scan(&balance, &held); err != nil {
		t.Fatalf("读余额: %v", err)
	}
	if !balance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance=%s 应恒为初始 100(reserve 只建 hold 不实扣)——并发下被误扣/破坏", balance)
	}
	wantHeld := decimal.NewFromFloat(0.01).Mul(decimal.NewFromInt(int64(succeeded)))
	if !held.Equal(wantHeld) {
		t.Fatalf("held=%s 与成功数 %d×0.01=%s 不一致(hold 泄漏或重复)", held, succeeded, wantHeld)
	}
}
