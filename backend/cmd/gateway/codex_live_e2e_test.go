//go:build e2e_codex_live

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

const (
	// 必须以 TestPrefix(hk_test_)开头才过 ValidCustomerFormat。前缀须短到让 unique
	// 落进 bearer 前 16 字符(=key_prefix),否则每次跑 key_prefix 相同、LIMIT 5 会漏掉新行→401。
	codexLiveBearerPrefix = "hk_test_"
	codexLiveBinaryName   = "gateway-codex-live-e2e.exe"
	codexLiveProtocol     = "openai_codex"
	codexLiveVendor       = credentialstore.VendorOpenAI
	codexLiveAuthMode     = credentialstore.AuthModeCodexCLIOAuth
	codexLiveDefaultModel = "gpt-5.5"
	// chatgpt.com 按模型强制最低 Codex 版本;旧版(如 0.99.0)对 gpt-5.5 返回
	// "requires a newer version of Codex" 400。须与已安装 codex CLI 版本一致。
	// 0.144.1:gpt-5.6 族有最低 Codex 版本门(实测 0.142→拒/0.144→过),旧版本会被上游拒
	// "requires a newer version of Codex";gpt-5.5 在新版本下向后兼容仍工作。
	codexLiveDefaultVersion = "0.144.1"
	codexLiveQuotaLimit     = "1000.00000000"

	codexLiveBootRetries   = 30
	codexLiveBootRetryWait = 200 * time.Millisecond
)

type codexLiveAuth struct {
	AccessToken string
	AccountID   string
}

