package credentialworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestDefaultModeAdapterRegistryCoversCredentialStoreModes(t *testing.T) {
	registry := DefaultModeAdapterRegistry()
	wantCount := len(credentialstore.DefaultHandlerRegistry().Names())
	if got := registry.Names(); len(got) != wantCount {
		t.Fatalf("mode adapter count=%d want %d: %v", len(got), wantCount, got)
	}
	for _, key := range credentialstore.DefaultHandlerRegistry().Names() {
		vendor, mode := splitCredentialModeKey(key)
		if _, ok := registry.Lookup(vendor, mode); !ok {
			t.Fatalf("missing mode refresh adapter %s", key)
		}
	}
}

func TestDefaultModeAdapterRegistryRoutesSlice26OAuthModes(t *testing.T) {
	registry := DefaultModeAdapterRegistry()
	cases := []struct {
		vendor   string
		authMode string
		key      string
		assert   func(*testing.T, ModeRefreshAdapter)
	}{
		{credentialstore.VendorGemini, credentialstore.AuthModeOAuth, "gemini/oauth", assertOperatorBoundOAuthModeAdapter},
		{credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth, "antigravity/oauth", assertOperatorBoundOAuthModeAdapter},
		{credentialstore.VendorWindsurf, credentialstore.AuthModeOAuth, "windsurf/oauth", assertWindsurfManualModeAdapter},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			adapter, ok := registry.Lookup(tc.vendor, tc.authMode)
			if !ok || adapter == nil {
				t.Fatalf("missing mode refresh adapter %s", tc.key)
			}
			tc.assert(t, adapter)
		})
	}
}

func TestModeRefreshWorkerFindsWindsurfOAuthAdapter(t *testing.T) {
	// Regression killed: windsurf/oauth credentials could be stored, but the
	// refresh worker's mode registry missed the executor and marked the account
	// adapter_missing. Mutation self-check: deleting the windsurf/oauth default
	// registration makes this test fail with failure:88:adapter_missing.
	calls := []string{}
	store := &recordingRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 88, TenantID: 1, ProviderAccountID: 188,
			Vendor: credentialstore.VendorWindsurf, AuthMode: credentialstore.AuthModeOAuth,
			CredentialVersion: 4, PlaintextPayload: []byte(`{"session_token":"windsurf-session","token_source":"windsurf_show_auth_token"}`),
		},
	}
	refresher := &AccountCredentialRefresher{store: store, registry: DefaultModeAdapterRegistry(), now: func() time.Time {
		return time.Date(2026, 5, 26, 9, 0, 0, 0, time.UTC)
	}}

	err := refresher.Refresh(context.Background(), 188)
	if err != nil {
		t.Fatalf("Refresh returned %v, want no adapter_missing for windsurf/oauth", err)
	}
	want := []string{"probe", "tx_begin", "lock:88", "reread"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
}

func assertOperatorBoundOAuthModeAdapter(t *testing.T, adapter ModeRefreshAdapter) {
	t.Helper()
	if _, ok := adapter.(legacyOAuthModeAdapter); ok {
		t.Fatalf("adapter type=%T must not be legacyOAuthModeAdapter", adapter)
	}
	if got := reflect.TypeOf(adapter).String(); got != "credentialworker.operatorOAuthModeAdapter" {
		t.Fatalf("adapter type=%s, want credentialworker.operatorOAuthModeAdapter", got)
	}
}

func assertWindsurfManualModeAdapter(t *testing.T, adapter ModeRefreshAdapter) {
	t.Helper()
	if got := reflect.TypeOf(adapter).String(); got != "credentialworker.windsurfManualModeAdapter" {
		t.Fatalf("adapter type=%s, want credentialworker.windsurfManualModeAdapter", got)
	}
}

func TestDefaultModeAdapterRegistryCodexFailsClosedWithoutOperatorConfig(t *testing.T) {
	// Regression killed: the default scheduled Codex refresh path must not
	// fall back to endpoint/client/scope embedded in credential JSON.
	adapter, ok := DefaultModeAdapterRegistry().Lookup(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
	if !ok {
		t.Fatal("Codex CLI OAuth mode adapter missing")
	}
	_, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 42,
		Vendor:            credentialstore.VendorOpenAI,
		AuthMode:          credentialstore.AuthModeCodexCLIOAuth,
		Payload: []byte(`{
			"refresh_token":"rt-old",
			"client_id":"credential-cid",
			"scope":"credential-scope",
			"oauth_token_endpoint":"http://evil.attacker.test/token"
		}`),
	})
	if !errors.Is(err, adapters.ErrCodexOAuthConfigRequired) {
		t.Fatalf("RefreshCredential err=%v, want ErrCodexOAuthConfigRequired", err)
	}
}

