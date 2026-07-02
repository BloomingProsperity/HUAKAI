package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type credentialCreator interface {
	Create(context.Context, credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error)
}

type fakeCredentialCreator struct {
	mu    sync.Mutex
	calls int
	next  int64
}

func (f *fakeCredentialCreator) Create(_ context.Context, in credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.next == 0 {
		f.next = 1000
	}
	f.next++
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	return credentialstore.CredentialMetadata{
		ID: f.next, TenantID: in.TenantID, ProviderAccountID: in.ProviderAccountID,
		Vendor: credentialstore.Normalize(in.Vendor), AuthMode: credentialstore.Normalize(in.AuthMode),
		State: credentialstore.StateActive, Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (f *fakeCredentialCreator) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestFinalizerValidatesCredentialStoreRegistryModes(t *testing.T) {
	finalizer := NewFinalizer(nil, credentialstore.DefaultHandlerRegistry(), &fakeCredentialCreator{}, nil)
	for i, key := range credentialstore.DefaultHandlerRegistry().Names() {
		vendor, mode := splitModeKeyForFinalizerTest(key)
		payload := samplePayloadForMode(vendor, mode)
		candidate := CredentialCandidate{
			TenantID: 1, ProviderAccountID: int64(100 + i),
			Vendor: vendor, AuthMode: mode, Payload: payload, ActorID: "admin-1",
		}
		if err := finalizer.ValidateCandidate(candidate); err != nil {
			t.Fatalf("%s validate: %v", key, err)
		}
	}
}

func TestFinalizerIsIdempotentByFlowID(t *testing.T) {
	creator := &fakeCredentialCreator{}
	store := newProductionTestStore(t, "flow-idem", credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth)
	finalizer := NewFinalizer(store, credentialstore.DefaultHandlerRegistry(), creator, nil)
	candidate := CredentialCandidate{
		TenantID: 1, ProviderAccountID: 2,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		Payload: samplePayloadForMode(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth),
		ActorID: "admin-1",
	}
	first, err := finalizer.Finalize(context.Background(), "flow-idem", candidate, "admin-1", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := finalizer.Finalize(context.Background(), "flow-idem", candidate, "admin-1", "req-2")
	if err != nil {
		t.Fatal(err)
	}
	if first.Credential.ID != second.Credential.ID {
		t.Fatalf("idempotency returned different ids: %d vs %d", first.Credential.ID, second.Credential.ID)
	}
	if !second.AlreadyFinalized {
		t.Fatal("second finalize should be reported as already finalized")
	}
	if got := creator.Calls(); got != 1 {
		t.Fatalf("creator calls=%d want 1", got)
	}
}

func TestFinalizerConcurrentFinalizeRaceIsIdempotent(t *testing.T) {
	creator := &fakeCredentialCreator{}
	store := newProductionTestStore(t, "flow-race", credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth)
	finalizer := NewFinalizer(store, credentialstore.DefaultHandlerRegistry(), creator, nil)
	candidate := CredentialCandidate{
		TenantID: 1, ProviderAccountID: 2,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		Payload: samplePayloadForMode(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth),
		ActorID: "admin-1",
	}

	const callers = 2
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]FinalizeResult, callers)
	errs := make([]error, callers)

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index], errs[index] = finalizer.Finalize(context.Background(), "flow-race", candidate, "admin-1", "req-race")
		}(i)
	}

	close(start)
	wg.Wait()

	successes := 0
	for i, err := range errs {
		if err != nil && !errors.Is(err, ErrFlowReplay) {
			t.Fatalf("caller %d finalize: %v", i, err)
		}
		if err == nil {
			if results[i].Credential.ID == 0 {
				t.Fatalf("caller %d returned empty credential id", i)
			}
			successes++
		}
	}
	if successes == 0 {
		t.Fatal("no caller finalized the flow")
	}
	if got := creator.Calls(); got != 1 {
		t.Fatalf("creator calls=%d want 1", got)
	}
}

func newProductionTestStore(t *testing.T, flowID, vendor, authMode string) *PostgresSessionStore {
	t.Helper()
	now := time.Date(2026, 5, 16, 5, 0, 0, 0, time.UTC)
	store := NewPostgresSessionStore(newTestSessionDB(now)).WithNow(func() time.Time { return now })
	if _, err := store.Create(context.Background(), Session{
		ID: flowID, TenantID: 1, ProviderAccountID: 2,
		Vendor: vendor, AuthMode: authMode, Kind: FlowKindPaste, Status: StatusStarted,
		ActorID: "admin-1", ActorRole: "platform_admin",
		ClientIdentitySource: ClientSourceNone, RedactedContext: map[string]any{},
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return store
}

func samplePayloadForMode(vendor, mode string) []byte {
	fields := map[string]any{}
	switch credentialstore.ModeKey(vendor, mode) {
	case "anthropic/api_key", "openai/api_key", "gemini/aistudio_api_key":
		fields["api_key"] = "test-api-key"
	case "anthropic/bedrock":
		fields["aws_access_key_id"] = "test-access-key"
		fields["aws_secret_access_key"] = "test-secret-key"
		fields["aws_region"] = "us-east-1"
	case "anthropic/vertex_anthropic", "gemini/vertex_sa":
		fields["client_email"] = "service@example.test"
		fields["metadata_token_endpoint"] = "https://metadata.example.test/token"
	case "openai/azure":
		fields["azure_api_key"] = "test-azure-key"
	case "openai/refresh_token":
		fields["refresh_token"] = "test-refresh-value"
	default:
		// 官 key 厂商(grok/deepseek/kimi/国内大厂)统一走纯 api_key 形状;其余默认按 OAuth 会话形状。
		if credentialstore.Normalize(mode) == credentialstore.AuthModeAPIKey {
			fields["api_key"] = "test-api-key"
		} else {
			fields["session_token"] = "test-session-value"
			fields["refresh_token"] = "test-refresh-value"
		}
	}
	raw, _ := json.Marshal(fields)
	return raw
}

func splitModeKeyForFinalizerTest(key string) (string, string) {
	vendor, mode, ok := strings.Cut(key, "/")
	if !ok {
		return key, ""
	}
	return vendor, mode
}
