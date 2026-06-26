package openai_codex

import (
	"context"
	"errors"
	"testing"
)

func TestOpenAICodexNewRefresherNilStoreFailsClosed(t *testing.T) {
	// 防回归：当没有接入 credentialstore.Store 时，credential-store 适配器必须
	// fail-closed（拒绝放行），而不是悄悄假装刷新已完成。
	err := NewRefresher(nil).Refresh(context.Background(), 42)
	if !errors.Is(err, ErrOpenAICodexStoreMissing) {
		t.Fatalf("Refresh err=%v, want ErrOpenAICodexStoreMissing", err)
	}
}