type codexLiveAuthFile struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func TestCodexLiveResponsesMatrix(t *testing.T) {
	dsn := firstCodexLiveNonEmpty(os.Getenv("HUAKAI_DATABASE_URL"), os.Getenv("HUAKAI_E2E_DATABASE_URL"))
	if strings.TrimSpace(dsn) == "" {
		t.Skip("HUAKAI_DATABASE_URL/HUAKAI_E2E_DATABASE_URL 未设置，跳过 codex live e2e")
	}
	auth := loadCodexLiveAuth(t)
	if strings.TrimSpace(auth.AccessToken) == "" {
		t.Skip("未找到 Codex live access_token，跳过 codex live e2e")
	}
	if strings.TrimSpace(auth.AccountID) == "" {
		t.Skip("未找到 Codex live account_id，跳过 codex live e2e")
	}
	model := firstCodexLiveNonEmpty(os.Getenv("HUAKAI_E2E_CODEX_MODEL"), codexLiveDefaultModel)
	version := firstCodexLiveNonEmpty(os.Getenv("HUAKAI_E2E_CODEX_VERSION"), codexLiveDefaultVersion)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 codex live e2e 数据库连接池: %v", err)
	}
	defer pgPool.Close()

	seed := seedCodexLiveGraph(t, ctx, pgPool, auth, model, version)
	assertCodexLiveSeedSelectable(t, ctx, pgPool, seed)

	binPath := buildCodexLiveGateway(t)
	defer os.Remove(binPath)

	addr := reserveCodexLiveLocalPort(t)
	cmd := startCodexLiveGateway(t, binPath, dsn, addr, seed, auth)
	t.Cleanup(func() { stopCodexLiveGateway(cmd) })
	waitForCodexLiveGateway(t, addr)

	client := &http.Client{Timeout: 180 * time.Second}
	cases := []struct {
		name       string
		body       map[string]any
		skipReason string
		assert     func(*testing.T, codexLiveResult)
	}{
		{
			name: "流式文本",
			body: codexLiveBaseBody(model, "Reply with exactly one short sentence.", "Say hello in three words.", true),
			assert: func(t *testing.T, res codexLiveResult) {
				assertCodexLiveSSEEvents(t, res, "response.created", "response.output_text.delta", "response.completed")
				if strings.TrimSpace(res.outputText) == "" {
					t.Fatalf("流式文本输出为空: body=%s", safeCodexLiveBody(res.body, ""))
				}
			},
		},
		{
			name: "工具调用",
			body: codexLiveToolBody(model),
			assert: func(t *testing.T, res codexLiveResult) {
				assertCodexLiveSSEEvents(t, res, "response.created", "response.completed")
				if !res.sawFunctionCall {
					t.Fatalf("未观察到 function_call 事件: events=%v body=%s", res.events, safeCodexLiveBody(res.body, ""))
				}
			},
		},
		{
			name: "图片生成",
			body: codexLiveImageBody(model),
			assert: func(t *testing.T, res codexLiveResult) {
				assertCodexLiveSSEEvents(t, res, "response.created", "response.completed")
				if !res.sawImage {
					t.Fatalf("未观察到 image_generation_call 事件(HUAKAI 可能未透传 image_generation 工具): events=%v", res.events)
				}
			},
		},
		{
			name: "reasoning",
			body: codexLiveReasoningBody(model),
			assert: func(t *testing.T, res codexLiveResult) {
				assertCodexLiveSSEEvents(t, res, "response.created", "response.completed")
				if !res.sawReasoning {
					t.Fatalf("未观察到 reasoning output_item: events=%v body=%s", res.events, safeCodexLiveBody(res.body, ""))
				}
			},
		},
		{
			// max 是 gpt-5.6 专属最高 effort 档(gpt-5.5 及以下上游直接 400)。
			// 本用例钉死 HUAKAI 对合法 effort 字节直通、不折叠；上游会原样回显 effort=max。
			// 注:ultra 不是 wire 层 effort 枚举值(上游返回 Invalid value: 'ultra'),
			// 它是客户端编排 mode(response 里体现为 reasoning.mode 而非 effort),故 relay
			// 层无从透传 ultra effort,这里验证的是最高合法档 max。
			name: "reasoning-max字节直通",
			body: codexLiveMaxEffortBody(model),
			// max 是 gpt-5.6 专属档;非 5.6 模型上游会拒(400),须在发请求前跳过而非在断言里
			// (断言在 HTTP 200 校验之后,来不及)。
			skipReason: func() string {
				if !strings.Contains(model, "5.6") {
					return "model=" + model + " 无 max 档(max 为 gpt-5.6 专属)"
				}
				return ""
			}(),
			assert: func(t *testing.T, res codexLiveResult) {
				assertCodexLiveSSEEvents(t, res, "response.created", "response.completed")
				// 变异：网关若把 max 折叠为 xhigh，回显会变化，本断言必须变红。
				if !bytes.Contains(res.body, []byte(`"effort":"max"`)) {
					t.Fatalf("max 未字节直通回显(疑被折叠): events=%v body=%s", res.events, safeCodexLiveBody(res.body, ""))
				}
				if bytes.Contains(res.body, []byte(`"effort":"xhigh"`)) {
					t.Fatalf("max 被折叠成 xhigh: body=%s", safeCodexLiveBody(res.body, ""))
				}
			},
		},
		{
			name: "图片输入",
			body: codexLiveVisionBody(model),
			assert: func(t *testing.T, res codexLiveResult) {
				assertCodexLiveSSEEvents(t, res, "response.created", "response.output_text.delta", "response.completed")
				if !strings.Contains(strings.ToLower(res.outputText), "red") {
					t.Fatalf("图片颜色回答=%q want 包含 red; body=%s", res.outputText, safeCodexLiveBody(res.body, ""))
				}
			},
		},
		{
			name: "请求变换验证",
			body: codexLiveTransformBody(model),
			assert: func(t *testing.T, res codexLiveResult) {
				if res.isSSE {
					assertCodexLiveSSEEvents(t, res, "response.created", "response.completed")
					return
				}
				if res.bufferedID == "" && strings.TrimSpace(res.outputText) == "" {
					t.Fatalf("非流式客户端经上游流式聚合后响应不可识别: body=%s", safeCodexLiveBody(res.body, ""))
				}
			},
		},
		{
			name: "非流式长输出聚合",
			body: codexLiveLongBufferedBody(model),
			assert: func(t *testing.T, res codexLiveResult) {
				if res.isSSE {
					t.Fatalf("非流式客户端不应收到原始 SSE: events=%v body=%s", res.events, safeCodexLiveBody(res.body, ""))
				}
				if res.bufferedID == "" {
					t.Fatalf("聚合后的 Responses JSON 缺 id: body=%s", safeCodexLiveBody(res.body, ""))
				}
				if strings.TrimSpace(res.outputText) == "" {
					t.Fatalf("聚合后的 Responses JSON 输出为空: body=%s", safeCodexLiveBody(res.body, ""))
				}
				if res.usage.InputTokens+res.usage.OutputTokens+res.usage.TotalTokens <= 0 {
					t.Fatalf("聚合后的 Responses JSON 缺 usage: %+v body=%s", res.usage, safeCodexLiveBody(res.body, ""))
				}
				// CF keepalive 判别:开启 HUAKAI_NONSTREAM_KEEPALIVE_INTERVAL 时,buffered 上游耗时
				// 远大于间隔(本用例真上游 ~数秒),响应体应带前导换行保活字节且不破坏 JSON 解析(上方
				// bufferedID/usage 已从含前导空白的 body 解析成功)。变异:keepalive 未接线 → 0 前导 → 红。
				if ka := strings.TrimSpace(os.Getenv("HUAKAI_NONSTREAM_KEEPALIVE_INTERVAL")); ka != "" && ka != "0" && ka != "0s" {
					lead := 0
					for lead < len(res.body) && (res.body[lead] == '\n' || res.body[lead] == ' ') {
						lead++
					}
					if lead == 0 {
						t.Fatalf("keepalive 开启(%s)时 buffered 响应应有前导保活字节,实际 0(接线断?)", ka)
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipReason != "" {
				t.Skipf("跳过用例:%s", tc.skipReason)
			}
			logicalID := "codex-live-" + uuid.NewString()
			res := postCodexLiveResponses(t, ctx, client, addr, seed, logicalID, tc.body)
			if res.statusCode != http.StatusOK {
				t.Fatalf("HTTP status=%d want 200 body=%s", res.statusCode, safeCodexLiveBody(res.body, auth.AccessToken))
			}
			tc.assert(t, res)
			claimID := assertCodexLivePG(t, ctx, pgPool, seed, logicalID)
			assertCodexLiveUsageRecord(t, ctx, pgPool, claimID)
			waitForCodexLiveInFlight(t, ctx, pgPool, seed.providerAccountID, 0)
		})
	}
}

func TestChatToCodexLiveMatrix(t *testing.T) {
	dsn := firstCodexLiveNonEmpty(os.Getenv("HUAKAI_DATABASE_URL"), os.Getenv("HUAKAI_E2E_DATABASE_URL"))
	if strings.TrimSpace(dsn) == "" {
		t.Skip("HUAKAI_DATABASE_URL/HUAKAI_E2E_DATABASE_URL 未设置，跳过 chat/messages→codex live e2e")
	}
	auth := loadCodexLiveAuth(t)
	if strings.TrimSpace(auth.AccessToken) == "" {
		t.Skip("未找到 Codex live access_token，跳过 chat/messages→codex live e2e")
	}
	if strings.TrimSpace(auth.AccountID) == "" {
		t.Skip("未找到 Codex live account_id，跳过 chat/messages→codex live e2e")
	}
	model := firstCodexLiveNonEmpty(os.Getenv("HUAKAI_E2E_CODEX_MODEL"), codexLiveDefaultModel)
	version := firstCodexLiveNonEmpty(os.Getenv("HUAKAI_E2E_CODEX_VERSION"), codexLiveDefaultVersion)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pgPool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 chat/messages→codex live e2e 数据库连接池: %v", err)
	}
	defer pgPool.Close()

	seed := seedCodexLiveGraph(t, ctx, pgPool, auth, model, version)
	assertCodexLiveSeedSelectable(t, ctx, pgPool, seed)

	binPath := buildCodexLiveGateway(t)
	defer os.Remove(binPath)

	addr := reserveCodexLiveLocalPort(t)
	cmd := startCodexLiveGateway(t, binPath, dsn, addr, seed, auth)
	t.Cleanup(func() { stopCodexLiveGateway(cmd) })
	waitForCodexLiveGateway(t, addr)

	streamTrue := true
	streamFalse := false
	client := &http.Client{Timeout: 180 * time.Second}
	cases := []struct {
		name       string
		path       string
		body       map[string]any
		wantStream *bool
		d2Probe    bool
		assert     func(*testing.T, codexLiveHTTPResult)
	}{
		{
			name:       "chat流式文本",
			path:       "/v1/chat/completions",
			body:       codexLiveChatTextBody(model, "Say hi in 3 words", true, 16),
			wantStream: &streamTrue,
			assert: func(t *testing.T, res codexLiveHTTPResult) {
				assertCodexLiveContentType(t, res, "text/event-stream")
				chat := parseCodexLiveChatResponse(t, res.body)
				if !chat.isSSE {
					t.Fatalf("chat 流式文本响应不是 SSE: body=%s", safeCodexLiveBody(res.body, ""))
				}
				if !chat.sawDeltaContent || strings.TrimSpace(chat.outputText) == "" {
					t.Fatalf("chat SSE 未观察到 choices[].delta.content: parsed=%+v body=%s", chat, safeCodexLiveBody(res.body, ""))
				}
			},
		},
		{
			name: "D2字段探测",
			path: "/v1/chat/completions",
			// chat stop 会在投影为 Codex Responses 后剥离；live 应返回 200。
			body: map[string]any{
				"model": model,
				"messages": []map[string]any{{
					"role":    "user",
					"content": "Return a tiny JSON object with answer set to ok. Do not call tools.",
				}},
				"stream":              true,
				"max_tokens":          16,
				"stop":                []string{"\n\n"},
				"temperature":         0.5,
				"top_p":               0.9,
				"tools":               []map[string]any{codexLiveChatFunctionTool("lookup_weather", "Look up a short weather summary.")},
				"tool_choice":         "auto",
				"parallel_tool_calls": true,
				// 注:response_format(json_schema)不在本子测试——它触发独立的
				// 结构化输出翻译 gap(chat response_format→Responses text.format,片2g)。
				// 本子测试验 stop 剥离 + temperature/top_p 剥离 + tools/tool_choice/
				// parallel_tool_calls 被 codex 接受。
			},
			wantStream: &streamTrue,
			d2Probe:    true,
			assert: func(t *testing.T, res codexLiveHTTPResult) {
				assertCodexLiveContentType(t, res, "text/event-stream")
			},
		},
		{
			name: "D2结构化输出json_schema",
			path: "/v1/chat/completions",
			body: map[string]any{
				"model": model,
				"messages": []map[string]any{{
					"role":    "user",
					"content": `Return exactly {"answer":"ok"} as JSON.`,
				}},
				"stream":     false,
				"max_tokens": 64,
				"response_format": map[string]any{
					"type": "json_schema",
					"json_schema": map[string]any{
						"name":   "live_answer",
						"strict": true,
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"answer": map[string]any{"type": "string"},
							},
							"required":             []string{"answer"},
							"additionalProperties": false,
						},
					},
				},
			},
			wantStream: &streamFalse,
			assert: func(t *testing.T, res codexLiveHTTPResult) {
				assertCodexLiveContentType(t, res, "application/json")
				chat := parseCodexLiveChatResponse(t, res.body)
				if strings.TrimSpace(chat.outputText) == "" {
					t.Fatalf("结构化输出 chat 响应为空: parsed=%+v body=%s", chat, safeCodexLiveBody(res.body, ""))
				}
				var got map[string]any
				if err := json.Unmarshal([]byte(chat.outputText), &got); err != nil {
					t.Fatalf("结构化输出不是 JSON object: output=%q err=%v body=%s", chat.outputText, err, safeCodexLiveBody(res.body, ""))
				}
				if got["answer"] != "ok" {
					t.Fatalf("结构化输出 answer=%v want ok; output=%q", got["answer"], chat.outputText)
				}
			},
		},
		{
			name: "chat工具调用",
			path: "/v1/chat/completions",
			body: map[string]any{
				"model": model,
				"messages": []map[string]any{{
					"role":    "user",
					"content": "Call get_current_weather for Paris. Do not answer with normal text.",
				}},
				"stream":      true,
				"max_tokens":  32,
				"tools":       []map[string]any{codexLiveChatFunctionTool("get_current_weather", "Return the current weather for a city.")},
				"tool_choice": "required",
			},
			wantStream: &streamTrue,
			assert: func(t *testing.T, res codexLiveHTTPResult) {
				chat := parseCodexLiveChatResponse(t, res.body)
				if !chat.sawToolCall {
					t.Fatalf("chat 工具调用未观察到 tool_calls: parsed=%+v body=%s", chat, safeCodexLiveBody(res.body, ""))
				}
			},
		},
		{
			name:       "chat非流式聚合",
			path:       "/v1/chat/completions",
			body:       codexLiveChatTextBody(model, "Reply with exactly: aggregation ok", false, 16),
			wantStream: &streamFalse,
			assert: func(t *testing.T, res codexLiveHTTPResult) {
				assertCodexLiveContentType(t, res, "application/json")
				chat := parseCodexLiveChatResponse(t, res.body)
				if chat.isSSE {
					t.Fatalf("chat 非流式聚合不应返回 SSE: parsed=%+v body=%s", chat, safeCodexLiveBody(res.body, ""))
				}
				if chat.object != "chat.completion" || chat.choiceCount != 1 {
					t.Fatalf("chat 非流式响应形态不对: parsed=%+v body=%s", chat, safeCodexLiveBody(res.body, ""))
				}
				if strings.TrimSpace(chat.outputText) == "" {
					t.Fatalf("chat 非流式 message.content 为空: body=%s", safeCodexLiveBody(res.body, ""))
				}
			},
		},
		{
			name: "chat视觉",
			path: "/v1/chat/completions",
			body: map[string]any{
				"model":      model,
				"stream":     false,
				"max_tokens": 16,
				"messages": []map[string]any{{
					"role": "user",
					"content": []map[string]any{
						{"type": "text", "text": "What color is this square? Answer with one English color word."},
						{"type": "image_url", "image_url": map[string]any{"url": codexLiveRedPNGDataURL()}},
					},
				}},
			},
			wantStream: &streamFalse,
			assert: func(t *testing.T, res codexLiveHTTPResult) {
				chat := parseCodexLiveChatResponse(t, res.body)
				if !strings.Contains(strings.ToLower(chat.outputText), "red") {
					t.Fatalf("chat 视觉颜色回答=%q want 包含 red; body=%s", chat.outputText, safeCodexLiveBody(res.body, ""))
				}
			},
		},
		{
			name: "anthropic到codex",
			path: "/v1/messages",
			body: map[string]any{
				"model":      model,
				"max_tokens": 16,
				"messages": []map[string]any{{
					"role":    "user",
					"content": "Say hi",
				}},
			},
			wantStream: &streamFalse,
			assert: func(t *testing.T, res codexLiveHTTPResult) {
				assertCodexLiveContentType(t, res, "application/json")
				msg := parseCodexLiveAnthropicResponse(t, res.body)
				if msg.typ != "message" || strings.TrimSpace(msg.outputText) == "" {
					t.Fatalf("anthropic_messages 响应不可识别: parsed=%+v body=%s", msg, safeCodexLiveBody(res.body, ""))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logicalID := "codex-live-chat-" + uuid.NewString()
			res := postCodexLiveClientRequest(t, ctx, client, addr, seed, logicalID, tc.path, tc.body)
			if res.statusCode != http.StatusOK {
				if tc.d2Probe {
					t.Fatalf("D2 探测 HTTP status=%d want 200(stop 已剥离); 完整错误 body=%s; 若需核对 HUAKAI 实发上游 body, 可设置 HUAKAI_E2E_CODEX_CAPTURE_URL",
						res.statusCode, safeCodexLiveBody(res.body, auth.AccessToken))
				}
				t.Fatalf("%s HTTP status=%d want 200 body=%s", tc.name, res.statusCode, safeCodexLiveBody(res.body, auth.AccessToken))
			}
			tc.assert(t, res)
			claimID := assertCodexLivePG(t, ctx, pgPool, seed, logicalID)
			assertCodexLiveUsageRecord(t, ctx, pgPool, claimID)
			if tc.wantStream != nil {
				assertCodexLiveUsageRecordStream(t, ctx, pgPool, claimID, *tc.wantStream)
			}
			waitForCodexLiveInFlight(t, ctx, pgPool, seed.providerAccountID, 0)
		})
	}
}

