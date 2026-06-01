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
	if !canFallbackAfterPASRError(errors.New("list accounts failed")) {
		t.Fatal("non-mutating generic error should allow fallback")
	}
}

func TestSlotAcquireRetryRetriesSerializationFailure(t *testing.T) {
	// Mutation check: remove the SQLSTATE 40001 retry and this test returns the first wrapped error.
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
