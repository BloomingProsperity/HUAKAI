package dispatcher

import (
	"errors"
	"testing"
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