func loadCodexLiveAuth(t *testing.T) codexLiveAuth {
	t.Helper()
	if token := strings.TrimSpace(os.Getenv("HUAKAI_CODEX_LIVE_ACCESS_TOKEN")); token != "" {
		return codexLiveAuth{
			AccessToken: token,
			AccountID:   strings.TrimSpace(os.Getenv("HUAKAI_CODEX_LIVE_ACCOUNT_ID")),
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("无法定位 HOME 读取 ~/.codex/auth.json: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		t.Skipf("读取 ~/.codex/auth.json 失败: %v", err)
	}
	var parsed codexLiveAuthFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("解析 ~/.codex/auth.json: %v", err)
	}
	return codexLiveAuth{
		AccessToken: strings.TrimSpace(parsed.Tokens.AccessToken),
		AccountID:   strings.TrimSpace(parsed.Tokens.AccountID),
	}
}

type codexLiveSeed struct {
	tenantID          int64
	apiKeyID          int64
	userID            int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
	modelID           int64
	aliasID           int64
	costQuotaPolicyID int64
	pricingVersion    string
	bearer            string
	model             string
	version           string
}

func seedCodexLiveGraph(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, auth codexLiveAuth, model, version string) *codexLiveSeed {
	t.Helper()
	unique := uuid.NewString()
	seed := &codexLiveSeed{
		pricingVersion: "e2e-codex-live-" + unique,
		model:          model,
		version:        version,
	}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"codex-live-e2e-tenant-"+unique,
	).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "codex-live-e2e-user-"+unique,
	).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	seed.bearer = codexLiveBearerPrefix + unique
	keyPrefix := seed.bearer
	if len(keyPrefix) > 16 {
		keyPrefix = keyPrefix[:16]
	}
	keyHash, err := bcrypt.GenerateFromPassword([]byte(seed.bearer), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash bearer: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		seed.tenantID, seed.userID, "codex-live-e2e-key-"+unique, string(keyHash), keyPrefix,
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
		 VALUES ($1, $2, 1000.00, 0, 1, now())`,
		seed.tenantID, seed.userID,
	); err != nil {
		t.Fatalf("seed user_balance: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pgPool.Exec(c, `DELETE FROM quota_audit_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_concurrency_slots WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_concurrency_scope_locks WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_reconciliation_jobs WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_reservations WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_windows WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM quota_policies WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM idempotency_replay_records WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM usage_records WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM balance_holds WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM model_pool_bindings WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM model_registry_capabilities WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM model_aliases WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM models WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM model_registry_snapshots WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM model_registry_tenant_policies WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM account_credentials WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM channels WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM providers WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM user_balances WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
		_, _ = pgPool.Exec(c, `DELETE FROM billing_pricing_versions WHERE tenant_id=0 AND version=$1`, seed.pricingVersion)
	})

	if _, err := pgPool.Exec(ctx,
		`INSERT INTO tenants (id, name, status, created_at, updated_at)
		 VALUES (0, 'public-pricing', 'active', now(), now())
		 ON CONFLICT (id) DO NOTHING`,
	); err != nil {
		t.Fatalf("seed public pricing tenant: %v", err)
	}
	pricingData := fmt.Sprintf(
		`{"providers":{"codex":{"models":{"%[1]s":{"input_micro_usd":"1","output_micro_usd":"2","cache_read_micro_usd":"1"}}},"openai_codex":{"models":{"%[1]s":{"input_micro_usd":"1","output_micro_usd":"2","cache_read_micro_usd":"1"}}},"openai":{"models":{"%[1]s":{"input_micro_usd":"1","output_micro_usd":"2","cache_read_micro_usd":"1"}}}}}`,
		model,
	)
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO billing_pricing_versions (
		    tenant_id, version, pricing_data, effective_from, created_by_actor, is_public
		  )
		  VALUES (0, $1, $2::jsonb, now(), $3, true)
		  ON CONFLICT (tenant_id, version) DO UPDATE
		  SET pricing_data = EXCLUDED.pricing_data,
		      effective_from = EXCLUDED.effective_from,
		      created_by_actor = EXCLUDED.created_by_actor,
		      is_public = true`,
		seed.pricingVersion, pricingData, "e2e:codex-live",
	); err != nil {
		t.Fatalf("seed billing pricing version: %v", err)
	}

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		seed.tenantID, codexLiveVendor, "codex live e2e "+unique, codexLiveProtocol,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "codex-live-e2e-pg-"+unique,
	).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, "codex-live-e2e-ch-"+unique,
	).Scan(&seed.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	seed.providerAccountID = seedCodexLiveProviderAccount(t, ctx, pgPool, seed, auth, unique)

	if err := pgPool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 128000, 'active')
		 RETURNING id`,
		seed.tenantID, model, codexLiveProtocol, model,
	).Scan(&seed.modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO model_aliases (tenant_id, scope, model_id,
		                            public_alias_normalized, public_alias_display, status)
		 VALUES ($1, 'tenant', $2, $3, $4, 'active')
		 RETURNING id`,
		seed.tenantID, seed.modelID, model, model,
	).Scan(&seed.aliasID); err != nil {
		t.Fatalf("seed model_alias: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO model_pool_bindings (tenant_id, model_id, pool_group_id, priority, weight, enabled)
		 VALUES ($1, $2, $3, 100, 1, true)`,
		seed.tenantID, seed.modelID, seed.poolGroupID,
	); err != nil {
		t.Fatalf("seed model_pool_bindings: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO model_registry_snapshots (tenant_id, version)
		 VALUES ($1, 1)
		 ON CONFLICT (tenant_id) DO UPDATE SET version = 1`,
		seed.tenantID,
	); err != nil {
		t.Fatalf("seed model_registry_snapshots: %v", err)
	}
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO quota_policies (
			tenant_id, scope_kind, scope_id, metric, window_kind, window_seconds,
			limit_value, burst_value, mode, priority, enabled, valid_from
		 )
		 VALUES ($1, 'user', $2, 'cost_usd', 'fixed', 3600,
		         $3::numeric, 0, 'enforce', 10, true, now())
		 RETURNING id`,
		seed.tenantID, strconv.FormatInt(seed.userID, 10), codexLiveQuotaLimit,
	).Scan(&seed.costQuotaPolicyID); err != nil {
		t.Fatalf("seed cost quota policy: %v", err)
	}
	return seed
}

