package routecontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// CredentialKind discriminates how the inbound credential was carried.
// Only two canonical kinds are accepted by Phase 2A (P2-A4); anything else
// is rejected with [ErrInvalidClientCredential] by [ParseClientCredential].
type CredentialKind string

const (
	// KindBearer — Rust observed "Authorization: Bearer <token>" inbound.
	// Canonical wire form: "bearer:<token>".
	KindBearer CredentialKind = "bearer"

	// KindXAPIKey — Rust observed "x-api-key: <token>" inbound.
	// Canonical wire form: "x-api-key:<token>".
	// Normalized to Authorization: Bearer when handed to the resolver
	// (P2-A4: both kinds end up as a single auth source for the resolver).
	KindXAPIKey CredentialKind = "x-api-key"
)

// ClientCredential holds the parsed client identity material.
//
// CRITICAL PII INVARIANT: the secret field is unexported and NEVER appears in
// fmt.Stringer / fmt.Formatter / fmt.GoStringer output, nor in any error
// returned by this package. The only path to use the secret is through
// [ClientCredential.ResolverRequest], which hands it off to
// auth.APIKeyResolver behind the Authorization header.
//
// For logs, metrics, audit trails — use [ClientCredential.Fingerprint]
// (SHA-256 first 8 hex chars over kind+secret).
//
// Zero value is safe: methods return sentinel "empty" forms instead of
// panicking; [ClientCredential.IsZero] reports the empty state.
type ClientCredential struct {
	kind   CredentialKind
	secret string // PII — never logged / formatted / exposed
}

// ParseClientCredential parses the canonical wire form produced by the Rust
// data plane in [RouteQueryRequest.client_credential]:
//
//	"bearer:<token>"
//	"x-api-key:<token>"
//
// Returns:
//
//   - [ErrMissingClientCredential] when canonical == "" (Rust must always
//     supply a value; empty means the Phase 1 A1 gate regressed).
//   - [ErrInvalidClientCredential] (wrapped) when the canonical form is
//     present but malformed: no ":" separator, unknown kind prefix, empty
//     or whitespace-only secret after the separator.
//
// PII safety: error messages never include the raw secret regardless of
// which failure path was taken; verified by TestErrors_NeverEmbedRawSecret.
func ParseClientCredential(canonical string) (ClientCredential, error) {
	if canonical == "" {
		return ClientCredential{}, ErrMissingClientCredential
	}

	idx := strings.IndexByte(canonical, ':')
	if idx < 0 {
		// Missing separator — could not extract kind. Do NOT echo the raw
		// canonical back in the error: it IS the secret.
		return ClientCredential{}, fmt.Errorf("%w: canonical form requires <kind>:<secret>", ErrInvalidClientCredential)
	}

	prefix := canonical[:idx]
	rawSecret := canonical[idx+1:]

	var kind CredentialKind
	switch prefix {
	case string(KindBearer):
		kind = KindBearer
	case string(KindXAPIKey):
		kind = KindXAPIKey
	default:
		// Unknown kind — do NOT include the prefix string in the error
		// either; while the prefix itself is not a secret, callers may
		// hand-craft canonical forms containing PII before the colon
		// (defense-in-depth: keep all wire-derived bytes out of errors).
		return ClientCredential{}, fmt.Errorf("%w: unknown credential kind prefix", ErrInvalidClientCredential)
	}

	secret := strings.TrimSpace(rawSecret)
	if secret == "" {
		return ClientCredential{}, fmt.Errorf("%w: empty secret after kind separator", ErrInvalidClientCredential)
	}

	return ClientCredential{kind: kind, secret: secret}, nil
}

// Kind returns the credential kind, or "" on a zero value.
func (c ClientCredential) Kind() CredentialKind { return c.kind }

// IsZero reports whether this credential is empty (parse failed or
// the caller never called [ParseClientCredential]).
func (c ClientCredential) IsZero() bool { return c.kind == "" }

// ResolverRequest constructs a minimal *http.Request carrying the credential
// as "Authorization: Bearer <secret>", ready to hand off to
// auth.APIKeyResolver.Resolve(ctx, *http.Request).
//
// Both canonical kinds (bearer / x-api-key) are normalized to a single
// "Authorization: Bearer" header — P2-A4 invariant: the resolver sees
// exactly one auth source regardless of how the client supplied it.
//
// The returned request has no URL, no body, and only the Authorization
// header set. APIKeyResolver.Resolve only reads req.Header.Get("Authorization"),
// so a fuller request is unnecessary and would risk leaking the secret into
// URL parsing logs.
//
// Passing nil ctx defaults to context.Background. Returns
// [ErrMissingClientCredential] on a zero value.
func (c ClientCredential) ResolverRequest(ctx context.Context) (*http.Request, error) {
	if c.IsZero() {
		return nil, ErrMissingClientCredential
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req := (&http.Request{
		Header: http.Header{
			"Authorization": []string{"Bearer " + c.secret},
		},
	}).WithContext(ctx)
	return req, nil
}

// Fingerprint returns SHA-256(kind || ":" || secret), first 4 bytes as 8 hex
// chars. Stable identifier for logs / metrics / audit; reveals nothing useful
// for credential reconstruction (8 hex chars = 32 bits vs SHA-256's 256 bits).
//
// Including kind in the digest ensures bearer:<x> and x-api-key:<x> produce
// DIFFERENT fingerprints — intentional: a confused-deputy attempt that
// forwards a bearer secret as x-api-key (or vice versa) becomes visible in
// audit log correlation rather than silently colliding.
//
// Returns "[empty]" on a zero value so log lines stay readable.
func (c ClientCredential) Fingerprint() string {
	if c.IsZero() {
		return "[empty]"
	}
	h := sha256.New()
	h.Write([]byte(c.kind))
	h.Write([]byte{':'})
	h.Write([]byte(c.secret))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:4]) // 8 hex chars (32 bits)
}

// String implements fmt.Stringer with PII safety. The secret NEVER appears.
//
// Format: "ClientCredential{kind=bearer sha256=abcd1234}"
// Zero value: "ClientCredential{empty}"
func (c ClientCredential) String() string {
	if c.IsZero() {
		return "ClientCredential{empty}"
	}
	return fmt.Sprintf("ClientCredential{kind=%s sha256=%s}", c.kind, c.Fingerprint())
}

// Format implements fmt.Formatter so that ALL fmt verbs (%v, %+v, %s, %q)
// route through [ClientCredential.String]. Without this method, %+v would
// reflect-walk the struct and expose the unexported secret field. With it,
// the secret stays hidden under every formatting verb.
func (c ClientCredential) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v', 's':
		_, _ = s.Write([]byte(c.String()))
	case 'q':
		_, _ = fmt.Fprintf(s, "%q", c.String())
	default:
		// Unknown verb — render as String() rather than fall through to
		// reflect-based default which could leak.
		_, _ = s.Write([]byte(c.String()))
	}
}

// GoString implements fmt.GoStringer for the %#v verb. Without it, %#v on
// a struct with unexported fields would reflect-walk and expose the secret.
func (c ClientCredential) GoString() string {
	return c.String()
}
