package billing

import (
	"regexp"
	"strings"
	"testing"
)

func TestUsagePendingQueriesKeepMarkerExclusionInsidePendingOnlyClause(t *testing.T) {
	pendingOnlyBlock := normalizeSQLForPendingQueryTest(`
		AND (
			$9::boolean = false
			OR (
				ur.pending_reconciliation = true
				AND NOT EXISTS (
					SELECT 1
					FROM usage_record_reconciliation_events re
					WHERE re.tenant_id = ur.tenant_id
					  AND re.original_usage_record_id = ur.id
					  AND re.reconciliation_source = 'stream_no_usage_finalized'
				)
			)
		)
	`)
	for name, query := range map[string]string{
		"list":  listUsageRecords,
		"count": countUsageRecords,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(normalizeSQLForPendingQueryTest(query), pendingOnlyBlock) {
				t.Fatalf("%s query must exclude no-usage markers only inside pending_reconciliation_only block", name)
			}
		})
	}
}

func normalizeSQLForPendingQueryTest(query string) string {
	return regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(query), " ")
}
