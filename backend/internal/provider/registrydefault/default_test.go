package registrydefault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	providerantigravity "github.com/BloomingProsperity/HUAKAI/internal/provider/antigravity"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

func TestBuild_DefaultProtocolFamiliesRegistered(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	want := append([]string(nil), defaultProtocolFamilies...)
	got := r.RegisteredProtocolFamilies()
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Errorf("registered count=%d want %d (%v)", len(got), len(want), got)
	}
	for i, pf := range want {
		if i >= len(got) || got[i] != pf {
			t.Errorf("missing protocol %q; registered=%v", pf, got)
		}
	}
}

// TestSupportedProtocolFamiliesMatchOptInRegistration 守导出集合是注册路径的
// 单一配置面真相源。变异证明:新增 MustRegister 但漏补集合 → len/成员断言红;
// 漏掉默认 Claude session family → 正向成员断言红。
func TestSupportedProtocolFamiliesMatchOptInRegistration(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	for _, env := range []string{
		cursorSessionAdapterEnv, copilotSessionAdapterEnv,
		geminiCodeAssistAdapterEnv,
		geminiAdvancedSessionAdapterEnv, antigravitySessionAdapterEnv,
		kiroSessionAdapterEnv, windsurfSessionAdapterEnv,
	} {
		t.Setenv(env, "true")
	}
	r := Build()
	got := r.RegisteredProtocolFamilies()
	want := SupportedProtocolFamilies()
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("registered count=%d want supported count=%d; registered=%v supported=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("registered[%d]=%q want supported[%d]=%q; registered=%v supported=%v", i, got[i], i, want[i], got, want)
		}
	}
	if !IsSupportedProtocolFamily(ProtocolAnthropicClaudeSession) {
		t.Fatalf("%q 已有默认注册路径,必须进入支持集合", ProtocolAnthropicClaudeSession)
	}
}

// TestMigration0174ContainsSupportedProtocolFamilies 守 DB CHECK 与 adapter
// 注册路径同步。变异证明:从 0174 up 的 CHECK 删除任一支持族 → 本测试红。
func TestMigration0174ContainsSupportedProtocolFamilies(t *testing.T) {
	raw := readRegistryDefaultMigration(t, "0174_models_protocol_family_anthropic_claude_session.up.sql")
	for _, family := range SupportedProtocolFamilies() {
		if !strings.Contains(raw, "'"+family+"'") {
			t.Errorf("0174 up migration missing protocol_family %q", family)
		}
	}
	if !strings.Contains(raw, "'"+ProtocolAnthropicClaudeSession+"'") {
		t.Fatalf("0174 up migration must include serving family %q", ProtocolAnthropicClaudeSession)
	}
}

// TestMigration0174ProtocolFamilySetsAreExact 防止“只包含但多放了未知族”或 down
// 回退到更早缩水集合。up 必须精确等于支持集；down 必须精确等于 0172 up。
func TestMigration0174ProtocolFamilySetsAreExact(t *testing.T) {
	up := protocolFamiliesInMigration(t, "0174_models_protocol_family_anthropic_claude_session.up.sql")
	down := protocolFamiliesInMigration(t, "0174_models_protocol_family_anthropic_claude_session.down.sql")
	previous := protocolFamiliesInMigration(t, "0172_models_protocol_family_registered_adapters.up.sql")
	assertStringSlicesEqual(t, up, sortedCopy(SupportedProtocolFamilies()), "0174 up vs supported")
	assertStringSlicesEqual(t, down, previous, "0174 down vs 0172 up")
}

var migrationFamilyLiteral = regexp.MustCompile(`'([a-z][a-z0-9_]+)'`)

