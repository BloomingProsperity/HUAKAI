package credentialacq

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDefaultExchangerRegistryIncludesAntigravityOAuthAlias(t *testing.T) {
	// Regression killed: Antigravity acquisition must be reachable through the
	// vendor-native antigravity/oauth key, not only the legacy
	// gemini/antigravity credentialstore mode.
	registry := DefaultExchangerRegistry()
	candidate, err := registry.Exchange(context.Background(), Session{
		TenantID: 1, ProviderAccountID: 42, Vendor: "antigravity", AuthMode: "oauth", ActorID: "owner",
	}, `{"session_token":"ag-session"}`)
	if err != nil {
		t.Fatalf("Exchange antigravity/oauth: %v", err)
	}
	if candidate.Vendor != "antigravity" || candidate.AuthMode != "oauth" {
		t.Fatalf("candidate mode=%s/%s, want antigravity/oauth", candidate.Vendor, candidate.AuthMode)
	}
	var payload map[string]string
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["session_token"] != "ag-session" {
		t.Fatalf("session_token=%q, want ag-session", payload["session_token"])
	}
}
