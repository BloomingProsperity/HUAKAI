//go:build integration_pg

package moderation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecordModerationViolation_AutoDisableOffKeepsEvidenceAndActiveKey(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStoreWithPool(pool)
	key := seedModerationAPIKey(t, ctx, pool, "auto-off", "active")

	result := recordViolation(t, ctx, store, key, "auto-off-request", ModerationConfig{
		TenantID: key.tenantID, BanThreshold: 1, BanWindowSeconds: 3600,
		AutoDisableKeyOnBan: false,
	})

	if !result.ThresholdReached || result.Disabled || result.Count != 1 {
		t.Fatalf("result=%+v，期望越线但不自动停用", result)
	}
	if status := apiKeyStatus(t, ctx, pool, key); status != "active" {
		t.Fatalf("status=%q，期望 active", status)
	}
	assertModerationPersistedCounts(t, ctx, pool, key, 1, 1, 0)
}

func TestRecordModerationViolation_ConcurrentReplayIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStoreWithPool(pool)
	key := seedModerationAPIKey(t, ctx, pool, "replay", "active")
	cfg := ModerationConfig{
		TenantID: key.tenantID, BanThreshold: 2, BanWindowSeconds: 3600,
		AutoDisableKeyOnBan: true,
	}
	event := ModerationEvent{
		TenantID: key.tenantID, APIKeyID: key.apiKeyID, UserID: key.userID,
		RequestID: "same-request", InputExcerpt: "重复请求",
		Decision: DecisionBlockKeyword, ReasonCode: "same-rule",
	}

	const workers = 12
	results := make([]BanResult, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = store.RecordModerationViolation(ctx, event, cfg)
		}(i)
	}
	wg.Wait()

	var eventID int64
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if eventID == 0 {
			eventID = results[i].EventID
		}
		if results[i].EventID != eventID || results[i].Count != 1 ||
			results[i].ThresholdReached || results[i].Disabled {
			t.Fatalf("worker %d result=%+v", i, results[i])
		}
	}
	assertModerationPersistedCounts(t, ctx, pool, key, 1, 1, 0)
}

func TestRecordModerationViolation_ConcurrentDistinctRequestsDisableAtThreshold(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStoreWithPool(pool)
	key := seedModerationAPIKey(t, ctx, pool, "threshold-race", "active")
	cfg := ModerationConfig{
		TenantID: key.tenantID, BanThreshold: 3, BanWindowSeconds: 3600,
		AutoDisableKeyOnBan: true,
	}

	const requests = 3
	results := make([]BanResult, requests)
	errs := make([]error, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = store.RecordModerationViolation(ctx, ModerationEvent{
				TenantID: key.tenantID, APIKeyID: key.apiKeyID, UserID: key.userID,
				RequestID:    "threshold-request-" + string(rune('a'+index)),
				InputExcerpt: "并发违规", Decision: DecisionBlockHash,
				ReasonCode: "hash-rule",
			}, cfg)
		}(i)
	}
	wg.Wait()

	disabledResults := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if results[i].Disabled {
			disabledResults++
			if results[i].Count != 3 || !results[i].ThresholdReached {
				t.Fatalf("disable result=%+v，期望在第 3 次越线", results[i])
			}
		}
	}
	if disabledResults != 1 {
		t.Fatalf("disabled results=%d，期望恰好 1", disabledResults)
	}
	if status := apiKeyStatus(t, ctx, pool, key); status != "disabled" {
		t.Fatalf("status=%q，期望 disabled", status)
	}
	assertModerationPersistedCounts(t, ctx, pool, key, 3, 3, 1)
}

func TestRecordModerationViolation_LogFailureRollsBackAllBusinessFacts(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStoreWithPool(pool)
	key := seedModerationAPIKey(t, ctx, pool, "rollback-log-failure", "active")
	installModerationLogFailureTrigger(t, ctx, pool, key.tenantID)

	_, err := store.RecordModerationViolation(ctx, ModerationEvent{
		TenantID: key.tenantID, APIKeyID: key.apiKeyID, UserID: key.userID,
		RequestID: "rollback-log-failure", InputExcerpt: "不得残留",
		Decision: DecisionBlockKeyword, ReasonCode: "rollback-fixture",
	}, ModerationConfig{
		TenantID: key.tenantID, BanThreshold: 1, BanWindowSeconds: 3600,
		AutoDisableKeyOnBan: true,
	})
	if err == nil {
		t.Fatal("日志写入故障未传回调用方")
	}
	if status := apiKeyStatus(t, ctx, pool, key); status != "active" {
		t.Fatalf("事务失败后 status=%q，期望 active", status)
	}
	assertModerationPersistedCounts(t, ctx, pool, key, 0, 0, 0)
}

