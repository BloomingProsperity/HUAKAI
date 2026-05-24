package routecontrol_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/routecontrol"
)

// Tests in this file are MUTATION-RESISTANT per CLAUDE.md #14: each test
// names the specific regression it catches in its leading comment. Before
// landing this commit, the author mentally introduces each defect and
// confirms the test would go red.

// =============================================================================
// ParseClientCredential — happy path
// =============================================================================

// TC-1 — empty canonical → ErrMissingClientCredential.
// Defect caught: parser returns ok+zero credential when given empty input
// (would silently downgrade requests to unauthenticated downstream).
func TestParse_Empty(t *testing.T) {
	c, err := routecontrol.ParseClientCredential("")
	if !errors.Is(err, routecontrol.ErrMissingClientCredential) {
		t.Fatalf("want ErrMissingClientCredential, got %v", err)
	}
	if !c.IsZero() {
		t.Fatalf("want zero credential, got kind=%q", c.Kind())
	}
}

// TC-2 — "bearer:<token>" → KindBearer.
// Defect caught: parser mis-classifies bearer as x-api-key (would change
// the audit fingerprint and confuse confused-deputy detection).
func TestParse_Bearer(t *testing.T) {
	c, err := routecontrol.ParseClientCredential("bearer:hk_test_PARSER_BEARER_TOKEN_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Kind() != routecontrol.KindBearer {
		t.Fatalf("want KindBearer, got %q", c.Kind())
	}
	if c.IsZero() {
		t.Fatal("want non-zero credential")
	}
}

// TC-3 — "x-api-key:<token>" → KindXAPIKey.
// Defect caught: parser ignores x-api-key kind entirely (would silently
// drop all Anthropic-style x-api-key inbound).
func TestParse_XAPIKey(t *testing.T) {
	c, err := routecontrol.ParseClientCredential("x-api-key:hk_test_PARSER_XAPIKEY_TOKEN_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Kind() != routecontrol.KindXAPIKey {
		t.Fatalf("want KindXAPIKey, got %q", c.Kind())
	}
}

// =============================================================================
// ParseClientCredential — failure paths
// =============================================================================

// TC-4 — no colon separator → ErrInvalidClientCredential.
// Defect caught: parser treats the whole string as secret with kind="" (would
// produce a zero-kind credential that bypasses kind-dispatch downstream).
// MUTATION CHECK: change `if idx < 0 { return ... }` to fall through; this
// test MUST then go red because the parser would return ok with empty kind.
func TestParse_NoColonSeparator(t *testing.T) {
	_, err := routecontrol.ParseClientCredential("hk_test_NO_SEPARATOR_TOKEN_001")
	if !errors.Is(err, routecontrol.ErrInvalidClientCredential) {
		t.Fatalf("want ErrInvalidClientCredential, got %v", err)
	}
}

// TC-5 — unknown kind prefix → ErrInvalidClientCredential.
// Defect caught: parser accepts arbitrary "basic:" / "oauth:" / "token:"
// prefixes (would allow attackers to bypass P2-A4's two-kind restriction).
// MUTATION CHECK: replace the switch default with `kind = CredentialKind(prefix)`;
// this test MUST then go red.
func TestParse_UnknownKindPrefix(t *testing.T) {
	cases := []string{
		"basic:dXNlcjpwYXNz",
		"oauth:hk_test_OAUTH_NOT_ALLOWED_001",
		"token:hk_test_TOKEN_NOT_ALLOWED_001",
		":hk_test_EMPTY_PREFIX_TOKEN_001", // empty prefix is also unknown
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			_, err := routecontrol.ParseClientCredential(tc)
			if !errors.Is(err, routecontrol.ErrInvalidClientCredential) {
				t.Fatalf("want ErrInvalidClientCredential, got %v", err)
			}
		})
	}
}

