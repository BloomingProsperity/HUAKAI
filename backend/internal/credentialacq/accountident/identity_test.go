package accountident

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// buildJWT assembles a compact 3-segment JWT whose payload base64url-encodes the given
// claims. The header/signature segments are inert placeholders (signature is never
// verified by the extractor). padPayload=false strips trailing padding so the fixture
// exercises the padding-restore branch; padPayload=true keeps standard padding.
func buildJWT(t *testing.T, claims map[string]any, stripPadding bool) string {
	t.Helper()
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.URLEncoding.EncodeToString(raw)
	if stripPadding {
		payload = strings.TrimRight(payload, "=")
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	return header + "." + payload + ".sig"
}

func TestParseJWTClaimsUnverified_DecodesPayload(t *testing.T) {
	// Discriminating fixture: the marshaled payload length is deliberately not a
	// multiple of 4 once padding is stripped, so the padding-restore branch is the
	// only thing that lets the decode succeed. Confirm that property holds.
	claims := map[string]any{
		"sub":   "u-123",
		"email": "a@b.com",
		openAIAuthClaimKey: map[string]any{
			"chatgpt_account_id": "acct-XYZ",
		},
	}
	token := buildJWT(t, claims, true /* stripPadding */)
	stripped := strings.Split(token, ".")[1]
	if len(stripped)%4 == 0 {
		t.Fatalf("fixture not discriminating: stripped payload len %d is already a multiple of 4; padding branch would not be exercised", len(stripped))
	}

	got, err := ParseJWTClaimsUnverified(token)
	if err != nil {
		// MUTATION: dropping the padding-restore branch in decodeBase64URLSegment
		// makes URLEncoding.DecodeString reject this segment -> this assertion goes red.
		t.Fatalf("ParseJWTClaimsUnverified: unexpected error %v (padding-restore branch likely missing)", err)
	}
	if got["sub"] != "u-123" {
		t.Fatalf("sub = %v, want u-123", got["sub"])
	}
	if got["email"] != "a@b.com" {
		t.Fatalf("email = %v, want a@b.com", got["email"])
	}
	auth, ok := got[openAIAuthClaimKey].(map[string]any)
	if !ok {
		t.Fatalf("auth claim missing or wrong type: %T", got[openAIAuthClaimKey])
	}
	if auth["chatgpt_account_id"] != "acct-XYZ" {
		t.Fatalf("chatgpt_account_id = %v, want acct-XYZ", auth["chatgpt_account_id"])
	}
}

func TestExtractChatGPT_PrefersJWTClaimOverBodyAndSub(t *testing.T) {
	// All three candidate sources carry DISTINCT values so a wrong-precedence bug
	// cannot accidentally pass: only reading the JWT auth claim yields acct-FROM-JWT.
	claims := map[string]any{
		"sub":   "u-sub",
		"email": "jwt@example.com",
		openAIAuthClaimKey: map[string]any{
			"chatgpt_account_id": "acct-FROM-JWT",
		},
	}
	token := buildJWT(t, claims, false)

	id := ExtractChatGPT(token, "acct-FROM-BODY")
	if id.AccountID != "acct-FROM-JWT" {
		// MUTATION: returning the body value or sub instead of the claim -> red.
		t.Fatalf("AccountID = %q, want acct-FROM-JWT (claim must win over body and sub)", id.AccountID)
	}
	if id.Source != SourceChatGPTJWTClaim {
		t.Fatalf("Source = %q, want %q", id.Source, SourceChatGPTJWTClaim)
	}
	if id.Email != "jwt@example.com" {
		t.Fatalf("Email = %q, want jwt@example.com", id.Email)
	}
}

func TestExtractChatGPT_FallsBackToBodyThenSub(t *testing.T) {
	// No auth claim -> body wins over sub.
	claims := map[string]any{"sub": "u-sub"}
	token := buildJWT(t, claims, false)
	id := ExtractChatGPT(token, "acct-FROM-BODY")
	if id.AccountID != "acct-FROM-BODY" {
		t.Fatalf("AccountID = %q, want acct-FROM-BODY (body must win over sub when claim absent)", id.AccountID)
	}

	// No auth claim, no body -> sub is the last resort.
	id = ExtractChatGPT(token, "")
	if id.AccountID != "u-sub" {
		t.Fatalf("AccountID = %q, want u-sub (sub is last-resort)", id.AccountID)
	}
}

func TestExtractAnthropic_UsesAccountUUIDAndEmail(t *testing.T) {
	id := ExtractAnthropic("acc-uuid-1", "acc@x.com", "" /* topEmail empty */)
	if id.AccountID != "acc-uuid-1" {
		// MUTATION: reverting exchanger.go to drop the new uuid field passes ""
		// here -> AccountID empty -> red.
		t.Fatalf("AccountID = %q, want acc-uuid-1", id.AccountID)
	}
	if id.Email != "acc@x.com" {
		t.Fatalf("Email = %q, want acc@x.com", id.Email)
	}
	if id.Source != SourceAnthropicAccountID {
		t.Fatalf("Source = %q, want %q", id.Source, SourceAnthropicAccountID)
	}
}

func TestExtractAnthropic_EmailPrefersAccountThenTop(t *testing.T) {
	id := ExtractAnthropic("acc-uuid-2", "", "top@x.com")
	if id.Email != "top@x.com" {
		t.Fatalf("Email = %q, want top@x.com (top-level fallback)", id.Email)
	}
}

func TestExtract_FailClosedToManual(t *testing.T) {
	// Malformed / empty id_token must never abort acquisition: each extractor returns
	// an empty manual Identity (no AccountID), and ParseJWTClaimsUnverified itself
	// reports the error so the in-test contract is explicit.
	for _, bad := range []string{"", "not.a.jwt.x", "onlyonesegment", "two.segments"} {
		if _, err := ParseJWTClaimsUnverified(bad); err == nil {
			t.Fatalf("ParseJWTClaimsUnverified(%q): expected error", bad)
		}

		// ChatGPT with no body fallback -> manual.
		if id := ExtractChatGPT(bad, ""); id.Source != SourceManual || id.AccountID != "" {
			// MUTATION: if the extractor propagated the decode error or returned a
			// non-manual identity here, this goes red.
			t.Fatalf("ExtractChatGPT(%q): got %+v, want empty manual identity", bad, id)
		}

		// Gemini -> manual.
		if id := ExtractGemini(bad, ""); id.Source != SourceManual || id.AccountID != "" {
			t.Fatalf("ExtractGemini(%q): got %+v, want empty manual identity", bad, id)
		}
	}

	// Anthropic with empty uuid -> manual (no error type exists; just empty result).
	if id := ExtractAnthropic("", "any@x.com", ""); id.Source != SourceManual || id.AccountID != "" {
		t.Fatalf("ExtractAnthropic empty uuid: got %+v, want empty manual identity", id)
	}
}

func TestExtractGemini_UsesSubAndEmail(t *testing.T) {
	claims := map[string]any{"sub": "g-sub-1", "email": "g@x.com"}
	token := buildJWT(t, claims, false)
	id := ExtractGemini(token, "userinfo@x.com")
	if id.AccountID != "g-sub-1" {
		t.Fatalf("AccountID = %q, want g-sub-1", id.AccountID)
	}
	if id.Email != "g@x.com" {
		t.Fatalf("Email = %q, want g@x.com (email claim wins over userinfo fallback)", id.Email)
	}
	if id.Source != SourceGoogleIDTokenSub {
		t.Fatalf("Source = %q, want %q", id.Source, SourceGoogleIDTokenSub)
	}

	// No email claim -> userinfo fallback.
	token2 := buildJWT(t, map[string]any{"sub": "g-sub-2"}, false)
	id2 := ExtractGemini(token2, "userinfo@x.com")
	if id2.Email != "userinfo@x.com" {
		t.Fatalf("Email = %q, want userinfo@x.com (fallback)", id2.Email)
	}
}
