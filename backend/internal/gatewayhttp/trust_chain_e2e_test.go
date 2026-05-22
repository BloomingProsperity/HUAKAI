package gatewayhttp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	protoanthropic "github.com/BloomingProsperity/HUAKAI/internal/proto/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	provideranthropic "github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

const (
	trustE2EModel      = "claude-3-5-sonnet-20241022"
	trustE2ERequestID  = "req-t10-e2e"
	trustE2ELedgerID   = "ledger-t10-e2e"
	trustE2ETenantID   = int64(7)
	trustE2EAccountID  = int64(1)
	trustE2EPoolID     = int64(42)
	trustE2ERouteID    = "registry:7:1;router:t10"
	trustE2EProvider   = "anthropic"
	headerHUAKAILedger = "X-HUAKAI-Ledger-ID"
	headerHUAKAIVerify = "X-HUAKAI-Verify"
	headerHUAKAISigFP  = "X-HUAKAI-Sig-Fingerprint"
)

func TestE2E_AnthropicMessages_发出_HopChain_链路完整(t *testing.T) {
	env := newTrustChainE2E(t)

	resp := env.postMessages(t)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /v1/messages status=%d want 200 body=%s", resp.StatusCode, body)
	}
	entry := env.entryFromResponseHeader(t, resp)
	if env.ledger.Size(context.Background()) != 1 {
		t.Fatalf("ledger size=%d want 1", env.ledger.Size(context.Background()))
	}
	want := []proto.HopHop{
		proto.HopIngress,
		proto.HopRouter,
		proto.HopPool,
		proto.HopAccount,
		proto.HopProvider,
		proto.HopResponse,
	}
	if len(entry.HopChain) != len(want) {
		t.Fatalf("HopChain len=%d want %d: %+v", len(entry.HopChain), len(want), entry.HopChain)
	}
	for i, hop := range want {
		if entry.HopChain[i].Hop != hop {
			t.Fatalf("HopChain[%d]=%q want %q; chain=%+v", i, entry.HopChain[i].Hop, hop, entry.HopChain)
		}
	}
}

func TestE2E_ModelChain_3字段全部填(t *testing.T) {
	env := newTrustChainE2E(t)

	resp := env.postMessages(t)
	defer resp.Body.Close()
	entry := env.entryFromResponseHeader(t, resp)
	if entry.ModelChain == nil {
		t.Fatal("ModelChain is nil")
	}
	if entry.ModelChain.Requested != trustE2EModel {
		t.Fatalf("ModelChain.Requested=%q want %q", entry.ModelChain.Requested, trustE2EModel)
	}
	if entry.ModelChain.RouteDecided != trustE2EModel {
		t.Fatalf("ModelChain.RouteDecided=%q want %q", entry.ModelChain.RouteDecided, trustE2EModel)
	}
	if entry.ModelChain.UpstreamReported != trustE2EModel {
		t.Fatalf("ModelChain.UpstreamReported=%q want %q", entry.ModelChain.UpstreamReported, trustE2EModel)
	}
}

func TestE2E_X_HUAKAI_响应头存在并指向真ledger(t *testing.T) {
	env := newTrustChainE2E(t)

	resp := env.postMessages(t)
	defer resp.Body.Close()
	ledgerID := resp.Header.Get(headerHUAKAILedger)
	if ledgerID == "" {
		t.Fatalf("%s header is empty", headerHUAKAILedger)
	}
	entry, err := env.ledger.GetByLedgerID(ledgerID)
	if err != nil {
		t.Fatalf("ledger id %q cannot be read back: %v", ledgerID, err)
	}
	if entry.RequestID != trustE2ERequestID {
		t.Fatalf("entry.RequestID=%q want %q", entry.RequestID, trustE2ERequestID)
	}
	if got := resp.Header.Get(headerHUAKAISigFP); got != env.signer.Fingerprint() {
		t.Fatalf("%s=%q want %q", headerHUAKAISigFP, got, env.signer.Fingerprint())
	}
	verifyHeader := resp.Header.Get(headerHUAKAIVerify)
	if !strings.Contains(verifyHeader, "ledger-id="+url.QueryEscape(ledgerID)) {
		t.Fatalf("%s=%q does not point at ledger-id %q", headerHUAKAIVerify, verifyHeader, ledgerID)
	}
	verifyURL, err := url.Parse(verifyHeader)
	if err != nil {
		t.Fatalf("%s=%q parse error: %v", headerHUAKAIVerify, verifyHeader, err)
	}
	if got, want := verifyURL.Query().Get("tenant_scope_ref"), auditledger.TenantScopeRef(trustE2ETenantID); got != want {
		t.Fatalf("%s tenant_scope_ref=%q want %q", headerHUAKAIVerify, got, want)
	}
}

