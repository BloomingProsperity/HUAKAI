package anthropicoauth

import "github.com/BloomingProsperity/HUAKAI/internal/credentialacq"

const (
	AnthropicAuthorizeURL       = "https://claude.ai/oauth/authorize"
	AnthropicTokenURL           = "https://api.anthropic.com/v1/oauth/token"
	AnthropicPublicCLIClientID  = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	defaultAnthropicRedirectURI = "http://localhost:54545/callback"
)

var anthropicOAuthScopes = []string{
	"user:profile",
	"user:inference",
	"user:sessions:claude_code",
	"user:mcp_servers",
	"user:file_upload",
}

func OAuthConfig(redirectURI string) credentialacq.OAuthClientConfig {
	if redirectURI == "" {
		redirectURI = defaultAnthropicRedirectURI
	}
	scopes := append([]string(nil), anthropicOAuthScopes...)
	return credentialacq.OAuthClientConfig{
		ClientID:    AnthropicPublicCLIClientID,
		AuthURL:     AnthropicAuthorizeURL,
		TokenURL:    AnthropicTokenURL,
		RedirectURI: redirectURI,
		Scopes:      scopes,
		Source:      credentialacq.ClientSourcePublicCLI,
	}
}
