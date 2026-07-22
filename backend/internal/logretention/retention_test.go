package logretention

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeBatchStore struct {
	mu        sync.Mutex
	calls     []tableSpec
	cutoffs   []time.Time
	limits    []int
	cutoff    time.Time
	cutoffErr error
	callback  func(tableSpec, int) (batchResult, error)
}

func (s *fakeBatchStore) retentionCutoff(context.Context) (time.Time, error) {
	if s.cutoffErr != nil {
		return time.Time{}, s.cutoffErr
	}
	if s.cutoff.IsZero() {
		return time.Now().UTC().AddDate(0, 0, -RetentionDays), nil
	}
	return s.cutoff, nil
}

func (s *fakeBatchStore) deleteExpiredBatch(_ context.Context, table tableSpec, cutoff time.Time, limit int) (batchResult, error) {
	s.mu.Lock()
	call := len(s.calls)
	s.calls = append(s.calls, table)
	s.cutoffs = append(s.cutoffs, cutoff)
	s.limits = append(s.limits, limit)
	s.mu.Unlock()
	if s.callback == nil {
		return batchResult{acquired: true, byCategory: map[string]int64{}}, nil
	}
	return s.callback(table, call)
}

func testOption(now time.Time, tables []tableSpec, batch, batches int) option {
	return func(settings *settings) {
		settings.now = func() time.Time { return now }
		settings.tables = tables
		settings.batchSize = batch
		settings.maxBatches = batches
		settings.runTimeout = time.Second
	}
}

func TestRunOnceUsesFixedThirtyDayCutoffAndAggregatesCategories(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	tables := []tableSpec{
		{name: "ops_runtime_logs", timeColumn: "ingested_at"},
		{name: "payment_audit_events", timeColumn: "ingested_at", fixedCategory: "financial"},
	}
	store := &fakeBatchStore{cutoff: now.AddDate(0, 0, -RetentionDays), callback: func(table tableSpec, _ int) (batchResult, error) {
		switch table.name {
		case "ops_runtime_logs":
			return batchResult{acquired: true, deleted: 2, byCategory: map[string]int64{"access": 1, "error": 1}}, nil
		case "payment_audit_events":
			return batchResult{acquired: true, deleted: 1, byCategory: map[string]int64{"financial": 1}}, nil
		default:
			t.Fatalf("意外表: %s", table.name)
			return batchResult{}, nil
		}
	}}
	manager := newManager(store, testOption(now, tables, 10, 2))
	result, err := manager.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	wantCutoff := now.AddDate(0, 0, -RetentionDays)
	if !result.Cutoff.Equal(wantCutoff) || result.RetentionDays != 30 {
		t.Fatalf("固定截止线错误: cutoff=%s days=%d", result.Cutoff, result.RetentionDays)
	}
	if result.Deleted != 3 || result.ByCategory["access"] != 1 || result.ByCategory["error"] != 1 || result.ByCategory["financial"] != 1 {
		t.Fatalf("分类累计错误: %+v", result)
	}
	for i, cutoff := range store.cutoffs {
		if !cutoff.Equal(wantCutoff) || store.limits[i] != 10 {
			t.Fatalf("第 %d 批参数错误: cutoff=%s limit=%d", i, cutoff, store.limits[i])
		}
	}
	health := manager.Health()
	if health.LastDeleted != 3 || health.TotalDeleted != 3 || health.ConsecutiveFailures != 0 || !health.LastSuccessAt.Equal(now) {
		t.Fatalf("健康状态错误: %+v", health)
	}
}

func TestRunOnceIsBatchBoundedAndReportsBacklog(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	store := &fakeBatchStore{callback: func(tableSpec, int) (batchResult, error) {
		return batchResult{acquired: true, deleted: 3, byCategory: map[string]int64{"access": 3}}, nil
	}}
	manager := newManager(store, testOption(now,
		[]tableSpec{{name: "ops_runtime_logs", timeColumn: "ingested_at"}}, 3, 2))
	result, err := manager.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(store.calls) != 2 || result.Batches != 2 || result.Deleted != 6 || !result.HasMore {
		t.Fatalf("有界批次错误: calls=%d result=%+v", len(store.calls), result)
	}
}

func TestRunOnceContinuesOtherTablesAndMarksFailure(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	tables := []tableSpec{
		{name: "first", timeColumn: "ingested_at", fixedCategory: "operation"},
		{name: "second", timeColumn: "ingested_at", fixedCategory: "security"},
	}
	store := &fakeBatchStore{callback: func(table tableSpec, _ int) (batchResult, error) {
		if table.name == "first" {
			return batchResult{}, errors.New("database unavailable")
		}
		return batchResult{acquired: true, deleted: 1, byCategory: map[string]int64{"security": 1}}, nil
	}}
	manager := newManager(store, testOption(now, tables, 10, 1))
	result, err := manager.RunOnce(context.Background())
	if err == nil {
		t.Fatal("单表失败必须向调用方返回错误")
	}
	if len(store.calls) != 2 || result.Deleted != 1 || len(result.FailedTables) != 1 || result.FailedTables[0] != "first" {
		t.Fatalf("失败后应继续其他表: calls=%d result=%+v", len(store.calls), result)
	}
	health := manager.Health()
	if health.ConsecutiveFailures != 1 || health.LastErrorTable != "first" || health.LastErrorClass != "dependency" {
		t.Fatalf("失败健康状态错误: %+v", health)
	}
}

