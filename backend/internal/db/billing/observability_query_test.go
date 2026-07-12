package billing

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// pendingOnlyBlockAt 生成 pending_reconciliation_only 契约块;占位符编号随各查询
// 的参数集不同(list 多带 user_id 过滤,块顺延到 $10;count/list_names 仍是 $9)。
func pendingOnlyBlockAt(n int) string {
	return normalizeSQLForPendingQueryTest(fmt.Sprintf(`
		AND (
			$%d::boolean = false
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
	`, n))
}

func TestUsagePendingQueriesExcludeAnyReconciliationEventInsidePendingOnlyClause(t *testing.T) {
	for name, tc := range map[string]struct {
		query      string
		pendingArg int
	}{
		"list":       {listUsageRecords, 10},
		"list_names": {listUsageRecordsWithNames, 9},
		"count":      {countUsageRecords, 9},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(normalizeSQLForPendingQueryTest(tc.query), pendingOnlyBlockAt(tc.pendingArg)) {
				t.Fatalf("%s query must exclude reconciled rows only inside pending_reconciliation_only block", name)
			}
			if strings.Contains(tc.query, "reconciliation_source = 'stream_no_usage_finalized'") {
				t.Fatalf("%s query must not restrict logical pending exclusion to one reconciliation source", name)
			}
		})
	}
}

func outcomeBlockAt(n int) string {
	return normalizeSQLForPendingQueryTest(fmt.Sprintf(`
		AND (
			$%[1]d::text IS NULL
			OR $%[1]d::text = 'all'
			OR ($%[1]d::text = 'success' AND ur.end_class IN ('stream_end_graceful', 'non_streaming'))
			OR ($%[1]d::text = 'error' AND ur.end_class NOT IN ('stream_end_graceful', 'non_streaming'))
		)
	`, n))
}

func TestUsageOutcomeSQLFilterContracts(t *testing.T) {
	for name, tc := range map[string]struct {
		query      string
		outcomeArg int
	}{
		"list":  {listUsageRecords, 11},
		"count": {countUsageRecords, 10},
	} {
		t.Run(name, func(t *testing.T) {
			// 变异:去掉这个块会让 outcome=error 返回成功行,并让总数与列表不一致。
			if !strings.Contains(normalizeSQLForPendingQueryTest(tc.query), outcomeBlockAt(tc.outcomeArg)) {
				t.Fatalf("%s query must include outcome end_class filter block", name)
			}
		})
	}
}

func TestProviderAccountRecentRequestsSQLContract(t *testing.T) {
	query := normalizeSQLForPendingQueryTest(listProviderAccountRecentRequests)
	for _, predicate := range []string{
		"ur.provider_account_id = $1",
		"ur.tenant_id = $2",
		"ORDER BY ur.settled_at DESC, ur.id DESC",
	} {
		if !strings.Contains(query, predicate) {
			t.Fatalf("recent requests query must contain %q", predicate)
		}
	}
	for _, forbidden := range []string{"actual_cost", "reserved_cost", "cost_snapshot"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("recent requests query must not expose money field %q", forbidden)
		}
	}
}

func normalizeSQLForPendingQueryTest(query string) string {
	return regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(query), " ")
}
