//go:build e2e_grok_video_live || e2e_gemini_video_live

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

const (
	grokVideoLiveModel              = "grok-imagine-video"
	grokVideoLivePollTimeout        = 12 * time.Minute
	grokVideoLiveSettingsLock int64 = 0x48554b4149564944
)

var grokVideoLiveCase = imageLiveCase{
	slug: "grok-video", model: grokVideoLiveModel,
	protocol: registrydefault.ProtocolGrokChat, vendor: credentialstore.VendorGrok,
	authMode: credentialstore.AuthModeAPIKey, keyEnv: "HUAKAI_E2E_GROK_KEY",
	pricingData: grokImageLivePricingData, capabilities: []string{"video", "video_output"},
}

func TestGrokVideoLiveGenerations(t *testing.T) {
	dsn := firstOpenAIImageLiveNonEmpty(
		os.Getenv("HUAKAI_E2E_DATABASE_URL"),
		os.Getenv("HUAKAI_DATABASE_URL"),
	)
	if dsn == "" {
		t.Skip("HUAKAI_E2E_DATABASE_URL/HUAKAI_DATABASE_URL 未设置，跳过 Grok 视频活体测试")
	}
	dsn = useDisposableSpecializedLiveDatabase(t, dsn)
	upstreamKey := strings.TrimSpace(os.Getenv(grokVideoLiveCase.keyEnv))
	if upstreamKey == "" {
		t.Skip(grokVideoLiveCase.keyEnv + " 未设置")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 Grok 视频活体测试数据库: %v", err)
	}
	t.Cleanup(pgPool.Close)

	seed := seedOpenAIImageLiveGraph(t, ctx, pgPool, grokVideoLiveCase)
	configureGrokVideoLiveSettings(t, ctx, pgPool)
	registerGrokVideoLiveCleanup(t, pgPool, seed.tenantID)

	binPath := buildOpenAIImageLiveGateway(t)
	addr := reserveOpenAIImageLiveLocalPort(t)
	processes := startOpenAIImageLiveGateway(t, binPath, dsn, addr, seed, upstreamKey)
	t.Cleanup(func() { stopSpecializedLiveProcesses(processes) })
	waitForOpenAIImageLiveGateway(t, addr)

	client := &http.Client{Timeout: 60 * time.Second}
	seed.providerAccountID = importOpenAIImageLiveAccount(t, ctx, client, addr, seed, upstreamKey)
	assertOpenAIImageLiveImportedAccount(t, ctx, pgPool, seed)
	assertOpenAIImageLiveSeedSelectable(t, ctx, pgPool, seed)

	requestBody, err := json.Marshal(map[string]any{
		"model":        grokVideoLiveModel,
		"prompt":       "a single red circle slowly moving from left to right on a white background",
		"duration":     1,
		"aspect_ratio": "1:1",
		"resolution":   "480p",
	})
	if err != nil {
		t.Fatalf("编码 Grok 视频请求: %v", err)
	}
	logicalID := "grok-video-live-" + uuid.NewString()
	requestID := submitGrokVideoLive(t, ctx, client, addr, seed.bearer, logicalID, requestBody, upstreamKey)
	final := pollGrokVideoLive(t, ctx, client, addr, seed.bearer, requestID, upstreamKey)
	if strings.TrimSpace(final.Video.URL) == "" {
		t.Fatalf("Grok 视频完成但 video.url 为空: body=%s", grokVideoLivePreview(final.Raw, upstreamKey, seed.bearer))
	}
	if final.Usage.CostTicks <= 0 {
		t.Fatalf("Grok 视频完成但 cost_in_usd_ticks=%d，不能按零成本验收", final.Usage.CostTicks)
	}
	assertGrokVideoLiveMoneyAndRelease(t, ctx, pgPool, seed, requestID)
}

type grokVideoLiveResult struct {
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Model    string `json:"model"`
	Video    struct {
		URL string `json:"url"`
	} `json:"video"`
	Usage struct {
		CostTicks int64 `json:"cost_in_usd_ticks"`
	} `json:"usage"`
	Raw []byte `json:"-"`
}

