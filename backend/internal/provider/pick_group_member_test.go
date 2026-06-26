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

// PROXY-05：轮换必须对每个账号是确定性的（住宅 IP 粘定），同时又要把不同账号
// 散布到整个代理池中（防聚集）。
func TestPickGroupMember(t *testing.T) {
	members := mkMembers(4)

	// 空池 -> nil（由调用方 fail-closed）
	if pickGroupMember(7, nil) != nil {
		t.Fatal("empty member list must return nil")
	}

	// 确定性：同一账号 -> 同一成员
	for _, id := range []int64{1, 42, 9999} {
		a := pickGroupMember(id, members)
		b := pickGroupMember(id, members)
		if a == nil || b == nil || a.host != b.host {
			t.Fatalf("account %d not sticky: %v vs %v", id, a, b)
		}
	}

	// 变异哨兵：若把索引写死，会让所有账号的轮换坍缩到同一个 IP
	// （破坏防聚集）-> 此散布断言会变红。
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

	// 选中的成员必须始终在池中
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