func TestE2E_verify_endpoint_往返(t *testing.T) {
	env := newTrustChainE2E(t)

	resp := env.postMessages(t)
	defer resp.Body.Close()
	ledgerID := resp.Header.Get(headerHUAKAILedger)
	var got trustE2EVerifyResponse
	env.getJSON(t, resp.Header.Get(headerHUAKAIVerify), &got)

	if got.LedgerEntry.LedgerID != ledgerID {
		t.Fatalf("verify ledger_id=%q want %q", got.LedgerEntry.LedgerID, ledgerID)
	}
	if len(got.LedgerEntry.HopChain) != 6 {
		t.Fatalf("verify hop_chain len=%d want 6", len(got.LedgerEntry.HopChain))
	}
	if got.LedgerEntry.ModelChain == nil || got.LedgerEntry.ModelChain.UpstreamReported != trustE2EModel {
		t.Fatalf("verify model_chain=%+v", got.LedgerEntry.ModelChain)
	}
	if !got.SignatureValid {
		t.Fatalf("signature_valid=false; response=%+v", got)
	}
	if !got.MerkleProof.Consistent || got.MerkleProof.EntryMerkleRoot == "" || got.MerkleProof.LatestMerkleRoot == "" {
		t.Fatalf("merkle_proof incomplete or inconsistent: %+v", got.MerkleProof)
	}
}

func TestE2E_篡改后_签名验证失败(t *testing.T) {
	env := newTrustChainE2E(t)

	resp := env.postMessages(t)
	defer resp.Body.Close()
	ledgerID := resp.Header.Get(headerHUAKAILedger)
	if err := env.ledger.TamperFirstHop(ledgerID); err != nil {
		t.Fatalf("tamper ledger: %v", err)
	}

	var got trustE2EVerifyResponse
	env.getJSON(t, resp.Header.Get(headerHUAKAIVerify), &got)
	if got.SignatureValid {
		t.Fatalf("signature_valid=true after tamper; response=%+v", got)
	}
	if got.MerkleProof.Consistent {
		t.Fatalf("merkle proof still consistent after tamper: %+v", got.MerkleProof)
	}
}

type trustChainE2EEnv struct {
	signer *sign.Signer
	ledger *trustE2ELedger
	server *httptest.Server
}