func TestRunOnceLeaseConflictDoesNotPretendToDelete(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	store := &fakeBatchStore{callback: func(tableSpec, int) (batchResult, error) {
		return batchResult{acquired: false}, nil
	}}
	manager := newManager(store, testOption(now,
		[]tableSpec{{name: "ops_runtime_logs", timeColumn: "ingested_at"}}, 10, 1))
	result, err := manager.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("租约冲突不是删除失败: %v", err)
	}
	if result.Deleted != 0 || len(result.LeaseConflicts) != 1 || !result.HasMore || manager.Health().LeaseConflictCount != 1 {
		t.Fatalf("租约冲突状态错误: %+v health=%+v", result, manager.Health())
	}
}

func TestRunOnceClassifiesTrustedClockFailureAndRequestsRetry(t *testing.T) {
	store := &fakeBatchStore{cutoffErr: context.DeadlineExceeded}
	manager := newManager(store, testOption(time.Now().UTC(),
		[]tableSpec{{name: "ops_runtime_logs", timeColumn: "ingested_at"}}, 10, 1))
	result, err := manager.RunOnce(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("可信时钟超时必须原因可识别: %v", err)
	}
	if !result.HasMore || len(result.FailedTables) != 1 || result.FailedTables[0] != "retention_clock" {
		t.Fatalf("可信时钟失败必须进入快速重试: %+v", result)
	}
	if got := manager.Health().LastErrorClass; got != "timeout" {
		t.Fatalf("时钟错误分类=%q want timeout", got)
	}
}

func TestRunOnceRejectsLocalOverlap(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	store := &fakeBatchStore{callback: func(tableSpec, int) (batchResult, error) {
		close(entered)
		<-release
		return batchResult{acquired: true, byCategory: map[string]int64{}}, nil
	}}
	manager := newManager(store, testOption(time.Now().UTC(),
		[]tableSpec{{name: "ops_runtime_logs", timeColumn: "ingested_at"}}, 10, 1))
	done := make(chan error, 1)
	go func() {
		_, err := manager.RunOnce(context.Background())
		done <- err
	}()
	<-entered
	if _, err := manager.RunOnce(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("重叠运行应明确冲突: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("首个任务失败: %v", err)
	}
}

func TestOrdinaryLogAllowlistExcludesDurableBusinessFacts(t *testing.T) {
	want := map[string]bool{
		"ops_runtime_logs": true, "admin_audit_events": true, "user_audit_events": true,
		"channel_health_audit_events": true, "credential_audit_events": true, "hermes_audit_events": true,
		"hermes_tool_calls":          true,
		"hermes_mutation_recovery":   true,
		"oauth_refresh_audit_events": true, "pool_routing_audit_events": true,
		"rate_limit_audit_events": true, "quota_audit_events": true, "payment_audit_events": true,
		"subscription_plan_audit_events": true, "moderation_log": true,
		"referral_reward_audit_events": true,
	}
	if len(ordinaryLogTables) != len(want) {
		t.Fatalf("白名单数量漂移: got=%d want=%d", len(ordinaryLogTables), len(want))
	}
	for _, table := range ordinaryLogTables {
		if !want[table.name] || table.timeColumn != "ingested_at" {
			t.Fatalf("白名单出现未经核实的表或时间轴: %+v", table)
		}
		if table.name == "hermes_mutation_recovery" {
			if table.requiredNotNullColumn != "audit_committed_at" {
				t.Fatalf("Hermes 恢复事实只能清理已补齐日志的记录: %+v", table)
			}
		} else if table.requiredNotNullColumn != "" {
			t.Fatalf("普通日志表出现未经核实的清理前置列: %+v", table)
		}
		delete(want, table.name)
	}
	if len(want) != 0 {
		t.Fatalf("白名单缺表: %+v", want)
	}
	forbidden := map[string]bool{
		"billing_events": true, "audit_ledger_entries": true, "billing_refund_operations": true,
		"payment_audit_log": true, "subscription_audit_events": true, "pricing_ratio_audit_log": true,
		"moderation_violation_events": true, "outbox_events": true, "dlq_events": true,
		"audit_refund_pending": true, "audit_signer_pubkeys": true,
		"usage_record_reconciliation_events": true, "async_processor_events": true,
		"alert_events": true, "channel_health_admin_alerts": true,
	}
	for _, table := range ordinaryLogTables {
		if forbidden[table.name] {
			t.Fatalf("持久业务事实不得进入 30 天清理白名单: %s", table.name)
		}
	}
}