func TestDefaultModeAdapterRegistryGeminiAntigravityOAuthUsesExistingConfigAndRefreshesSessionToken(t *testing.T) {
	// Regression killed: gemini/oauth and antigravity/oauth scheduled refresh
	// must use existing HUAKAI_GEMINI_OAUTH_* operator config and must replace
	// stale session_token with the freshly refreshed access_token. Mutation
	// self-checks: reading only the newer HERMES-specific env names fails before
	// the request; deleting session_token synchronization leaves old-session in
	// the saved payload and makes this test red.
	cases := []struct {
		name     string
		vendor   string
		authMode string
	}{
		{
			name:     "gemini",
			vendor:   credentialstore.VendorGemini,
			authMode: credentialstore.AuthModeOAuth,
		},
		{
			name:     "antigravity",
			vendor:   credentialstore.VendorAntigravity,
			authMode: credentialstore.AuthModeOAuth,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operatorEndpoint := "https://operator." + tc.name + ".example.test/oauth/token"
			operatorClientID := "operator-" + tc.name + "-client"
			wantAccessToken := "operator-" + tc.name + "-access"
			wantRefreshToken := "operator-" + tc.name + "-refresh"
			oldSessionToken := "old-" + tc.name + "-session"
			t.Setenv("HUAKAI_DATABASE_URL", "postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable")
			t.Setenv("HUAKAI_GEMINI_OAUTH_TOKEN_URL", operatorEndpoint)
			t.Setenv("HUAKAI_GEMINI_OAUTH_CLIENT_ID", operatorClientID)
			t.Setenv("HUAKAI_GEMINI_OAUTH_CLIENT_SECRET", "")
			previousClient := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = previousClient })
			var gotURL string
			var gotForm url.Values
			http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				gotURL = r.URL.String()
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read refresh body: %v", err)
				}
				gotForm, err = url.ParseQuery(string(body))
				if err != nil {
					t.Fatalf("parse refresh body %q: %v", string(body), err)
				}
				return jsonResponse(`{"access_token":"` + wantAccessToken + `","refresh_token":"` + wantRefreshToken + `","expires_in":1800,"token_type":"Bearer"}`), nil
			})}

			adapter, ok := DefaultModeAdapterRegistry().Lookup(tc.vendor, tc.authMode)
			if !ok {
				t.Fatalf("missing mode refresh adapter %s/%s", tc.vendor, tc.authMode)
			}
			result, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
				ProviderAccountID: 91,
				Vendor:            tc.vendor,
				AuthMode:          tc.authMode,
				Payload: []byte(`{
					"session_token":"` + oldSessionToken + `",
					"access_token":"old-access-token",
					"refresh_token":"refresh-from-credential",
					"oauth_token_endpoint":"https://attacker.example.test/token",
					"token_endpoint":"https://attacker.example.test/alt-token",
					"client_id":"credential-client",
					"client_secret":"credential-secret"
				}`),
			})
			if err != nil {
				t.Fatalf("RefreshCredential: %v", err)
			}
			if gotURL != operatorEndpoint {
				t.Fatalf("refresh endpoint=%q want operator endpoint %q", gotURL, operatorEndpoint)
			}
			if gotForm.Get("client_id") != operatorClientID {
				t.Fatalf("client_id=%q want operator client %q", gotForm.Get("client_id"), operatorClientID)
			}
			if got := gotForm.Get("client_secret"); got != "" {
				t.Fatalf("client_secret=%q want empty when operator secret env is empty", got)
			}
			var payload map[string]string
			if err := json.Unmarshal(result.Payload, &payload); err != nil {
				t.Fatalf("result payload json: %v", err)
			}
			if got := payload["access_token"]; got != wantAccessToken {
				t.Fatalf("access_token=%q want refreshed token %q; payload=%s", got, wantAccessToken, result.Payload)
			}
			if got := payload["session_token"]; got != wantAccessToken {
				t.Fatalf("session_token=%q want refreshed access token %q; old token %q must not survive; payload=%s", got, wantAccessToken, oldSessionToken, result.Payload)
			}
			if got := payload["refresh_token"]; got != wantRefreshToken {
				t.Fatalf("refresh_token=%q want rotated token %q; payload=%s", got, wantRefreshToken, result.Payload)
			}
		})
	}
}

