package credentialacq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// TestB2DeviceCodeSecretNotPersistedInPlaintext is the discriminative test for B2 [S2]:
// the RFC 8628 device_code is a confidential bearer credential — whoever holds it together with
// client_id can poll the token endpoint and, the instant the legitimate user approves at
// verification_uri, mint (and steal) the upstream account's access_token + refresh_token = full
// provider-account takeover for the flow window. The sibling PKCE code_verifier is deliberately put
// through AES-256-GCM (EncryptTransientPayload); the device_code must not be persisted weaker than
// its sibling. This asserts that once a store has a transient cipher configured (production wiring
// always does), the persisted device_code_payload contains NO plaintext device_code.
//
// RED (pre-fix): normalizeDeviceStartResponse writes the raw device_code into the payload map and
// startDeviceAuthorization persists it verbatim via SetAuthPayload -> device_code_payload jsonb in
// cleartext, so the secret is present in the reloaded payload.
func TestB2DeviceCodeSecretNotPersistedInPlaintext(t *testing.T) {
	now := time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC)
	const secret = "dc-super-secret-BEARER-9f3a2b17"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonHTTPResponse(t, map[string]any{
			"device_code":      secret,
			"user_code":        "WXYZ-1234",
			"verification_uri": "https://auth.kimi.com/device",
			"expires_in":       900,
			"interval":         5,
		}), nil
	})}
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	store := NewPostgresSessionStore(newTestSessionDB(now)).WithKeyProvider(keys).WithNow(func() time.Time { return now })
	in := StartInput{
		TenantID: 7, ProviderAccountID: 8,
		Vendor: credentialstore.VendorKimi, AuthMode: credentialstore.AuthModeKimiOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
		ClientIdentitySource: ClientSourcePublicCLI,
	}
	cfg := OAuthClientConfig{
		ClientID: "kimi-client", AuthURL: "https://auth.kimi.com/api/oauth/device_authorization",
		TokenURL: "https://auth.kimi.com/api/oauth/token", HTTPClient: client, Source: ClientSourcePublicCLI,
	}
	res, err := startDeviceAuthorization(context.Background(), store, in, cfg, AuthTypeDeviceCode)
	if err != nil {
		t.Fatalf("startDeviceAuthorization: %v", err)
	}

	reloaded, err := store.Get(context.Background(), res.Session.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	raw, err := json.Marshal(reloaded.DeviceCodePayload)
	if err != nil {
		t.Fatalf("marshal reloaded payload: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("device_code bearer secret persisted in PLAINTEXT in device_code_payload at rest: %s", raw)
	}
	if v, ok := reloaded.DeviceCodePayload["device_code"]; ok {
		t.Fatalf("plaintext device_code key present at rest: %v", v)
	}
}

// TestB2PostJSONStatusRejectsOversizedResponseBody is the discriminative test for the second B2
// defect: postJSONStatus is the read path for device-authorization START and BOTH device-code token
// pollers (which loop every ~5s until expiry). It read the whole response with an unbounded
// io.ReadAll — a compromised/MITM'd/misbehaving token endpoint returning a multi-GB body would be
// buffered wholesale into memory each iteration -> OOM of the credential worker. Every sibling read
// path caps at io.LimitReader; this one must too.
//
// RED (pre-fix): the oversized (but valid-JSON) body is read and unmarshalled successfully, so
// postJSONStatus returns (200, nil) instead of ErrResponseTooLarge.
func TestB2PostJSONStatusRejectsOversizedResponseBody(t *testing.T) {
	pad := strings.Repeat("A", oauthFormResponseMaxBytes+64)
	oversized := []byte(`{"error":"authorization_pending","pad":"` + pad + `"}`)
	if len(oversized) <= oauthFormResponseMaxBytes {
		t.Fatalf("test setup: body %d not larger than cap %d", len(oversized), oauthFormResponseMaxBytes)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(oversized)),
			Header:     http.Header{},
		}, nil
	})}
	var out map[string]any
	status, err := postJSONStatus(context.Background(), client, "https://token.example.test/token", map[string]any{"x": "y"}, &out)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("postJSONStatus over cap: status=%d err=%v, want ErrResponseTooLarge", status, err)
	}
}

// TestB2DeviceCodeSealedSecretStillPollable is the usability guard for the encryption fix: after the
// device_code is sealed at rest, the poller — given the transient cipher via WithDeviceCodeSecretCipher
// — must recover the exact plaintext device_code and send it to the token endpoint. Without this, the
// at-rest fix would render the device-code flow unusable once poll polling is wired.
func TestB2DeviceCodeSealedSecretStillPollable(t *testing.T) {
	now := time.Date(2026, 5, 24, 9, 30, 0, 0, time.UTC)
	const secret = "dc-super-secret-BEARER-round-trip"
	var seenDeviceCode, seenClientID string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "device_authorization"):
			return jsonHTTPResponse(t, map[string]any{
				"device_code":      secret,
				"user_code":        "WXYZ-1234",
				"verification_uri": "https://auth.kimi.com/device",
				"expires_in":       900,
				"interval":         5,
			}), nil
		default: // token endpoint
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			seenDeviceCode = stringField(body, "device_code")
			seenClientID = stringField(body, "client_id")
			return jsonHTTPResponse(t, map[string]any{"access_token": "access-from-poll", "refresh_token": "refresh-from-poll"}), nil
		}
	})}
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	store := NewPostgresSessionStore(newTestSessionDB(now)).WithKeyProvider(keys).WithNow(func() time.Time { return now })
	in := StartInput{
		TenantID: 7, ProviderAccountID: 8,
		Vendor: credentialstore.VendorKimi, AuthMode: credentialstore.AuthModeKimiOAuth,
		ActorID: "admin-1", ActorRole: "platform_admin",
		ClientIdentitySource: ClientSourcePublicCLI,
	}
	cfg := OAuthClientConfig{
		ClientID: "kimi-client", AuthURL: "https://auth.kimi.com/api/oauth/device_authorization",
		TokenURL: "https://auth.kimi.com/api/oauth/token", HTTPClient: client, Source: ClientSourcePublicCLI,
	}
	res, err := startDeviceAuthorization(context.Background(), store, in, cfg, AuthTypeDeviceCode)
	if err != nil {
		t.Fatalf("startDeviceAuthorization: %v", err)
	}

	// Reload from the (sealed) store, exactly as a worker would before polling.
	reloaded, err := store.Get(context.Background(), res.Session.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := reloaded.DeviceCodePayload["device_code"]; ok {
		t.Fatalf("sealed payload still exposes plaintext device_code")
	}

	candidate, err := PollDeviceCodeToken(context.Background(), reloaded, cfg,
		WithDeviceCodeHTTPClient(client),
		WithDeviceCodeNow(func() time.Time { return now }),
		WithDeviceCodeSecretCipher(store),
		WithDeviceCodeSleeper(func(context.Context, time.Duration) error {
			return errors.New("poll should not sleep after immediate success")
		}),
	)
	if err != nil {
		t.Fatalf("PollDeviceCodeToken over sealed secret: %v", err)
	}
	if seenDeviceCode != secret {
		t.Fatalf("poller sent device_code=%q, want decrypted %q", seenDeviceCode, secret)
	}
	if seenClientID != "kimi-client" {
		t.Fatalf("poller sent client_id=%q", seenClientID)
	}
	if candidate.TenantID != 7 || candidate.ProviderAccountID != 8 {
		t.Fatalf("candidate target tenant/account=%d/%d", candidate.TenantID, candidate.ProviderAccountID)
	}
}
