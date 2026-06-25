package auditledger

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func fullTrustHopChain(requestID string) []proto.HopAttestation {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	kinds := []string{
		"ingress_auth",
		"policy_match",
		"pool_select",
		"credential_select",
		"upstream_dispatch",
		"response_finalize",
	}
	out := make([]proto.HopAttestation, 0, len(kinds))
	for i, kind := range kinds {
		out = append(out, proto.HopAttestation{
			SchemaVersion: "trust.hop.v1",
			HopIndex:      i,
			HopKind:       kind,
			Actor:         "gateway",
			StartedAt:     now.Add(time.Duration(i) * time.Millisecond).Format(time.RFC3339Nano),
			EndedAt:       now.Add(time.Duration(i+1) * time.Millisecond).Format(time.RFC3339Nano),
			DecisionRef:   "reason_class=safe_metadata",
			FeatureRefs:   []string{"F-TRUST-001"},
			RequestID:     requestID,
		})
	}
	return out
}

func TestATTrust001001MemoryLedgerOneRowRequestUniqueTenant(t *testing.T) {
	signer, err := NewLocalEd25519Signer(testPrivateKey(11), nil)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("NewMemoryLedger: %v", err)
	}
	ctx := context.Background()
	entry, err := ledger.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{
		RequestID: "req-at-001",
		TenantID:  123,
		HopChain:  fullTrustHopChain("req-at-001"),
	}))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if ledger.Size(ctx) != 1 {
		t.Fatalf("ledger size: got %d want 1", ledger.Size(ctx))
	}
	got, err := ledger.GetByRequestID(ctx, "req-at-001")
	if err != nil {
		t.Fatalf("GetByRequestID: %v", err)
	}
	if got.RequestID != "req-at-001" || got.TenantID != 123 || entry.TenantID != 123 {
		t.Fatalf("request/tenant mismatch: got %+v entry %+v", got, entry)
	}
	if _, err := ledger.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req-at-001", TenantID: 123})); err != ErrDuplicateRequestID {
		t.Fatalf("duplicate request_id: got %v want %v", err, ErrDuplicateRequestID)
	}
}

func TestATTrust001002HopChainCompleteAndRedacted(t *testing.T) {
	entry := LedgerEntry{
		LedgerID:          "ldg-at-002",
		Timestamp:         "2026-05-17T12:00:00Z",
		RequestID:         "req-at-002",
		TenantID:          123,
		HopChain:          fullTrustHopChain("req-at-002"),
		PubkeyFingerprint: "0011223344556677",
	}
	payload := CanonicalPayload(entry)
	for _, want := range []string{"ingress_auth", "policy_match", "pool_select", "credential_select", "upstream_dispatch", "response_finalize"} {
		if !bytes.Contains(payload, []byte(want)) {
			t.Fatalf("canonical hop_chain missing %s in %s", want, payload)
		}
	}
	for _, forbidden := range []string{"PROMPT_SENTINEL", "completion", "cookie=", "Authorization", "sk-"} {
		if bytes.Contains(payload, []byte(forbidden)) {
			t.Fatalf("canonical hop_chain leaked forbidden sentinel %q: %s", forbidden, payload)
		}
	}
}

func TestATTrust001003ModelChainVerdictClasses(t *testing.T) {
	for _, verdict := range []string{"match", "allowed_alias", "mismatch", "unknown"} {
		entry := canonicalSampleEntry()
		entry.ModelChain.Verdict = verdict
		payload := CanonicalPayload(entry)
		if !bytes.Contains(payload, []byte(`"verdict":"`+verdict+`"`)) {
			t.Fatalf("model_chain verdict %s missing from %s", verdict, payload)
		}
		for _, field := range []string{"requested", "route_decided", "upstream_reported"} {
			if !bytes.Contains(payload, []byte(`"`+field+`"`)) {
				t.Fatalf("model_chain field %s missing from %s", field, payload)
			}
		}
	}
}

func TestATTrust001004ChainContinuity100Entries(t *testing.T) {
	signer, err := NewLocalEd25519Signer(testPrivateKey(12), nil)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("NewMemoryLedger: %v", err)
	}
	ctx := context.Background()
	var prev [32]byte
	for i := 0; i < 100; i++ {
		out, err := ledger.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{
			RequestID: "req-at-004-" + itoa(i),
			TenantID:  123,
			HopChain:  fullTrustHopChain("req-at-004-" + itoa(i)),
		}))
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if out.PrevMerkleRoot != prev {
			t.Fatalf("entry %d prev root mismatch", i)
		}
		prev = out.MerkleRoot
	}
	if err := VerifyChain(ledger.Snapshot()); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

func TestPostgresAdvisoryLockKeyIsTenantScoped(t *testing.T) {
	if auditLedgerAdvisoryLockKey(1) == auditLedgerAdvisoryLockKey(2) {
		t.Fatal("tenant advisory lock keys must differ")
	}
	// 同入参两次调用须得同值(确定性守卫);分别捕获再判,避免 SA4000 把确定性断言误判为自反比较。
	stableKeyA := auditLedgerAdvisoryLockKey(1)
	stableKeyB := auditLedgerAdvisoryLockKey(1)
	if stableKeyA != stableKeyB {
		t.Fatal("tenant advisory lock key must be stable")
	}
}

func TestATTrust001005EntrySignatureVerifyAndTamper(t *testing.T) {
	ctx := context.Background()
	signer, err := NewLocalEd25519Signer(testPrivateKey(13), nil)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("NewMemoryLedger: %v", err)
	}
	entry, err := ledger.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{
		RequestID: "req-at-005",
		TenantID:  123,
		HopChain:  fullTrustHopChain("req-at-005"),
	}))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	hash, err := EntryHash(&entry)
	if err != nil {
		t.Fatalf("EntryHash: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(entry.Signature)
	if err != nil {
		t.Fatalf("signature base64: %v", err)
	}
	ok, err := signer.Verify(ctx, hash[:], sig, entry.PubkeyFingerprint)
	if err != nil || !ok {
		t.Fatalf("verify entry: ok=%v err=%v", ok, err)
	}
	hash[0] ^= 0xff
	ok, err = signer.Verify(ctx, hash[:], sig, entry.PubkeyFingerprint)
	if err != nil {
		t.Fatalf("verify tampered hash returned error: %v", err)
	}
	if ok {
		t.Fatal("tampered hash verified")
	}
}

func TestATTrust001006AppendOnlyMigrationDefinesUpdateDeleteTriggers(t *testing.T) {
	raw, err := os.ReadFile("../../sql/migrations/0027_ledger_append_only_trigger.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE OR REPLACE FUNCTION enforce_ledger_append_only()",
		"RAISE EXCEPTION 'audit_ledger_entries is append-only: %', TG_OP",
		"CREATE TRIGGER ledger_append_only_update BEFORE UPDATE ON audit_ledger_entries",
		"CREATE TRIGGER ledger_append_only_delete BEFORE DELETE ON audit_ledger_entries",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}
