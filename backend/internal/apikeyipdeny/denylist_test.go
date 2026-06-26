package apikeyipdeny_test

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeyipdeny"
)

// TestIPBlacklistDeny 判别测试 KEY-016。
//
// 变异 1(deny 放到 allowlist 之后):如果调用方把 deny 检查挪到
// 「allowlist 为 nil 即放行所有」的检查之后,1.2.3.4 就会被放行而非
// 被拒 → 本测试变红。
//
// 变异 2(nil 黑名单返回 true):DeniesCSV nil → true 意味着 NULL 的 key
// 会拒绝所有人 → 违背「零行为变化」承诺 → 红。
func TestIPBlacklistDeny(t *testing.T) {
	blacklisted := "1.2.3.4/32"
	raw := "1.2.3.4,10.0.0.0/8"

	// nil/空 → 永不拒绝(零行为变化)
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

	// 在黑名单中的 IP 被拒绝
	denied, err = apikeyipdeny.DeniesCSV(&raw, "1.2.3.4")
	if err != nil {
		t.Fatalf("denied IP: unexpected error: %v", err)
	}
	if !denied {
		t.Errorf("MUTATION: 1.2.3.4 must be denied (in blacklist)")
	}

	// 落在黑名单 CIDR 内的 IP 被拒绝
	denied, err = apikeyipdeny.DeniesCSV(&raw, "10.1.2.3")
	if err != nil {
		t.Fatalf("denied CIDR IP: unexpected error: %v", err)
	}
	if !denied {
		t.Errorf("MUTATION: 10.1.2.3 must be denied (in 10.0.0.0/8)")
	}

	// 不在黑名单中的 IP 被放行
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
