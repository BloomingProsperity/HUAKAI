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
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

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
	if !strings.Contains(out.String(), "✅") || !strings.Contains(out.String(), signer.Fingerprint()) {
		t.Fatalf("success output missing checkmarks or fingerprint: %s", out.String())
	}
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
			wantOutput:  "✅",
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
	if !strings.Contains(out.String(), "❌") || !strings.Contains(out.String(), "fingerprint mismatch") {
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
	if !strings.Contains(out.String(), "❌") || !strings.Contains(out.String(), "HTTP 404") {
		t.Fatalf("not_found output missing HTTP status: %s", out.String())
	}
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
	_, err = ledger.Append(context.Background(), auditledger.LedgerEntry{
		LedgerID:  "lid_cli",
		RequestID: "req_cli",
		TenantID:  7,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	mux := http.NewServeMux()
	deps := gatewayhttp.AuditVerifyStaticDeps{Ledger: ledger}
	mux.HandleFunc("/v1/audit/verify", gatewayhttp.NewAuditVerifyHandler(deps))
	mux.HandleFunc("/v1/audit/merkle-tree.json", gatewayhttp.NewAuditMerkleTreeHandler(deps))
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
	entry, err := ledger.Append(context.Background(), auditledger.LedgerEntry{
		LedgerID:  "lid_substitution",
		RequestID: entryReqID,
		TenantID:  entryTenant,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	verifyBodyBytes, err := json.Marshal(gatewayhttp.AuditVerifyResponse{
		LedgerEntry: gatewayhttp.AuditLedgerEntryJSON{
			LedgerID:       entry.LedgerID,
			Timestamp:      entry.Timestamp,
			RequestID:      entry.RequestID,
			TenantScopeRef: auditledger.TenantScopeRef(entry.TenantID),
			HopChain:       entry.HopChain,
		},
		ChainProof: gatewayhttp.AuditChainProofJSON{
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
