package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func fakePgErr(code string) error { return &pgconn.PgError{Code: code} }

func countingSleep(calls *int) func(context.Context, time.Duration) bool {
	return func(context.Context, time.Duration) bool { *calls++; return true }
}

func zeroRand(int64) int64 { return 0 }

// TestRetryReserve_RetriesSerializationThenSucceeds:前 2 次 40001、第 3 次成功 →
// 最终成功且只重试 2 次(退避 2 次)。变异契约:去掉 retryReserve 里
// isReserveSerializationConflict 的重试分支(第一次 err 就返回)→ 第 1 次 40001 直接返回,
// calls==1 断言红。
func TestRetryReserve_RetriesSerializationThenSucceeds(t *testing.T) {
	calls, sleeps := 0, 0
	want := &ReserveResult{ClaimID: 42}
	fn := func(context.Context) (*ReserveResult, error) {
		calls++
		if calls <= 2 {
			return nil, fakePgErr("40001")
		}
		return want, nil
	}
	res, err := retryReserve(context.Background(), fn, countingSleep(&sleeps), zeroRand)
	if err != nil {
		t.Fatalf("应最终成功,得 err=%v", err)
	}
	if res != want {
		t.Fatalf("res=%v want claim 42", res)
	}
	if calls != 3 {
		t.Fatalf("应跑 3 次(2 重试+1 成功),得 %d", calls)
	}
	if sleeps != 2 {
		t.Fatalf("应退避 2 次,得 %d", sleeps)
	}
}

// TestRetryTx2_RetriesSerializationThenSucceeds 覆盖 Settle/Abort 共用的 Tx2
// 包装:前 2 次 40001/40P01、第 3 次成功 → 最终成功且只退避 2 次。
// 变异契约:让 retryTx2 首次遇到 40001 直接返回 → calls==1 / err!=nil,本测试红。
// Settle/Abort 外层包装的变异证据由 settler_integration_test 覆盖。
func TestRetryTx2_RetriesSerializationThenSucceeds(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		t.Run(code, func(t *testing.T) {
			calls, sleeps := 0, 0
			err := retryTx2(context.Background(), "settle", settleTx2RetryPolicy, func(context.Context) error {
				calls++
				if calls <= 2 {
					return fakePgErr(code)
				}
				return nil
			}, countingSleep(&sleeps), zeroRand)
			if err != nil {
				t.Fatalf("Tx2 应在 %s 后重试成功,得 err=%v", code, err)
			}
			if calls != 3 {
				t.Fatalf("Tx2 应跑 3 次(2 重试+1 成功),得 %d", calls)
			}
			if sleeps != 2 {
				t.Fatalf("Tx2 应退避 2 次,得 %d", sleeps)
			}
		})
	}
}

// TestRetryTx2_BusinessErrorsNotRetried:Tx2 的业务哨兵和普通错误代表确定性结果,
// 不能被当成并发冲突重跑。变异契约:把 retryTx2 改成任意错误都重试 →
// ErrClaimNotReserving / ErrSlotReleaseMissed 等会 calls>1,本测试红。
func TestRetryTx2_BusinessErrorsNotRetried(t *testing.T) {
	for _, sentinel := range []error{
		ErrClaimNotReserving,
		ErrAcquisitionTokenMismatch,
		ErrSlotReleaseMissed,
		errors.New("plain tx2 error"),
	} {
		calls := 0
		err := retryTx2(context.Background(), "abort", abortTx2RetryPolicy, func(context.Context) error {
			calls++
			return sentinel
		}, countingSleep(new(int)), zeroRand)
		if !errors.Is(err, sentinel) {
			t.Fatalf("Tx2 错误 %v 应原样返回,得 %v", sentinel, err)
		}
		if calls != 1 {
			t.Fatalf("Tx2 错误 %v 不应重试,跑了 %d 次", sentinel, calls)
		}
	}
}

// TestRetryTx2_ExhaustsToLastError:Tx2 重试预算耗尽后返回最后一次原始 40001,
// 让 settlement recovery / lease sweep 等既有兜底继续按原错误处理。
// 变异契约:若错误被吞掉、被映射成 ErrClaimRace 或提前停止,本测试红。
func TestRetryTx2_ExhaustsToLastError(t *testing.T) {
	calls, sleeps := 0, 0
	lastErr := fakePgErr("40001")
	err := retryTx2(context.Background(), "settle", settleTx2RetryPolicy, func(context.Context) error {
		calls++
		return lastErr
	}, countingSleep(&sleeps), zeroRand)
	if err != lastErr {
		t.Fatalf("Tx2 耗尽应返回最后原始错误,got %v want %v", err, lastErr)
	}
	if calls != 6 {
		t.Fatalf("Settle Tx2 应维持 6 次总尝试,得 %d", calls)
	}
	if sleeps != 5 {
		t.Fatalf("Settle Tx2 应维持 5 次退避,得 %d", sleeps)
	}
}

