package provider

import "testing"

func mkMembers(n int) []proxyFields {
	out := make([]proxyFields, 0, n)
	for i := 0; i < n; i++ {
		h := string(rune('a' + i))
		out = append(out, proxyFields{protocol: "http", host: h, port: 1000 + i})
	}
	return out
}

// PROXY-05: rotation must be deterministic per account (sticky residential IP)
// yet spread different accounts across the pool (anti-clustering).
func TestPickGroupMember(t *testing.T) {
	members := mkMembers(4)

	// empty pool -> nil (caller fail-closes)
	if pickGroupMember(7, nil) != nil {
		t.Fatal("empty member list must return nil")
	}

	// deterministic: same account -> same member
	for _, id := range []int64{1, 42, 9999} {
		a := pickGroupMember(id, members)
		b := pickGroupMember(id, members)
		if a == nil || b == nil || a.host != b.host {
			t.Fatalf("account %d not sticky: %v vs %v", id, a, b)
		}
	}

	// MUTATION GUARD: hardcoding the index collapses rotation to one IP for every
	// account (defeats anti-clustering) -> this spread assertion goes red.
	seen := map[string]bool{}
	for id := int64(1); id <= 64; id++ {
		m := pickGroupMember(id, members)
		if m == nil {
			t.Fatalf("non-empty pool returned nil for account %d", id)
		}
		seen[m.host] = true
	}
	if len(seen) < 2 {
		t.Fatalf("rotation must spread accounts across the pool, collapsed to %d", len(seen))
	}

	// picked member is always in the pool
	for id := int64(1); id <= 20; id++ {
		m := pickGroupMember(id, members)
		ok := false
		for i := range members {
			if members[i].host == m.host {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("account %d picked a member not in the pool: %v", id, m)
		}
	}
}