func TestUnbanAPIKey_RejectsLaterStatusGeneration(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStoreWithPool(pool)
	key := seedModerationAPIKey(t, ctx, pool, "generation-cas", "active")
	recordViolation(t, ctx, store, key, "generation-request", ModerationConfig{
		TenantID: key.tenantID, BanThreshold: 1, BanWindowSeconds: 3600,
		AutoDisableKeyOnBan: true,
	})

	if _, err := pool.Exec(ctx,
		`UPDATE api_keys SET status='active' WHERE tenant_id=$1 AND id=$2`,
		key.tenantID, key.apiKeyID,
	); err != nil {
		t.Fatalf("模拟其他状态来源恢复: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE api_keys SET status='disabled' WHERE tenant_id=$1 AND id=$2`,
		key.tenantID, key.apiKeyID,
	); err != nil {
		t.Fatalf("模拟其他状态来源: %v", err)
	}
	_, err := store.UnbanAPIKey(ctx, UnbanAPIKeyRequest{
		TenantID: key.tenantID, APIKeyID: key.apiKeyID,
		IdempotencyKey: "generation-cas-unban",
		ActorID:        "tenant:7", ActorRole: "tenant_operator",
	})
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("err=%v，期望 ErrStateConflict", err)
	}
	if status := apiKeyStatus(t, ctx, pool, key); status != "disabled" {
		t.Fatalf("status=%q，状态冲突时不得覆盖", status)
	}
}

func TestDisableAPIKey_RequiresMatchingThresholdEventAndSupportsTenantRecovery(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStoreWithPool(pool)
	key := seedModerationAPIKey(t, ctx, pool, "manual-disable", "active")
	event := recordViolation(t, ctx, store, key, "manual-disable-request", ModerationConfig{
		TenantID: key.tenantID, BanThreshold: 1, BanWindowSeconds: 3600,
		AutoDisableKeyOnBan: false,
	})

	disabled, err := store.DisableAPIKey(ctx, DisableAPIKeyRequest{
		TenantID: key.tenantID, APIKeyID: key.apiKeyID,
		ViolationEventID: event.EventID,
		IdempotencyKey:   "manual-disable-1",
		ActorID:          "admin:1", ActorRole: "platform_admin",
		Reason: "人工确认",
	})
	if err != nil {
		t.Fatalf("DisableAPIKey: %v", err)
	}
	if disabled.Status != "disabled" || disabled.LogID == 0 {
		t.Fatalf("disable result=%+v", disabled)
	}
	banned, err := store.ListBannedAPIKeys(ctx, key.tenantID, 10, 0)
	if err != nil {
		t.Fatalf("ListBannedAPIKeys: %v", err)
	}
	if len(banned) != 1 || banned[0].Source != "manual" ||
		banned[0].DisableGeneration <= 0 {
		t.Fatalf("banned=%+v", banned)
	}

	recovered, err := store.UnbanAPIKey(ctx, UnbanAPIKeyRequest{
		TenantID: key.tenantID, APIKeyID: key.apiKeyID,
		IdempotencyKey: "tenant-unban-1",
		ActorID:        "tenant:7", ActorRole: "tenant_operator",
		Reason: "租户复核通过",
	})
	if err != nil {
		t.Fatalf("UnbanAPIKey: %v", err)
	}
	if recovered.Status != "active" || apiKeyStatus(t, ctx, pool, key) != "active" {
		t.Fatalf("recovered=%+v", recovered)
	}
}

func TestModerationKeyOperations_ReplayReturnsStableResultAndRejectsChangedRequest(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStoreWithPool(pool)
	key := seedModerationAPIKey(t, ctx, pool, "operation-replay", "active")
	event := recordViolation(t, ctx, store, key, "operation-replay-violation", ModerationConfig{
		TenantID: key.tenantID, BanThreshold: 1, BanWindowSeconds: 3600,
		AutoDisableKeyOnBan: false,
	})

	disableRequest := DisableAPIKeyRequest{
		TenantID: key.tenantID, APIKeyID: key.apiKeyID,
		ViolationEventID: event.EventID, IdempotencyKey: "disable-replay-1",
		ActorID: "admin:1", ActorRole: "platform_admin", Reason: "人工确认",
	}
	firstDisable, err := store.DisableAPIKey(ctx, disableRequest)
	if err != nil {
		t.Fatalf("首次 DisableAPIKey: %v", err)
	}
	replayedDisable, err := store.DisableAPIKey(ctx, disableRequest)
	if err != nil {
		t.Fatalf("重放 DisableAPIKey: %v", err)
	}
	if replayedDisable != firstDisable {
		t.Fatalf("禁用重放结果=%+v，期望稳定结果=%+v", replayedDisable, firstDisable)
	}
	changedDisable := disableRequest
	changedDisable.Reason = "换一个理由"
	if _, err := store.DisableAPIKey(ctx, changedDisable); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("同一禁用幂等键换参数 err=%v，期望 ErrStateConflict", err)
	}
	assertModerationOperationCounts(t, ctx, pool, key, "disable-replay-1", DecisionAdminDisable, 1, 1)

	unbanRequest := UnbanAPIKeyRequest{
		TenantID: key.tenantID, APIKeyID: key.apiKeyID,
		IdempotencyKey: "unban-replay-1",
		ActorID:        "tenant:7", ActorRole: "tenant_operator", Reason: "租户复核通过",
	}
	firstUnban, err := store.UnbanAPIKey(ctx, unbanRequest)
	if err != nil {
		t.Fatalf("首次 UnbanAPIKey: %v", err)
	}
	replayedUnban, err := store.UnbanAPIKey(ctx, unbanRequest)
	if err != nil {
		t.Fatalf("重放 UnbanAPIKey: %v", err)
	}
	if replayedUnban != firstUnban {
		t.Fatalf("解封重放结果=%+v，期望稳定结果=%+v", replayedUnban, firstUnban)
	}
	changedUnban := unbanRequest
	changedUnban.APIKeyID++
	if _, err := store.UnbanAPIKey(ctx, changedUnban); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("同一解封幂等键换 Key err=%v，期望 ErrStateConflict", err)
	}
	assertModerationOperationCounts(t, ctx, pool, key, "unban-replay-1", DecisionAdminUnban, 1, 1)
}

