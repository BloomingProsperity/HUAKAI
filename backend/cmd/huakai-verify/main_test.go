package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auditverifyhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/trustreceipt"
)

func TestRunCLI_DetachedTOFUFirstFetchCachesKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	signer, canonical, sigB64, receiptFile := detachedReceiptFixture(t)
	fetches := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/.well-known/huakai-pubkey.json" {
			return testHTTPResponse(req, http.StatusNotFound, "not found"), nil
		}
		fetches++
		return testHTTPResponse(req, http.StatusOK, jwkSetBody(signer, "active")), nil
	})}

	var out bytes.Buffer
	code := runCLI([]string{
		"--server=https://huakai-verify.test",
		"--receipt-file=" + receiptFile,
		"--signature=" + sigB64,
		"--json",
	}, &out, client)
	if code != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if fetches != 1 {
		t.Fatalf("well-known fetches=%d want 1", fetches)
	}
	if !strings.Contains(out.String(), `"status":"signed-only"`) || !strings.Contains(out.String(), `"signature_valid":true`) {
		t.Fatalf("json output missing signed-only result: %s", out.String())
	}
	cachePath := filepath.Join(home, ".huakai", "known_keys", "huakai-verify.test.json")
	cached, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("cache not written at %s: %v", cachePath, err)
	}
	if !strings.Contains(string(cached), signer.Fingerprint()) || !strings.Contains(string(cached), base64.RawURLEncoding.EncodeToString(signer.PublicKey())) || len(canonical) == 0 {
		t.Fatalf("cache missing key material: %s", cached)
	}
}

func TestRunCLI_DetachedCacheHitDoesNotRefetch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	signer, _, sigB64, receiptFile := detachedReceiptFixture(t)
	cacheDir := filepath.Join(home, ".huakai", "known_keys")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "huakai-verify.test.json"), []byte(jwkSetBody(signer, "active")), 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	fetches := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		fetches++
		return testHTTPResponse(req, http.StatusInternalServerError, "cache should avoid fetch"), nil
	})}

	var out bytes.Buffer
	code := runCLI([]string{
		"--server=https://huakai-verify.test",
		"--receipt-file=" + receiptFile,
		"--signature=" + sigB64,
	}, &out, client)
	if code != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if fetches != 0 {
		t.Fatalf("cache hit performed fetches=%d", fetches)
	}
	if !strings.Contains(out.String(), "签名状态: signed-only") {
		t.Fatalf("friendly output missing status: %s", out.String())
	}
}

func TestRunCLI_DetachedFingerprintMismatchFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	signer, _, sigB64, receiptFile := detachedReceiptFixture(t)
	wrong, _ := sign.GenerateKey()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusOK, jwkSetBody(wrong, "active")), nil
	})}

	var out bytes.Buffer
	code := runCLI([]string{
		"--server=https://huakai-verify.test",
		"--receipt-file=" + receiptFile,
		"--signature=" + sigB64,
		"--fingerprint=" + signer.Fingerprint(),
	}, &out, client)
	if code != 1 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "fingerprint mismatch") {
		t.Fatalf("mismatch output missing reason: %s", out.String())
	}
}

func TestRunCLI_DetachedRevokedKeyFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	signer, _, sigB64, receiptFile := detachedReceiptFixture(t)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusOK, jwkSetBody(signer, "revoked")), nil
	})}

	var out bytes.Buffer
	code := runCLI([]string{
		"--server=https://huakai-verify.test",
		"--receipt-file=" + receiptFile,
		"--signature=" + sigB64,
	}, &out, client)
	if code != 1 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "key_revoked") || strings.Contains(out.String(), "signed-only") {
		t.Fatalf("revoked output mismatch: %s", out.String())
	}
}

func TestRunCLI_HappyPath(t *testing.T) {
	signer, gateway := newVerifyGateway(t)
	defer gateway.Close()

	var out bytes.Buffer
	code := runCLI([]string{
		"--pubkey-url=" + gateway.URL + "/.well-known/huakai-pubkey.json",
		"--request-id=req_cli",
		"--tenant-scope-ref=" + auditledger.TenantScopeRef(7),
		"--gateway-url=" + gateway.URL,
	}, &out, gateway.Client())
	if code != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "audit verification passed") || !strings.Contains(out.String(), signer.Fingerprint()) {
		t.Fatalf("success output missing pass marker or fingerprint: %s", out.String())
	}
}

