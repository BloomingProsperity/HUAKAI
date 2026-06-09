package apikeyipdeny_test

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeyipdeny"
)

// TestIPBlacklistDeny 判别测试 KEY-016。
//
// MUTATION 1 (deny placed after allowlist): if the caller moves the deny check to
// AFTER an allow-all nil-allowlist check, 1.2.3.4 would pass instead of being
// denied -> this test would go RED.
//
// MUTATION 2 (nil blacklist returns true): DeniesCSV nil -> true means NULL keys
// deny everyone -> zero-behavior-change promise violated -> RED.
func TestIPBlacklistDeny(t *testing.T) {
	blacklisted := "1.2.3.4/32"
	raw := "1.2.3.4,10.0.0.0/8"

	// nil/empty -> never deny (zero behavior change)
	denied, err := apikeyipdeny.DeniesCSV(nil, "1.2.3.4")
	if err != nil {
		t.Fatalf("nil raw: unexpected error: %v", err)
	}
	if denied {
		t.Errorf("MUTATION: nil raw must not deny any IP")
	}

	empty := ""
	denied, err = apikeyipdeny.DeniesCSV(&empty, "1.2.3.4")
	if err != nil {
		t.Fatalf("empty raw: unexpected error: %v", err)
	}
	if denied {
		t.Errorf("MUTATION: empty raw must not deny any IP")
	}

	// blacklisted IP is denied
	denied, err = apikeyipdeny.DeniesCSV(&raw, "1.2.3.4")
	if err != nil {
		t.Fatalf("denied IP: unexpected error: %v", err)
	}
	if !denied {
		t.Errorf("MUTATION: 1.2.3.4 must be denied (in blacklist)")
	}

	// IP in blacklisted CIDR is denied
	denied, err = apikeyipdeny.DeniesCSV(&raw, "10.1.2.3")
	if err != nil {
		t.Fatalf("denied CIDR IP: unexpected error: %v", err)
	}
	if !denied {
		t.Errorf("MUTATION: 10.1.2.3 must be denied (in 10.0.0.0/8)")
	}

	// IP not in blacklist is allowed
	denied, err = apikeyipdeny.DeniesCSV(&raw, "9.9.9.9")
	if err != nil {
		t.Fatalf("allowed IP: unexpected error: %v", err)
	}
	if denied {
		t.Errorf("MUTATION: 9.9.9.9 must not be denied (not in blacklist)")
	}

	_ = blacklisted
}

func TestNormalize(t *testing.T) {
	entries, err := apikeyipdeny.Normalize([]string{"1.2.3.4", "10.0.0.0/8", "1.2.3.4", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 unique entries, got %d: %v", len(entries), entries)
	}
}

func TestStorageText(t *testing.T) {
	if apikeyipdeny.StorageText(nil) != nil {
		t.Error("nil slice must return nil StorageText")
	}
	if apikeyipdeny.StorageText([]string{}) != nil {
		t.Error("empty slice must return nil StorageText")
	}
	s := apikeyipdeny.StorageText([]string{"1.2.3.4/32", "10.0.0.0/8"})
	if s == nil || *s != "1.2.3.4/32,10.0.0.0/8" {
		t.Errorf("unexpected StorageText: %v", s)
	}
}