func TestDisableAPIKey_ConcurrentReplayHasSingleEffect(t *testing.T) {
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	store := NewSQLStoreWithPool(pool)
	key := seedModerationAPIKey(t, ctx, pool, "operation-concurrent", "active")
	event := recordViolation(t, ctx, store, key, "operation-concurrent-violation", ModerationConfig{
		TenantID: key.tenantID, BanThreshold: 1, BanWindowSeconds: 3600,
		AutoDisableKeyOnBan: false,
	})
	request := DisableAPIKeyRequest{
		TenantID: key.tenantID, APIKeyID: key.apiKeyID,
		ViolationEventID: event.EventID, IdempotencyKey: "disable-concurrent-1",
		ActorID: "admin:1", ActorRole: "platform_admin", Reason: "并发确认",
	}

	const workers = 8
	results := make([]DisableAPIKeyResult, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index], errs[index] = store.DisableAPIKey(ctx, request)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("并发调用 %d: %v", i, errs[i])
		}
		if results[i] != results[0] {
			t.Fatalf("并发结果 %d=%+v，期望 %+v", i, results[i], results[0])
		}
	}
	assertModerationOperationCounts(t, ctx, pool, key, "disable-concurrent-1", DecisionAdminDisable, 1, 1)
}

func assertModerationOperationCounts(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	key moderationAPIKeySeed,
	idempotencyKey string,
	decision Decision,
	wantOperations int64,
	wantLogs int64,
) {
	t.Helper()
	var operations, logs int64
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM moderation_key_operations
     WHERE tenant_id=$1 AND api_key_id=$2 AND idempotency_key=$3),
    (SELECT count(*) FROM moderation_log
     WHERE tenant_id=$1 AND api_key_id=$2 AND request_id=$3 AND decision=$4)`,
		key.tenantID, key.apiKeyID, idempotencyKey, string(decision),
	).Scan(&operations, &logs); err != nil {
		t.Fatalf("查询幂等事实与日志: %v", err)
	}
	if operations != wantOperations || logs != wantLogs {
		t.Fatalf("operations/logs=%d/%d，期望 %d/%d",
			operations, logs, wantOperations, wantLogs)
	}
}

func assertModerationPersistedCounts(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	key moderationAPIKeySeed,
	wantEvents int64,
	wantLogs int64,
	wantStates int64,
) {
	t.Helper()
	var events, logs, states int64
	if err := pool.QueryRow(ctx,
		`SELECT
		    (SELECT count(*) FROM moderation_violation_events WHERE tenant_id=$1 AND api_key_id=$2),
		    (SELECT count(*) FROM moderation_log WHERE tenant_id=$1 AND api_key_id=$2 AND violation_event_id IS NOT NULL),
		    (SELECT count(*) FROM moderation_key_states WHERE tenant_id=$1 AND api_key_id=$2 AND state='disabled')`,
		key.tenantID, key.apiKeyID,
	).Scan(&events, &logs, &states); err != nil {
		t.Fatalf("query persisted counts: %v", err)
	}
	if events != wantEvents || logs != wantLogs || states != wantStates {
		t.Fatalf("events/logs/states=%d/%d/%d，期望 %d/%d/%d",
			events, logs, states, wantEvents, wantLogs, wantStates)
	}
}

func installModerationLogFailureTrigger(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
) {
	t.Helper()
	functionName := fmt.Sprintf("test_fail_moderation_log_%d", tenantID)
	triggerName := fmt.Sprintf("test_fail_moderation_log_%d", tenantID)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'injected moderation log failure';
END
$$;
CREATE TRIGGER %s
BEFORE INSERT ON moderation_log
FOR EACH ROW
WHEN (NEW.tenant_id = %d)
EXECUTE FUNCTION %s()`,
		functionName, triggerName, tenantID, functionName,
	)); err != nil {
		t.Fatalf("安装日志故障触发器: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx,
			fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON moderation_log", triggerName))
		_, _ = pool.Exec(cleanupCtx,
			fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
	})
}