// TC-6 — empty secret after kind → ErrInvalidClientCredential.
// Defect caught: parser returns kind=bearer with secret="" (would hand empty
// bearer to APIKeyResolver, producing confusing 401 with no audit fingerprint).
func TestParse_EmptySecret(t *testing.T) {
	cases := []string{
		"bearer:",
		"x-api-key:",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			_, err := routecontrol.ParseClientCredential(tc)
			if !errors.Is(err, routecontrol.ErrInvalidClientCredential) {
				t.Fatalf("want ErrInvalidClientCredential, got %v", err)
			}
		})
	}
}

// TC-7 — whitespace-only secret after kind → ErrInvalidClientCredential.
// Defect caught: parser accepts "bearer:   " as kind=bearer + secret="   "
// (which would then go to the auth resolver as a valid-looking but
// authentication-impossible bearer).
// MUTATION CHECK: remove the TrimSpace + secret == "" guard; this test
// MUST then go red because parser would accept whitespace-secret.
func TestParse_WhitespaceOnlySecret(t *testing.T) {
	cases := []string{
		"bearer:   ",
		"bearer:\t",
		"bearer:\n",
		"x-api-key:    ",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			_, err := routecontrol.ParseClientCredential(tc)
			if !errors.Is(err, routecontrol.ErrInvalidClientCredential) {
				t.Fatalf("want ErrInvalidClientCredential, got %v", err)
			}
		})
	}
}

// =============================================================================
// ResolverRequest — secret handoff to APIKeyResolver
// =============================================================================

