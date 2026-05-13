package auditledger

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func sampleEntry(seq int) LedgerEntry {
	return LedgerEntry{
		LedgerID:          "lid_" + itoa(seq),
		Timestamp:         "2026-05-13T10:00:00Z",
		RequestID:         "req_" + itoa(seq),
		TenantID:          int64(seq),
		HopChain:          []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
		PubkeyFingerprint: "fp" + itoa(seq),
	}
}

func TestEntryHash_DeterministicAndUnique(t *testing.T) {
	a := sampleEntry(1)
	b := sampleEntry(1)
	ha, _ := EntryHash(&a)
	hb, _ := EntryHash(&b)
	if ha != hb {
		t.Errorf("same entry must hash same: %x vs %x", ha, hb)
	}
	c := sampleEntry(2)
	hc, _ := EntryHash(&c)
	if ha == hc {
		t.Error("different entries must hash differently")
	}
}

func TestEntryHash_NilSafe(t *testing.T) {
	h, err := EntryHash(nil)
	if err != nil {
		t.Errorf("nil entry err: %v", err)
	}
	if h != ZeroRoot {
		t.Errorf("nil entry must hash to zero, got %x", h)
	}
}

func TestNextMerkleRoot_ChainChange(t *testing.T) {
	e1 := sampleEntry(1)
	e2 := sampleEntry(2)
	h1, _ := EntryHash(&e1)
	h2, _ := EntryHash(&e2)
	r1 := NextMerkleRoot(ZeroRoot, h1)
	r2 := NextMerkleRoot(r1, h2)
	if r1 == ZeroRoot {
		t.Error("r1 must differ from ZeroRoot")
	}
	if r2 == r1 {
		t.Error("r2 must differ from r1")
	}
}

func TestVerifyChain_HappyPath(t *testing.T) {
	e1 := sampleEntry(1)
	h1, _ := EntryHash(&e1)
	e1.PrevMerkleRoot = ZeroRoot
	e1.MerkleRoot = NextMerkleRoot(ZeroRoot, h1)

	e2 := sampleEntry(2)
	h2, _ := EntryHash(&e2)
	e2.PrevMerkleRoot = e1.MerkleRoot
	e2.MerkleRoot = NextMerkleRoot(e1.MerkleRoot, h2)

	if err := VerifyChain([]LedgerEntry{e1, e2}); err != nil {
		t.Errorf("happy chain verify failed: %v", err)
	}
}

func TestVerifyChain_DetectTamperedFirstPrev(t *testing.T) {
	e1 := sampleEntry(1)
	e1.PrevMerkleRoot[0] = 0xff // not ZeroRoot
	err := VerifyChain([]LedgerEntry{e1})
	if err == nil {
		t.Fatal("tampered first prev_root must fail")
	}
	ce, ok := err.(*ChainError)
	if !ok || ce.Index != 0 || ce.Reason != "prev_merkle_root_mismatch" {
		t.Errorf("expected ChainError prev_merkle_root_mismatch idx=0, got %v", err)
	}
}

func TestVerifyChain_DetectTamperedMiddleRoot(t *testing.T) {
	e1 := sampleEntry(1)
	h1, _ := EntryHash(&e1)
	e1.PrevMerkleRoot = ZeroRoot
	e1.MerkleRoot = NextMerkleRoot(ZeroRoot, h1)

	e2 := sampleEntry(2)
	e2.PrevMerkleRoot = e1.MerkleRoot
	// 故意篡改 e2.MerkleRoot
	e2.MerkleRoot[15] ^= 0xff

	err := VerifyChain([]LedgerEntry{e1, e2})
	if err == nil {
		t.Fatal("tampered middle root must fail")
	}
	ce, ok := err.(*ChainError)
	if !ok || ce.Index != 1 || ce.Reason != "merkle_root_mismatch" {
		t.Errorf("expected ChainError merkle_root_mismatch idx=1, got %v", err)
	}
}

func TestVerifyChain_EmptyOK(t *testing.T) {
	if err := VerifyChain(nil); err != nil {
		t.Errorf("empty chain must verify, got %v", err)
	}
}
