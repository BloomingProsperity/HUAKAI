package gatewayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/authcooldown"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

// TestSignalFromClassification_AuthRoutesToChallengeLane:401/坏 key 的令牌类分类必须映射为
// SignalAuthChallenge(独立 auth 车道),而非空信号(旧行为=对选号空操作=黑洞根因)。
// 判别:把 token_revoked/oauth_invalid_grant 分支改回 return "" → 断言红。
func TestSignalFromClassification_AuthRoutesToChallengeLane(t *testing.T) {
	for _, class := range []gateway.ErrorClass{gateway.ErrorClassTokenRevoked, gateway.ErrorClassOAuthInvalidGrant} {
		got := gateway.SignalFromClassification(http.StatusUnauthorized, gateway.Classification{Class: class})
		if got != channelhealth.SignalAuthChallenge {
			t.Fatalf("class=%s 应映射 SignalAuthChallenge,实得 %q", class, got)
		}
	}
	// 非 auth 类不受影响。
	if got := gateway.SignalFromClassification(http.StatusTooManyRequests, gateway.Classification{Class: gateway.ErrorClassRateLimited}); got != channelhealth.SignalRateLimit {
		t.Fatalf("rate_limited 应仍映射 SignalRateLimit,实得 %q", got)
	}
}

// TestAuthFailureClassFromClassification:iron-clad(关键词铁证/token_revoked/grok)vs ambiguous(通用 401)。
// 判别：把通用 401(R-009)错标 iron-clad 会让瞬时 401 的正常账号被推向硬禁，断言必须变红。
func TestAuthFailureClassFromClassification(t *testing.T) {
	cases := []struct {
		name string
		c    gateway.Classification
		want authcooldown.FailureClass
	}{
		{"invalid_grant_R001", gateway.Classification{Class: gateway.ErrorClassOAuthInvalidGrant, RuleID: "R-001"}, authcooldown.ClassIronClad},
		{"generic_401_R009", gateway.Classification{Class: gateway.ErrorClassOAuthInvalidGrant, RuleID: "R-009"}, authcooldown.ClassAmbiguous},
		{"token_revoked", gateway.Classification{Class: gateway.ErrorClassTokenRevoked, RuleID: "R-004"}, authcooldown.ClassIronClad},
		{"grok_token_revoked", gateway.Classification{Class: gateway.ErrorClassTokenRevoked, RuleID: "R-024"}, authcooldown.ClassIronClad},
		{"non_auth", gateway.Classification{Class: gateway.ErrorClassRateLimited, RuleID: "R-013"}, authcooldown.ClassAmbiguous},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gateway.AuthFailureClassFromClassification(c.c); got != c.want {
				t.Fatalf("AuthFailureClassFromClassification=%v, 期望 %v", got, c.want)
			}
		})
	}
}

// TestRecordChannelHealthSignalCarriesAuthClass:recordChannelHealthSignal 必须把 authClass 透传进
// Signal.AuthFailureClass,否则 gateway 路径算出的 iron-clad 分级到不了 auth 车道(坏号永远只当
// ambiguous、无法硬禁)。判别:把 Signal 的 AuthFailureClass 写死 0 → 断言红。
func TestRecordChannelHealthSignalCarriesAuthClass(t *testing.T) {
	health := &recordingChannelHealth{}
	key := channelhealth.ChannelKey{TenantID: 7, Vendor: "openai", ProviderAccountID: 101, AccountCredentialID: 9001, CredentialVersion: 1}
	recordChannelHealthSignal(context.Background(), ChatHandlerDeps{ChannelHealth: health}, key,
		channelhealth.SignalAuthChallenge, http.StatusUnauthorized, 0, "req-1", nil, authcooldown.ClassIronClad)
	if len(health.signals) != 1 {
		t.Fatalf("signals=%+v want 1", health.signals)
	}
	if health.signals[0].AuthFailureClass != authcooldown.ClassIronClad {
		t.Fatalf("Signal.AuthFailureClass=%v want iron-clad(authClass 未透传到 Signal)", health.signals[0].AuthFailureClass)
	}
}