func detachedReceiptFixture(t *testing.T) (*sign.Signer, []byte, string, string) {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	receipt := trustreceipt.TrustReceiptV1{
		RequestID:       "req-cli-detached",
		ReceiptSequence: 0,
		TenantScopeRef:  "tenant:7",
		OccurredAt:      mustParseTime(t, "2026-05-27T12:30:00Z"),
		Provider:        "openai",
		RequestedModel:  "gpt-4o",
		RoutedModel:     "gpt-4o-mini",
		UpstreamModel:   "gpt-4o-mini",
		DeliveredModel:  "gpt-4o-mini",
		CostCents:       12,
		TokenCounts:     trustreceipt.TokenCounts{Input: 40, Output: 12, Cached: 3},
		PriceSnapshot:   trustreceipt.PriceSnapshot{RateTableSnapshotID: 44, SnapshotVersion: "registry:7:44", CurrencyCode: "USD"},
		ValidationState: "valid",
		RedactedMetadataAllowlist: map[string]any{
			"safe_label": "green",
		},
	}
	canonical, err := trustreceipt.Canonical(receipt)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	path := filepath.Join(t.TempDir(), "receipt.canonical.json")
	if err := os.WriteFile(path, canonical, 0o600); err != nil {
		t.Fatalf("write receipt: %v", err)
	}
	return signer, canonical, base64.StdEncoding.EncodeToString(signer.Sign(canonical)), path
}

func jwkSetBody(signer *sign.Signer, status string) string {
	revoked := "[]"
	if status == "revoked" {
		revoked = fmt.Sprintf(`[{"fingerprint":%q,"revoked_at":"2026-05-27T12:00:00Z","reason_class":"key_compromise"}]`, signer.Fingerprint())
	}
	return fmt.Sprintf(`{"schema_version":"huakai.pubkey.v1","keys":[{"kty":"OKP","crv":"Ed25519","kid":%q,"x":%q,"alg":"EdDSA","use":"sig","status":%q,"revoked_at":"2026-05-27T12:00:00Z","reason_class":"key_compromise"}],"current":%q,"revoked":%s}`,
		signer.Fingerprint(),
		base64.RawURLEncoding.EncodeToString(signer.PublicKey()),
		status,
		signer.Fingerprint(),
		revoked,
	)
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return parsed.UTC()
}

func TestRunCLI_VerifiesResponseMatchesRequestedEntry(t *testing.T) {
	cases := []struct {
		name        string
		entryReqID  string
		entryTenant int64
		wantCode    int
		wantOutput  string
	}{
		{
			name:        "matching entry succeeds",
			entryReqID:  "req_cli",
			entryTenant: 7,
			wantCode:    0,
			wantOutput:  "audit verification passed",
		},
		{
			name:        "request id mismatch fails",
			entryReqID:  "req_other",
			entryTenant: 7,
			wantCode:    1,
			wantOutput:  "request_id mismatch",
		},
		{
			name:        "tenant scope ref mismatch fails",
			entryReqID:  "req_cli",
			entryTenant: 8,
			wantCode:    1,
			wantOutput:  "tenant_scope_ref mismatch",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gatewayURL, client := newSubstitutingVerifyClient(t, tc.entryReqID, tc.entryTenant)

			var out bytes.Buffer
			code := runCLI([]string{
				"--pubkey-url=" + gatewayURL + "/.well-known/huakai-pubkey.json",
				"--request-id=req_cli",
				"--tenant-scope-ref=" + auditledger.TenantScopeRef(7),
				"--gateway-url=" + gatewayURL,
			}, &out, client)
			if code != tc.wantCode {
				t.Fatalf("code=%d want %d output=%s", code, tc.wantCode, out.String())
			}
			if !strings.Contains(out.String(), tc.wantOutput) {
				t.Fatalf("output missing %q: %s", tc.wantOutput, out.String())
			}
			if tc.wantCode != 0 && strings.Contains(out.String(), "audit verification passed") {
				t.Fatalf("mismatch output reported success: %s", out.String())
			}
		})
	}
}

