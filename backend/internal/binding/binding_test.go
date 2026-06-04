// binding_test.go — U1-A 测试：interface + noop 行为契约。
//
// 不连数据库；不需 schema migration；schema-free e2e-lite。
package binding

import (
	"context"
	"testing"
)

func TestNoopCache_LookupAlwaysMiss(t *testing.T) {
	cache := NoopCache{}
	scope := BindingScope{
		TenantID: 1,
		APIKeyID: 42,
		UserID:   7,
		Model:    "gpt-4o",
	}
	snap, hit, err := cache.Lookup(context.Background(), scope)
	if err != nil {
		t.Errorf("noop Lookup 不应有 err，得 %v", err)
	}
	if hit {
		t.Errorf("noop Lookup 应总 miss，得 hit=true")
	}
	if snap.BindingID != 0 || snap.Kind != "" {
		t.Errorf("noop snap 应零值，得 %+v", snap)
	}
}

func TestNoopCache_NilContextAccepted(t *testing.T) {
	// noop 不读 ctx — nil 安全
	cache := NoopCache{}
	_, hit, err := cache.Lookup(nil, BindingScope{TenantID: 1}) //nolint:staticcheck
	if err != nil || hit {
		t.Errorf("nil ctx noop 应静默 miss")
	}
}

func TestNoopCache_ConcurrentSafe(t *testing.T) {
	cache := NoopCache{}
	const n = 100
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			scope := BindingScope{TenantID: int64(id), Model: "x"}
			_, _, err := cache.Lookup(context.Background(), scope)
			if err != nil {
				t.Errorf("goroutine %d err=%v", id, err)
			}
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
}

// TestBindingScope_KindEnumValues 守界：BindingKind 枚举值已声明（防漏 commit）
func TestBindingScope_KindEnumValues(t *testing.T) {
	cases := []BindingKind{BindingKindPoolGroup, BindingKindProviderAccount, BindingKindTenantDefault}
	seen := map[BindingKind]bool{}
	for _, k := range cases {
		if k == "" {
			t.Errorf("BindingKind 不应为空字符串")
		}
		if seen[k] {
			t.Errorf("BindingKind %q 重复", k)
		}
		seen[k] = true
	}
}

// TestBindingCacheInterface_NoopSatisfies 编译期断言已在源文件; 此处再
// 跑时验证一次确保未来不被改坏。
func TestBindingCacheInterface_NoopSatisfies(t *testing.T) {
	var c BindingCache = NoopCache{}
	if c == nil {
		t.Fatal("NoopCache 应可作 BindingCache 赋值")
	}
}

// TestBindingScope_Valid 守界: degenerate scope 应判 invalid，
// caller 据此 fail-fast，不进入 noop miss 假阳。
func TestBindingScope_Valid(t *testing.T) {
	cases := []struct {
		name  string
		scope BindingScope
		want  bool
	}{
		{"happy api_key", BindingScope{TenantID: 1, APIKeyID: 42}, true},
		{"happy user", BindingScope{TenantID: 1, UserID: 7}, true},
		{"happy both", BindingScope{TenantID: 1, APIKeyID: 42, UserID: 7}, true},
		{"with model", BindingScope{TenantID: 1, APIKeyID: 42, Model: "gpt-4o"}, true},
		// degenerate
		{"missing tenant", BindingScope{APIKeyID: 42}, false},
		{"tenant zero + key/user zero", BindingScope{TenantID: 0}, false},
		{"tenant only no key/user", BindingScope{TenantID: 1}, false},
		{"negative tenant", BindingScope{TenantID: -1, APIKeyID: 42}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.scope.Valid(); got != tc.want {
				t.Errorf("Valid()=%v want %v scope=%+v", got, tc.want, tc.scope)
			}
		})
	}
}
