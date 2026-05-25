package openai_codex

import (
	"context"
	"errors"
	"testing"
)

func TestOpenAICodexNewRefresherNilStoreFailsClosed(t *testing.T) {
	// Regression killed: the credential-store adapter must fail closed when no
	// credentialstore.Store is wired, rather than silently pretending refresh
	// happened.
	err := NewRefresher(nil).Refresh(context.Background(), 42)
	if !errors.Is(err, ErrOpenAICodexStoreMissing) {
		t.Fatalf("Refresh err=%v, want ErrOpenAICodexStoreMissing", err)
	}
}
