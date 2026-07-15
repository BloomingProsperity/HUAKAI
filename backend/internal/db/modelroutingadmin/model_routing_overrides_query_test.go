package modelroutingadmindb

import (
	"strings"
	"testing"
)

// 账号归属必须同时下推 tenant、pool 与完整 ID 数组，不能只靠 HTTP gate。
func TestAccountOwnershipQueryKeepsTenantPoolAndIDPredicates(t *testing.T) {
	for _, predicate := range []string{
		"pa.tenant_id = $1::bigint",
		"c.pool_group_id = $2::bigint",
		"pa.id = ANY($3::bigint[])",
	} {
		if !strings.Contains(lockModelRoutingAccountsForPool, predicate) {
			t.Errorf("账号归属查询缺少谓词 %q", predicate)
		}
	}
}

func TestPoolOwnershipQueryKeepsTenantPredicate(t *testing.T) {
	if !strings.Contains(lockModelRoutingPoolForTenant, "tenant_id = $1::bigint") {
		t.Fatal("池组归属查询缺少 tenant_id 谓词")
	}
}
