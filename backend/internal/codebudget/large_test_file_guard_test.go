package codebudget

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	maxUntrackedTestFileLines = 1000
	testFileGrowthAllowance   = 0.05
)

var largeTestFileBaseline = map[string]int{
	"cmd/gateway/openapi_consistency_test.go":                            1100,
	"cmd/gateway/wiring_test.go":                                         1020,
	"internal/audit/receipt_formatter_test.go":                           1076,
	"internal/auditledger/ledger_test.go":                                1202,
	"internal/billing/settler_integration_test.go":                       1203,
	"internal/gateway/forwarder_test.go":                                 1520,
	"internal/gatewayhttp/admin_credential_acquisition_handler_test.go":   1126,
	"internal/gatewayhttp/auth_session_handler_test.go":                  2050,
	"internal/gatewayhttp/chat_completions_dispatch_test.go":             1456,
	"internal/gatewayhttp/chat_completions_pricing_test.go":              1484,
	"internal/gatewayhttp/chat_completions_retry_failover_test.go":        1593,
	"internal/gatewayhttp/chat_completions_stream_test.go":               2100,
	"internal/gatewayhttp/cost_receipt_handler_test.go":                  1117,
	"internal/proto/anthropic/sse_test.go":                               1079,
	"internal/proto/envelope_test.go":                                    2713,
	"internal/quota/service_integration_test.go":                         1130,
	"internal/registry/postgres_registry_integration_test.go":             1017,
	"internal/userauth/service_test.go":                                  1432,
	"internal/voucher/store_postgres_money_integration_test.go":           1214,
}

func TestLargeTestFilesDoNotGrowSilently(t *testing.T) {
	root := filepath.Join("..", "..")
	seen := map[string]bool{}
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, "internal/") && !strings.HasPrefix(rel, "cmd/") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Count(string(raw), "\n") + 1
		base, tracked := largeTestFileBaseline[rel]
		if !tracked {
			if lines > maxUntrackedTestFileLines {
				violations = append(violations, rel+":"+strconv.Itoa(lines)+": 新增未登记巨型测试文件,请拆分测试职责或显式登记基线")
			}
			return nil
		}
		seen[rel] = true
		if float64(lines) > float64(base)*(1+testFileGrowthAllowance) {
			violations = append(violations, fmt.Sprintf(
				"%s:%d: 测试文件超过基线 %d 的 %.0f%% 余量,请拆分测试职责而不是继续堆大文件",
				rel, lines, base, testFileGrowthAllowance*100))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan test file sizes: %v", err)
	}
	for rel := range largeTestFileBaseline {
		if !seen[rel] {
			violations = append(violations, rel+": 巨型测试文件基线已失效,请删除基线项")
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("巨型测试文件体量检查失败 %d 项:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}