// TC-8 — bearer credential becomes Authorization: Bearer in resolver request.
// Defect caught: ResolverRequest drops the Authorization header.
func TestResolverRequest_Bearer(t *testing.T) {
	const secret = "hk_test_RR_BEARER_TOKEN_001"
	c, err := routecontrol.ParseClientCredential("bearer:" + secret)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := c.ResolverRequest(context.Background())
	if err != nil {
		t.Fatalf("ResolverRequest: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer "+secret {
		t.Fatalf("want Authorization=%q, got %q", "Bearer "+secret, got)
	}
}

// TC-9 — x-api-key credential is NORMALIZED into Authorization: Bearer.
// Defect caught: ResolverRequest passes x-api-key through unchanged instead
// of normalizing — APIKeyResolver only reads Authorization, so x-api-key
// inbound would silently fail to authenticate (P2-A4 violation).
// MUTATION CHECK: change the Header construction to set "X-Api-Key" instead
// of "Authorization"; this test MUST then go red.
func TestResolverRequest_XAPIKeyNormalized(t *testing.T) {
	const secret = "hk_test_RR_XAPIKEY_NORMALIZE_TOKEN_001"
	c, err := routecontrol.ParseClientCredential("x-api-key:" + secret)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := c.ResolverRequest(context.Background())
	if err != nil {
		t.Fatalf("ResolverRequest: %v", err)
	}
	// Discriminating: Authorization MUST be Bearer-prefixed with the same secret.
	if got := req.Header.Get("Authorization"); got != "Bearer "+secret {
		t.Fatalf("want Authorization=Bearer %s, got %q", secret, got)
	}
	// Discriminating: X-Api-Key MUST NOT appear in the resolver request
	// (resolver only reads Authorization; sending both would split the audit).
	if got := req.Header.Get("X-Api-Key"); got != "" {
		t.Fatalf("X-Api-Key leaked into resolver request: %q", got)
	}
}

// TC-10 — context propagates through ResolverRequest.
// Defect caught: ResolverRequest hard-codes context.Background, breaking
// cancellation chains that the caller wanted to honor.
func TestResolverRequest_ContextPropagates(t *testing.T) {
	c, _ := routecontrol.ParseClientCredential("bearer:hk_test_RR_CTX_TOKEN_001")
	type ctxKey string
	const probeKey ctxKey = "routecontrol-test-probe"
	ctx := context.WithValue(context.Background(), probeKey, "probe-value")
	req, err := c.ResolverRequest(ctx)
	if err != nil {
		t.Fatalf("ResolverRequest: %v", err)
	}
	if got := req.Context().Value(probeKey); got != "probe-value" {
		t.Fatalf("context not propagated, got %v", got)
	}
}

// TC-11 — ResolverRequest on zero credential → ErrMissingClientCredential.
// Defect caught: ResolverRequest panics on zero value or returns a valid
// request with empty bearer (would produce empty-secret 401).
func TestResolverRequest_ZeroValue(t *testing.T) {
	var c routecontrol.ClientCredential
	_, err := c.ResolverRequest(context.Background())
	if !errors.Is(err, routecontrol.ErrMissingClientCredential) {
		t.Fatalf("want ErrMissingClientCredential, got %v", err)
	}
}

// TC-12 — nil ctx defaults to context.Background, does NOT panic.
// Defect caught: nil-ctx panic crashes the gRPC handler.
func TestResolverRequest_NilContextSafe(t *testing.T) {
	c, _ := routecontrol.ParseClientCredential("bearer:hk_test_RR_NILCTX_TOKEN_001")
	req, err := c.ResolverRequest(nil)
	if err != nil {
		t.Fatalf("nil ctx not handled: %v", err)
	}
	if req == nil || req.Context() == nil {
		t.Fatal("request or context missing")
	}
}

// =============================================================================
// Fingerprint — determinism, distinctness, kind-binding
// =============================================================================

// TC-13 — Fingerprint is deterministic (same input → same 8-hex output).
// Defect caught: fingerprint uses random salt; audit log correlation breaks
// across two requests with the same credential.
func TestFingerprint_Deterministic(t *testing.T) {
	c1, _ := routecontrol.ParseClientCredential("bearer:hk_test_FP_DETERMINISTIC_001")
	c2, _ := routecontrol.ParseClientCredential("bearer:hk_test_FP_DETERMINISTIC_001")
	fp1, fp2 := c1.Fingerprint(), c2.Fingerprint()
	if fp1 != fp2 {
		t.Fatalf("non-deterministic: %q vs %q", fp1, fp2)
	}
	if len(fp1) != 8 {
		t.Fatalf("want 8 hex chars, got %d (%q)", len(fp1), fp1)
	}
}

// TC-14 — Fingerprint differs for different secrets.
// Defect caught: fingerprint always identical (broken hash); all credentials
// collide in audit log, identity ambiguity escapes detection.
// MUTATION CHECK: hard-code Fingerprint() to return "abcd1234"; this test
// MUST then go red.
func TestFingerprint_DiffersOnDiffSecret(t *testing.T) {
	c1, _ := routecontrol.ParseClientCredential("bearer:hk_test_FP_DIFF_A_001")
	c2, _ := routecontrol.ParseClientCredential("bearer:hk_test_FP_DIFF_B_002")
	if c1.Fingerprint() == c2.Fingerprint() {
		t.Fatalf("fingerprints collided: %q", c1.Fingerprint())
	}
}

// TC-15 — Fingerprint binds kind to secret (confused-deputy detection).
// Defect caught: fingerprint hashes only the secret, so the same token
// presented as bearer vs x-api-key produces an identical fingerprint —
// hides confused-deputy attempts from the audit log.
// MUTATION CHECK: remove `h.Write([]byte(c.kind))` from Fingerprint; this
// test MUST then go red.
func TestFingerprint_DiffersByKind(t *testing.T) {
	c1, _ := routecontrol.ParseClientCredential("bearer:hk_test_FP_KIND_BIND_001")
	c2, _ := routecontrol.ParseClientCredential("x-api-key:hk_test_FP_KIND_BIND_001")
	if c1.Fingerprint() == c2.Fingerprint() {
		t.Fatalf("kind not bound: %q (bearer == x-api-key with same secret)", c1.Fingerprint())
	}
}

// TC-16 — Zero value Fingerprint returns "[empty]" sentinel.
// Defect caught: Fingerprint panics or returns "" on zero value, breaking
// log lines that uniformly format the fingerprint marker.
func TestFingerprint_ZeroValue(t *testing.T) {
	var c routecontrol.ClientCredential
	if got := c.Fingerprint(); got != "[empty]" {
		t.Fatalf("want [empty], got %q", got)
	}
}

// =============================================================================
// PII safety: Format / String / GoString never leak the raw secret (P2-A5)
// =============================================================================

// TC-17 — fmt verbs (%v / %+v / %s / %#v) NEVER leak the raw secret.
// Defect caught: a developer adds a debug log with %+v and inadvertently
// dumps the secret to logs. The Format/String/GoString overrides prevent
// reflect-based default formatting from walking unexported fields.
// MUTATION CHECK: delete the Format() method; %+v then reflect-walks and
// embeds the secret, this test MUST go red.
func TestFormat_NoRawSecretLeak(t *testing.T) {
	const secret = "hk_test_FORMAT_LEAK_PROBE_NEVER_LEAK_001"
	c, _ := routecontrol.ParseClientCredential("bearer:" + secret)

	cases := []struct {
		name, value string
	}{
		{"%v", fmt.Sprintf("%v", c)},
		{"%+v", fmt.Sprintf("%+v", c)},
		{"%s", fmt.Sprintf("%s", c)},
		{"%#v", fmt.Sprintf("%#v", c)},
		{"%q", fmt.Sprintf("%q", c)},
		{"%d", fmt.Sprintf("%d", c)}, // unknown verb falls through to String()
		{"String()", c.String()},
		{"GoString()", c.GoString()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.value, secret) {
				t.Errorf("formatted output leaked secret: %s", tc.value)
			}
			// Discriminating: must contain kind and sha256 markers so reviewers
			// can confirm the safe path was taken (vs the formatter silently
			// returning empty).
			if !strings.Contains(tc.value, "kind=bearer") {
				t.Errorf("formatted output missing kind marker: %s", tc.value)
			}
			if !strings.Contains(tc.value, "sha256=") {
				t.Errorf("formatted output missing fingerprint marker: %s", tc.value)
			}
		})
	}
}