func newTrustChainE2E(t *testing.T) *trustChainE2EEnv {
	t.Helper()
	_ = t.TempDir() // 本组测试只用 in-memory ledger；这里保留临时目录守门，避免误接真实 DB。

	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger := newTrustE2ELedger(t, signer)

	upstream := newGatewayHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-API-Key"); got != "sk-ant-test" {
			http.Error(w, "missing x-api-key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeAnthropicSSE(t, w, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    "msg-t10",
				"type":  "message",
				"role":  "assistant",
				"model": trustE2EModel,
				"usage": map[string]any{"input_tokens": 2, "output_tokens": 0},
			},
		})
		writeAnthropicSSE(t, w, "content_block_start", map[string]any{
			"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		writeAnthropicSSE(t, w, "content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "ok"},
		})
		writeAnthropicSSE(t, w, "content_block_stop", map[string]any{
			"type": "content_block_stop", "index": 0,
		})
		writeAnthropicSSE(t, w, "message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
			"usage": map[string]any{"input_tokens": 2, "output_tokens": 1},
		})
		writeAnthropicSSE(t, w, "message_stop", map[string]any{"type": "message_stop"})
	}))
	t.Cleanup(upstream.Close)

	providerAdapters := provider.NewStaticRegistry()
	providerAdapters.MustRegister("anthropic_messages", &provideranthropic.PassthroughAdapter{
		Endpoint: upstream.URL + "/v1/messages",
	})
	protocolAdapters := gateway.NewStaticProtocolAdapterRegistry()
	protocolAdapters.MustRegister("anthropic_messages", &trustE2EAnthropicAdapter{
		base:      &protoanthropic.Adapter{},
		ledger:    ledger,
		requestID: trustE2ERequestID,
		endpoint:  upstream.URL + "/v1/messages",
	})

	vault := provider.NewStaticVault()
	if err := vault.Set(trustE2EAccountID, provider.Credential{
		Type:  provider.CredentialTypeAPIKey,
		Value: "sk-ant-test",
		Extra: map[string]string{"anthropic_version": "2023-06-01"},
	}, provider.AccountInfo{
		AccountID:   trustE2EAccountID,
		Platform:    trustE2EProvider,
		AccountType: "apikey",
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}

	deps := minimalDeps()
	deps.CredentialVault = vault
	deps.Dispatcher = &gateway.UpstreamDispatcher{Adapters: providerAdapters, TransportFactory: transport.NewFactory(), ProtocolAdapters: protocolAdapters, HTTPClient: upstream.Client()}
	deps.Forwarder = &gateway.StreamForwarder{ProtocolAdapters: protocolAdapters, Scanners: gateway.BuildDefaultStreamScannerRegistry()}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Post("/v1/messages", trustE2EHeaderMiddleware(ledger, NewMessagesHandler(deps)))
	r.Get("/v1/audit/verify", newTrustE2EVerifyHandler(ledger, signer.PublicKey()))
	r.Get("/v1/audit/merkle-tree.json", NewAuditMerkleTreeHandler(AuditVerifyStaticDeps{Ledger: ledger}))

	server := newGatewayHTTPTestServer(t, r)
	t.Cleanup(func() {
		server.Close()
		ledger.Reset(t)
	})
	return &trustChainE2EEnv{signer: signer, ledger: ledger, server: server}
}

