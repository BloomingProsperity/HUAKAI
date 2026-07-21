//go:build e2e_gemini_video_live

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

const (
	geminiVideoLiveModel       = "veo-3.1-lite-generate-preview"
	geminiVideoLivePollTimeout = 12 * time.Minute
)

var geminiVideoLiveCase = imageLiveCase{
	slug: "gemini-video", model: geminiVideoLiveModel,
	protocol: registrydefault.ProtocolGeminiMessages, vendor: credentialstore.VendorGemini,
	authMode: credentialstore.AuthModeAIStudioAPIKey, accountType: "api_key", keyEnv: "HUAKAI_E2E_GEMINI_KEY",
	pricingData: openAIImageLivePricingData, capabilities: []string{"video", "video_output"},
}

func TestGeminiVideoLiveGenerateDownloadAndSettle(t *testing.T) {
	dsn := firstOpenAIImageLiveNonEmpty(os.Getenv("HUAKAI_E2E_DATABASE_URL"), os.Getenv("HUAKAI_DATABASE_URL"))
	if dsn == "" {
		t.Skip("HUAKAI_E2E_DATABASE_URL/HUAKAI_DATABASE_URL 未设置，跳过 Gemini 视频活体测试")
	}
	key := strings.TrimSpace(os.Getenv(geminiVideoLiveCase.keyEnv))
	if key == "" {
		t.Skip(geminiVideoLiveCase.keyEnv + " 未设置")
	}
	dsn = useDisposableSpecializedLiveDatabase(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 Gemini 视频活体测试数据库: %v", err)
	}
	t.Cleanup(pool.Close)

	seed := seedOpenAIImageLiveGraph(t, ctx, pool, geminiVideoLiveCase)
	configureGrokVideoLiveSettings(t, ctx, pool)
	registerGrokVideoLiveCleanup(t, pool, seed.tenantID)
	binPath := buildOpenAIImageLiveGateway(t)
	addr := reserveOpenAIImageLiveLocalPort(t)
	processes := startOpenAIImageLiveGateway(t, binPath, dsn, addr, seed, key)
	t.Cleanup(func() { stopSpecializedLiveProcesses(processes) })
	waitForOpenAIImageLiveGateway(t, addr)

	client := &http.Client{Timeout: 90 * time.Second}
	seed.providerAccountID = importOpenAIImageLiveAccount(t, ctx, client, addr, seed, key)
	assertOpenAIImageLiveImportedAccount(t, ctx, pool, seed)
	assertOpenAIImageLiveSeedSelectable(t, ctx, pool, seed)

	body, err := json.Marshal(map[string]any{
		"model": geminiVideoLiveModel, "prompt": "a solid red circle moving slowly on a plain white background",
		"duration": 4, "aspect_ratio": "16:9", "resolution": "720p",
	})
	if err != nil {
		t.Fatalf("编码 Gemini 视频请求: %v", err)
	}
	requestID := submitGeminiVideoLive(t, ctx, client, addr, seed.bearer, "gemini-video-live-"+uuid.NewString(), body, key)
	contentPath := pollGeminiVideoLive(t, ctx, client, addr, seed.bearer, requestID, key)
	downloadGeminiVideoLive(t, ctx, client, addr, seed.bearer, contentPath, key)
	assertGeminiVideoLiveSettlement(t, ctx, pool, seed, requestID)
}

func submitGeminiVideoLive(t *testing.T, ctx context.Context, client *http.Client, addr, bearer, logicalID string, body []byte, secrets ...string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/videos/generations", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("构造 Gemini 视频提交请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", logicalID)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("提交 Gemini 视频任务: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Gemini 视频提交失败: status=%d err=%v body=%s", resp.StatusCode, err, grokVideoLivePreview(raw, append(secrets, bearer)...))
	}
	var decoded struct {
		RequestID string `json:"request_id"`
	}
	if json.Unmarshal(raw, &decoded) != nil || !strings.HasPrefix(decoded.RequestID, "video_") {
		t.Fatalf("Gemini 视频提交未返回本地任务号: body=%s", grokVideoLivePreview(raw, append(secrets, bearer)...))
	}
	return decoded.RequestID
}

