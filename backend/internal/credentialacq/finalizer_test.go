package credentialacq

import (
	"context"
	"encoding/json"
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

type mockFinalizer struct {
	registry *credentialstore.HandlerRegistry
	creator  credentialCreator
	mu       sync.Mutex
	seen     map[string]credentialstore.CredentialMetadata
}

func newMockFinalizer(registry *credentialstore.HandlerRegistry, creator credentialCreator) *mockFinalizer {
	return &mockFinalizer{registry: registry, creator: creator, seen: map[string]credentialstore.CredentialMetadata{}}
}

func (f *mockFinalizer) Finalize(ctx context.Context, flowID string, candidate acqCandidate) (credentialstore.CredentialMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if meta, ok := f.seen[flowID]; ok {
		return meta, nil
	}

	handler, err := f.registry.MustLookup(candidate.Vendor, candidate.AuthMode)
	if err != nil {
		return credentialstore.CredentialMetadata{}, errUnknownMode
	}
	if err := handler.ValidatePayload(candidate.Payload); err != nil {
		return credentialstore.CredentialMetadata{}, err
	}
	meta, err := f.creator.Create(ctx, credentialstore.CreateCredentialInput{
		TenantID: candidate.TenantID, ProviderAccountID: candidate.ProviderAccountID,
		Vendor: candidate.Vendor, AuthMode: candidate.AuthMode, Payload: candidate.Payload,
		ActorID: candidate.ActorID,
	})
	if err != nil {
		return credentialstore.CredentialMetadata{}, err
	}

	f.seen[flowID] = meta
	return meta, nil
}

func TestFinalizerValidatesAllFifteenModesAgainstCredentialStoreRegistry(t *testing.T) {
	finalizer := newMockFinalizer(credentialstore.DefaultHandlerRegistry(), &fakeCredentialCreator{})
	for i, plan := range phaseAModePlans() {
		payload := samplePayloadForMode(plan.Vendor, plan.AuthMode)
		meta, err := finalizer.Finalize(context.Background(), plan.Vendor+"/"+plan.AuthMode, acqCandidate{
			TenantID: 1, ProviderAccountID: int64(100 + i),
			Vendor: plan.Vendor, AuthMode: plan.AuthMode, Payload: payload, ActorID: "admin-1",
		})
		if err != nil {
			t.Fatalf("%s/%s finalize: %v", plan.Vendor, plan.AuthMode, err)
		}
		if meta.Vendor != plan.Vendor || meta.AuthMode != plan.AuthMode {
			t.Fatalf("meta target=%s/%s want %s/%s", meta.Vendor, meta.AuthMode, plan.Vendor, plan.AuthMode)
		}
	}
}

func TestFinalizerIsIdempotentByFlowID(t *testing.T) {
	creator := &fakeCredentialCreator{}
	finalizer := newMockFinalizer(credentialstore.DefaultHandlerRegistry(), creator)
	candidate := acqCandidate{
		TenantID: 1, ProviderAccountID: 2,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		Payload: samplePayloadForMode(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth),
		ActorID: "admin-1",
	}
	first, err := finalizer.Finalize(context.Background(), "flow-idem", candidate)
	if err != nil {
		t.Fatal(err)
	}
	second, err := finalizer.Finalize(context.Background(), "flow-idem", candidate)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotency returned different ids: %d vs %d", first.ID, second.ID)
	}
	if got := creator.Calls(); got != 1 {
		t.Fatalf("creator calls=%d want 1", got)
	}
}

func TestFinalizerConcurrentFinalizeRaceIsIdempotent(t *testing.T) {
	creator := &fakeCredentialCreator{}
	finalizer := newMockFinalizer(credentialstore.DefaultHandlerRegistry(), creator)
	candidate := acqCandidate{
		TenantID: 1, ProviderAccountID: 2,
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth,
		Payload: samplePayloadForMode(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth),
		ActorID: "admin-1",
	}

	const callers = 2
	start := make(chan struct{})
	var wg sync.WaitGroup
	metas := make([]credentialstore.CredentialMetadata, callers)
	errs := make([]error, callers)

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			metas[index], errs[index] = finalizer.Finalize(context.Background(), "flow-race", candidate)
		}(i)
	}

	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d finalize: %v", i, err)
		}
	}
	if metas[0].ID == 0 {
		t.Fatal("first caller returned empty credential id")
	}
	for i := 1; i < callers; i++ {
		if metas[i].ID != metas[0].ID {
			t.Fatalf("caller %d returned id=%d want %d", i, metas[i].ID, metas[0].ID)
		}
	}
	if got := creator.Calls(); got != 1 {
		t.Fatalf("creator calls=%d want 1", got)
	}
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
		fields["session_token"] = "test-session-value"
		fields["refresh_token"] = "test-refresh-value"
	}
	raw, _ := json.Marshal(fields)
	return raw
}