func protocolFamiliesInMigration(t *testing.T, name string) []string {
	t.Helper()
	raw := readRegistryDefaultMigration(t, name)
	if start := strings.LastIndex(raw, "ALTER TABLE models ADD CONSTRAINT models_protocol_family_check"); start >= 0 {
		raw = raw[start:]
	}
	seen := map[string]bool{}
	var out []string
	for _, match := range migrationFamilyLiteral.FindAllStringSubmatch(raw, -1) {
		family := match[1]
		if seen[family] {
			continue
		}
		seen[family] = true
		out = append(out, family)
	}
	sort.Strings(out)
	return out
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func assertStringSlicesEqual(t *testing.T, got, want []string, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d\ngot=%v\nwant=%v", label, len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s mismatch at %d: got=%q want=%q\ngot=%v\nwant=%v", label, i, got[i], want[i], got, want)
		}
	}
}

func readRegistryDefaultMigration(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "sql", "migrations", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(raw)
}

// TestEveryRegisteredPlatformHasTransportPolicy 族集对称守卫第 6 站:出站
// 注册表里每个 adapter 的 Platform() 都必须在 transport 的
// allowedModesByProvider 里至少允许 standard 模式——否则该族的请求在
// dispatcher 取 RoundTripper 时 ErrUnknownProvider,即便 marshal/三注册表
// 全对也整族不可用(kimi/qwen/glm/yi/baichuan/doubao/ernie/step/hunyuan/
// minimax/cohere/ollama 12 平台曾如此)。占位 session 族开 env 后一并校验。
// 变异:从 transport/policy.go 删任一平台条目 → 对应子断言红。
func TestEveryRegisteredPlatformHasTransportPolicy(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	for _, env := range []string{
		cursorSessionAdapterEnv, copilotSessionAdapterEnv,
		geminiCodeAssistAdapterEnv,
		geminiAdvancedSessionAdapterEnv, antigravitySessionAdapterEnv,
		kiroSessionAdapterEnv, windsurfSessionAdapterEnv,
	} {
		t.Setenv(env, "true")
	}
	r := Build()
	for _, pf := range r.RegisteredProtocolFamilies() {
		a, err := r.For(pf)
		if err != nil {
			t.Errorf("For(%q) err=%v", pf, err)
			continue
		}
		platform := a.Platform()
		if platform == "" {
			t.Errorf("family %q 的 adapter Platform() 为空", pf)
			continue
		}
		if err := transport.ValidateModeForProvider(transport.ProviderCode(platform), transport.TransportModeStandard); err != nil {
			t.Errorf("family %q platform %q 无 transport 策略(dispatcher 取 RoundTripper 必挂): %v", pf, platform, err)
		}
	}
}

func TestBuild_AdaptersAreReachable(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	for _, pf := range []string{
		ProtocolOpenAIChat,
		ProtocolAnthropicMessages,
		ProtocolAnthropicClaudeSession,
		ProtocolGeminiMessages,
		ProtocolOpenRouterChat,
		ProtocolBedrockInvoke,
		ProtocolGrokChat,
		ProtocolKimiChat,
		ProtocolOllamaNative,
		ProtocolDifyChat,
		ProtocolReplicateImage,
		ProtocolVertexGemini,
		ProtocolVertexAnthropic,
	} {
		a, err := r.For(pf)
		if err != nil {
			t.Errorf("For(%q) err=%v", pf, err)
			continue
		}
		if a == nil {
			t.Errorf("For(%q) returned nil adapter", pf)
		}
		if a.Platform() == "" {
			t.Errorf("For(%q) adapter.Platform() empty", pf)
		}
	}
}

