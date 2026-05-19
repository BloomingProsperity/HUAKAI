package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
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
		"--gateway-url=" + gateway.URL,
	}, &out, gateway.Client())
	if code != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "✅") || !strings.Contains(out.String(), signer.Fingerprint()) {
		t.Fatalf("success output missing checkmarks or fingerprint: %s", out.String())
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
