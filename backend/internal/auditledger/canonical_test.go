package auditledger

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func canonicalSampleEntry() LedgerEntry {
	return LedgerEntry{
		LedgerID:          "ldg_demo",
		Timestamp:         "2026-05-17T10:11:12Z",
		RequestID:         "req_demo",
		TenantID:          42,
		PubkeyFingerprint: "0011223344556677",
		HopChain: []proto.HopAttestation{
			{
				SchemaVersion: "trust.hop.v1",
				HopIndex:      4,
				HopKind:       "upstream_dispatch",
				Actor:         "gateway",
				StartedAt:     "2026-05-17T10:11:12Z",
				EndedAt:       "2026-05-17T10:11:13Z",
				DecisionRef:   "provider_family=anthropic",
				FeatureRefs:   []string{"F-TRUST-001"},
				Detail:        json.RawMessage(`{"z":2,"a":1}`),
			},
		},
		ModelChain: &proto.ModelChain{
			Requested:        "claude-opus-4-7",
			RouteDecided:     "claude-opus-4-7",
			UpstreamReported: "claude-opus-4-7",
			Verdict:          "match",
		},
	}
}

func TestCanonicalPayloadStableAcrossCalls(t *testing.T) {
	entry := canonicalSampleEntry()
	a := CanonicalPayload(entry)
	b := CanonicalPayload(entry)
	if !bytes.Equal(a, b) {
		t.Fatalf("canonical payload changed across calls:\n%s\n%s", a, b)
	}
	entry.Signature = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 64))
	if c := CanonicalPayload(entry); !bytes.Equal(a, c) {
		t.Fatalf("signature must be excluded from canonical payload:\n%s\n%s", a, c)
	}
}

func TestCanonicalPayloadPythonReproducible(t *testing.T) {
	entry := canonicalSampleEntry()
	goPayload := CanonicalPayload(entry)
	script := `
import hashlib, json
tenant_scope = "tenant:" + hashlib.sha256(b"huakai-ledger-tenant-scope-v1:42").hexdigest()[:16]
obj = {
  "schema_version": "trust.ledger.v1",
  "ledger_id": "ldg_demo",
  "occurred_at": "2026-05-17T10:11:12Z",
  "request_id": "req_demo",
  "tenant_scope_ref": tenant_scope,
  "hop_chain": [{
    "actor": "gateway",
    "decision_ref": "provider_family=anthropic",
    "detail": {"a": 1, "z": 2},
    "ended_at": "2026-05-17T10:11:13Z",
    "feature_refs": ["F-TRUST-001"],
    "hop_index": 4,
    "hop_kind": "upstream_dispatch",
    "schema_version": "trust.hop.v1",
    "started_at": "2026-05-17T10:11:12Z"
  }],
  "model_chain": {
    "requested": "claude-opus-4-7",
    "route_decided": "claude-opus-4-7",
    "upstream_reported": "claude-opus-4-7",
    "verdict": "match"
  },
  "prev_merkle_root": "0000000000000000000000000000000000000000000000000000000000000000",
  "pubkey_fingerprint": "0011223344556677"
}
print(json.dumps(obj, separators=(",", ":")), end="")
`
	python := python3ForTest(t)
	out, err := exec.Command(python, "-c", script).Output()
	if err != nil {
		t.Fatalf("python canonical reproduction failed: %v", err)
	}
	if !bytes.Equal(goPayload, out) {
		t.Fatalf("Go/Python canonical payload mismatch:\nGo: %s\nPy: %s", goPayload, out)
	}
}

func python3ForTest(t *testing.T) string {
	t.Helper()

	var attempts []string
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("%s lookpath: %v", name, err))
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, runErr := exec.CommandContext(ctx, path, "-c", "import sys; print(sys.version_info[0])").CombinedOutput()
		cancel()
		trimmed := strings.TrimSpace(string(out))
		if runErr == nil && trimmed == "3" {
			return path
		}
		if ctx.Err() == context.DeadlineExceeded {
			attempts = append(attempts, fmt.Sprintf("%s timed out", name))
			continue
		}
		attempts = append(attempts, fmt.Sprintf("%s unusable: %v output=%q", name, runErr, trimmed))
	}
	t.Skipf("python3/python 不可用或不是 Python 3，跳过跨语言可复现验证: %s", strings.Join(attempts, "; "))
	return ""
}

func TestEntryHashUsesCanonicalPayload(t *testing.T) {
	entry := canonicalSampleEntry()
	got, err := EntryHash(&entry)
	if err != nil {
		t.Fatalf("EntryHash: %v", err)
	}
	want := sha256.Sum256(CanonicalPayload(entry))
	if got != want {
		t.Fatalf("EntryHash mismatch: got %s want %s", hex.EncodeToString(got[:]), hex.EncodeToString(want[:]))
	}
}

func TestTenantScopeRefDoesNotExposeTenantID(t *testing.T) {
	ref := TenantScopeRef(42)
	if !strings.HasPrefix(ref, "tenant:") {
		t.Fatalf("tenant scope ref prefix: %q", ref)
	}
	if strings.Contains(ref, "42") {
		t.Fatalf("tenant scope ref exposed raw tenant id: %q", ref)
	}
}