func seedCodexLiveProviderAccount(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *codexLiveSeed, auth codexLiveAuth, unique string) int64 {
	t.Helper()
	extra := map[string]string{
		"account_id":    auth.AccountID,
		"codex_version": seed.version,
		"originator":    "codex_cli_rs",
		"oai_device_id": "huakai-codex-live-e2e",
		"user_agent":    "codex_cli_rs/" + seed.version + " (linux; x86_64)",
		"oai_country":   "US",
	}
	// 诊断钩子:设了 HUAKAI_E2E_CODEX_CAPTURE_URL 则把出站指向本地捕获服务器,看 HUAKAI 实发请求。
	if cap := strings.TrimSpace(os.Getenv("HUAKAI_E2E_CODEX_CAPTURE_URL")); cap != "" {
		extra["base_url"] = cap
	}
	legacyCredentials, err := json.Marshal(map[string]any{
		"access_token": auth.AccessToken,
		"account_id":   auth.AccountID,
		"extra":        extra,
	})
	if err != nil {
		t.Fatalf("marshal legacy codex credential: %v", err)
	}
	extraJSON, err := json.Marshal(extra)
	if err != nil {
		t.Fatalf("marshal provider account extra: %v", err)
	}

	var accountID int64
	if err := pgPool.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type,
			cap_concurrency, in_flight_count, priority, health_state, credential_state,
			model_allow_list, capability_flags, credentials, extra
		) VALUES ($1, $2, $3, $4, $5,
			1, 0, 100, 'healthy', 'valid',
			ARRAY[$6]::text[], ARRAY['stream','tools','vision','json'], $7::jsonb, $8::jsonb) RETURNING id`,
		seed.tenantID, seed.providerID, seed.channelID, "codex-live-e2e-acct-"+unique,
		// account_type 是账号类型(oauth/session/...),非 auth_mode;codex session 用 'session'。
		"session", seed.model, string(legacyCredentials), string(extraJSON),
	).Scan(&accountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"access_token":  auth.AccessToken,
		"account_id":    auth.AccountID,
		"codex_version": seed.version,
		"originator":    "codex_cli_rs",
		"oai_device_id": "huakai-codex-live-e2e",
		"user_agent":    "codex_cli_rs/" + seed.version + " (linux; x86_64)",
		"extra":         extra,
	})
	if err != nil {
		t.Fatalf("marshal account credential payload: %v", err)
	}
	credKP, err := credentialstore.NewStaticKeyProvider("local-v1", make([]byte, 32))
	if err != nil {
		t.Fatalf("cred key provider: %v", err)
	}
	credEnv, err := credentialstore.NewCipher(credKP).Encrypt(ctx,
		payload,
		credentialstore.AAD{
			TenantID:          seed.tenantID,
			ProviderAccountID: accountID,
			Vendor:            codexLiveVendor,
			AuthMode:          codexLiveAuthMode,
			Version:           1,
		})
	if err != nil {
		t.Fatalf("encrypt credential for account %d: %v", accountID, err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO account_credentials (tenant_id, provider_account_id, vendor, auth_mode, state,
		   credential_version, encrypted_payload, encryption_scheme, key_id, nonce, aad_hash)
		 VALUES ($1, $2, $3, $4, 'active', 1, $5, 'aes-256-gcm', $6, $7, $8)`,
		seed.tenantID, accountID, codexLiveVendor, codexLiveAuthMode,
		credEnv.Ciphertext, credEnv.KeyID, credEnv.Nonce, credEnv.AADHash,
	); err != nil {
		t.Fatalf("seed account credential: %v", err)
	}
	return accountID
}