// TestWriteJSONErrorProducesValidJSONForControlChars 守护 gateway error writer:
// 即便 code/message 带有控制字节(admin create 时 vendor="\x01" 会把 err.Error()
// 流进 message),也必须发出 RFC 合法的 JSON。旧的手写格式化用 fmt %q,会输出
// 像 \x01 这样的 Go 字面量转义 —— 合法 Go、非法 JSON —— 因此严格的 SDK/proxy/日志
// 解析器会失败。
//
// 变异:在 writeJSONError 中恢复 `fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, ...)`;
// json.Valid 会因 \x01 字节而变 false,且 message 的往返相等性
// 失败(字面量转义无法解码回原始字节)→ 本测试变红。
func TestWriteJSONErrorProducesValidJSONForControlChars(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	msg := "credentialstore: unknown vendor/auth_mode: vendor=\x01 auth_mode=\"oauth\"\nline\\two"
	writeJSONError(rec, http.StatusBadRequest, "admin_bad_request", msg)

	body := rec.Body.Bytes()
	if !json.Valid(body) {
		t.Fatalf("error body must be valid JSON even with control chars; got %q", body)
	}
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal error body: %v; body=%q", err, body)
	}
	if parsed.Error.Code != "admin_bad_request" {
		t.Fatalf("code must round-trip; got %q", parsed.Error.Code)
	}
	if parsed.Error.Message != msg {
		t.Fatalf("message must round-trip exactly; want %q got %q", msg, parsed.Error.Message)
	}
}

