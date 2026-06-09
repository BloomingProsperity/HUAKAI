package recentreq

import (
	"testing"
	"time"
)

func TestRingRecordAndSummary(t *testing.T) {
	r := NewRing()
	const accountID = int64(42)

	// no data yet
	s := r.Summary(accountID)
	if s.Total != 0 || s.Success != 0 || s.Failure != 0 {
		t.Fatalf("empty summary=%+v want zeros", s)
	}

	before := time.Now()
	r.Record(accountID, true)
	r.Record(accountID, true)
	r.Record(accountID, false)
	after := time.Now()

	s = r.Summary(accountID)
	if s.Total != 3 {
		t.Fatalf("total=%d want 3", s.Total)
	}
	if s.Success != 2 {
		t.Fatalf("success=%d want 2", s.Success)
	}
	if s.Failure != 1 {
		t.Fatalf("failure=%d want 1", s.Failure)
	}
	if s.LastAt.Before(before) || s.LastAt.After(after) {
		t.Fatalf("last_at=%v not in [%v, %v]", s.LastAt, before, after)
	}
}

func TestRingEvictsOldestAtCap(t *testing.T) {
	r := NewRing()
	const accountID = int64(7)

	// fill to cap: all failures
	for i := 0; i < ringCap; i++ {
		r.Record(accountID, false)
	}
	// one more: evicts oldest failure, adds success
	r.Record(accountID, true)

	s := r.Summary(accountID)
	if s.Total != ringCap {
		t.Fatalf("total=%d want %d (cap)", s.Total, ringCap)
	}
	if s.Success != 1 {
		t.Fatalf("success=%d want 1", s.Success)
	}
	if s.Failure != ringCap-1 {
		t.Fatalf("failure=%d want %d", s.Failure, ringCap-1)
	}
}

func TestRingNilSafe(t *testing.T) {
	var r *Ring
	r.Record(1, true)
	s := r.Summary(1)
	if s.Total != 0 {
		t.Fatalf("nil ring summary total=%d want 0", s.Total)
	}
}

func TestRingIsolatesAccounts(t *testing.T) {
	r := NewRing()
	r.Record(1, true)
	r.Record(2, false)

	s1 := r.Summary(1)
	s2 := r.Summary(2)
	if s1.Success != 1 || s1.Failure != 0 {
		t.Fatalf("account 1: success=%d failure=%d want 1/0", s1.Success, s1.Failure)
	}
	if s2.Success != 0 || s2.Failure != 1 {
		t.Fatalf("account 2: success=%d failure=%d want 0/1", s2.Success, s2.Failure)
	}
}