func assertCodexLiveSeedSelectable(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *codexLiveSeed) {
	t.Helper()
	resolved, err := registry.NewPostgresRegistry(pgPool, nil).ResolveModel(ctx, seed.model, seed.tenantID)
	if err != nil {
		t.Fatalf("seed registry resolve %q: %v", seed.model, err)
	}
	if resolved.ProtocolFamily != codexLiveProtocol {
		t.Fatalf("resolved protocol_family=%q want %q", resolved.ProtocolFamily, codexLiveProtocol)
	}
	rows, err := dbbilling.New(pgPool).ListEligibleAccountsByPoolGroup(ctx, dbbilling.ListEligibleAccountsByPoolGroupParams{
		TenantID:                seed.tenantID,
		PoolGroupID:             seed.poolGroupID,
		RequestedModel:          seed.model,
		RequestedProtocolFamily: resolved.ProtocolFamily,
		RequiredCapabilities:    []string{},
	})
	if err != nil {
		t.Fatalf("seed selector eligibility query: %v", err)
	}
	for _, row := range rows {
		if row.ID == seed.providerAccountID {
			return
		}
	}
	t.Fatalf("selector eligibility 未返回 provider_account_id=%d; rows=%v", seed.providerAccountID, rows)
}

func buildCodexLiveGateway(t *testing.T) string {
	t.Helper()
	moduleRoot := goModuleRootForCodexLive(t)
	binPath := moduleRoot + "/" + codexLiveBinaryName
	stamp := fmt.Sprintf("codex-live-e2e-%d", time.Now().UnixNano())
	cmd := exec.Command("go", "build",
		"-ldflags", "-X main.smokeBuildStamp="+stamp,
		"-o", binPath, "./cmd/gateway")
	cmd.Dir = moduleRoot
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build gateway from %s: %v", moduleRoot, err)
	}
	return binPath
}