func TestSignalFromClassification_Suppresses401AuthHealthSignal(t *testing.T) {
	t.Parallel()

	classification, err := gateway.Classify(http.StatusUnauthorized, nil, []byte("invalid_grant"), "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	// 缺口① 修复后:401 auth 归 SignalAuthChallenge —— 走独立 auth 降级车道(补上临时排除坏号一步),
	// 但仍不写健康降级(applySignal 单独路由,不改 State/Score/窗口)。这里守护「不是健康降级类」:
	// 绝不能落到 rate_limit/5xx/error/forbidden/suspended,否则 401 又会污染健康分。
	got := gateway.SignalFromClassification(http.StatusUnauthorized, classification)
	if got != channelhealth.SignalAuthChallenge {
		t.Fatalf("signalFromClassification(401 auth)=%q want SignalAuthChallenge", got)
	}
	for _, degrading := range []channelhealth.SignalClass{
		channelhealth.SignalRateLimit, channelhealth.SignalUpstream5xx,
		channelhealth.SignalChannelError, channelhealth.SignalForbidden, channelhealth.SignalAccountSuspended,
	} {
		if got == degrading {
			t.Fatalf("401 auth 不得映射为健康降级类 %q", degrading)
		}
	}
}

func TestSignalFromClassification_StillEmits403Forbidden(t *testing.T) {
	t.Parallel()

	classification, err := gateway.Classify(http.StatusForbidden, nil, nil, "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := gateway.SignalFromClassification(http.StatusForbidden, classification); got != channelhealth.SignalForbidden {
		t.Fatalf("signalFromClassification(403)=%q want %q", got, channelhealth.SignalForbidden)
	}
}

func TestSignalFromDispatchError_SuppressesInfrastructureCooldown(t *testing.T) {
	t.Parallel()

	err := &transport.TransportError{
		Class:      transport.TransportErrorClassSidecarUnavailable,
		Mode:       transport.TransportModeMimicryClaudeCode,
		SocketPath: "/tmp/huakai-sidecar-down.sock",
		Err:        context.DeadlineExceeded,
	}
	classification, classifyErr := gateway.Classify(0, nil, []byte(err.Error()), "anthropic")
	if classifyErr != nil {
		t.Fatalf("Classify: %v", classifyErr)
	}
	if got := signalFromDispatchError(err, classification); got != "" {
		t.Fatalf("sidecar 基础设施故障不应产生 per-account 冷却信号, got %q", got)
	}

	proxyErr := errors.New("proxyconnect tcp: dial tcp 127.0.0.1:8080: connection refused")
	proxyClassification, classifyErr := gateway.Classify(0, nil, []byte(proxyErr.Error()), "openai")
	if classifyErr != nil {
		t.Fatalf("Classify proxy: %v", classifyErr)
	}
	if got := signalFromDispatchError(proxyErr, proxyClassification); got != "" {
		t.Fatalf("本地代理设施故障不应产生 per-account 冷却信号, got %q", got)
	}
}

func TestSignalFromDispatchError_StillEmitsUpstreamAuthCooldown(t *testing.T) {
	t.Parallel()

	upstreamErr := &gateway.UpstreamHTTPError{
		StatusCode: http.StatusUnauthorized,
		Body:       []byte(`{"error":{"message":"invalid_grant"}}`),
		Header:     make(http.Header),
	}
	_, classification, err := gateway.ClassifyAttemptHTTPError(upstreamErr.StatusCode, upstreamErr.Header, upstreamErr.Body, "openai")
	if err != nil {
		t.Fatalf("ClassifyAttemptHTTPError: %v", err)
	}
	if got := signalFromDispatchError(upstreamErr, classification); got != channelhealth.SignalAuthChallenge {
		t.Fatalf("真实上游 401 仍应产生 auth 冷却信号, got %q want %q", got, channelhealth.SignalAuthChallenge)
	}
}

func TestRecordModelCooldownFromUpstreamErrorScopes404AccountAndModel(t *testing.T) {
	t.Parallel()

	rec := &recordingModelRateLimiter{}
	recordModelCooldownFromUpstreamError(context.Background(), ChatHandlerDeps{ModelCooldowns: rec}, 7, 101, "upstream-gpt-4o", http.StatusNotFound, "req-404", nil, rate.ReasonModelLimitExceeded)
	if rec.calls != 1 {
		t.Fatalf("calls=%d, want 1", rec.calls)
	}
	if rec.input.TenantID != 7 || rec.input.ProviderAccountID != 101 || rec.input.ModelKey != "upstream-gpt-4o" {
		t.Fatalf("input scope=(tenant=%d account=%d model=%q), want tenant 7 account 101 model upstream-gpt-4o",
			rec.input.TenantID, rec.input.ProviderAccountID, rec.input.ModelKey)
	}
	if rec.input.StatusCode != http.StatusNotFound || rec.input.UpstreamRequestID != "req-404" {
		t.Fatalf("upstream evidence=(%d,%q), want 404 req-404", rec.input.StatusCode, rec.input.UpstreamRequestID)
	}

	recordModelCooldownFromUpstreamError(context.Background(), ChatHandlerDeps{ModelCooldowns: rec}, 7, 101, "upstream-gpt-4o", http.StatusBadRequest, "req-400", nil, "")
	if rec.calls != 1 {
		t.Fatalf("non-404 status recorded model cooldown; calls=%d want 1", rec.calls)
	}
}

func TestApplyUpstreamErrorCooldown_ModelScoped429DoesNotForceAccountCooldown(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	rec := &recordingModelRateLimiter{}
	health := &recordingChannelHealth{}
	classification, err := gateway.Classify(http.StatusTooManyRequests, http.Header{"Retry-After": []string{"3600"}}, []byte(`{"error":"rate limited"}`), "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	ex := modelCooldownTestExecution(rec, health, rate.NewUpstreamRateService(func() time.Time { return now }, time.Minute))

	outcome := ex.applyUpstreamErrorCooldown(&gateway.UpstreamHTTPError{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"3600"}},
		Body:       []byte(`{"error":"rate limited"}`),
	}, classification, true)
	if !outcome.ModelScoped {
		t.Fatal("纯 429 限速应下沉到模型格,返回 modelScoped=true")
	}
	if rec.calls != 1 {
		t.Fatalf("model cooldown calls=%d want 1", rec.calls)
	}
	if rec.input.TenantID != validIdentity().TenantID || rec.input.ProviderAccountID != 101 || rec.input.ModelKey != "upstream-gpt-4o" {
		t.Fatalf("model cooldown scope=%+v want tenant/current account/upstream model", rec.input)
	}
	if rec.input.Reason != rate.ReasonRateLimitRPM {
		t.Fatalf("reason=%q want %q", rec.input.Reason, rate.ReasonRateLimitRPM)
	}
	if !rec.input.ResetAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("reset_at=%s want %s", rec.input.ResetAt, now.Add(time.Hour))
	}
	if len(health.forceCooldowns) != 0 {
		t.Fatalf("ForceCooldown calls=%d want 0 for pure model 429", len(health.forceCooldowns))
	}
}

