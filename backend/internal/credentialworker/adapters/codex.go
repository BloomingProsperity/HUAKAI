package adapters

import (
	"context"
	"net/http"
	"time"
)

// CodexRefresh 复用 OpenAI / ChatGPT subscription OAuth 刷新逻辑。
// Codex 若后续确认独立 endpoint，可通过 OpenAI.Endpoint 注入，不复制刷新流程。
type CodexRefresh struct {
	OpenAI OpenAIRefresh
}

func (r CodexRefresh) RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) ([]byte, time.Time, error) {
	return r.OpenAI.RefreshForProvider(ctx, accountID, "codex", currentCredential)
}

func NewCodexRefresh(endpoint, clientID, scope string, client *http.Client) CodexRefresh {
	return CodexRefresh{OpenAI: OpenAIRefresh{
		Endpoint:   endpoint,
		ClientID:   clientID,
		Scope:      scope,
		HTTPClient: client,
	}}
}