func goModuleRootForCodexLive(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := string(bytes.TrimSpace(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Fatalf("not in a Go module")
	}
	const suffix = "/go.mod"
	if strings.HasSuffix(gomod, suffix) {
		return strings.TrimSuffix(gomod, suffix)
	}
	t.Fatalf("unexpected GOMOD path: %q", gomod)
	return ""
}

func reserveCodexLiveLocalPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}
	return addr
}

func startCodexLiveGateway(t *testing.T, binPath, dsn, addr string, seed *codexLiveSeed, auth codexLiveAuth) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"HUAKAI_DATABASE_URL="+dsn,
		"HUAKAI_ADDR="+addr,
		"HUAKAI_RELEASE_MODE=dev",
		"HUAKAI_CREDENTIAL_KEY_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HUAKAI_SESSION_SIGNING_KEY_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"HUAKAI_DEV_MOCK_UPSTREAM=false",
		"HUAKAI_BILLING_POLICY_VERSION="+seed.pricingVersion,
		"HUAKAI_QUOTA_ENFORCE=true",
		"HUAKAI_CACHE_L2_ENABLED=0",
		"HUAKAI_EVENTBUS_ENABLED=0",
		"HUAKAI_RATE_PRECHECK_ENABLED=false",
		"HUAKAI_BINDING_RATE_LIMIT_ENABLED=false",
		"HUAKAI_KEY_RPM_LIMIT=0",
		"HUAKAI_KEY_TPM_LIMIT=0",
		"HUAKAI_DISPATCH_HCSF=1",
	)
	stderr, _ := cmd.StderrPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	go drainCodexLivePipe("gateway-stderr", stderr, auth.AccessToken)
	go drainCodexLivePipe("gateway-stdout", stdout, auth.AccessToken)
	return cmd
}

func drainCodexLivePipe(label string, r io.ReadCloser, token string) {
	if r == nil {
		return
	}
	defer r.Close()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fmt.Printf("[%s] %s\n", label, redactCodexLiveSecrets(scanner.Text(), token))
	}
}

func stopCodexLiveGateway(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}

func waitForCodexLiveGateway(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < codexLiveBootRetries; i++ {
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(codexLiveBootRetryWait)
	}
	t.Fatalf("gateway did not start listening on %s within %v",
		addr, time.Duration(codexLiveBootRetries)*codexLiveBootRetryWait)
}

type codexLiveResult struct {
	statusCode      int
	body            []byte
	isSSE           bool
	events          map[string]bool
	outputText      string
	usage           codexLiveUsage
	sawFunctionCall bool
	sawReasoning    bool
	sawImage        bool
	bufferedID      string
}

type codexLiveUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type codexLiveHTTPResult struct {
	statusCode  int
	contentType string
	body        []byte
}

type codexLiveChatParsed struct {
	isSSE           bool
	object          string
	choiceCount     int
	outputText      string
	sawDeltaContent bool
	sawToolCall     bool
}

type codexLiveAnthropicParsed struct {
	typ        string
	outputText string
}

func postCodexLiveResponses(t *testing.T, ctx context.Context, client *http.Client, addr string, seed *codexLiveSeed, logicalID string, body map[string]any) codexLiveResult {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal codex live body: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/responses", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+seed.bearer)
	req.Header.Set("Idempotency-Key", logicalID)
	req.Header.Set("User-Agent", "codex_cli_rs/"+seed.version+" (linux; x86_64)")
	req.Header.Set("X-Client-Name", "codex-cli")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/responses: %v", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	result := codexLiveResult{statusCode: resp.StatusCode, body: respBody, events: map[string]bool{}}
	if resp.StatusCode == http.StatusOK {
		result = parseCodexLiveResponse(t, respBody)
		result.statusCode = resp.StatusCode
		result.body = respBody
	}
	return result
}

func postCodexLiveClientRequest(t *testing.T, ctx context.Context, client *http.Client, addr string, seed *codexLiveSeed, logicalID, path string, body map[string]any) codexLiveHTTPResult {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal codex live client body: %v", err)
	}
	if !strings.HasPrefix(path, "/") {
		t.Fatalf("codex live client path=%q must start with /", path)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewRequest %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if codexLiveBodyStream(body) {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+seed.bearer)
	req.Header.Set("Idempotency-Key", logicalID)
	req.Header.Set("User-Agent", "codex-live-e2e/"+seed.version)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s body: %v", path, err)
	}
	return codexLiveHTTPResult{
		statusCode:  resp.StatusCode,
		contentType: resp.Header.Get("Content-Type"),
		body:        respBody,
	}
}

func codexLiveBodyStream(body map[string]any) bool {
	stream, ok := body["stream"].(bool)
	return ok && stream
}

