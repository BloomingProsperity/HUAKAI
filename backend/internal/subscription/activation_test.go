// HUAKAI · iKun

package subscription

import (
	"testing"

	"github.com/shopspring/decimal"
)

// TestWindowDominates 单窗口支配判定 (only-up 闸核心, mutation 主战场)。
// 判别性: 把 new==nil 分支翻成 false → "new=nil 期望 true" 变红;
//
//	把 >= 改成 > → "new==cur 期望 true" 变红; 把 cur==nil 分支翻成 true → "new有限 cur无限 期望 false" 变红。
func TestWindowDominates(t *testing.T) {
	cases := []struct {
		name string
		newC *string // nil = 无限
		curC *string
		want bool
	}{
		{"new unlimited, cur finite -> dominate", nil, sp("10"), true},
		{"new unlimited, cur unlimited -> dominate", nil, nil, true},
		{"new finite, cur unlimited -> NOT dominate", sp("100"), nil, false},
		{"new > cur -> dominate", sp("100"), sp("10"), true},
		{"new < cur -> NOT dominate", sp("10"), sp("100"), false},
		{"new == cur -> dominate", sp("50"), sp("50"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := windowDominates(ptrDec(tc.newC), ptrDec(tc.curC))
			if got != tc.want {
				t.Fatalf("windowDominates(new=%v cur=%v) = %v, want %v", tc.newC, tc.curC, got, tc.want)
			}
		})
	}
}

// TestCapsDominate 三窗口联合支配: 任一窗口往低则整体不支配。
// 判别性: 把 && 改成 || → "一窗口更低期望 false" 变红。
func TestCapsDominate(t *testing.T) {
	cases := []struct {
		name string
		newC capsTriple
		curC capsTriple
		want bool
	}{
		{"all higher -> dominate", triple("100", "200", "300"), triple("10", "20", "30"), true},
		{"all equal -> dominate", triple("10", "20", "30"), triple("10", "20", "30"), true},
		{"daily lower -> NOT dominate", triple("5", "200", "300"), triple("10", "20", "30"), false},
		{"weekly lower -> NOT dominate", triple("100", "5", "300"), triple("10", "20", "30"), false},
		{"monthly lower -> NOT dominate", triple("100", "200", "5"), triple("10", "20", "30"), false},
		{"new all unlimited -> dominate", capsTriple{}, triple("10", "20", "30"), true},
		{"new daily finite vs cur daily unlimited -> NOT dominate", triple("100", "200", "300"), capsTriple{Weekly: ptrDec(sp("20")), Monthly: ptrDec(sp("30"))}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := capsDominate(tc.newC, tc.curC); got != tc.want {
				t.Fatalf("capsDominate = %v, want %v", got, tc.want)
			}
		})
	}
}

func sp(s string) *string { return &s }

func ptrDec(s *string) *decimal.Decimal {
	if s == nil {
		return nil
	}
	d := decimal.RequireFromString(*s)
	return &d
}

func triple(d, w, m string) capsTriple {
	return capsTriple{Daily: ptrDec(sp(d)), Weekly: ptrDec(sp(w)), Monthly: ptrDec(sp(m))}
}