// TC-18 — error wrappers NEVER embed the raw secret.
// Defect caught: a careless fmt.Errorf("bad token: %s", canonical) returns
// the secret to the caller. Verified by exhaustively triggering every parser
// error path with a known-distinctive secret and asserting the secret never
// appears in err.Error().
// MUTATION CHECK: change ParseClientCredential to include canonical in the
// "missing separator" error; this test MUST then go red.
func TestErrors_NeverEmbedRawSecret(t *testing.T) {
	const secret = "hk_test_ERROR_LEAK_PROBE_NEVER_LEAK_001"

	triggers := []struct {
		name      string
		canonical string
	}{
		{"no-separator", secret},                         // secret IS the canonical, no colon
		{"unknown-kind", "basic:" + secret},              // unknown prefix carrying secret in tail
		{"empty-prefix", ":" + secret},                   // empty prefix → unknown kind
		{"weird-prefix", "BearerCap:" + secret},          // mixed-case prefix → unknown kind
		// empty / whitespace cases produce errors that contain no secret by construction
	}
	for _, tg := range triggers {
		t.Run(tg.name, func(t *testing.T) {
			_, err := routecontrol.ParseClientCredential(tg.canonical)
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error message leaked secret: %s", err.Error())
			}
			// Discriminating: error must wrap ErrInvalidClientCredential so
			// callers can map to the right gRPC status code.
			if !errors.Is(err, routecontrol.ErrInvalidClientCredential) {
				t.Errorf("error does not wrap ErrInvalidClientCredential: %v", err)
			}
		})
	}
}

// =============================================================================
// Sentinel error stability — service.go relies on these for status mapping
// =============================================================================