func TestRunCLI_WrongPubKeyFails(t *testing.T) {
	_, gateway := newVerifyGateway(t)
	defer gateway.Close()
	wrong, _ := sign.GenerateKey()
	pubkey := base64.StdEncoding.EncodeToString(wrong.PublicKey())
	pubkeyServer := newLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, pubkey)
	}))
	defer pubkeyServer.Close()

	var out bytes.Buffer
	code := runCLI([]string{
		"--pubkey-url=" + pubkeyServer.URL,
		"--request-id=req_cli",
		"--tenant-scope-ref=" + auditledger.TenantScopeRef(7),
		"--gateway-url=" + gateway.URL,
	}, &out, gateway.Client())
	if code != 1 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "audit verification failed") || !strings.Contains(out.String(), "fingerprint mismatch") {
		t.Fatalf("failure output missing reason: %s", out.String())
	}
}

func TestRunCLI_NotFoundFails(t *testing.T) {
	_, gateway := newVerifyGateway(t)
	defer gateway.Close()

	var out bytes.Buffer
	code := runCLI([]string{
		"--pubkey-url=" + gateway.URL + "/.well-known/huakai-pubkey.json",
		"--request-id=missing",
		"--tenant-scope-ref=" + auditledger.TenantScopeRef(7),
		"--gateway-url=" + gateway.URL,
	}, &out, gateway.Client())
	if code != 1 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "audit verification failed") || !strings.Contains(out.String(), "HTTP 404") {
		t.Fatalf("not_found output missing HTTP status: %s", out.String())
	}
}

// TestRunCLI_RequestIDFlowRevokedKeyFails 守审计 wy94u3tn9 最后一个 S1 的 CLI 面:request-id 流
// 此前丢弃 well-known 文档的 revoked 集合,导致吊销的 signing key 仍被报"验证通过"。这里网关的
// well-known 把该 key 标为已吊销,验证必须失败(exit!=0、输出含 revoked、不含 passed)。
// 判别(变异):删 main.go request-id 流里的吊销检查(if keyRevoked ...)→ 又报 passed/exit 0 → 本测试变红。
func TestRunCLI_RequestIDFlowRevokedKeyFails(t *testing.T) {
	_, gateway := newVerifyGatewayRevoked(t)
	defer gateway.Close()

	var out bytes.Buffer
	code := runCLI([]string{
		"--pubkey-url=" + gateway.URL + "/.well-known/huakai-pubkey.json",
		"--request-id=req_cli",
		"--tenant-scope-ref=" + auditledger.TenantScopeRef(7),
		"--gateway-url=" + gateway.URL,
	}, &out, gateway.Client())
	if code == 0 {
		t.Fatalf("吊销 key 的 request-id 验证应失败(exit!=0),实际 code=0 output=%s", out.String())
	}
	if !strings.Contains(out.String(), "revoked") {
		t.Fatalf("输出应说明 signing key 已吊销,实际: %s", out.String())
	}
	if strings.Contains(out.String(), "audit verification passed") {
		t.Fatalf("吊销 key 不应报验证通过: %s", out.String())
	}
}