func TestBuild_PlatformIDsCorrect(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	cases := map[string]string{
		ProtocolOpenAIChat:             "openai",
		ProtocolOpenAIResponses:        "openai",
		ProtocolOpenAICodex:            "openai_codex",
		ProtocolAnthropicMessages:      "anthropic",
		ProtocolAnthropicClaudeSession: "anthropic",
		ProtocolGeminiMessages:         "gemini",
		ProtocolOpenRouterChat:         "openrouter",
		ProtocolBedrockInvoke:          "bedrock",
		ProtocolGrokChat:               "grok",
		ProtocolDeepSeekChat:           "deepseek",
		ProtocolMistralChat:            "mistral",
		ProtocolGroqCloudChat:          "groqcloud",
		ProtocolTogetherChat:           "together",
		ProtocolPerplexityChat:         "perplexity",
		ProtocolFireworksChat:          "fireworks",
		ProtocolKimiChat:               "kimi",
		ProtocolQwenChat:               "qwen",
		ProtocolGLMChat:                "glm",
		ProtocolYiChat:                 "yi",
		ProtocolBaichuanChat:           "baichuan",
		ProtocolDoubaoChat:             "doubao",
		ProtocolErnieChat:              "ernie",
		ProtocolStepChat:               "step",
		ProtocolHunyuanChat:            "hunyuan",
		ProtocolMinimaxChat:            "minimax",
		ProtocolCohereChat:             "cohere",
		ProtocolOllamaChat:             "ollama",
		ProtocolOllamaNative:           "ollama",
		ProtocolDifyChat:               "dify",
		ProtocolReplicateImage:         "replicate",
		ProtocolVertexGemini:           "vertex",
		ProtocolVertexAnthropic:        "vertex",
	}
	for pf, wantPlatform := range cases {
		a, err := r.For(pf)
		if err != nil {
			t.Fatalf("For(%q) err=%v", pf, err)
		}
		if got := a.Platform(); got != wantPlatform {
			t.Errorf("%q Platform=%q want %q", pf, got, wantPlatform)
		}
	}
}

func TestBuild_OpenAICompatChatRegistrationsPreservePlatformAndEndpoint(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	cases := []struct {
		protocol string
		platform string
		endpoint string
	}{
		{ProtocolOpenRouterChat, "openrouter", "https://openrouter.ai/api/v1/chat/completions"},
		{ProtocolGrokChat, "grok", "https://api.x.ai/v1/chat/completions"},
		{ProtocolDeepSeekChat, "deepseek", "https://api.deepseek.com/v1/chat/completions"},
		{ProtocolMistralChat, "mistral", "https://api.mistral.ai/v1/chat/completions"},
		{ProtocolGroqCloudChat, "groqcloud", "https://api.groq.com/openai/v1/chat/completions"},
		{ProtocolTogetherChat, "together", "https://api.together.xyz/v1/chat/completions"},
		{ProtocolPerplexityChat, "perplexity", "https://api.perplexity.ai/chat/completions"},
		{ProtocolFireworksChat, "fireworks", "https://api.fireworks.ai/inference/v1/chat/completions"},
		{ProtocolKimiChat, "kimi", "https://api.kimi.com/coding/v1/chat/completions"},
		{ProtocolQwenChat, "qwen", "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"},
		{ProtocolGLMChat, "glm", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{ProtocolYiChat, "yi", "https://api.lingyiwanwu.com/v1/chat/completions"},
		{ProtocolBaichuanChat, "baichuan", "https://api.baichuan-ai.com/v1/chat/completions"},
		{ProtocolDoubaoChat, "doubao", "https://ark.cn-beijing.volces.com/api/v3/chat/completions"},
		{ProtocolErnieChat, "ernie", "https://qianfan.baidubce.com/v2/chat/completions"},
		{ProtocolStepChat, "step", "https://api.stepfun.com/v1/chat/completions"},
		{ProtocolHunyuanChat, "hunyuan", "https://api.hunyuan.cloud.tencent.com/v1/chat/completions"},
		{ProtocolMinimaxChat, "minimax", "https://api.minimaxi.com/v1/chat/completions"},
		{ProtocolCohereChat, "cohere", "https://api.cohere.ai/compatibility/v1/chat/completions"},
		{ProtocolOllamaChat, "ollama", "http://127.0.0.1:11434/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			a, err := r.For(tc.protocol)
			if err != nil {
				t.Fatalf("For(%q) err=%v", tc.protocol, err)
			}
			if got := a.Platform(); got != tc.platform {
				t.Fatalf("%q Platform=%q want %q", tc.protocol, got, tc.platform)
			}
			req, err := a.BuildRequest(context.Background(), provider.BuildInput{
				InboundBody: []byte(`{}`),
				Credential: provider.Credential{
					Type:  provider.CredentialTypeAPIKey,
					Value: "test-key",
				},
			})
			if err != nil {
				t.Fatalf("%q BuildRequest err=%v", tc.protocol, err)
			}
			if got := req.URL.String(); got != tc.endpoint {
				t.Fatalf("%q endpoint=%q want %q", tc.protocol, got, tc.endpoint)
			}
		})
	}
}