func pollGeminiVideoLive(t *testing.T, ctx context.Context, client *http.Client, addr, bearer, requestID string, secrets ...string) string {
	t.Helper()
	deadline := time.Now().Add(geminiVideoLivePollTimeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/v1/videos/"+requestID, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("轮询 Gemini 视频任务: %v", err)
		}
		raw, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("Gemini 视频轮询失败: status=%d err=%v body=%s", resp.StatusCode, readErr, grokVideoLivePreview(raw, append(secrets, bearer)...))
		}
		var result struct {
			Status string `json:"status"`
			Data   []struct {
				URL string `json:"url"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("解析 Gemini 视频轮询响应: %v", err)
		}
		switch strings.ToLower(strings.TrimSpace(result.Status)) {
		case "completed", "succeeded", "success", "done":
			if len(result.Data) != 1 || result.Data[0].URL != "/v1/videos/"+requestID+"/content" {
				t.Fatalf("Gemini 视频没有返回本地鉴权下载地址: body=%s", grokVideoLivePreview(raw, append(secrets, bearer)...))
			}
			if strings.Contains(string(raw), "generativelanguage.googleapis.com") {
				t.Fatalf("Gemini 上游受保护地址泄露: body=%s", grokVideoLivePreview(raw, append(secrets, bearer)...))
			}
			return result.Data[0].URL
		case "failed", "expired":
			t.Fatalf("Gemini 视频任务进入失败终态: body=%s", grokVideoLivePreview(raw, append(secrets, bearer)...))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("等待 Gemini 视频终态: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatalf("Gemini 视频任务 %s 在 %s 内未完成", requestID, geminiVideoLivePollTimeout)
	return ""
}

func downloadGeminiVideoLive(t *testing.T, ctx context.Context, client *http.Client, addr, bearer, path string, secrets ...string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("下载 Gemini 视频: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("下载 Gemini 视频失败: status=%d body=%s", resp.StatusCode, grokVideoLivePreview(raw, append(secrets, bearer)...))
	}
	written, err := io.Copy(io.Discard, resp.Body)
	if err != nil || written == 0 {
		t.Fatalf("Gemini 视频产物为空: bytes=%d err=%v", written, err)
	}
}

func assertGeminiVideoLiveSettlement(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed *openAIImageLiveSeed, requestID string) {
	t.Helper()
	var status, providerName, claimStatus, slotStatus, held string
	var accountID, actualCents, usageCount, inFlight int64
	if err := pool.QueryRow(ctx, `
SELECT mt.status, mt.provider, mt.provider_account_id, mt.actual_cents,
       blc.status, psa.status, ub.held::text, pa.in_flight_count,
       (SELECT count(*) FROM usage_records ur WHERE ur.tenant_id=mt.tenant_id AND ur.claim_id=blc.id)
FROM media_tasks mt
JOIN billing_ledger_claims blc ON blc.tenant_id=mt.tenant_id AND blc.logical_request_id=mt.request_id
JOIN pool_slot_acquisitions psa ON psa.tenant_id=blc.tenant_id AND psa.claim_id=blc.id
JOIN user_balances ub ON ub.tenant_id=mt.tenant_id AND ub.user_id=mt.user_id
JOIN provider_accounts pa ON pa.tenant_id=mt.tenant_id AND pa.id=mt.provider_account_id
WHERE mt.tenant_id=$1 AND mt.request_id=$2`, seed.tenantID, requestID).Scan(
		&status, &providerName, &accountID, &actualCents, &claimStatus, &slotStatus, &held, &inFlight, &usageCount,
	); err != nil {
		t.Fatalf("读取 Gemini 视频结算闭环: %v", err)
	}
	if status != "succeeded" || providerName != "gemini_video" || accountID != seed.providerAccountID ||
		actualCents <= 0 || claimStatus != "committed" || slotStatus != "released_success" ||
		held != "0.00000000" || inFlight != 0 || usageCount != 1 {
		t.Fatalf("Gemini 视频结算未闭环: task=%s/%s account=%d cents=%d claim=%s slot=%s held=%s in_flight=%d usage=%d",
			status, providerName, accountID, actualCents, claimStatus, slotStatus, held, inFlight, usageCount)
	}
}