func TestWindsurfManualModeAdapterRejectsRefreshTokenOnlyCredential(t *testing.T) {
	// Regression killed: a stored Windsurf OAuth payload with only refresh_token
	// is unusable by the runtime session adapter, so scheduled refresh must
	// fail closed instead of silently treating it as a manual no-op. Mutation
	// self-check: deleting the session/access-token guard returns
	// ErrNoRefreshRequired and makes this test red.
	adapter, ok := DefaultModeAdapterRegistry().Lookup(credentialstore.VendorWindsurf, credentialstore.AuthModeOAuth)
	if !ok {
		t.Fatal("missing windsurf/oauth mode adapter")
	}
	_, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 92,
		Vendor:            credentialstore.VendorWindsurf,
		AuthMode:          credentialstore.AuthModeOAuth,
		Payload:           []byte(`{"refresh_token":"refresh-only"}`),
	})
	if err == nil {
		t.Fatal("RefreshCredential err=nil, want invalid credential material")
	}
	if errors.Is(err, ErrNoRefreshRequired) {
		t.Fatalf("RefreshCredential err=%v, want fail-closed invalid credential material", err)
	}
	if !errors.Is(err, adapters.ErrInvalidCredentialMaterial) {
		t.Fatalf("RefreshCredential err=%v, want ErrInvalidCredentialMaterial", err)
	}
}

func TestWindsurfManualModeAdapterPreservesSessionTokenManualNoop(t *testing.T) {
	adapter, ok := DefaultModeAdapterRegistry().Lookup(credentialstore.VendorWindsurf, credentialstore.AuthModeOAuth)
	if !ok {
		t.Fatal("missing windsurf/oauth mode adapter")
	}
	_, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{
		ProviderAccountID: 93,
		Vendor:            credentialstore.VendorWindsurf,
		AuthMode:          credentialstore.AuthModeOAuth,
		Payload:           []byte(`{"session_token":"windsurf-session-token"}`),
	})
	if !errors.Is(err, ErrNoRefreshRequired) {
		t.Fatalf("RefreshCredential err=%v, want ErrNoRefreshRequired for manual session token", err)
	}
}

func TestModeRefreshCodexOperatorConfigFailureRecordsOperatorClass(t *testing.T) {
	calls := []string{}
	store := &recordingRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 45, TenantID: 1, ProviderAccountID: 102,
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
			CredentialVersion: 3, PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
		},
	}
	refresher := &AccountCredentialRefresher{store: store, registry: DefaultModeAdapterRegistry(), now: func() time.Time {
		return time.Date(2026, 5, 24, 14, 20, 0, 0, time.UTC)
	}}

	err := refresher.Refresh(context.Background(), 102)
	if !errors.Is(err, adapters.ErrCodexOAuthConfigRequired) {
		t.Fatalf("Refresh err=%v, want ErrCodexOAuthConfigRequired", err)
	}
	want := []string{"probe", "tx_begin", "lock:45", "reread", "failure:45:operator_config_required"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
}

func TestMockTokenExchangeAdapterRefreshesAzureWithoutSDK(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s want POST", r.Method)
		}
		return jsonResponse(`{"access_token":"azure-access","expires_in":1800,"token_type":"Bearer"}`), nil
	})}

	raw := []byte(`{"mock_token_endpoint":"http://mock.local/token","tenant_id":"t"}`)
	result, err := (mockTokenExchangeAdapter{providerName: "azure", client: client}).RefreshCredential(context.Background(), ModeRefreshInput{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAzure, Payload: raw, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(result.Payload, &got); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if got["access_token"] != "azure-access" || got["token_type"] != "Bearer" {
		t.Fatalf("payload=%v", got)
	}
	if time.Until(result.AccessExpiresAt) <= time.Minute {
		t.Fatalf("AccessExpiresAt not advanced: %s", result.AccessExpiresAt)
	}
}

func TestMetadataTokenAdapterUsesStdlibMetadataRequest(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Metadata-Flavor"); got != "Google" {
			t.Fatalf("Metadata-Flavor=%q want Google", got)
		}
		return jsonResponse(`{"access_token":"gcp-access","expires_in":3600}`), nil
	})}

	raw := []byte(`{"metadata_token_endpoint":"http://metadata.local/token","client_email":"svc@example.test"}`)
	result, err := (metadataTokenAdapter{client: client}).RefreshCredential(context.Background(), ModeRefreshInput{
		Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeVertexSA, Payload: raw, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(result.Payload, &got); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if got["access_token"] != "gcp-access" {
		t.Fatalf("payload=%v", got)
	}
}

func TestRefreshAdvisoryLockPrecedesRereadAndSave(t *testing.T) {
	calls := []string{}
	store := &recordingRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 44, TenantID: 1, ProviderAccountID: 101,
			Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeRefreshToken,
			CredentialVersion: 3, PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
		},
	}
	registry := NewModeAdapterRegistry()
	if err := registry.Register(credentialstore.VendorOpenAI, credentialstore.AuthModeRefreshToken, recordingModeAdapter{calls: &calls}); err != nil {
		t.Fatal(err)
	}
	refresher := &AccountCredentialRefresher{store: store, registry: registry, now: func() time.Time {
		return time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	}}
	if err := refresher.Refresh(context.Background(), 101); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want := []string{"probe", "tx_begin", "lock:44", "reread", "adapter:44", "save:44"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
}