func TestKimiRuntimeAdapterRegistered(t *testing.T) {
	// 变异：删除 ProtocolKimiChat 注册、改其 endpoint/platform，或恢复成 API key
	// 不识别 base_url 的旧门，均会让对应完整 URL 断言转红。
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	a, err := r.For(ProtocolKimiChat)
	if err != nil {
		t.Fatalf("For(%q) err=%v", ProtocolKimiChat, err)
	}
	if got := a.Platform(); got != "kimi" {
		t.Fatalf("Kimi Platform=%q want kimi", got)
	}
	tests := []struct {
		name              string
		credential        provider.Credential
		wantURL           string
		wantAuthorization string
	}{
		{
			name:              "API key 未配置 base_url 保持编程订阅默认地址",
			credential:        provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "kimi-key"},
			wantURL:           "https://api.kimi.com/coding/v1/chat/completions",
			wantAuthorization: "Bearer kimi-key",
		},
		{
			name: "API key 使用 Moonshot 自定义地址",
			credential: provider.Credential{
				Type:  provider.CredentialTypeAPIKey,
				Value: "moonshot-key",
				Extra: map[string]string{"base_url": "https://api.moonshot.cn/v1"},
			},
			wantURL:           "https://api.moonshot.cn/v1/chat/completions",
			wantAuthorization: "Bearer moonshot-key",
		},
		{
			name:              "透传凭据未配置 base_url 保持原行为",
			credential:        provider.Credential{Type: provider.CredentialTypeUpstreamPassthrough, Value: "Bearer kimi-access"},
			wantURL:           "https://api.kimi.com/coding/v1/chat/completions",
			wantAuthorization: "Bearer kimi-access",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := a.BuildRequest(context.Background(), provider.BuildInput{
				InboundBody: []byte(`{"model":"kimi-k2","messages":[]}`),
				Credential:  tc.credential,
			})
			if err != nil {
				t.Fatalf("Kimi BuildRequest: %v", err)
			}
			if got := req.URL.String(); got != tc.wantURL {
				t.Fatalf("Kimi endpoint=%q want %q", got, tc.wantURL)
			}
			if got := req.Header.Get("Authorization"); got != tc.wantAuthorization {
				t.Fatalf("Kimi Authorization=%q want %q", got, tc.wantAuthorization)
			}
		})
	}
}