// newVerifyGatewayRevoked 与 newVerifyGateway 同构,但 well-known 文档把 signing key 标为已吊销,
// 用于验证 request-id 流对吊销 key 的独立拒绝(网关自身的吊销表为空,吊销只来自 well-known)。
func newVerifyGatewayRevoked(t *testing.T) (*sign.Signer, *httptest.Server) {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	ctx := context.Background()
	prepared, err := auditledger.PrepareEntry(ctx, auditledger.LedgerEntry{
		LedgerID:  "lid_cli",
		RequestID: "req_cli",
		TenantID:  7,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err = ledger.Append(ctx, prepared); err != nil {
		t.Fatalf("append: %v", err)
	}

	mux := http.NewServeMux()
	deps := auditverifyhttp.AuditVerifyStaticDeps{Ledger: ledger}
	mux.HandleFunc("/v1/audit/verify", auditverifyhttp.NewAuditVerifyHandler(deps))
	mux.HandleFunc("/v1/audit/merkle-tree.json", auditverifyhttp.NewAuditMerkleTreeHandler(deps))
	mux.HandleFunc("/.well-known/huakai-pubkey.json", func(w http.ResponseWriter, _ *http.Request) {
		doc := fmt.Sprintf(`{"keys":[{"fingerprint":%q,"public_key":%q}],"revoked":[{"fingerprint":%q}]}`,
			signer.Fingerprint(), base64.StdEncoding.EncodeToString(signer.PublicKey()), signer.Fingerprint())
		_, _ = fmt.Fprint(w, doc)
	})
	return signer, newLoopbackHTTPServer(t, mux)
}

func newVerifyGateway(t *testing.T) (*sign.Signer, *httptest.Server) {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	ctx := context.Background()
	prepared, err := auditledger.PrepareEntry(ctx, auditledger.LedgerEntry{
		LedgerID:  "lid_cli",
		RequestID: "req_cli",
		TenantID:  7,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	_, err = ledger.Append(ctx, prepared)
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	mux := http.NewServeMux()
	deps := auditverifyhttp.AuditVerifyStaticDeps{Ledger: ledger}
	mux.HandleFunc("/v1/audit/verify", auditverifyhttp.NewAuditVerifyHandler(deps))
	mux.HandleFunc("/v1/audit/merkle-tree.json", auditverifyhttp.NewAuditMerkleTreeHandler(deps))
	mux.HandleFunc("/.well-known/huakai-pubkey.json", func(w http.ResponseWriter, _ *http.Request) {
		doc := fmt.Sprintf(`{"keys":[{"fingerprint":%q,"public_key":%q}]}`,
			signer.Fingerprint(), base64.StdEncoding.EncodeToString(signer.PublicKey()))
		_, _ = fmt.Fprint(w, doc)
	})
	return signer, newLoopbackHTTPServer(t, mux)
}

func newSubstitutingVerifyClient(t *testing.T, entryReqID string, entryTenant int64) (string, *http.Client) {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	ctx := context.Background()
	prepared, err := auditledger.PrepareEntry(ctx, auditledger.LedgerEntry{
		LedgerID:  "lid_substitution",
		RequestID: entryReqID,
		TenantID:  entryTenant,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	entry, err := ledger.Append(ctx, prepared)
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	verifyBodyBytes, err := json.Marshal(auditverifyhttp.AuditVerifyResponse{
		LedgerEntry: auditverifyhttp.AuditLedgerEntryJSON{
			LedgerID:       entry.LedgerID,
			Timestamp:      entry.Timestamp,
			RequestID:      entry.RequestID,
			TenantScopeRef: auditledger.TenantScopeRef(entry.TenantID),
			HopChain:       entry.HopChain,
		},
		ChainProof: auditverifyhttp.AuditChainProofJSON{
			PrevMerkleRoot:    fmt.Sprintf("%x", entry.PrevMerkleRoot),
			MerkleRoot:        fmt.Sprintf("%x", entry.MerkleRoot),
			Signature:         entry.Signature,
			PubkeyFingerprint: entry.PubkeyFingerprint,
		},
	})
	if err != nil {
		t.Fatalf("marshal verify response: %v", err)
	}
	verifyBody := string(verifyBodyBytes)
	treeBody := fmt.Sprintf(`{"latest_merkle_root":"%x","size":1}`, entry.MerkleRoot)
	pubkeyBody := fmt.Sprintf(`{"keys":[{"fingerprint":%q,"public_key":%q}]}`,
		signer.Fingerprint(), base64.StdEncoding.EncodeToString(signer.PublicKey()))
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		bodyByPath := map[string]string{
			"/v1/audit/verify":                verifyBody,
			"/v1/audit/merkle-tree.json":      treeBody,
			"/.well-known/huakai-pubkey.json": pubkeyBody,
		}
		body, ok := bodyByPath[req.URL.Path]
		if !ok {
			return testHTTPResponse(req, http.StatusNotFound, "not found"), nil
		}
		return testHTTPResponse(req, http.StatusOK, body), nil
	})}
	return "https://huakai-verify.test", client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func newLoopbackHTTPServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("本地 loopback 监听不可用，跳过需要 httptest server 的 CLI 测试: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = ln
	server.Start()
	return server
}