func TestGeminiFallbackAuditWrittenInRefreshTransaction(t *testing.T) {
	calls := []string{}
	store := &recordingRefreshStore{
		calls: &calls,
		rec: credentialstore.CredentialRecord{
			ID: 55, TenantID: 1, ProviderAccountID: 101,
			Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeCodeAssist,
			CredentialVersion: 2, PlaintextPayload: []byte(`{"refresh_token":"rt-old"}`),
		},
	}
	registry := NewModeAdapterRegistry()
	if err := registry.Register(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist, recordingModeAdapter{
		calls:   &calls,
		payload: []byte(`{"refresh_token":"rt-new","cross_client_fallback_attempted":true,"cross_client_fallback_from":"code_assist","cross_client_fallback_to":"ai_studio"}`),
	}); err != nil {
		t.Fatal(err)
	}
	refresher := &AccountCredentialRefresher{store: store, registry: registry, now: func() time.Time {
		return time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	}}
	if err := refresher.Refresh(context.Background(), 101); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want := []string{
		"probe", "tx_begin", "lock:55", "reread", "adapter:55",
		"audit:gemini_cross_client_fallback:code_assist:ai_studio:true", "save:55",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want %v", calls, want)
	}
}

func splitCredentialModeKey(key string) (string, string) {
	for i, r := range key {
		if r == '/' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

type recordingRefreshStore struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
}

func (s *recordingRefreshStore) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*s.calls = append(*s.calls, "probe")
	return s.rec, nil
}

func (s *recordingRefreshStore) WithRefreshTransaction(_ context.Context, fn func(accountCredentialRefreshTxStore, db.DBTX) error) error {
	*s.calls = append(*s.calls, "tx_begin")
	tx := &recordingRefreshTx{calls: s.calls, rec: s.rec}
	return fn(tx, tx)
}

type recordingRefreshTx struct {
	calls *[]string
	rec   credentialstore.CredentialRecord
}

func (tx *recordingRefreshTx) Exec(_ context.Context, _ string, args ...interface{}) (pgconn.CommandTag, error) {
	*tx.calls = append(*tx.calls, "lock:"+strconv.FormatInt(args[0].(int64), 10))
	return pgconn.CommandTag{}, nil
}

func (tx *recordingRefreshTx) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (tx *recordingRefreshTx) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func (tx *recordingRefreshTx) LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error) {
	*tx.calls = append(*tx.calls, "reread")
	return tx.rec, nil
}

func (tx *recordingRefreshTx) SaveRefreshSuccess(_ context.Context, rec credentialstore.CredentialRecord, _ []byte, _ time.Time, _ string) error {
	*tx.calls = append(*tx.calls, "save:"+strconv.FormatInt(rec.ID, 10))
	return nil
}

func (tx *recordingRefreshTx) SaveRefreshFailure(_ context.Context, rec credentialstore.CredentialRecord, failureClass string, _ time.Time) error {
	*tx.calls = append(*tx.calls, "failure:"+strconv.FormatInt(rec.ID, 10)+":"+failureClass)
	return nil
}

func (tx *recordingRefreshTx) InsertAuditEvent(_ context.Context, e credentialstore.AuditEvent) error {
	*tx.calls = append(*tx.calls, "audit:"+e.EventType+":"+
		auditPayloadString(e.Payload, "from_client")+":"+
		auditPayloadString(e.Payload, "to_client")+":"+
		strconv.FormatBool(auditPayloadBool(e.Payload, "success")))
	return nil
}

func auditPayloadString(payload map[string]any, key string) string {
	if v, ok := payload[key].(string); ok {
		return v
	}
	return ""
}

func auditPayloadBool(payload map[string]any, key string) bool {
	if v, ok := payload[key].(bool); ok {
		return v
	}
	return false
}

type recordingModeAdapter struct {
	calls   *[]string
	payload []byte
}

func (a recordingModeAdapter) RefreshCredential(_ context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	*a.calls = append(*a.calls, "adapter:"+strconv.FormatInt(in.CredentialID, 10))
	payload := a.payload
	if len(payload) == 0 {
		payload = []byte(`{"refresh_token":"rt-new"}`)
	}
	return ModeRefreshResult{Payload: payload, AccessExpiresAt: time.Now().Add(time.Hour)}, nil
}