// TestVertexRuntimeAdapterRegistered 是 vertex 出站注册的变异守卫:删任一
// MustRegister(vertex_*) 或改其 Mode/平台 → 本测试红。判别性:断言两族产出的
// 完整 URL（含 publisher google vs anthropic、action generateContent vs
// rawPredict）+ 平台 + Authorization 头无双 Bearer。
func TestVertexRuntimeAdapterRegistered(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()

	cases := []struct {
		protocol string
		model    string
		body     string
		wantURL  string
	}{
		{
			ProtocolVertexGemini, "gemini-2.5-pro", `{"contents":[]}`,
			"https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent",
		},
		{
			ProtocolVertexAnthropic, "claude-opus-4-1", `{"model":"claude-opus-4-1","messages":[]}`,
			"https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/publishers/anthropic/models/claude-opus-4-1:rawPredict",
		},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			a, err := r.For(tc.protocol)
			if err != nil {
				t.Fatalf("For(%q) err=%v", tc.protocol, err)
			}
			if got := a.Platform(); got != "vertex" {
				t.Fatalf("%q Platform=%q want vertex", tc.protocol, got)
			}
			req, err := a.BuildRequest(context.Background(), provider.BuildInput{
				UpstreamModelID: tc.model,
				InboundBody:     []byte(tc.body),
				Credential: provider.Credential{
					Type:  provider.CredentialTypeUpstreamPassthrough,
					Value: "Bearer vertex-access",
					Extra: map[string]string{"project_id": "p", "auth_header": "Authorization"},
				},
			})
			if err != nil {
				t.Fatalf("%q BuildRequest: %v", tc.protocol, err)
			}
			if got := req.URL.String(); got != tc.wantURL {
				t.Fatalf("%q URL=%q\nwant %q", tc.protocol, got, tc.wantURL)
			}
			if got := req.Header.Get("Authorization"); got != "Bearer vertex-access" {
				t.Fatalf("%q Authorization=%q want Bearer vertex-access (无双 Bearer)", tc.protocol, got)
			}
			if got := req.Header.Get("X-Goog-User-Project"); got != "p" {
				t.Fatalf("%q X-Goog-User-Project=%q want p", tc.protocol, got)
			}
		})
	}
}

func TestBuild_OpenAIResponsesEndpointIsResponsesAPI(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	a, err := r.For(ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("For(%q) err=%v", ProtocolOpenAIResponses, err)
	}
	passthrough, ok := a.(*openai.PassthroughAdapter)
	if !ok {
		t.Fatalf("adapter type=%T want *openai.PassthroughAdapter", a)
	}
	if passthrough.Endpoint != "https://api.openai.com/v1/responses" {
		t.Fatalf("Responses endpoint=%q want https://api.openai.com/v1/responses", passthrough.Endpoint)
	}
}

func TestBuild_AnthropicClaudeSessionDefaultServing(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()

	a, err := r.For(ProtocolAnthropicClaudeSession)
	if err != nil {
		t.Fatalf("For(%q): %v", ProtocolAnthropicClaudeSession, err)
	}
	if _, ok := a.(*anthropic.OAuthSessionAdapter); !ok {
		t.Fatalf("For(%q) type=%T want *anthropic.OAuthSessionAdapter", ProtocolAnthropicClaudeSession, a)
	}
	if got := a.Platform(); got != "anthropic" {
		t.Fatalf("For(%q) Platform=%q want anthropic", ProtocolAnthropicClaudeSession, got)
	}
	wantTypes := []provider.CredentialType{
		provider.CredentialTypeOAuthAccessToken,
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
	gotTypes := a.AcceptableCredentialTypes()
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("For(%q) credential types=%v want %v", ProtocolAnthropicClaudeSession, gotTypes, wantTypes)
	}
	for i := range wantTypes {
		if gotTypes[i] != wantTypes[i] {
			t.Fatalf("For(%q) credential types=%v want %v", ProtocolAnthropicClaudeSession, gotTypes, wantTypes)
		}
	}
}

func TestBuild_PlaceholderSessionAdaptersDefaultOff(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	for _, pf := range placeholderSessionProtocolFamilies() {
		_, err := r.For(pf)
		if !errors.Is(err, provider.ErrAdapterNotRegistered) {
			t.Errorf("For(%q) err=%v want ErrAdapterNotRegistered", pf, err)
		}
	}
}

func TestBuild_LegacyPlaceholderSessionFlagDoesNotEnableAllFamilies(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "true")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	for _, pf := range placeholderSessionProtocolFamilies() {
		_, err := r.For(pf)
		if !errors.Is(err, provider.ErrAdapterNotRegistered) {
			t.Fatalf("legacy aggregate flag registered %q; each placeholder session family must be gated independently", pf)
		}
	}
}