func TestApplyUpstreamErrorCooldown_Quota429StaysAccountSignal(t *testing.T) {
	t.Parallel()

	rec := &recordingModelRateLimiter{}
	health := &recordingChannelHealth{}
	body := []byte(`{"error":{"type":"insufficient_quota","message":"billing hard limit reached"}}`)
	classification, err := gateway.Classify(http.StatusTooManyRequests, nil, body, "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if classification.Class != gateway.ErrorClassCreditExhausted {
		t.Fatalf("class=%s want credit_exhausted", classification.Class)
	}
	ex := modelCooldownTestExecution(rec, health, rate.NewUpstreamRateService(func() time.Time {
		return time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	}, time.Minute))

	outcome := ex.applyUpstreamErrorCooldown(&gateway.UpstreamHTTPError{StatusCode: http.StatusTooManyRequests, Body: body}, classification, true)
	if outcome.ModelScoped {
		t.Fatal("配额耗尽 429 不得写模型格")
	}
	if rec.calls != 0 {
		t.Fatalf("model cooldown calls=%d want 0 for quota exhausted 429", rec.calls)
	}
	if len(health.forceCooldowns) != 0 {
		t.Fatalf("ForceCooldown calls=%d want 0;配额耗尽由账号信号禁用", len(health.forceCooldowns))
	}
	signal := gateway.SignalFromClassification(http.StatusTooManyRequests, classification)
	if signal != channelhealth.SignalAccountSuspended {
		t.Fatalf("signal=%s want account_suspended", signal)
	}
	recordChannelHealthSignal(context.Background(), ChatHandlerDeps{ChannelHealth: health}, ex.healthKey, signal, http.StatusTooManyRequests, 0, "req-quota", nil, gateway.AuthFailureClassFromClassification(classification))
	if len(health.signals) != 1 || health.signals[0].Class != channelhealth.SignalAccountSuspended {
		t.Fatalf("health signals=%+v want one account_suspended", health.signals)
	}
}

