package gemini

import (
	"context"
	"errors"
	"testing"
)

func TestGeminiNewRefresherNilStoreFailsClosed(t *testing.T) {
	// 锁定回归：当未接入 credentialstore.Store 时，credential-store adapter 必须
	// fail closed，而不能静默地假装刷新已发生。
	err := NewRefresher(nil).Refresh(context.Background(), 42)
	if !errors.Is(err, ErrGeminiStoreMissing) {
		t.Fatalf("Refresh err=%v, want ErrGeminiStoreMissing", err)
	}
}
