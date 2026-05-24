package credentialworker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
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

func (tx *recordingRefreshTx) SaveRefreshFailure(context.Context, credentialstore.CredentialRecord, string, time.Time) error {
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
