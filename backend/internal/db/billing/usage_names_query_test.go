package billing

import "testing"

// MUTATION: remove tenant_id from either name join -> this SQL text guard fails -> RED.
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