func TestQuota429AccountSuspendedSignalDisablesChannelHealthRecord(t *testing.T) {
	t.Parallel()

	body := []byte(`{"error":{"type":"insufficient_quota"}}`)
	classification, err := gateway.Classify(http.StatusTooManyRequests, nil, body, "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	signal := gateway.SignalFromClassification(http.StatusTooManyRequests, classification)
	if signal != channelhealth.SignalAccountSuspended {
		t.Fatalf("signal=%s want account_suspended", signal)
	}
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	store := channelhealth.NewMemoryStore()
	svc := channelhealth.NewService(store, channelhealth.DefaultPolicy(), fixedChannelHealthClock{now: now})
	key := channelhealth.ChannelKey{
		TenantID:            validIdentity().TenantID,
		Vendor:              "openai",
		ProviderAccountID:   101,
		AccountCredentialID: 9001,
		CredentialVersion:   1,
	}
	key.ChannelID = key.StableChannelID()

	rec, err := svc.ApplySignal(context.Background(), channelhealth.Signal{Key: key, Class: signal, StatusCode: http.StatusTooManyRequests})
	if err != nil {
		t.Fatalf("ApplySignal: %v", err)
	}
	if rec.State != channelhealth.StateDisabled || rec.CooldownUntil == nil {
		t.Fatalf("record=%+v want disabled with cooldown_until", rec)
	}
}

type fixedChannelHealthClock struct {
	now time.Time
}

func (c fixedChannelHealthClock) Now() time.Time { return c.now }

func TestApplyUpstreamErrorCooldown_Concurrent429WritesSameModelScope(t *testing.T) {
	t.Parallel()

	const goroutines = 24
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	rec := &recordingModelRateLimiter{}
	health := &recordingChannelHealth{}
	classification, err := gateway.Classify(http.StatusTooManyRequests, http.Header{"Retry-After": []string{"30"}}, []byte(`{"error":"rate limited"}`), "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	ex := modelCooldownTestExecution(rec, health, rate.NewUpstreamRateService(func() time.Time { return now }, time.Minute))
	upstreamErr := &gateway.UpstreamHTTPError{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"30"}},
		Body:       []byte(`{"error":"rate limited"}`),
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if !ex.applyUpstreamErrorCooldown(upstreamErr, classification, true).ModelScoped {
				t.Errorf("并发 429 应全部返回 modelScoped=true")
			}
		}()
	}
	wg.Wait()

	inputs := rec.inputsSnapshot()
	if len(inputs) != goroutines {
		t.Fatalf("model cooldown writes=%d want %d", len(inputs), goroutines)
	}
	for i, in := range inputs {
		if in.TenantID != validIdentity().TenantID || in.ProviderAccountID != 101 || in.ModelKey != "upstream-gpt-4o" {
			t.Fatalf("input[%d] scope=%+v want tenant/current account/upstream model", i, in)
		}
		if in.StatusCode != http.StatusTooManyRequests || in.Reason != rate.ReasonRateLimitRPM || !in.ResetAt.Equal(now.Add(30*time.Second)) {
			t.Fatalf("input[%d]=%+v want 429/rpm/reset+30s", i, in)
		}
	}
	if len(health.forceCooldowns) != 0 || len(health.signals) != 0 {
		t.Fatalf("health force/signals=%d/%d want 0/0 for pure model 429", len(health.forceCooldowns), len(health.signals))
	}
}

func TestApplyUpstreamErrorCooldown_ModelWriteFailFallsBackToAccountHealth(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	// 模型格落库失败(DB 抖动)时:纯 429 限速下沉写模型格报错,helper 必须返回 false,
	// 让调用方回落 recordChannelHealthSignal——否则该账号该模型零冷却、被立刻重选反复挨限速(片3a 回退)。
	rec := &recordingModelRateLimiter{err: context.DeadlineExceeded}
	health := &recordingChannelHealth{}
	header := http.Header{"Retry-After": []string{"60"}}
	classification, err := gateway.Classify(http.StatusTooManyRequests, header, []byte(`{"error":"rate limited"}`), "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	ex := modelCooldownTestExecution(rec, health, rate.NewUpstreamRateService(func() time.Time { return now }, time.Minute))

	outcome := ex.applyUpstreamErrorCooldown(&gateway.UpstreamHTTPError{
		StatusCode: http.StatusTooManyRequests,
		Header:     header,
		Body:       []byte(`{"error":"rate limited"}`),
	}, classification, true)
	if outcome.ModelScoped {
		t.Fatal("模型格写失败必须返回 false 以回落账号健康,不能吞掉冷却")
	}
	if rec.calls != 1 {
		t.Fatalf("model cooldown 尝试次数=%d want 1(应已试写但失败)", rec.calls)
	}
}

func TestApplyUpstreamErrorCooldown_PoolModeSuppressesEveryLocalWrite(t *testing.T) {
	t.Parallel()

	recorder := &recordingModelRateLimiter{}
	health := &recordingChannelHealth{}
	ex := modelCooldownTestExecution(recorder, health, suppressingLocalStateRateService{})
	classification, err := gateway.Classify(http.StatusTooManyRequests, nil, []byte(`{"error":"rate limited"}`), "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	outcome := ex.applyUpstreamErrorCooldown(&gateway.UpstreamHTTPError{
		StatusCode: http.StatusTooManyRequests,
		Body:       []byte(`{"error":"rate limited"}`),
	}, classification, true)
	if !outcome.ModelScoped {
		t.Fatal("pool_mode 抑制信号必须阻止调用方继续写账号健康状态")
	}
	if recorder.calls != 0 || len(health.forceCooldowns) != 0 || len(health.signals) != 0 {
		t.Fatalf("pool_mode 未匹配分支发生本地写入：model=%d force=%d signal=%d", recorder.calls, len(health.forceCooldowns), len(health.signals))
	}
}