func (e *trustChainE2EEnv) postMessages(t *testing.T) *http.Response {
	t.Helper()
	body := bytes.NewBufferString(`{"model":"` + trustE2EModel + `","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req, err := http.NewRequest(http.MethodPost, e.server.URL+"/v1/messages", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer hk-test")
	req.Header.Set(middleware.RequestIDHeader, trustE2ERequestID)
	resp, err := e.server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /v1/messages: %v", err)
	}
	return resp
}

func (e *trustChainE2EEnv) entryFromResponseHeader(t *testing.T, resp *http.Response) auditledger.LedgerEntry {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d want 200 body=%s", resp.StatusCode, body)
	}
	ledgerID := resp.Header.Get(headerHUAKAILedger)
	if ledgerID == "" {
		t.Fatalf("%s header is empty", headerHUAKAILedger)
	}
	entry, err := e.ledger.GetByLedgerID(ledgerID)
	if err != nil {
		t.Fatalf("ledger %q not found: %v", ledgerID, err)
	}
	return entry
}

func (e *trustChainE2EEnv) getJSON(t *testing.T, path string, dst any) {
	t.Helper()
	resp, err := e.server.Client().Get(e.server.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s status=%d want 200 body=%s", path, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

type trustE2ELedger struct {
	signer   *sign.Signer
	inner    *auditledger.MemoryLedger
	mu       sync.RWMutex
	byID     map[string]string
	tampered map[string]auditledger.LedgerEntry
}

func newTrustE2ELedger(t *testing.T, signer *sign.Signer) *trustE2ELedger {
	t.Helper()
	inner, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("memory ledger: %v", err)
	}
	return &trustE2ELedger{
		signer:   signer,
		inner:    inner,
		byID:     map[string]string{},
		tampered: map[string]auditledger.LedgerEntry{},
	}
}

func (l *trustE2ELedger) Append(ctx context.Context, entry auditledger.LedgerEntry) (auditledger.LedgerEntry, error) {
	appended, err := l.inner.Append(ctx, entry)
	if err != nil {
		return auditledger.LedgerEntry{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.byID[appended.LedgerID] = appended.RequestID
	return cloneLedgerEntry(appended), nil
}

func (l *trustE2ELedger) GetByRequestID(ctx context.Context, requestID string) (auditledger.LedgerEntry, error) {
	l.mu.RLock()
	entry, ok := l.tampered[requestID]
	l.mu.RUnlock()
	if ok {
		return cloneLedgerEntry(entry), nil
	}
	entry, err := l.inner.GetByRequestID(ctx, requestID)
	if err != nil {
		return auditledger.LedgerEntry{}, err
	}
	return cloneLedgerEntry(entry), nil
}

func (l *trustE2ELedger) GetByRequestIDAndTenantScope(ctx context.Context, requestID, tenantScopeRef string) (auditledger.LedgerEntry, error) {
	entry, err := l.GetByRequestID(ctx, requestID)
	if err != nil {
		return auditledger.LedgerEntry{}, err
	}
	entryScope := entry.TenantScopeRef
	if entryScope == "" {
		entryScope = auditledger.TenantScopeRef(entry.TenantID)
	}
	if entryScope == "" || entryScope != tenantScopeRef {
		return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
	}
	return entry, nil
}

func (l *trustE2ELedger) GetByLedgerID(ledgerID string) (auditledger.LedgerEntry, error) {
	l.mu.RLock()
	requestID, ok := l.byID[ledgerID]
	l.mu.RUnlock()
	if !ok {
		return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
	}
	return l.GetByRequestID(context.Background(), requestID)
}

func (l *trustE2ELedger) LatestMerkleRoot(ctx context.Context) ([32]byte, error) {
	return l.inner.LatestMerkleRoot(ctx)
}

func (l *trustE2ELedger) Size(ctx context.Context) int {
	return l.inner.Size(ctx)
}

func (l *trustE2ELedger) Snapshot() []auditledger.LedgerEntry {
	l.mu.RLock()
	ids := make([]string, 0, len(l.byID))
	for _, requestID := range l.byID {
		ids = append(ids, requestID)
	}
	l.mu.RUnlock()
	out := make([]auditledger.LedgerEntry, 0, len(ids))
	for _, requestID := range ids {
		if entry, err := l.GetByRequestID(context.Background(), requestID); err == nil {
			out = append(out, entry)
		}
	}
	return out
}

func (l *trustE2ELedger) TamperFirstHop(ledgerID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	requestID, ok := l.byID[ledgerID]
	if !ok {
		return auditledger.ErrLedgerEntryNotFound
	}
	entry, err := l.inner.GetByRequestID(context.Background(), requestID)
	if err != nil {
		return err
	}
	entry = cloneLedgerEntry(entry)
	if len(entry.HopChain) == 0 {
		return errors.New("empty hop chain")
	}
	entry.HopChain[0].RequestID = entry.HopChain[0].RequestID + "-tampered"
	l.tampered[requestID] = entry
	return nil
}

func (l *trustE2ELedger) Reset(t *testing.T) {
	t.Helper()
	inner, err := auditledger.NewMemoryLedger(l.signer)
	if err != nil {
		t.Fatalf("reset memory ledger: %v", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inner = inner
	l.byID = map[string]string{}
	l.tampered = map[string]auditledger.LedgerEntry{}
}

func cloneLedgerEntry(in auditledger.LedgerEntry) auditledger.LedgerEntry {
	out := in
	if in.HopChain != nil {
		out.HopChain = append([]proto.HopAttestation(nil), in.HopChain...)
	}
	if in.ModelChain != nil {
		mc := *in.ModelChain
		out.ModelChain = &mc
	}
	return out
}

type trustE2EAnthropicAdapter struct {
	base      proto.UpstreamAdapter
	ledger    *trustE2ELedger
	requestID string
	endpoint  string

	mu               sync.Mutex
	upstreamReported string
	appended         bool
}

func (a *trustE2EAnthropicAdapter) CanonicalToProviderRequest(ctx context.Context, env *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return a.base.CanonicalToProviderRequest(ctx, env)
}

func (a *trustE2EAnthropicAdapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return a.base.ProviderResponseToCanonical(ctx, raw)
}

func (a *trustE2EAnthropicAdapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []proto.ProtocolLossEntry, error) {
	eventType, model := inspectAnthropicEvent(providerEvt)
	if model != "" {
		a.mu.Lock()
		a.upstreamReported = model
		a.mu.Unlock()
	}
	events, losses, err := a.base.ProviderEventToCanonicalEvents(ctx, providerEvt, state)
	if err != nil {
		return events, losses, err
	}
	if eventType == "message_stop" {
		if err := a.appendLedgerOnce(ctx); err != nil {
			return nil, losses, err
		}
	}
	return events, losses, nil
}

func (a *trustE2EAnthropicAdapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
	return a.base.FinalizeUpstreamStream(ctx, state)
}

func (a *trustE2EAnthropicAdapter) appendLedgerOnce(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.appended {
		return nil
	}
	reported := a.upstreamReported
	if reported == "" {
		reported = trustE2EModel
	}
	_, err := a.ledger.Append(ctx, auditledger.LedgerEntry{
		LedgerID:  trustE2ELedgerID,
		RequestID: a.requestID,
		TenantID:  trustE2ETenantID,
		HopChain:  buildTrustE2EHopChain(a.requestID, a.endpoint),
		ModelChain: &proto.ModelChain{
			Requested:        trustE2EModel,
			RouteDecided:     trustE2EModel,
			UpstreamReported: reported,
		},
	})
	if err != nil {
		return fmt.Errorf("append trust-chain ledger: %w", err)
	}
	a.appended = true
	return nil
}

func inspectAnthropicEvent(providerEvt any) (eventType, model string) {
	raw, ok := providerEvt.([]byte)
	if !ok {
		return "", ""
	}
	var env struct {
		Type    string `json:"type"`
		Message struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", ""
	}
	return env.Type, env.Message.Model
}

func buildTrustE2EHopChain(requestID, endpoint string) []proto.HopAttestation {
	now := time.Now().UTC()
	return []proto.HopAttestation{
		{Hop: proto.HopIngress, Timestamp: now.Format(time.RFC3339Nano), RequestID: requestID},
		{Hop: proto.HopRouter, Timestamp: now.Add(time.Nanosecond).Format(time.RFC3339Nano), RequestID: requestID, RouteID: trustE2ERouteID},
		{Hop: proto.HopPool, Timestamp: now.Add(2 * time.Nanosecond).Format(time.RFC3339Nano), RequestID: requestID, PoolID: fmt.Sprint(trustE2EPoolID)},
		{Hop: proto.HopAccount, Timestamp: now.Add(3 * time.Nanosecond).Format(time.RFC3339Nano), RequestID: requestID, AccountIDHash: "sha256:e2e-account-hash"},
		{Hop: proto.HopProvider, Timestamp: now.Add(4 * time.Nanosecond).Format(time.RFC3339Nano), RequestID: requestID, Provider: trustE2EProvider, Endpoint: endpoint},
		{Hop: proto.HopResponse, Timestamp: now.Add(5 * time.Nanosecond).Format(time.RFC3339Nano), RequestID: requestID, DurationMS: 1},
	}
}

func trustE2EHeaderMiddleware(ledger *trustE2ELedger, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		next(rec, r)
		if rec.Code == 0 {
			rec.Code = http.StatusOK
		}
		if rec.Code >= 200 && rec.Code < 300 {
			if requestID := middleware.GetReqID(r.Context()); requestID != "" {
				if entry, err := ledger.GetByRequestID(r.Context(), requestID); err == nil {
					rec.Header().Set(headerHUAKAILedger, entry.LedgerID)
					rec.Header().Set(headerHUAKAISigFP, entry.PubkeyFingerprint)
					query := url.Values{}
					query.Set("ledger-id", entry.LedgerID)
					query.Set("request_id", entry.RequestID)
					if scopeRef := auditledger.TenantScopeRef(entry.TenantID); scopeRef != "" {
						query.Set("tenant_scope_ref", scopeRef)
					}
					rec.Header().Set(headerHUAKAIVerify, "/v1/audit/verify?"+query.Encode())
				}
			}
		}
		for k, values := range rec.Header() {
			for _, value := range values {
				w.Header().Add(k, value)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}
}

type trustE2EVerifyResponse struct {
	AuditVerifyResponse
	SignatureValid bool                `json:"signature_valid"`
	MerkleProof    trustE2EMerkleProof `json:"merkle_proof"`
}

type trustE2EMerkleProof struct {
	LatestMerkleRoot string `json:"latest_merkle_root"`
	EntryMerkleRoot  string `json:"entry_merkle_root"`
	Size             int    `json:"size"`
	Consistent       bool   `json:"consistent"`
	Error            string `json:"error,omitempty"`
}

func newTrustE2EVerifyHandler(ledger *trustE2ELedger, pub ed25519.PublicKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAuditJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
			return
		}
		ledgerID := r.URL.Query().Get("ledger-id")
		if ledgerID == "" {
			writeAuditJSONError(w, http.StatusBadRequest, "missing_ledger_id", "ledger-id query parameter required")
			return
		}
		entry, err := ledger.GetByLedgerID(ledgerID)
		if errors.Is(err, auditledger.ErrLedgerEntryNotFound) {
			writeAuditJSONError(w, http.StatusNotFound, "audit_entry_not_found", "ledger-id not found")
			return
		}
		if err != nil {
			writeAuditJSONError(w, http.StatusInternalServerError, "audit_ledger_error", err.Error())
			return
		}
		latest, latestErr := ledger.LatestMerkleRoot(r.Context())
		if latestErr != nil {
			writeAuditJSONError(w, http.StatusInternalServerError, "audit_ledger_error", latestErr.Error())
			return
		}
		consistent := auditledger.VerifyChain(ledger.Snapshot()) == nil
		var merkleErr string
		if !consistent {
			merkleErr = auditledger.VerifyChain(ledger.Snapshot()).Error()
		}
		resp := trustE2EVerifyResponse{
			AuditVerifyResponse: auditVerifyResponse(entry),
			SignatureValid:      verifyTrustE2ESignature(entry, pub),
			MerkleProof: trustE2EMerkleProof{
				LatestMerkleRoot: rootHex(latest),
				EntryMerkleRoot:  rootHex(entry.MerkleRoot),
				Size:             ledger.Size(r.Context()),
				Consistent:       consistent,
				Error:            merkleErr,
			},
		}
		writeAuditJSON(w, http.StatusOK, resp)
	}
}

func verifyTrustE2ESignature(entry auditledger.LedgerEntry, pub ed25519.PublicKey) bool {
	sig, err := base64.StdEncoding.DecodeString(entry.Signature)
	if err != nil {
		return false
	}
	hash, err := auditledger.EntryHash(&entry)
	if err != nil {
		return false
	}
	return sign.Verify(pub, hash[:], sig) == nil
}

func writeAnthropicSSE(t *testing.T, w http.ResponseWriter, event string, payload map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal SSE payload: %v", err)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
