package billing

import (
	"strings"
	"testing"
)

func TestListEligibleAccountsByPoolGroupSQLFiltersChannelLifecycle(t *testing.T) {
	sql := strings.Join(strings.Fields(listEligibleAccountsByPoolGroup), " ")
	for _, want := range []string{
		"INNER JOIN channels c ON c.id = pa.channel_id",
		"c.enabled = true",
		"c.deleted_at IS NULL",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("ListEligibleAccountsByPoolGroup SQL missing channel lifecycle filter %q in %q", want, sql)
		}
	}
}
