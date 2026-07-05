package billing

import (
	"regexp"
	"strings"
	"testing"
)

func TestUsagePendingQueriesExcludeAnyReconciliationEventInsidePendingOnlyClause(t *testing.T) {
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
				)
			)
		)
	`)
	for name, query := range map[string]string{
		"list":       listUsageRecords,
		"list_names": listUsageRecordsWithNames,
		"count":      countUsageRecords,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(normalizeSQLForPendingQueryTest(query), pendingOnlyBlock) {
				t.Fatalf("%s query must exclude reconciled rows only inside pending_reconciliation_only block", name)
			}
			if strings.Contains(query, "reconciliation_source = 'stream_no_usage_finalized'") {
				t.Fatalf("%s query must not restrict logical pending exclusion to one reconciliation source", name)
			}
		})
	}
}

func TestUsageOutcomeSQLFilterContracts(t *testing.T) {
	outcomeBlock := normalizeSQLForPendingQueryTest(`
		AND (
			$10::text IS NULL
			OR $10::text = 'all'
			OR ($10::text = 'success' AND ur.end_class IN ('stream_end_graceful', 'non_streaming'))
			OR ($10::text = 'error' AND ur.end_class NOT IN ('stream_end_graceful', 'non_streaming'))
		)
	`)
	for name, query := range map[string]string{
		"list":  listUsageRecords,
		"count": countUsageRecords,
	} {
		t.Run(name, func(t *testing.T) {
			// 变异:去掉这个块会让 outcome=error 返回成功行,并让总数与列表不一致。
			if !strings.Contains(normalizeSQLForPendingQueryTest(query), outcomeBlock) {
				t.Fatalf("%s query must include outcome end_class filter block", name)
			}
		})
	}
}

func normalizeSQLForPendingQueryTest(query string) string {
	return regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(query), " ")
}