func parseCodexLiveResponse(t *testing.T, raw []byte) codexLiveResult {
	t.Helper()
	if bytes.Contains(raw, []byte("event:")) || bytes.Contains(raw, []byte("data:")) {
		return parseCodexLiveSSE(t, raw)
	}
	var resp struct {
		ID     string `json:"id"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage codexLiveUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode buffered codex live response: %v body=%s", err, safeCodexLiveBody(raw, ""))
	}
	var text strings.Builder
	for _, item := range resp.Output {
		for _, part := range item.Content {
			text.WriteString(part.Text)
		}
	}
	return codexLiveResult{
		bufferedID: resp.ID,
		outputText: text.String(),
		usage:      resp.Usage,
		events:     map[string]bool{},
	}
}

func parseCodexLiveSSE(t *testing.T, raw []byte) codexLiveResult {
	t.Helper()
	out := codexLiveResult{isSSE: true, events: map[string]bool{}}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	// 图片生成的 partial_image 单行 base64 可达数 MB,远超 bufio 默认 64KB token 上限;放大缓冲。
	scanner.Buffer(make([]byte, 0, 1<<20), 32<<20)
	var currentEvent string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "event:"):
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			out.events[currentEvent] = true
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var evt struct {
				Type  string `json:"type"`
				Delta string `json:"delta"`
				Item  struct {
					Type string `json:"type"`
				} `json:"item"`
				Response struct {
					Usage  codexLiveUsage `json:"usage"`
					Output []struct {
						Type    string `json:"type"`
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"output"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				t.Fatalf("decode codex live SSE data: %v line=%s body=%s", err, line, safeCodexLiveBody(raw, ""))
			}
			if evt.Type != "" {
				out.events[evt.Type] = true
			}
			if currentEvent == "response.output_text.delta" || evt.Type == "response.output_text.delta" {
				out.outputText += evt.Delta
			}
			if strings.Contains(data, "function_call") {
				out.sawFunctionCall = true
			}
			if strings.Contains(data, "reasoning") {
				out.sawReasoning = true
			}
			if strings.Contains(data, "image_generation_call") {
				out.sawImage = true
			}
			if evt.Type == "response.completed" || currentEvent == "response.completed" {
				out.usage = evt.Response.Usage
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan codex live SSE: %v", err)
	}
	return out
}

func parseCodexLiveChatResponse(t *testing.T, raw []byte) codexLiveChatParsed {
	t.Helper()
	if bytes.Contains(raw, []byte("data:")) {
		return parseCodexLiveChatSSE(t, raw)
	}
	var resp struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content   *string           `json:"content"`
				ToolCalls []json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode chat completion response: %v body=%s", err, safeCodexLiveBody(raw, ""))
	}
	var text strings.Builder
	sawToolCall := false
	for _, choice := range resp.Choices {
		if choice.Message.Content != nil {
			text.WriteString(*choice.Message.Content)
		}
		if len(choice.Message.ToolCalls) > 0 {
			sawToolCall = true
		}
	}
	return codexLiveChatParsed{
		object:      resp.Object,
		choiceCount: len(resp.Choices),
		outputText:  text.String(),
		sawToolCall: sawToolCall,
	}
}

func parseCodexLiveChatSSE(t *testing.T, raw []byte) codexLiveChatParsed {
	t.Helper()
	out := codexLiveChatParsed{isSSE: true}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Object  string `json:"object"`
			Choices []struct {
				Delta struct {
					Content   string            `json:"content"`
					ToolCalls []json.RawMessage `json:"tool_calls"`
				} `json:"delta"`
				Message struct {
					Content   *string           `json:"content"`
					ToolCalls []json.RawMessage `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("decode chat SSE data: %v line=%s body=%s", err, line, safeCodexLiveBody(raw, ""))
		}
		if out.object == "" {
			out.object = chunk.Object
		}
		for _, choice := range chunk.Choices {
			out.choiceCount++
			if choice.Delta.Content != "" {
				out.sawDeltaContent = true
				out.outputText += choice.Delta.Content
			}
			if len(choice.Delta.ToolCalls) > 0 || len(choice.Message.ToolCalls) > 0 {
				out.sawToolCall = true
			}
			if choice.Message.Content != nil {
				out.outputText += *choice.Message.Content
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan chat SSE: %v", err)
	}
	return out
}

func parseCodexLiveAnthropicResponse(t *testing.T, raw []byte) codexLiveAnthropicParsed {
	t.Helper()
	var resp struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode anthropic_messages response: %v body=%s", err, safeCodexLiveBody(raw, ""))
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return codexLiveAnthropicParsed{typ: resp.Type, outputText: text.String()}
}

func codexLiveBaseBody(model, instructions, input string, stream bool) map[string]any {
	return map[string]any{
		"model":        model,
		"instructions": instructions,
		// codex /responses 要求 input 是 list(纯字符串 → 400 "Input must be a list")。
		"input": []map[string]any{{
			"type":    "message",
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": input}},
		}},
		"stream": stream,
		"store":  false,
	}
}

func codexLiveChatTextBody(model, prompt string, stream bool, maxTokens int) map[string]any {
	return map[string]any{
		"model": model,
		"messages": []map[string]any{{
			"role":    "user",
			"content": prompt,
		}},
		"stream":     stream,
		"max_tokens": maxTokens,
	}
}

func codexLiveChatFunctionTool(name, description string) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": description,
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{
						"type":        "string",
						"description": "City name.",
					},
				},
				"required":             []string{"city"},
				"additionalProperties": false,
			},
		},
	}
}

func codexLiveToolBody(model string) map[string]any {
	body := codexLiveBaseBody(model, "Use the lookup_city tool and do not answer in plain text.", "Call lookup_city for Paris.", true)
	body["tools"] = []map[string]any{{
		"type":        "function",
		"name":        "lookup_city",
		"description": "Return a short city fact.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string"},
			},
			"required":             []string{"city"},
			"additionalProperties": false,
		},
	}}
	body["tool_choice"] = map[string]any{"type": "function", "name": "lookup_city"}
	return body
}

// codexLiveImageBody 用内建 image_generation 工具经 /responses 生成图片(OAuth 订阅额度出图,
// 验证 HUAKAI 中转能否透传 image_generation 工具并流回图片数据)。
func codexLiveImageBody(model string) map[string]any {
	body := codexLiveBaseBody(model, "Use the image_generation tool to fulfill any image request.", "Generate a tiny simple sticker of an orange cat.", true)
	body["tools"] = []map[string]any{{"type": "image_generation"}}
	return body
}