// TestRetryReserve_ExhaustsToClaimRace:一直 40001 → 耗尽后映射 ErrClaimRace(可重试
// 409+Retry-After),而非原 pgErr(不透明 500)。变异契约:把耗尽 return ErrClaimRace
// 改成 return err(原 pgErr)→ errors.Is(err, ErrClaimRace) 变 false,本测试红。
func TestRetryReserve_ExhaustsToClaimRace(t *testing.T) {
	calls := 0
	fn := func(context.Context) (*ReserveResult, error) {
		calls++
		return nil, fakePgErr("40001")
	}
	res, err := retryReserve(context.Background(), fn, countingSleep(new(int)), zeroRand)
	if !errors.Is(err, ErrClaimRace) {
		t.Fatalf("耗尽应映射 ErrClaimRace(可重试 409),得 %v", err)
	}
	if res != nil {
		t.Fatalf("耗尽应无 result,得 %v", res)
	}
	if calls != reserveRetryMax+1 {
		t.Fatalf("应跑 max+1=%d 次,得 %d", reserveRetryMax+1, calls)
	}
}

// TestRetryReserve_BusinessSentinelsNotRetried:业务哨兵与非 pg 错误立即原样返回、
// 绝不重试(它们是确定性业务结果)。变异契约:把 isReserveSerializationConflict 改成
// 对所有 err 返 true → 哨兵被重试 calls>1,本测试红。
func TestRetryReserve_BusinessSentinelsNotRetried(t *testing.T) {
	for _, sentinel := range []error{
		ErrClaimRace, ErrFingerprintConflict, ErrInsufficientBalance, ErrTenantInactive, errors.New("plain"),
	} {
		calls := 0
		fn := func(context.Context) (*ReserveResult, error) {
			calls++
			return nil, sentinel
		}
		_, err := retryReserve(context.Background(), fn, countingSleep(new(int)), zeroRand)
		if !errors.Is(err, sentinel) {
			t.Fatalf("哨兵 %v 应原样返回,得 %v", sentinel, err)
		}
		if calls != 1 {
			t.Fatalf("哨兵 %v 不应重试,跑了 %d 次", sentinel, calls)
		}
	}
}

// TestRetryReserve_ContextCancelStops:ctx 取消后立即停止重试(不再睡眠、不再重跑)。
func TestRetryReserve_ContextCancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls, sleeps := 0, 0
	fn := func(context.Context) (*ReserveResult, error) {
		calls++
		cancel() // 第一次调用后取消
		return nil, fakePgErr("40001")
	}
	_, err := retryReserve(ctx, fn, countingSleep(&sleeps), zeroRand)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx 取消应返 context.Canceled,得 %v", err)
	}
	if calls != 1 {
		t.Fatalf("ctx 取消后不应再跑,跑了 %d 次", calls)
	}
	if sleeps != 0 {
		t.Fatalf("ctx 取消应先于睡眠返回,睡了 %d 次", sleeps)
	}
}

// TestIsReserveSerializationConflict:只 40001/40P01 可重试;业务哨兵、其它 SQLSTATE、
// nil 均不可重试。变异契约:把判别改成只认 40001(漏 40P01)→ 40P01 用例红。
func TestIsReserveSerializationConflict(t *testing.T) {
	retryable := []error{fakePgErr("40001"), fakePgErr("40P01")}
	notRetryable := []error{
		fakePgErr("23505"), fakePgErr("23503"),
		ErrClaimRace, ErrFingerprintConflict, ErrInsufficientBalance, ErrTenantInactive,
		errors.New("x"), nil,
	}
	for _, e := range retryable {
		if !isReserveSerializationConflict(e) {
			t.Errorf("%v 应可重试", e)
		}
	}
	for _, e := range notRetryable {
		if isReserveSerializationConflict(e) {
			t.Errorf("%v 不应可重试", e)
		}
	}
}

// TestReserveBackoff_WithinBounds:decorrelated jitter 退避恒落 [base, cap];rand 取 0
// 得 base,rand 取满多轮逼近 cap(几何增长被封顶)。变异契约:去掉 cap 封顶 → 多轮后
// 越界,本测试红。
func TestReserveBackoff_WithinBounds(t *testing.T) {
	if got := reserveBackoff(reserveBackoffBase, zeroRand); got != reserveBackoffBase {
		t.Errorf("rand=0 应得 base=%v,得 %v", reserveBackoffBase, got)
	}
	full := func(n int64) int64 { return n } // rand 取满
	prev := reserveBackoffBase
	for i := 0; i < 12; i++ {
		d := reserveBackoff(prev, full)
		if d < reserveBackoffBase || d > reserveBackoffCap {
			t.Fatalf("退避 %v 越界 [%v,%v]", d, reserveBackoffBase, reserveBackoffCap)
		}
		prev = d
	}
	if prev != reserveBackoffCap {
		t.Errorf("满 rand 多轮后应达 cap=%v,得 %v", reserveBackoffCap, prev)
	}
}
