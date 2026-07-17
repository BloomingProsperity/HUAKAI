package alertmetrics

import (
	"context"
	"strings"
	"testing"
)

// 变异：从目录删任一静态键、把键改错、漏掉生产者输出或漏掉前缀族，
// 目录数、快照包含关系或生产键反向比对至少一项会变红。
func TestCatalogEntriesMatchProducedSnapshot(t *testing.T) {
	source := NewCompositeMetricSource(CompositeMetricSourceConfig{
		UsageRolluper: &stubUsageRolluper{rollup: RecentUsageRollup{
			RequestCount: 10,
			SuccessCount: 8,
			ErrorCount:   2,
			TotalCostUSD: 1.5,
			LatencyP95MS: 120,
			LatencyP99MS: 240,
		}},
		AccountHealth: stubAccountHealth{counts: map[string]int64{"throttled": 2}},
	})
	snapshot, err := source.Snapshot(context.Background(), 7)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	entries := CatalogEntries()
	if len(entries) != 11 {
		t.Fatalf("目录项数=%d，want 10 个静态键 + 1 个前缀族", len(entries))
	}
	staticNames := make(map[string]struct{}, 10)
	prefixCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.Label) == "" || strings.TrimSpace(entry.Unit) == "" || strings.TrimSpace(entry.Description) == "" {
			t.Fatalf("目录项元数据不完整：%+v", entry)
		}
		if entry.IsPrefix {
			prefixCount++
			if entry.Name != "account.unhealthy_" {
				t.Fatalf("健康状态前缀=%q，want account.unhealthy_", entry.Name)
			}
			continue
		}
		if _, exists := staticNames[entry.Name]; exists {
			t.Fatalf("目录静态键重复：%q", entry.Name)
		}
		staticNames[entry.Name] = struct{}{}
		if _, produced := snapshot[entry.Name]; !produced {
			t.Fatalf("目录静态键 %q 未出现在完整快照：%v", entry.Name, snapshot)
		}
	}
	if prefixCount != 1 || len(staticNames) != 10 {
		t.Fatalf("目录分类错误：静态=%d 前缀=%d", len(staticNames), prefixCount)
	}
	if snapshot["account.unhealthy_throttled"] != 2 {
		t.Fatalf("动态健康指标未按目录前缀产出：%v", snapshot)
	}

	// 完整 fake 快照只比静态生产键；明确移除本 fixture 唯一的动态键后做反向比对，
	// 避免“从目录删一键后，静态键仍是快照子集”的假绿。
	delete(snapshot, MetricAccountUnhealthyPrefix+"throttled")
	if len(snapshot) != len(staticNames) {
		t.Fatalf("生产静态键=%d，目录静态键=%d；snapshot=%v", len(snapshot), len(staticNames), snapshot)
	}
	for name := range snapshot {
		if _, cataloged := staticNames[name]; !cataloged {
			t.Fatalf("生产静态键 %q 未登记到目录", name)
		}
	}
}

// 变异：CatalogEntries 直接返回内部切片时，调用方改名会污染后续请求并使断言变红。
func TestCatalogEntriesReturnsCopy(t *testing.T) {
	first := CatalogEntries()
	first[0].Name = "tampered"
	if got := CatalogEntries()[0].Name; got != MetricUsageRequestCount {
		t.Fatalf("目录内部状态被调用方改写：got %q", got)
	}
}
