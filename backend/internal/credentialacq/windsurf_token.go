package credentialacq

import (
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func NewWindsurfCodeiumAuthTokenCandidate(tenantID, providerAccountID int64, actorID, token string) (CredentialCandidate, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return CredentialCandidate{}, ErrInvalidTokenShape
	}
	payload, err := json.Marshal(map[string]any{
		"session_token": token,
		"access_token":  token,
		"auth_header":   "Authorization",
		"token_source":  "windsurf_show_auth_token",
	})
	if err != nil {
		return CredentialCandidate{}, err
	}
	return CredentialCandidate{
		TenantID: tenantID, ProviderAccountID: providerAccountID,
		Vendor: credentialstore.VendorWindsurf, AuthMode: credentialstore.AuthModeOAuth,
		Payload: payload, ActorID: actorID,
		RedactedContext: map[string]any{"shape": "windsurf_codeium_auth_token"},
	}, nil
}
