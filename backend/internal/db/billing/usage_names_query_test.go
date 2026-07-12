package billing

import "testing"

// 变异:从任一 name join 去掉 tenant_id -> 这个 SQL 文本守卫失败 -> 变红。
func TestListUsageRecordsWithNamesTenantJoinPinned(t *testing.T) {
	for _, snippet := range []string{
		"ak.id = ur.api_key_id AND ak.tenant_id = ur.tenant_id",
		"u.id = ur.user_id AND u.tenant_id = ur.tenant_id",
	} {
		if !containsSQLSnippet(listUsageRecordsWithNames, snippet) {
			t.Fatalf("ListUsageRecordsWithNames missing tenant-scoped join snippet %q", snippet)
		}
	}
}

func containsSQLSnippet(sql string, snippet string) bool {
	return sql != "" && snippet != "" && stringsContains(sql, snippet)
}

func stringsContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
