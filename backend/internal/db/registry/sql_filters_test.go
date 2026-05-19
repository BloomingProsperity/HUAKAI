package registry

import (
	"strings"
	"testing"
)

func TestListModelPoolBindingsSQLFiltersPoolGroupLifecycle(t *testing.T) {
	sql := strings.Join(strings.Fields(listModelPoolBindings), " ")
	for _, want := range []string{
		"INNER JOIN pool_groups pg ON pg.id = mpb.pool_group_id",
		"pg.tenant_id = mpb.tenant_id",
		"pg.enabled = true",
		"pg.deleted_at IS NULL",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("ListModelPoolBindings SQL missing pool_group lifecycle filter %q in %q", want, sql)
		}
	}
}