type suppressingLocalStateRateService struct{}

func (suppressingLocalStateRateService) HandleUpstreamError(context.Context, int64, int, http.Header, []byte) (rate.Decision, error) {
	return rate.Decision{ShouldFailover: true, SuppressLocalState: true}, nil
}

func (suppressingLocalStateRateService) ClearCascade(context.Context, int64, string) error {
	return nil
}

func (suppressingLocalStateRateService) UpdateSessionWindow(context.Context, int64, http.Header) error {
	return nil
}

func modelCooldownTestExecution(rec *recordingModelRateLimiter, health *recordingChannelHealth, svc rate.Service) *chatExecution {
	ident := validIdentity()
	key := channelhealth.ChannelKey{
		TenantID:            ident.TenantID,
		Vendor:              "openai",
		ProviderAccountID:   101,
		AccountCredentialID: 9001,
		CredentialVersion:   1,
	}
	key.ChannelID = key.StableChannelID()
	return &chatExecution{
		ctx:               context.Background(),
		ident:             ident,
		requestID:         "req-429",
		acquiredAccountID: 101,
		upstreamModelID:   "upstream-gpt-4o",
		d: ChatHandlerDeps{
			ModelCooldowns: rec,
			ChannelHealth:  health,
			RateService:    svc,
		},
		healthKey:   key,
		healthKeyOK: true,
	}
}

type recordingModelRateLimiter struct {
	mu     sync.Mutex
	calls  int
	input  rate.ModelCooldownInput
	inputs []rate.ModelCooldownInput
	err    error // 非 nil 时模拟落库失败,验证写失败要回落账号健康
}

func (r *recordingModelRateLimiter) RecordModelRateLimit(_ context.Context, in rate.ModelCooldownInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.input = in
	r.inputs = append(r.inputs, in)
	return r.err
}

func (r *recordingModelRateLimiter) inputsSnapshot() []rate.ModelCooldownInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]rate.ModelCooldownInput(nil), r.inputs...)
}

func TestChatCompletionsPublicErrorsDoNotUseRawErrorStrings(t *testing.T) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`writeJSONError\([^\n]*\.Error\(\)`),
		regexp.MustCompile(`classifiedFailureFromDecision\([^\n]*\.Error\(\)`),
		regexp.MustCompile(`retryableLocalAttemptFailure\([^\n]*\.Error\(\)`),
		regexp.MustCompile(`terminalLocalAttemptFailure\([^\n]*\.Error\(\)`),
		regexp.MustCompile(`Header\(\)\.Set\("X-Huakai-[^"]+",[^\n]*\.Error\(\)\)`),
		regexp.MustCompile(`AbortReason[^\n]*\.Error\(\)`),
		regexp.MustCompile(`ClientMessage[^\n]*\.Error\(\)`),
	}
	files, err := filepath.Glob("chat_completions*.go")
	if err != nil {
		t.Fatalf("glob chat_completions*.go: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, pattern := range patterns {
			if loc := pattern.FindIndex(raw); loc != nil {
				t.Fatalf("%s contains public raw error pattern %q near %q", file, pattern.String(), raw[loc[0]:min(len(raw), loc[1]+80)])
			}
		}
	}
}