func submitGrokVideoLive(t *testing.T, ctx context.Context, client *http.Client, addr, bearer, logicalID string, body []byte, secrets ...string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/videos/generations", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("构造 Grok 视频提交请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", logicalID)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("提交 Grok 视频任务: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 Grok 视频提交响应: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Grok 视频提交状态=%d，期望 202，body=%s", resp.StatusCode, grokVideoLivePreview(raw, append(secrets, bearer)...))
	}
	var decoded struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || !strings.HasPrefix(decoded.RequestID, "video_") {
		t.Fatalf("Grok 视频提交未返回本地任务号: err=%v body=%s", err, grokVideoLivePreview(raw, append(secrets, bearer)...))
	}
	return decoded.RequestID
}

func pollGrokVideoLive(t *testing.T, ctx context.Context, client *http.Client, addr, bearer, requestID string, secrets ...string) grokVideoLiveResult {
	t.Helper()
	deadline := time.Now().Add(grokVideoLivePollTimeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/v1/videos/"+requestID, nil)
		if err != nil {
			t.Fatalf("构造 Grok 视频轮询请求: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("轮询 Grok 视频任务: %v", err)
		}
		raw, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			t.Fatalf("读取 Grok 视频轮询响应: %v", readErr)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Grok 视频轮询状态=%d，期望 200，body=%s", resp.StatusCode, grokVideoLivePreview(raw, append(secrets, bearer)...))
		}
		var decoded grokVideoLiveResult
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("解析 Grok 视频轮询响应: %v body=%s", err, grokVideoLivePreview(raw, append(secrets, bearer)...))
		}
		decoded.Raw = append([]byte(nil), raw...)
		status := strings.ToLower(strings.TrimSpace(decoded.Status))
		switch status {
		case "done", "completed", "succeeded", "success":
			return decoded
		case "failed", "expired":
			t.Fatalf("Grok 视频任务进入失败终态 %q: body=%s", status, grokVideoLivePreview(raw, append(secrets, bearer)...))
		case "pending", "queued", "in_progress", "processing", "":
			if strings.TrimSpace(decoded.Video.URL) != "" {
				return decoded
			}
		default:
			t.Fatalf("Grok 视频返回未知状态 %q: body=%s", status, grokVideoLivePreview(raw, append(secrets, bearer)...))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("等待 Grok 视频终态: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatalf("Grok 视频任务 %s 在 %s 内未完成", requestID, grokVideoLivePollTimeout)
	return grokVideoLiveResult{}
}

func assertGrokVideoLiveMoneyAndRelease(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *openAIImageLiveSeed, requestID string) {
	t.Helper()
	var taskStatus, providerName, upstreamTaskID, protocol, requestedModel, upstreamModel, routeID string
	var taskAPIKeyID, taskAccountID, taskPoolID, actualCents int64
	if err := pgPool.QueryRow(ctx, `
SELECT status, provider, provider_task_id, api_key_id, provider_account_id, pool_group_id,
       protocol_family, requested_model, provider_model_id, route_id, actual_cents
FROM media_tasks
WHERE tenant_id=$1 AND request_id=$2`, seed.tenantID, requestID).Scan(
		&taskStatus, &providerName, &upstreamTaskID, &taskAPIKeyID, &taskAccountID, &taskPoolID,
		&protocol, &requestedModel, &upstreamModel, &routeID, &actualCents,
	); err != nil {
		t.Fatalf("读取 Grok 视频持久任务: %v", err)
	}
	if taskStatus != "succeeded" || providerName != "grok_video" || strings.TrimSpace(upstreamTaskID) == "" ||
		taskAPIKeyID != seed.apiKeyID || taskAccountID != seed.providerAccountID || taskPoolID != seed.poolGroupID ||
		protocol != grokVideoLiveCase.protocol || requestedModel != grokVideoLiveModel || upstreamModel != grokVideoLiveModel ||
		strings.TrimSpace(routeID) == "" || actualCents <= 0 {
		t.Fatalf("Grok 视频任务绑定或终态错误: status=%s provider=%s upstream_task=%q key/account/pool=%d/%d/%d protocol=%s models=%s/%s route=%q cents=%d",
			taskStatus, providerName, upstreamTaskID, taskAPIKeyID, taskAccountID, taskPoolID, protocol, requestedModel, upstreamModel, routeID, actualCents)
	}

	var claimID int64
	var claimStatus, acquisitionToken, claimCost string
	var claimKeyID, claimUserID, claimAccountID, claimPoolID int64
	if err := pgPool.QueryRow(ctx, `
SELECT id, status, acquisition_token::text, actual_cost::text,
       api_key_id, user_id, provider_account_id, pooling_group_id
FROM billing_ledger_claims
WHERE tenant_id=$1 AND logical_request_id=$2`, seed.tenantID, requestID).Scan(
		&claimID, &claimStatus, &acquisitionToken, &claimCost,
		&claimKeyID, &claimUserID, &claimAccountID, &claimPoolID,
	); err != nil {
		t.Fatalf("读取 Grok 视频 claim: %v", err)
	}
	claimCostValue, parseErr := strconv.ParseFloat(claimCost, 64)
	if claimStatus != "committed" || acquisitionToken == "" || parseErr != nil || claimCostValue <= 0 ||
		claimKeyID != seed.apiKeyID || claimUserID != seed.userID || claimAccountID != seed.providerAccountID || claimPoolID != seed.poolGroupID {
		t.Fatalf("Grok 视频 claim 错误: status=%s token=%q cost=%q key/user/account/pool=%d/%d/%d/%d",
			claimStatus, acquisitionToken, claimCost, claimKeyID, claimUserID, claimAccountID, claimPoolID)
	}

	var usageCount int
	var usageEndClass, usageRequestedModel, usageUpstreamModel, usageCost string
	var usageKeyID, usageUserID, usageAccountID int64
	if err := pgPool.QueryRow(ctx, `
SELECT count(*)::int, COALESCE(max(end_class), ''), COALESCE(max(requested_model), ''),
       COALESCE(max(upstream_model), ''), COALESCE(max(actual_cost), 0)::text,
       COALESCE(max(api_key_id), 0), COALESCE(max(user_id), 0), COALESCE(max(provider_account_id), 0)
FROM usage_records
WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, claimID).Scan(
		&usageCount, &usageEndClass, &usageRequestedModel, &usageUpstreamModel, &usageCost,
		&usageKeyID, &usageUserID, &usageAccountID,
	); err != nil {
		t.Fatalf("读取 Grok 视频用量: %v", err)
	}
	usageCostValue, usageParseErr := strconv.ParseFloat(usageCost, 64)
	if usageCount != 1 || usageEndClass != "non_streaming" || usageRequestedModel != grokVideoLiveModel ||
		usageUpstreamModel != grokVideoLiveModel || usageParseErr != nil || usageCostValue <= 0 ||
		usageKeyID != seed.apiKeyID || usageUserID != seed.userID || usageAccountID != seed.providerAccountID {
		t.Fatalf("Grok 视频用量错误: count=%d end=%s models=%s/%s cost=%s key/user/account=%d/%d/%d",
			usageCount, usageEndClass, usageRequestedModel, usageUpstreamModel, usageCost, usageKeyID, usageUserID, usageAccountID)
	}

	var quotaStatus, quotaCost, held, slotStatus string
	var inFlight, receiptCount, ledgerCount, hopCount int
	if err := pgPool.QueryRow(ctx, `SELECT status, settled_cost::text FROM quota_reservations WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, claimID).Scan(&quotaStatus, &quotaCost); err != nil {
		t.Fatalf("读取 Grok 视频配额预留: %v", err)
	}
	if err := pgPool.QueryRow(ctx, `SELECT held::text FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&held); err != nil {
		t.Fatalf("读取 Grok 视频余额占用: %v", err)
	}
	if err := pgPool.QueryRow(ctx, `SELECT in_flight_count FROM provider_accounts WHERE tenant_id=$1 AND id=$2`, seed.tenantID, seed.providerAccountID).Scan(&inFlight); err != nil {
		t.Fatalf("读取 Grok 视频账号槽位: %v", err)
	}
	if err := pgPool.QueryRow(ctx, `SELECT status FROM pool_slot_acquisitions WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, claimID).Scan(&slotStatus); err != nil {
		t.Fatalf("读取 Grok 视频槽位记录: %v", err)
	}
	if err := pgPool.QueryRow(ctx, `
SELECT count(*)::int
FROM user_cost_receipt_owners o
JOIN user_cost_receipts r ON r.tenant_id=o.tenant_id AND r.request_id=o.request_id AND r.receipt_sequence=o.receipt_sequence
WHERE o.tenant_id=$1 AND o.claim_id=$2 AND octet_length(r.signed_hash)>0`, seed.tenantID, claimID).Scan(&receiptCount); err != nil {
		t.Fatalf("读取 Grok 视频签名费用凭证: %v", err)
	}
	if err := pgPool.QueryRow(ctx, `
SELECT count(*)::int, COALESCE(max(jsonb_array_length(hop_chain)), 0)::int
FROM audit_ledger_entries
WHERE tenant_id=$1 AND request_id=$2`, seed.tenantID, requestID).Scan(&ledgerCount, &hopCount); err != nil {
		t.Fatalf("读取 Grok 视频六跳日志链: %v", err)
	}
	quotaCostValue, quotaParseErr := strconv.ParseFloat(quotaCost, 64)
	if quotaStatus != "settled" || quotaParseErr != nil || quotaCostValue <= 0 || held != "0.00000000" ||
		inFlight != 0 || slotStatus != "released_success" || receiptCount != 1 || ledgerCount != 1 || hopCount != 6 {
		t.Fatalf("Grok 视频收尾错误: quota=%s/%s held=%s in_flight=%d slot=%s receipt=%d ledger/hops=%d/%d",
			quotaStatus, quotaCost, held, inFlight, slotStatus, receiptCount, ledgerCount, hopCount)
	}
}

func configureGrokVideoLiveSettings(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool) {
	t.Helper()
	conn, err := pgPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("获取 Grok 视频设置锁连接: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, grokVideoLiveSettingsLock); err != nil {
		conn.Release()
		t.Fatalf("获取 Grok 视频设置锁: %v", err)
	}
	store := platformsettings.NewPostgresStore(pgPool)
	type snapshot struct {
		key     platformsettings.SettingKey
		setting platformsettings.StoredSetting
		exists  bool
	}
	values := []struct {
		key   platformsettings.SettingKey
		value string
	}{
		{platformsettings.KeyMediaTaskEnabled, "true"},
		{platformsettings.KeyMediaTaskPollIntervalSecs, "1"},
		{platformsettings.KeyMediaTaskTimeoutSecs, "900"},
		{platformsettings.KeyMediaTaskDefaultEstimatedCents, `{"image_generation":100,"music_generation":300,"video_generation":1000}`},
	}
	snapshots := make([]snapshot, 0, len(values))
	for _, item := range values {
		previous, exists, err := store.Get(ctx, platformsettings.GlobalScope, string(item.key))
		if err != nil {
			_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, grokVideoLiveSettingsLock)
			conn.Release()
			t.Fatalf("读取 Grok 视频设置 %s: %v", item.key, err)
		}
		snapshots = append(snapshots, snapshot{key: item.key, setting: previous, exists: exists})
		if _, err := store.Upsert(ctx, platformsettings.GlobalScope, string(item.key), item.value, "e2e:grok-video-live"); err != nil {
			_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, grokVideoLiveSettingsLock)
			conn.Release()
			t.Fatalf("写入 Grok 视频设置 %s: %v", item.key, err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for index := len(snapshots) - 1; index >= 0; index-- {
			item := snapshots[index]
			if item.exists {
				_, err = conn.Exec(cleanupCtx, `
INSERT INTO platform_settings (scope, setting_key, setting_value, updated_at, updated_by)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (scope, setting_key) DO UPDATE
SET setting_value=EXCLUDED.setting_value, updated_at=EXCLUDED.updated_at, updated_by=EXCLUDED.updated_by`,
					item.setting.Scope, string(item.key), item.setting.Value, item.setting.UpdatedAt, item.setting.UpdatedBy)
			} else {
				_, err = conn.Exec(cleanupCtx, `DELETE FROM platform_settings WHERE scope=$1 AND setting_key=$2`, platformsettings.GlobalScope, string(item.key))
			}
			if err != nil {
				t.Errorf("恢复 Grok 视频设置 %s: %v", item.key, err)
			}
		}
		if _, err := conn.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1)`, grokVideoLiveSettingsLock); err != nil {
			t.Errorf("释放 Grok 视频设置锁: %v", err)
		}
		conn.Release()
	})
}

func registerGrokVideoLiveCleanup(t *testing.T, pgPool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, statement := range []string{
			`DELETE FROM media_task_orphans WHERE tenant_id=$1`,
			`DELETE FROM media_tasks WHERE tenant_id=$1`,
		} {
			if _, err := pgPool.Exec(ctx, statement, tenantID); err != nil {
				t.Errorf("清理 Grok 视频活体任务: %v", err)
				return
			}
		}
	})
}

func grokVideoLivePreview(raw []byte, secrets ...string) string {
	const maxBytes = 4096
	value := raw
	if len(value) > maxBytes {
		value = value[:maxBytes]
	}
	preview := redactOpenAIImageLiveSecrets(string(value), secrets...)
	if len(raw) > maxBytes {
		preview += "...[已截断]"
	}
	return preview
}