func codexLiveReasoningBody(model string) map[string]any {
	body := codexLiveBaseBody(model, "Think briefly, then answer with one sentence.", "What is 2+2?", true)
	body["reasoning"] = map[string]any{"effort": "low"}
	return body
}

func codexLiveMaxEffortBody(model string) map[string]any {
	// max = gpt-5.6 专属最高档;跟随矩阵 model,以便 seed 已注册该 model 时直接命中。
	body := codexLiveBaseBody(model, "Answer with exactly one word.", "Say OK.", true)
	body["reasoning"] = map[string]any{"effort": "max"}
	return body
}

func codexLiveVisionBody(model string) map[string]any {
	return map[string]any{
		"model":        model,
		"instructions": "Answer with one lowercase color word.",
		"stream":       true,
		"store":        false,
		"input": []map[string]any{{
			"type": "message",
			"role": "user",
			"content": []map[string]string{
				{"type": "input_text", "text": "What color is this square?"},
				{"type": "input_image", "image_url": codexLiveRedPNGDataURL()},
			},
		}},
	}
}

func codexLiveTransformBody(model string) map[string]any {
	return map[string]any{
		"model":        model,
		"instructions": "Reply with exactly: transform ok",
		"input": []map[string]any{{
			"type":    "message",
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": "confirm"}},
		}},
		"stream":            false,
		"store":             true,
		"temperature":       0.7,
		"top_p":             0.9,
		"max_output_tokens": 16,
	}
}

func codexLiveLongBufferedBody(model string) map[string]any {
	return codexLiveBaseBody(
		model,
		"List exactly 10 concise practical points. Use one sentence of explanation per point.",
		"Give 10 practical points for keeping a small software gateway reliable.",
		false,
	)
}

func codexLiveRedPNGDataURL() string {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func assertCodexLiveSSEEvents(t *testing.T, res codexLiveResult, want ...string) {
	t.Helper()
	if !res.isSSE {
		t.Fatalf("响应不是 SSE: body=%s", safeCodexLiveBody(res.body, ""))
	}
	for _, event := range want {
		if !res.events[event] {
			t.Fatalf("SSE 缺事件 %s; got=%v body=%s", event, res.events, safeCodexLiveBody(res.body, ""))
		}
	}
}

func assertCodexLiveContentType(t *testing.T, res codexLiveHTTPResult, wantSubstr string) {
	t.Helper()
	if !strings.Contains(strings.ToLower(res.contentType), strings.ToLower(wantSubstr)) {
		t.Fatalf("Content-Type=%q want 包含 %q; body=%s", res.contentType, wantSubstr, safeCodexLiveBody(res.body, ""))
	}
}

func assertCodexLivePG(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, seed *codexLiveSeed, logicalID string) int64 {
	t.Helper()
	var claimID int64
	var status string
	var actualCostRaw string
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := pgPool.QueryRow(ctx,
			`SELECT id, status, COALESCE(actual_cost, 0)::text
			   FROM billing_ledger_claims
			  WHERE tenant_id=$1 AND api_key_id=$2 AND logical_request_id=$3`,
			seed.tenantID, seed.apiKeyID, logicalID,
		).Scan(&claimID, &status, &actualCostRaw); err != nil {
			t.Fatalf("PG claim for logical_id=%s: %v", logicalID, err)
		}
		if status == "committed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("claim %d logical_id=%s status=%q want committed", claimID, logicalID, status)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if actualCost := parseCodexLiveNonNegativeFloat(t, "actual_cost", actualCostRaw); actualCost < 0 {
		t.Fatalf("claim %d actual_cost=%v want >=0", claimID, actualCost)
	}
	return claimID
}

func assertCodexLiveUsageRecord(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, claimID int64) {
	t.Helper()
	var count int
	var tokensInput, tokensOutput int
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*), COALESCE(sum(tokens_input), 0)::int, COALESCE(sum(tokens_output), 0)::int
		   FROM usage_records
		  WHERE claim_id=$1`,
		claimID,
	).Scan(&count, &tokensInput, &tokensOutput); err != nil {
		t.Fatalf("PG usage_records for claim %d: %v", claimID, err)
	}
	if count < 1 {
		t.Fatalf("claim %d usage_records count=%d want >=1", claimID, count)
	}
	if tokensInput+tokensOutput < 0 {
		t.Fatalf("claim %d token sum=%d want >=0", claimID, tokensInput+tokensOutput)
	}
}

func assertCodexLiveUsageRecordStream(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, claimID int64, want bool) {
	t.Helper()
	var count int
	var matched bool
	if err := pgPool.QueryRow(ctx,
		`SELECT count(*), COALESCE(bool_and(stream = $2), false)
		   FROM usage_records
		  WHERE claim_id=$1`,
		claimID, want,
	).Scan(&count, &matched); err != nil {
		t.Fatalf("PG usage_records stream for claim %d: %v", claimID, err)
	}
	if count < 1 {
		t.Fatalf("claim %d usage_records count=%d want >=1", claimID, count)
	}
	if !matched {
		t.Fatalf("claim %d usage_records.stream want 全部为 %v", claimID, want)
	}
}

func waitForCodexLiveInFlight(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, providerAccountID int64, want int32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last int32
	for {
		if err := pgPool.QueryRow(ctx,
			`SELECT in_flight_count FROM provider_accounts WHERE id=$1`, providerAccountID,
		).Scan(&last); err != nil {
			t.Fatalf("read in_flight_count: %v", err)
		}
		if last == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider_accounts.in_flight_count=%d want %d", last, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

var codexLiveBearerRedactionRE = regexp.MustCompile(`(?i)Bearer[[:space:]]+[A-Za-z0-9._~+/=-]+`)

func safeCodexLiveBody(raw []byte, token string) string {
	const maxBodyBytes = 4096
	body := string(raw)
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes] + "...<truncated>"
	}
	return redactCodexLiveSecrets(body, token)
}

func redactCodexLiveSecrets(s, token string) string {
	if token != "" {
		s = strings.ReplaceAll(s, token, "<redacted>")
	}
	return codexLiveBearerRedactionRE.ReplaceAllString(s, "Bearer <redacted>")
}

func parseCodexLiveNonNegativeFloat(t *testing.T, label, raw string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", label, raw, err)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		t.Fatalf("%s=%v want non-negative finite", label, v)
	}
	return v
}

func firstCodexLiveNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