func TestBuild_PlaceholderSessionAdaptersOptIn(t *testing.T) {
	cases := []struct {
		protocol     string
		env          string
		wantPlatform string
	}{
		{ProtocolCursorSession, cursorSessionAdapterEnv, "cursor"},
		{ProtocolCopilotSession, copilotSessionAdapterEnv, "copilot"},
		{ProtocolGeminiAdvancedSession, geminiAdvancedSessionAdapterEnv, "gemini_advanced"},
		{ProtocolAntigravitySession, antigravitySessionAdapterEnv, "antigravity"},
		{ProtocolKiroSession, kiroSessionAdapterEnv, "kiro"},
		{ProtocolWindsurfSession, windsurfSessionAdapterEnv, "windsurf"},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			t.Setenv(placeholderSessionAdaptersEnv, "true")
			clearPlaceholderSessionAdapterEnvs(t)
			t.Setenv(tc.env, "true")
			r := Build()
			a, err := r.For(tc.protocol)
			if err != nil {
				t.Fatalf("For(%q) err=%v", tc.protocol, err)
			}
			if got := a.Platform(); got != tc.wantPlatform {
				t.Errorf("%q Platform=%q want %q", tc.protocol, got, tc.wantPlatform)
			}
			for _, sibling := range placeholderSessionProtocolFamilies() {
				if sibling == tc.protocol {
					continue
				}
				_, err := r.For(sibling)
				if !errors.Is(err, provider.ErrAdapterNotRegistered) {
					t.Fatalf("enabling %s also registered sibling %s", tc.env, sibling)
				}
			}
		})
	}
}

// TestBuild_GeminiCodeAssistEnvGated 守卫 gemini_code_assist 出站注册的
// env-gate 姿态:默认 off(不注册,避免把真实 OAuth session credential 默认发
// 到 Google 内部 cloudcode-pa 端点);开 env 后注册且 Platform()=="gemini_code_assist"。
// 变异:把 registrydefault 的注册改成无条件 → 默认 off 子断言红;删注册 →
// 开 env 子断言红。
func TestBuild_GeminiCodeAssistEnvGated(t *testing.T) {
	t.Run("default off not registered", func(t *testing.T) {
		t.Setenv(placeholderSessionAdaptersEnv, "")
		clearPlaceholderSessionAdapterEnvs(t)
		r := Build()
		if _, err := r.For(ProtocolGeminiCodeAssist); !errors.Is(err, provider.ErrAdapterNotRegistered) {
			t.Fatalf("gemini_code_assist 默认应 unregistered(高危 OAuth/内部端点),got err=%v", err)
		}
	})
	t.Run("env on registers with vertex-distinct platform", func(t *testing.T) {
		t.Setenv(placeholderSessionAdaptersEnv, "")
		clearPlaceholderSessionAdapterEnvs(t)
		t.Setenv(geminiCodeAssistAdapterEnv, "true")
		r := Build()
		a, err := r.For(ProtocolGeminiCodeAssist)
		if err != nil {
			t.Fatalf("env on 后 For(gemini_code_assist) err=%v", err)
		}
		if got := a.Platform(); got != "gemini_code_assist" {
			t.Errorf("Platform=%q want gemini_code_assist", got)
		}
		// 平台必须有 transport 策略,否则 dispatcher 取 RoundTripper 整族挂。
		if err := transport.ValidateModeForProvider(transport.ProviderCode(a.Platform()), transport.TransportModeStandard); err != nil {
			t.Errorf("gemini_code_assist 平台无 transport 策略: %v", err)
		}
	})
}

