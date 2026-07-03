package registrydefault

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

func TestBuild_DefaultProtocolFamiliesRegistered(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()
	want := []string{
		ProtocolOpenAIChat,
		ProtocolOpenAIResponses,
		ProtocolOpenAICodex,
		ProtocolAnthropicMessages,
		ProtocolGeminiMessages,
		ProtocolOpenRouterChat,
		ProtocolBedrockInvoke,
		ProtocolGrokChat,
		ProtocolDeepSeekChat,
		ProtocolMistralChat,
		ProtocolGroqCloudChat,
		ProtocolTogetherChat,
		ProtocolPerplexityChat,
		ProtocolFireworksChat,
		ProtocolKimiChat,
		ProtocolQwenChat,
		ProtocolGLMChat,
		ProtocolYiChat,
		ProtocolBaichuanChat,
		ProtocolDoubaoChat,
		ProtocolErnieChat,
		ProtocolStepChat,
		ProtocolHunyuanChat,
		ProtocolMinimaxChat,
		ProtocolCohereChat,
		ProtocolOllamaChat,
		ProtocolOllamaNative,
		ProtocolDifyChat,
		ProtocolReplicateImage,
		ProtocolVertexGemini,
		ProtocolVertexAnthropic,
	}
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
		ProtocolOpenAIChat:        "openai",
		ProtocolOpenAIResponses:   "openai",
		ProtocolOpenAICodex:       "openai_codex",
		ProtocolAnthropicMessages: "anthropic",
		ProtocolGeminiMessages:    "gemini",
		ProtocolOpenRouterChat:    "openrouter",
		ProtocolBedrockInvoke:     "bedrock",
		ProtocolGrokChat:          "grok",
		ProtocolDeepSeekChat:      "deepseek",
		ProtocolMistralChat:       "mistral",
		ProtocolGroqCloudChat:     "groqcloud",
		ProtocolTogetherChat:      "together",
		ProtocolPerplexityChat:    "perplexity",
		ProtocolFireworksChat:     "fireworks",
		ProtocolKimiChat:          "kimi",
		ProtocolQwenChat:          "qwen",
		ProtocolGLMChat:           "glm",
		ProtocolYiChat:            "yi",
		ProtocolBaichuanChat:      "baichuan",
		ProtocolDoubaoChat:        "doubao",
		ProtocolErnieChat:         "ernie",
		ProtocolStepChat:          "step",
		ProtocolHunyuanChat:       "hunyuan",
		ProtocolMinimaxChat:       "minimax",
		ProtocolCohereChat:        "cohere",
		ProtocolOllamaChat:        "ollama",
		ProtocolOllamaNative:      "ollama",
		ProtocolDifyChat:          "dify",
		ProtocolReplicateImage:    "replicate",
		ProtocolVertexGemini:      "vertex",
		ProtocolVertexAnthropic:   "vertex",
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
	// 变异：删除 ProtocolKimiChat 注册或改其 endpoint/platform；
	// 在任何 gateway 路由能静默指向 Kimi 之前，本测试必须变红。
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
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		InboundBody: []byte(`{"model":"kimi-k2","messages":[]}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer kimi-access",
		},
	})
	if err != nil {
		t.Fatalf("Kimi BuildRequest: %v", err)
	}
	if got := req.URL.String(); got != "https://api.kimi.com/coding/v1/chat/completions" {
		t.Fatalf("Kimi endpoint=%q", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer kimi-access" {
		t.Fatalf("Kimi Authorization=%q", got)
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

func TestBuild_AnthropicClaudeSessionDefaultFailClosed(t *testing.T) {
	t.Setenv(placeholderSessionAdaptersEnv, "")
	clearPlaceholderSessionAdapterEnvs(t)
	r := Build()

	_, err := r.For(ProtocolAnthropicClaudeSession)
	if !errors.Is(err, provider.ErrAdapterNotRegistered) {
		t.Fatalf("For(%q) err=%v want ErrAdapterNotRegistered", ProtocolAnthropicClaudeSession, err)
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
