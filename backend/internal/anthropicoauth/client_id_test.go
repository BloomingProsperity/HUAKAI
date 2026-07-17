package anthropicoauth

import "testing"

func TestAnthropicTokenEndpointMatchesCurrentApprovedProfile(t *testing.T) {
	const expected = "https://platform.claude.com/v1/oauth/token"
	if AnthropicTokenURL != expected {
		t.Fatalf("AnthropicTokenURL=%q want %q", AnthropicTokenURL, expected)
	}
	if AnthropicRefreshTokenURL != expected {
		t.Fatalf("AnthropicRefreshTokenURL=%q want %q", AnthropicRefreshTokenURL, expected)
	}
}
