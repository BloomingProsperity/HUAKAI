package auditledger

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func TestNoopLedger_AllNoop(t *testing.T) {
	l := NoopLedger{}
	ctx := context.Background()
	in := LedgerEntry{RequestID: "req_001"}
	out, err := l.Append(ctx, in)
	if err != nil {
		t.Errorf("Append err: %v", err)
	}
	if out.RequestID != "req_001" {
		t.Errorf("Append must return input unchanged")
	}
	if _, err := l.GetByRequestID(ctx, "req_001"); !errors.Is(err, ErrLedgerEntryNotFound) {
		t.Errorf("Noop Get must be ErrLedgerEntryNotFound")
	}
	root, _ := l.LatestMerkleRoot(ctx)
	if root != ZeroRoot {
		t.Errorf("Noop root must be ZeroRoot")
	}
	if l.Size(ctx) != 0 {
		t.Errorf("Noop Size must be 0")
	}
}

func TestNewMemoryLedger_NilSignerRejected(t *testing.T) {
	if _, err := NewMemoryLedger(nil); !errors.Is(err, ErrSignerNil) {
		t.Errorf("expected ErrSignerNil, got %v", err)
	}
}

func TestMemoryLedger_AppendComputesFields(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	entry := LedgerEntry{
		LedgerID:  "lid_1",
		RequestID: "req_1",
		TenantID:  42,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	}
	out, err := l.Append(ctx, entry)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if out.Timestamp == "" {
		t.Error("Timestamp must be auto-filled")
	}
	if out.PrevMerkleRoot != ZeroRoot {
		t.Error("first entry PrevMerkleRoot must be ZeroRoot")
	}
	if out.MerkleRoot == ZeroRoot {
		t.Error("MerkleRoot must be non-zero")
	}
	if out.PubkeyFingerprint != signer.Fingerprint() {
		t.Errorf("PubkeyFingerprint mismatch: got %q want %q", out.PubkeyFingerprint, signer.Fingerprint())
	}
	if out.Signature == "" {
		t.Error("Signature must be non-empty")
	}
	// 解 base64 signature 应得 64 bytes ed25519。
	sig, err := base64.StdEncoding.DecodeString(out.Signature)
	if err != nil || len(sig) != 64 {
		t.Errorf("Signature decode wrong: err=%v len=%d", err, len(sig))
	}
}

func TestMemoryLedger_ChainContinuity(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	e1, _ := l.Append(ctx, LedgerEntry{LedgerID: "1", RequestID: "r1"})
	e2, _ := l.Append(ctx, LedgerEntry{LedgerID: "2", RequestID: "r2"})

	if e2.PrevMerkleRoot != e1.MerkleRoot {
		t.Errorf("e2.PrevMerkleRoot != e1.MerkleRoot")
	}
	if l.Size(ctx) != 2 {
		t.Errorf("size: %d", l.Size(ctx))
	}
	root, _ := l.LatestMerkleRoot(ctx)
	if root != e2.MerkleRoot {
		t.Errorf("latest root mismatch")
	}
}

func TestMemoryLedger_GetByRequestID(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	_, _ = l.Append(ctx, LedgerEntry{LedgerID: "1", RequestID: "rA"})
	_, _ = l.Append(ctx, LedgerEntry{LedgerID: "2", RequestID: "rB"})

	got, err := l.GetByRequestID(ctx, "rB")
	if err != nil {
		t.Fatalf("Get rB: %v", err)
	}
	if got.LedgerID != "2" {
		t.Errorf("got LedgerID=%q want 2", got.LedgerID)
	}

	if _, err := l.GetByRequestID(ctx, "missing"); !errors.Is(err, ErrLedgerEntryNotFound) {
		t.Errorf("expected not found, got %v", err)
	}
}

func TestMemoryLedger_SnapshotIsDeepCopy(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	_, _ = l.Append(ctx, LedgerEntry{LedgerID: "1", RequestID: "r1"})
	snap1 := l.Snapshot()
	_, _ = l.Append(ctx, LedgerEntry{LedgerID: "2", RequestID: "r2"})
	snap2 := l.Snapshot()

	if len(snap1) != 1 || len(snap2) != 2 {
		t.Errorf("snapshots don't reflect appends: %d / %d", len(snap1), len(snap2))
	}
	// 篡改 snap1 不应影响内部 chain。
	snap1[0].LedgerID = "polluted"
	got, _ := l.GetByRequestID(ctx, "r1")
	if got.LedgerID != "1" {
		t.Error("snapshot mutation polluted internal chain")
	}
}

func TestMemoryLedger_VerifyChainHappyPath(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, _ = l.Append(ctx, LedgerEntry{LedgerID: itoa(i), RequestID: "r" + itoa(i)})
	}
	snap := l.Snapshot()
	if err := VerifyChain(snap); err != nil {
		t.Errorf("verify chain failed: %v", err)
	}
}

func TestMemoryLedger_VerifySignaturesIndependently(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	entry, _ := l.Append(ctx, LedgerEntry{LedgerID: "1", RequestID: "r1"})

	// 模拟 user 用公开 pubkey 独立 verify entry signature
	sig, err := base64.StdEncoding.DecodeString(entry.Signature)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	eh, _ := EntryHash(&entry)
	if err := sign.Verify(signer.PublicKey(), eh[:], sig); err != nil {
		t.Errorf("independent verify failed: %v", err)
	}
}
