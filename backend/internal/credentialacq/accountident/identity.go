// Package accountident extracts upstream-provider account identity (account id +
// email) from the OAuth token-exchange response or id_token captured at credential
// acquisition. It exists as its own responsibility-scoped package so the larger
// credentialacq package does not absorb this logic.
//
// The identity captured here is account-management metadata, NOT an authorization
// decision. It is read from the provider response AFTER the provider's own auth
// server has issued the token over the exchange HUAKAI initiated, so id_token claim
// parsing is intentionally unverified — the value must never feed any access-control,
// billing, or quota path. Every extractor is fail-open: a parse error returns an empty
// manual-sourced Identity and never an error, so identity capture can never block
// credential acquisition.
package accountident

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Provenance values for Identity.Source. They let the admin surface distinguish an
// auto-detected upstream id from an operator-entered fallback, and feed audit.
const (
	SourceManual             = "manual"
	SourceAnthropicAccountID = "anthropic_account_uuid"
	SourceChatGPTJWTClaim    = "chatgpt_jwt_claim"
	SourceGoogleIDTokenSub   = "google_id_token_sub"
)

// openAIAuthClaimKey is the namespaced custom claim in the ChatGPT/Codex id_token that
// carries the chatgpt account identifier. It is a provider-defined claim name (a public
// protocol fact, like a header name), not borrowed implementation.
const openAIAuthClaimKey = "https://api.openai.com/auth"

// Identity is the non-secret upstream account metadata captured at acquisition.
// An empty Identity (or one with Source == SourceManual) means extraction yielded
// nothing and the manual/operator value should win.
type Identity struct {
	AccountID string
	Email     string
	Source    string
}

// Empty reports whether the identity carries no upstream account id.
func (i Identity) Empty() bool {
	return strings.TrimSpace(i.AccountID) == ""
}

// manualIdentity is the fail-open result: no id, no email, manual provenance.
func manualIdentity() Identity {
	return Identity{Source: SourceManual}
}

// ParseJWTClaimsUnverified splits a compact JWT on ".", base64url-decodes the payload
// segment (re-adding the padding the compact encoding omits), and unmarshals it into a
// generic claims map. The signature is intentionally NOT verified: this is identity
// metadata introspection of a token the provider auth server already issued, not an
// authentication step. Callers must treat the result as untrusted display metadata.
func ParseJWTClaimsUnverified(idToken string) (map[string]any, error) {
	trimmed := strings.TrimSpace(idToken)
	if trimmed == "" {
		return nil, fmt.Errorf("accountident: empty id_token")
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("accountident: id_token must have 3 segments, got %d", len(parts))
	}
	decoded, err := decodeBase64URLSegment(parts[1])
	if err != nil {
		return nil, fmt.Errorf("accountident: decode claims segment: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("accountident: unmarshal claims: %w", err)
	}
	return claims, nil
}

// decodeBase64URLSegment decodes a base64url segment that may be missing its trailing
// padding (compact JWT serialization drops it). It restores padding so a fixed-alphabet
// decoder can read it; without this, segments whose length is not a multiple of 4 fail.
func decodeBase64URLSegment(segment string) ([]byte, error) {
	segment = strings.TrimSpace(segment)
	if pad := len(segment) % 4; pad != 0 {
		segment += strings.Repeat("=", 4-pad)
	}
	return base64.URLEncoding.DecodeString(segment)
}

// ExtractAnthropic builds an Identity from the Anthropic token-exchange response fields.
// accountUUID is the response account.uuid, accountEmail the account.email_address, and
// topEmail the top-level email. The account uuid is the stable upstream identifier;
// email prefers the account-scoped value then the top-level value.
func ExtractAnthropic(accountUUID, accountEmail, topEmail string) Identity {
	id := strings.TrimSpace(accountUUID)
	if id == "" {
		return manualIdentity()
	}
	return Identity{
		AccountID: id,
		Email:     firstNonEmpty(accountEmail, topEmail),
		Source:    SourceAnthropicAccountID,
	}
}

// ExtractChatGPT builds an Identity from a ChatGPT/Codex id_token plus the token-body
// fallback. Precedence for the account id: the chatgpt account id inside the namespaced
// auth claim, then the token-body value, then the standard subject claim. Email comes
// from the email claim. A malformed/empty id_token does not abort: if the body fallback
// is present it is still used, otherwise a manual Identity is returned.
func ExtractChatGPT(idToken, bodyAccountID string) Identity {
	bodyAccountID = strings.TrimSpace(bodyAccountID)
	claims, err := ParseJWTClaimsUnverified(idToken)
	if err != nil {
		if bodyAccountID != "" {
			return Identity{AccountID: bodyAccountID, Source: SourceChatGPTJWTClaim}
		}
		return manualIdentity()
	}
	claimAccountID := chatgptAccountIDFromClaims(claims)
	subject := stringClaim(claims, "sub")
	accountID := firstNonEmpty(claimAccountID, bodyAccountID, subject)
	if accountID == "" {
		return manualIdentity()
	}
	return Identity{
		AccountID: accountID,
		Email:     stringClaim(claims, "email"),
		Source:    SourceChatGPTJWTClaim,
	}
}

// ExtractGemini builds an Identity from a Google/Gemini id_token. account id is the
// subject claim; email is the email claim, with the supplied userinfoEmail as fallback.
// A malformed/empty id_token returns a manual Identity (the live userinfo HTTP lookup is
// deferred to a roadmap follow-up to avoid new egress inside the SSRF-guarded path).
func ExtractGemini(idToken, userinfoEmail string) Identity {
	claims, err := ParseJWTClaimsUnverified(idToken)
	if err != nil {
		return manualIdentity()
	}
	subject := stringClaim(claims, "sub")
	if subject == "" {
		return manualIdentity()
	}
	return Identity{
		AccountID: subject,
		Email:     firstNonEmpty(stringClaim(claims, "email"), userinfoEmail),
		Source:    SourceGoogleIDTokenSub,
	}
}

// chatgptAccountIDFromClaims reads the chatgpt account id from the namespaced auth claim
// object. The claim is an arbitrary JSON object; it is read defensively.
func chatgptAccountIDFromClaims(claims map[string]any) string {
	raw, ok := claims[openAIAuthClaimKey]
	if !ok {
		return ""
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	return stringClaim(obj, "chatgpt_account_id")
}

// stringClaim reads a trimmed string value for key from a generic claims map.
func stringClaim(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}
	if v, ok := claims[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// firstNonEmpty returns the first trimmed non-empty value.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
