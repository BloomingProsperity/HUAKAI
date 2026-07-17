package modelroutingadminhttp

import (
	"errors"
	"testing"
)

// 查询只要少命中一个账号，就必须整体拒绝；这代表数组里存在跨租户、跨池或已删除账号。
// 变异：删除 validateMatchedAccountIDs 的集合完整性校验，本用例立即转红。
func TestValidateMatchedAccountIDsRejectsPartialMatch(t *testing.T) {
	if err := validateMatchedAccountIDs([]int64{11, 13}, []int64{11}); !errors.Is(err, ErrAccountsNotOwned) {
		t.Fatalf("错误=%v，期望 ErrAccountsNotOwned", err)
	}
	if err := validateMatchedAccountIDs([]int64{11, 13}, []int64{13, 11}); err != nil {
		t.Fatalf("完整集合不应被拒绝：%v", err)
	}
}

func TestNormalizeProviderAccountIDsPreservesFirstOccurrence(t *testing.T) {
	got, err := normalizeProviderAccountIDs([]int64{13, 11, 13})
	if err != nil {
		t.Fatalf("去重失败：%v", err)
	}
	if len(got) != 2 || got[0] != 13 || got[1] != 11 {
		t.Fatalf("去重顺序错误：%v", got)
	}
}