// TestBuild_AntigravityEnvGateDefaultsOffAndBuildsCloudCodeAdapter 守住 rollout
// 姿态：env 关闭时注册表完全不含该族，不进入转发热路径；env 开启后构造的
// 必须是真实 Cloud Code adapter，而不是旧占位实现。无条件注册或删 env 分支
// 分别会使两个子测试变红。
func TestBuild_AntigravityEnvGateDefaultsOffAndBuildsCloudCodeAdapter(t *testing.T) {
	t.Run("默认关闭不进入热路径", func(t *testing.T) {
		clearPlaceholderSessionAdapterEnvs(t)
		r := Build()
		if _, err := r.For(ProtocolAntigravitySession); !errors.Is(err, provider.ErrAdapterNotRegistered) {
			t.Fatalf("Antigravity 默认应未注册，err=%v", err)
		}
		for _, family := range r.RegisteredProtocolFamilies() {
			if family == ProtocolAntigravitySession {
				t.Fatalf("默认注册集合不应含 %q", ProtocolAntigravitySession)
			}
		}
	})

	t.Run("显式开启构造正确 adapter", func(t *testing.T) {
		clearPlaceholderSessionAdapterEnvs(t)
		t.Setenv(antigravitySessionAdapterEnv, "true")
		r := Build()
		raw, err := r.For(ProtocolAntigravitySession)
		if err != nil {
			t.Fatalf("env 开启后取 adapter 失败：%v", err)
		}
		adapter, ok := raw.(*providerantigravity.AntigravitySessionAdapter)
		if !ok {
			t.Fatalf("adapter type=%T，期望 *antigravity.AntigravitySessionAdapter", raw)
		}
		req, err := adapter.BuildRequest(context.Background(), provider.BuildInput{
			UpstreamModelID: "gemini-3-flash",
			InboundBody:     []byte(`{"contents":[]}`),
			Credential: provider.Credential{
				Type: provider.CredentialTypeSessionToken, Value: "access-token",
				Extra: map[string]string{"project_id": "project-id"},
			},
		})
		if err != nil {
			t.Fatalf("已构造 adapter 无法 BuildRequest：%v", err)
		}
		if got := req.URL.String(); got != "https://cloudcode-pa.googleapis.com/v1internal:generateContent" {
			t.Fatalf("env 开启后的出站 URL=%q", got)
		}
		if got := req.Header.Get("User-Agent"); got != "antigravity/hub/2.2.1 darwin/arm64" {
			t.Fatalf("env 开启后的 User-Agent=%q", got)
		}
	})
}

func TestBuild_UnregisteredReturnsErrAdapterNotRegistered(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	for _, pf := range []string{
		"chatgpt_session",
		"anthropic_oauth",
		"copilot_oauth",
		"unknown",
	} {
		_, err := r.For(pf)
		if err == nil {
			t.Errorf("For(%q) expected error", pf)
			continue
		}
		if errStr := err.Error(); errStr == "" {
			t.Errorf("For(%q) error text empty", pf)
		}
	}
}

func TestBuild_ConsistentWithProviderInterface(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	var _ provider.Adapter
	for _, pf := range r.RegisteredProtocolFamilies() {
		a, err := r.For(pf)
		if err != nil {
			t.Fatal(err)
		}
		_ = a.Platform()
		_ = a.AcceptableCredentialTypes()
	}
}

func placeholderSessionProtocolFamilies() []string {
	return []string{
		ProtocolCursorSession,
		ProtocolCopilotSession,
		ProtocolGeminiAdvancedSession,
		ProtocolAntigravitySession,
		ProtocolKiroSession,
		ProtocolWindsurfSession,
	}
}

func clearPlaceholderSessionAdapterEnvs(t *testing.T) {
	t.Helper()
	for _, env := range placeholderSessionAdapterEnvNames() {
		t.Setenv(env, "")
	}
}

func placeholderSessionAdapterEnvNames() []string {
	return []string{
		cursorSessionAdapterEnv,
		copilotSessionAdapterEnv,
		geminiCodeAssistAdapterEnv,
		geminiAdvancedSessionAdapterEnv,
		antigravitySessionAdapterEnv,
		kiroSessionAdapterEnv,
		windsurfSessionAdapterEnv,
	}
}
