package tlsfpresolve

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

func validFields() mimicry.ProfileFields {
	return mimicry.ProfileFields{
		Name:                 "tenant-chrome",
		GreaseEnabled:        true,
		CipherSuites:         []int{0x1301, 0x1302},
		SupportedCurves:      []int{29, 23},
		EcPointFormats:       []int{0},
		SignatureAlgorithms:  []int{0x0403},
		AlpnProtocols:        []string{"h2"},
		TLSSupportedVersions: []int{0x0304},
		KeyShareGroups:       []int{29},
		PskModes:             []int{1},
		ExtensionsOrder:      []int{0, 23},
		ExpectedJA3Hash:      "abc",
	}
}

func fieldsWithJA3(ja3 string) mimicry.ProfileFields {
	f := validFields()
	f.ExpectedJA3Hash = ja3
	return f
}

type fakeFetcher struct {
	st  accountState
	err error
}

func (f fakeFetcher) fetch(context.Context, int64) (accountState, error) { return f.st, f.err }

func ptr(f mimicry.ProfileFields) *mimicry.ProfileFields { return &f }

// ---- ResolveRoundTripper(经 fake fetcher 端到端)----

func TestResolver_BoundActive_BuildsRT(t *testing.T) {
	r := newResolver(fakeFetcher{st: accountState{bound: ptr(validFields())}})
	rt, err := r.ResolveRoundTripper(context.Background(), 7)
	if err != nil || rt == nil {
		t.Fatalf("bound active profile should build a uTLS RT, got rt=%v err=%v", rt, err)
	}
}

func TestResolver_NoProfile_Builtin(t *testing.T) {
	r := newResolver(fakeFetcher{st: accountState{}})
	rt, err := r.ResolveRoundTripper(context.Background(), 7)
	if err != nil || rt != nil {
		t.Fatalf("no profile must fall back to builtin (nil RT), got rt=%v err=%v", rt, err)
	}
}

func TestResolver_InvalidBound_FallsBackBuiltin(t *testing.T) {
	bad := validFields()
	bad.CipherSuites = []int{0x10000}
	r := newResolver(fakeFetcher{st: accountState{bound: ptr(bad)}})
	rt, err := r.ResolveRoundTripper(context.Background(), 7)
	if err != nil || rt != nil {
		t.Fatalf("invalid profile must fall back to builtin (nil RT, no error), got rt=%v err=%v", rt, err)
	}
}

func TestResolver_Rotate_PicksFromPool_BuildsRT(t *testing.T) {
	pool := []mimicry.ProfileFields{fieldsWithJA3("a"), fieldsWithJA3("b"), fieldsWithJA3("c")}
	r := newResolver(fakeFetcher{st: accountState{rotate: true, pool: pool}})
	rt, err := r.ResolveRoundTripper(context.Background(), 7)
	if err != nil || rt == nil {
		t.Fatalf("rotate + non-empty pool should build a uTLS RT, got rt=%v err=%v", rt, err)
	}
}

func TestResolver_Rotate_EmptyPool_Builtin(t *testing.T) {
	r := newResolver(fakeFetcher{st: accountState{rotate: true}})
	rt, err := r.ResolveRoundTripper(context.Background(), 7)
	if err != nil || rt != nil {
		t.Fatalf("rotate + empty pool must fall back to builtin (nil RT), got rt=%v err=%v", rt, err)
	}
}

func TestResolver_FetchError_Propagates(t *testing.T) {
	sentinel := errors.New("db down")
	r := newResolver(fakeFetcher{err: sentinel})
	if _, err := r.ResolveRoundTripper(context.Background(), 7); !errors.Is(err, sentinel) {
		t.Fatalf("infra fetch error should propagate, got %v", err)
	}
}

func TestResolver_ZeroAccount_Builtin(t *testing.T) {
	r := newResolver(fakeFetcher{st: accountState{bound: ptr(validFields())}})
	rt, err := r.ResolveRoundTripper(context.Background(), 0)
	if err != nil || rt != nil {
		t.Fatalf("accountID 0 -> builtin (nil RT), got rt=%v err=%v", rt, err)
	}
}

// ---- 纯选择逻辑 ----

func TestPickIndex_DeterministicAndSpread(t *testing.T) {
	for _, id := range []int64{1, 2, 99} {
		// 同入参两次调用须得同 index(确定性守卫);分别捕获再判,避免 SA4000 误判为自反比较。
		idxA := pickIndex(id, 4)
		idxB := pickIndex(id, 4)
		if idxA != idxB {
			t.Fatalf("pickIndex not deterministic for account %d", id)
		}
	}
	// 变异守卫:把 pickIndex 写死为常量会让轮换坍缩为单一 profile
	// (即 0037 所警告的 JA3 聚簇)-> 本测试变红。
	seen := map[int]bool{}
	for id := int64(1); id <= 64; id++ {
		seen[pickIndex(id, 4)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("rotation must spread accounts across the pool, collapsed to %d index(es)", len(seen))
	}
	if pickIndex(5, 0) != 0 {
		t.Fatalf("pickIndex(_,0) must be safe")
	}
}

func TestSelectProfile_RotateUsesPool(t *testing.T) {
	pool := []mimicry.ProfileFields{fieldsWithJA3("a"), fieldsWithJA3("b"), fieldsWithJA3("c")}
	st := accountState{rotate: true, pool: pool}
	// 变异守卫:让 selectProfile 忽略 pool(改用 bound)会在此返回 nil -> 变红。
	p1 := selectProfile(7, st)
	if p1 == nil {
		t.Fatal("rotate must pick a profile from the pool")
	}
	// 同一账号 -> 同一选择(粘性)
	if p2 := selectProfile(7, st); p2 == nil || p2.ExpectedJA3Hash != p1.ExpectedJA3Hash {
		t.Fatal("rotation must be sticky per account")
	}
	// 被选中的那个确实在 pool 中
	in := false
	for _, pp := range pool {
		if pp.ExpectedJA3Hash == p1.ExpectedJA3Hash {
			in = true
		}
	}
	if !in {
		t.Fatalf("picked JA3 %q not in pool", p1.ExpectedJA3Hash)
	}
}

func TestSelectProfile_NonRotateUsesBound(t *testing.T) {
	b := fieldsWithJA3("bound")
	got := selectProfile(7, accountState{bound: &b})
	if got == nil || got.ExpectedJA3Hash != "bound" {
		t.Fatalf("non-rotate must use the bound profile, got %v", got)
	}
}

func TestSelectProfile_RotateEmptyPool_Nil(t *testing.T) {
	if selectProfile(7, accountState{rotate: true}) != nil {
		t.Fatal("rotate + empty pool must select nothing (builtin)")
	}
}
