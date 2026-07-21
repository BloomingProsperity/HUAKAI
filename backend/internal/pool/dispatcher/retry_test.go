package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCanFallbackAfterPASRError(t *testing.T) {
	if !canFallbackAfterPASRError(ErrPASRPreMutationFail) {
		t.Fatal("pre-mutation failure should allow fallback")
	}
	if canFallbackAfterPASRError(ErrPASRPostMutationFail) {
		t.Fatal("post-mutation failure must not fallback")
	}
	if canFallbackAfterPASRError(fmt.Errorf("wrapped: %w", ErrBindingConcurrencyLimited)) {
		t.Fatal("binding 并发饱和是终态，禁止 fallback 绕过或重复选号")
	}
	if canFallbackAfterPASRError(fmt.Errorf("wrapped: %w", ErrGroupPolicyUnavailable)) {
		t.Fatal("分组策略真相未知是终态，禁止 fallback 到其它选号器或账号池")
	}
	if !canFallbackAfterPASRError(errors.New("list accounts failed")) {
		t.Fatal("non-mutating generic error should allow fallback")
	}
}

func TestSlotAcquireRetryRetriesSerializationFailure(t *testing.T) {
	// 变异检查:去掉对 SQLSTATE 40001 的重试,本测试就会返回第一个被包装的错误。
	want := &AcquireResult{AcquisitionToken: uuid.New()}
	attempts := 0

	got, err := retrySerializableSlotAcquire(context.Background(), func(context.Context) (*AcquireResult, error) {
		attempts++
		if attempts == 1 {
			return nil, fmt.Errorf("wrapped serialization failure: %w", &pgconn.PgError{Code: "40001"})
		}
		return want, nil
	})
	if err != nil {
		t.Fatalf("retrySerializableSlotAcquire returned err=%v", err)
	}
	if got != want {
		t.Fatalf("retrySerializableSlotAcquire result=%p want %p", got, want)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d want 2", attempts)
	}
}

func TestSlotAcquireRetryDoesNotRetryCapacityMiss(t *testing.T) {
	attempts := 0
	_, err := retrySerializableSlotAcquire(context.Background(), func(context.Context) (*AcquireResult, error) {
		attempts++
		return nil, ErrNoSlotAvailable
	})
	if !errors.Is(err, ErrNoSlotAvailable) {
		t.Fatalf("err=%v want ErrNoSlotAvailable", err)
	}
	if attempts != 1 {
		t.Fatalf("capacity miss attempts=%d want 1", attempts)
	}
}