// TC-19 — all Phase 2A acceptance-gate sentinels are exported and distinct.
// Defect caught: an editor accidentally merges two sentinels into one
// (e.g., `ErrAuthBackend = ErrInvalidClientCredential`), breaking the
// status-code mapping the service layer needs.
func TestErrorSentinels_DistinctAndExported(t *testing.T) {
	sentinels := []error{
		routecontrol.ErrMissingClientCredential,
		routecontrol.ErrInvalidClientCredential,
		routecontrol.ErrTenantIDMismatch,
		routecontrol.ErrAuthBackend,
		routecontrol.ErrRouteContractIncomplete,
	}
	seen := make(map[string]bool, len(sentinels))
	for _, e := range sentinels {
		if e == nil {
			t.Errorf("nil sentinel in list")
			continue
		}
		msg := e.Error()
		if seen[msg] {
			t.Errorf("duplicate sentinel message: %q", msg)
		}
		seen[msg] = true
		if !strings.HasPrefix(msg, "routecontrol: ") {
			t.Errorf("sentinel %q missing package prefix", msg)
		}
	}
}

// =============================================================================
// Zero-value safety — methods do not panic on default ClientCredential{}
// =============================================================================

// TC-20 — every public method handles zero value without panic.
// Defect caught: a method dereferences c.secret via unprotected length / index
// access and panics on the zero value.
func TestZeroValue_AllMethodsSafe(t *testing.T) {
	var c routecontrol.ClientCredential

	if !c.IsZero() {
		t.Error("IsZero false on zero value")
	}
	if c.Kind() != "" {
		t.Errorf("Kind = %q on zero value", c.Kind())
	}
	if got := c.Fingerprint(); got != "[empty]" {
		t.Errorf("Fingerprint = %q on zero value", got)
	}
	if got := c.String(); got != "ClientCredential{empty}" {
		t.Errorf("String = %q on zero value", got)
	}
	// Format / GoString go through String() — verified above by formatter test.
	_, err := c.ResolverRequest(context.Background())
	if !errors.Is(err, routecontrol.ErrMissingClientCredential) {
		t.Errorf("ResolverRequest zero-value err = %v", err)
	}
}

// =============================================================================
// Edge case: ResolverRequest does NOT mutate the underlying secret across calls
// =============================================================================

// TC-21 — repeated ResolverRequest calls produce independent requests.
// Defect caught: ResolverRequest re-uses a single http.Request and mutating
// one breaks subsequent calls (concurrency hazard).
func TestResolverRequest_IndependentCopies(t *testing.T) {
	c, _ := routecontrol.ParseClientCredential("bearer:hk_test_RR_INDEPENDENT_001")
	r1, _ := c.ResolverRequest(context.Background())
	r2, _ := c.ResolverRequest(context.Background())
	if r1 == r2 {
		t.Fatal("ResolverRequest returned the same pointer twice (shared state)")
	}
	// Mutating one should not affect the other.
	r1.Header.Set("X-Probe", "mutated")
	if r2.Header.Get("X-Probe") != "" {
		t.Fatal("mutation on r1 leaked to r2")
	}
}

// =============================================================================
// Discriminating sanity: the parser does NOT accept input that *almost*
// matches the canonical form (e.g., colon at position 0 with secret after).
// =============================================================================

// TC-22 — canonical form is strict — leading colon, leading space, etc.
// Defect caught: parser becomes permissive ("loose parsing") and accepts
// inputs that would not roundtrip with the Rust canonicalizer.
func TestParse_StrictCanonicalForm(t *testing.T) {
	rejects := []string{
		" bearer:hk_test_LEADING_SPACE_001",  // leading whitespace before kind
		"BEARER:hk_test_UPPER_KIND_001",      // upper-case kind not recognized
		"Bearer:hk_test_TITLE_KIND_001",      // title-case kind not recognized
		"bearer :hk_test_SPACE_BEFORE_001",   // space between kind and colon (kind contains trailing space → unknown)
		"bearer\t:hk_test_TAB_BEFORE_001",    // tab before colon (kind contains tab → unknown)
	}
	for _, in := range rejects {
		t.Run(in, func(t *testing.T) {
			_, err := routecontrol.ParseClientCredential(in)
			if !errors.Is(err, routecontrol.ErrInvalidClientCredential) {
				t.Fatalf("want ErrInvalidClientCredential for %q, got %v", in, err)
			}
		})
	}
}
