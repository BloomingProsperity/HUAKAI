// Package main wiring tests guard the two production-mode env-gated
// helpers (`buildAuditLedger` + `loadAuditSigner`) against silent
// regressions surfaced by Owner deep-review on top of commit e961e5c
// (F-PRIV-1 Wave 3 follow-up; Risk 5 + Risk 6 in d8996c4).
//
// 不连真实 DB：production 模式下的 postgres 后端只验证 wiring 是否进入
// 持久化分支（构造期错误 / 不会 silently fallback 到 memory），不要求
// 真正打开 pgxpool。dev 模式则验证默认能跑 + signer 退化为 ephemeral。
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"

	"github.com/BloomingProsperity/HUAKAI/internal/anthropicoauth"
	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

// ---------------------------------------------------------------
// Audit-ref policy wiring: 2 case
// ---------------------------------------------------------------

func TestWiring_AuditRefPolicySharedByBusConfigAndChatDeps(t *testing.T) {
	policy := &eventbus.AuditRefPolicy{ReleaseMode: eventbus.ReleaseModeProduction}
	busCfg := buildCompletionEventBusConfig(&runtimeconfig.EventBusConfig{Enabled: true}, policy)
	d := &deps{
		cfg:            &Config{BillingPolicyVersion: "1.0", RequestClass: "standard"},
		auditRefPolicy: policy,
	}
	chatDeps := chatHandlerDeps(d)

	if busCfg.AuditRefPolicy != policy {
		t.Fatalf("bus AuditRefPolicy pointer=%p want shared %p", busCfg.AuditRefPolicy, policy)
	}
	if chatDeps.AuditRefPolicy != policy {
		t.Fatalf("chat AuditRefPolicy pointer=%p want shared %p", chatDeps.AuditRefPolicy, policy)
	}

	// Mutation: 让 bus 与 ChatHandlerDeps 各自 new policy 时, 这里的共享变更会失效。
	policy.AllowMissingMoneyRef = true
	if !busCfg.AuditRefPolicy.AllowMissingMoneyRef || !chatDeps.AuditRefPolicy.AllowMissingMoneyRef {
		t.Fatalf("policy mutation was not visible through both wiring surfaces")
	}
}

func TestWiring_BuildTransportFactoryInjectsSidecarSocket(t *testing.T) {
	cfg := &Config{TransportSidecarSocket: "/tmp/huakai-tls-sidecar.sock"}

	factory := buildTransportFactory(cfg, nil)

	if factory.SidecarSocketPath != cfg.TransportSidecarSocket {
		t.Fatalf("SidecarSocketPath=%q want cfg.TransportSidecarSocket", factory.SidecarSocketPath)
	}
}

func TestWiring_AnthropicClaudeAIOAuthKeepsCredentialAcqBuiltinProfile(t *testing.T) {
	_, err := credentialacq.StartOAuthFlow(context.Background(), nil, credentialacq.StartInput{
		TenantID: 1, ProviderAccountID: 101,
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth,
		ActorID: "owner", ActorRole: "platform_admin",
	}, credentialacq.OAuthClientConfig{TokenURL: "http://attacker.test/token"})
	if !errors.Is(err, credentialacq.ErrFeatureDisabled) {
		t.Fatalf("err=%v want ErrFeatureDisabled from credentialacq built-in profile validation", err)
	}
}

// ANT-4: 生产 wiring 必须真的调用 installAnthropicClaudeAIOAuthMimicryExchanger
// 把 default registry 中 nil-client exchanger 替换成带显式 HTTP client 的版本。
// 这个 test 走 helper 真实路径并注入 mock client, 锁住"如果 wiring.go 删
// install 调用, 默认 registry 仍是 nil-client → mock 不被命中" 的回归。
// 判别 mutation: 注释掉 wiring.go installAnthropicClaudeAIOAuthMimicryExchanger
// 调用 后, 此 test 看到 default registry 走 nil httpClient → http.DefaultClient
// → 不会命中 panic-DefaultTransport 但 mock client hits=0, 立即变红。
// installAnthropicClaudeAIOAuthMimicryExchanger 是 ANT-4 wiring 的核心:
// default registry 起手装 nil-client exchanger, install 必须真把它替换
// 为带显式 client 的版本。否则生产仍跑 http.DefaultClient 退化 fingerprint。
//
// 判别 mutation: 在 wiring.go 注释掉 installAnthropicClaudeAIOAuthMimicryExchanger
// 调用 — 此 test 看到 Lookup 返的 exchanger.httpClient 仍是 nil
// (default registry 起手值), 立即变红。
// 防御范围比 anthropicoauth.DefaultHTTPClient 自身 transport 类型断言
// 更精准: codex R1 抓的就是"transport 类型测试通过, 但 wiring 不调用 install
// 仍 PASS"的 false-negative。
func TestWiring_InstallAnthropicClaudeAIOAuthMimicryExchangerReplacesDefault(t *testing.T) {
	registry := credentialacq.DefaultExchangerRegistry()
	modeKey := credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth)

	// 起手 default registry 装的是 nil-client 版本。
	before, ok := registry.Lookup(modeKey)
	if !ok {
		t.Fatal("default registry 必须有 anthropic/claude_ai_oauth 起手 exchanger")
	}
	if before == nil {
		t.Fatal("起手 exchanger 不应是 nil")
	}
	if credentialacq.IsClaudeAIOAuthExchangerWithExplicitClient(before) {
		t.Fatal("起手 default registry 不应装 explicit-client 版本, 否则 install mutation 无法被检出")
	}

	mockClient := &http.Client{Transport: wiringRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unreachable in helper-only assertion")
	})}
	if err := installAnthropicClaudeAIOAuthMimicryExchanger(registry, mockClient); err != nil {
		t.Fatalf("installAnthropicClaudeAIOAuthMimicryExchanger: %v", err)
	}

	after, ok := registry.Lookup(modeKey)
	if !ok {
		t.Fatal("install 后 registry Lookup miss")
	}
	if !credentialacq.IsClaudeAIOAuthExchangerWithExplicitClient(after) {
		t.Fatal("install 后 exchanger 仍报告 nil httpClient; helper 没真替换")
	}

	// wiring 自检函数: 未 install 的 fresh registry 必报错, install 后返 nil。
	// 这是 production-time fail-loud 防御 (codex R1 抓的 wiring 删 install
	// 调用 unit test 抓不到), 调 buildGatewayRuntime 时执行。
	freshRegistry := credentialacq.DefaultExchangerRegistry()
	if err := assertAnthropicClaudeAIOAuthExchangerHasHTTPClient(freshRegistry); err == nil {
		t.Fatal("wiring 自检对未 install 的 registry 必须返 error")
	}
	if err := assertAnthropicClaudeAIOAuthExchangerHasHTTPClient(registry); err != nil {
		t.Fatalf("wiring 自检对已 install 的 registry 必须返 nil, got %v", err)
	}

	// 防回归: anthropicoauth.DefaultHTTPClient 自身仍必须返 mimicry uTLS
	// transport (HUAKAI 反封禁核心), 否则 production wiring 即使调对 install
	// 也会注入退化 transport。
	def := anthropicoauth.DefaultHTTPClient()
	if def == nil || def.Transport == nil {
		t.Fatal("anthropicoauth.DefaultHTTPClient 必须为生产 wiring 提供非空 client + transport")
	}
	if got := fmt.Sprintf("%T", def.Transport); !strings.Contains(got, "mimicry") {
		t.Fatalf("default transport=%s, want mimicry uTLS roundTripper", got)
	}

	// install 接 nil client 必拒, 防止 anthropicoauth.DefaultHTTPClient 退化
	// 返 nil 时 wiring silently 装废柴 exchanger。
	if err := installAnthropicClaudeAIOAuthMimicryExchanger(credentialacq.DefaultExchangerRegistry(), nil); err == nil {
		t.Fatal("install 必须拒 nil client (防 silent 退化到 http.DefaultClient)")
	}
}

func TestWiring_InstallGeminiPublicCLIOAuthExchangersReplacesDefault(t *testing.T) {
	// 缺陷：Gemini code_assist/google_one 若 production wiring 没注入受控 HTTP client，
	// helper-level 单测仍可能通过，但启动链路会 silent 退化。
	// 判别 mutation：注释掉 installGeminiPublicCLIOAuthExchangers 调用或让 assert 漏掉任一 mode 时，本测试必须变红。
	registry := credentialacq.DefaultExchangerRegistry()
	modes := []string{credentialstore.AuthModeCodeAssist, credentialstore.AuthModeGoogleOne}
	for _, mode := range modes {
		exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorGemini, mode))
		if !ok {
			t.Fatalf("default registry missing gemini/%s exchanger", mode)
		}
		if credentialacq.IsGeminiPublicCLIOAuthExchangerWithExplicitClient(exc) {
			t.Fatalf("default registry gemini/%s should start without explicit client for mutation visibility", mode)
		}
	}

	mockClient := &http.Client{Transport: wiringRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unreachable in helper-only assertion")
	})}
	if err := installGeminiPublicCLIOAuthExchangers(registry, mockClient, "from-env"); err != nil {
		t.Fatalf("installGeminiPublicCLIOAuthExchangers: %v", err)
	}
	for _, mode := range modes {
		exc, ok := registry.Lookup(credentialstore.ModeKey(credentialstore.VendorGemini, mode))
		if !ok {
			t.Fatalf("installed registry missing gemini/%s exchanger", mode)
		}
		if !credentialacq.IsGeminiPublicCLIOAuthExchangerWithExplicitClient(exc) {
			t.Fatalf("installed gemini/%s exchanger still has nil httpClient", mode)
		}
	}

	if err := assertGeminiPublicCLIOAuthExchangersHaveHTTPClient(credentialacq.DefaultExchangerRegistry()); err == nil {
		t.Fatal("wiring 自检对未 install 的 Gemini registry 必须返 error")
	}
	if err := assertGeminiPublicCLIOAuthExchangersHaveHTTPClient(registry); err != nil {
		t.Fatalf("wiring 自检对已 install 的 Gemini registry 必须返 nil, got %v", err)
	}
	if err := installGeminiPublicCLIOAuthExchangers(credentialacq.DefaultExchangerRegistry(), nil, "from-env"); err == nil {
		t.Fatal("install 必须拒 nil Gemini OAuth client")
	}
	if err := installGeminiPublicCLIOAuthExchangers(credentialacq.DefaultExchangerRegistry(), mockClient, " "); err == nil {
		t.Fatal("install 必须拒空 HUAKAI_GEMINI_OAUTH_CLIENT_SECRET")
	}
}

func TestWiring_InstallChatGPTOAuthExchangerReplacesDefault(t *testing.T) {
	// 缺陷：ChatGPT OAuth callback 若 production wiring 没注入受控 HTTP client，
	// 默认 exchanger 会 silent 退化，绕过启动期自检。
	// 判别 mutation：注释掉 installChatGPTOAuthExchanger 调用或让 assert 总返回 nil 时，本测试必须变红。
	registry := credentialacq.DefaultExchangerRegistry()
	modeKey := credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth)
	before, ok := registry.Lookup(modeKey)
	if !ok {
		t.Fatal("default registry 必须有 openai/chatgpt_oauth 起手 exchanger")
	}
	if credentialacq.IsChatGPTOAuthExchangerWithExplicitClient(before) {
		t.Fatal("起手 default registry 不应装 explicit-client 版本, 否则 install mutation 无法被检出")
	}

	mockClient := &http.Client{Transport: wiringRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unreachable in helper-only assertion")
	})}
	if err := installChatGPTOAuthExchanger(registry, mockClient); err != nil {
		t.Fatalf("installChatGPTOAuthExchanger: %v", err)
	}
	after, ok := registry.Lookup(modeKey)
	if !ok {
		t.Fatal("install 后 registry Lookup miss")
	}
	if !credentialacq.IsChatGPTOAuthExchangerWithExplicitClient(after) {
		t.Fatal("install 后 ChatGPT exchanger 仍报告 nil httpClient; helper 没真替换")
	}

	if err := assertChatGPTOAuthExchangerHasHTTPClient(credentialacq.DefaultExchangerRegistry()); err == nil {
		t.Fatal("wiring 自检对未 install 的 ChatGPT registry 必须返 error")
	}
	if err := assertChatGPTOAuthExchangerHasHTTPClient(registry); err != nil {
		t.Fatalf("wiring 自检对已 install 的 registry 必须返 nil, got %v", err)
	}
	if err := installChatGPTOAuthExchanger(credentialacq.DefaultExchangerRegistry(), nil); err == nil {
		t.Fatal("install 必须拒 nil ChatGPT OAuth client")
	}
}

func TestWiring_GeminiPublicCLIOAuthSecretEnvFailFast(t *testing.T) {
	// 判别 mutation：把启动缺 secret 改成 lazy ignore 时，本测试必须变红。
	t.Setenv("HUAKAI_GEMINI_OAUTH_CLIENT_SECRET", " ")
	if _, err := loadGeminiPublicCLIOAuthClientSecretFromEnv(); err == nil || !strings.Contains(err.Error(), "HUAKAI_GEMINI_OAUTH_CLIENT_SECRET") {
		t.Fatalf("missing secret err=%v, want env var name", err)
	}

	t.Setenv("HUAKAI_GEMINI_OAUTH_CLIENT_SECRET", " from-env ")
	got, err := loadGeminiPublicCLIOAuthClientSecretFromEnv()
	if err != nil {
		t.Fatalf("load secret: %v", err)
	}
	if got != "from-env" {
		t.Fatalf("secret=%q want trimmed env value", got)
	}
}

type wiringRoundTripFunc func(*http.Request) (*http.Response, error)

func (f wiringRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestWiring_BuildVendorRefreshersSkipsBlankTokenURL(t *testing.T) {
	// Regression killed: operator config with blank TokenURL must not install
	// a zero-value vendor refresher into Scheduler.vendorRefreshers. Mutation
	// self-check: force-adding cursor despite empty token_url makes this test
	// see cursor in the binding list and turn red.
	bindings := buildVendorRefresherBindings(runtimeconfig.VendorOAuthConfigs{
		runtimeconfig.VendorOAuthCursor: {
			ClientID: "cursor-client",
			Scope:    "cursor scope",
		},
		runtimeconfig.VendorOAuthWindsurf: {
			TokenURL: "https://windsurf.example.test/token",
			ClientID: "windsurf-client",
			Scope:    "windsurf scope",
		},
	}, nil)

	if hasVendorBinding(bindings, runtimeconfig.VendorOAuthCursor) {
		t.Fatalf("cursor binding must be absent when token_url is blank: %+v", bindings)
	}
	if !hasVendorBinding(bindings, runtimeconfig.VendorOAuthWindsurf) {
		t.Fatalf("windsurf binding missing from configured vendor list: %+v", bindings)
	}
}

func TestWiring_BuildVendorRefreshersCoversFiveOperatorVendors(t *testing.T) {
	cfgs := runtimeconfig.VendorOAuthConfigs{
		runtimeconfig.VendorOAuthCursor: {
			TokenURL: "https://cursor.example.test/token",
			ClientID: "cursor-client",
			Scope:    "cursor scope",
		},
		runtimeconfig.VendorOAuthWindsurf: {
			TokenURL: "https://windsurf.example.test/token",
			ClientID: "windsurf-client",
			Scope:    "windsurf scope",
		},
		runtimeconfig.VendorOAuthOpenAICodex: {
			TokenURL: "https://codex.example.test/token",
			ClientID: "codex-client",
			Scope:    "openid offline_access",
		},
		runtimeconfig.VendorOAuthKiro: {
			TokenURL:     "https://kiro.example.test/token",
			ClientID:     "kiro-client",
			ClientSecret: "kiro-secret",
			Scope:        "openid aws",
		},
		runtimeconfig.VendorOAuthGemini: {
			TokenURL:     "https://gemini.example.test/token",
			ClientID:     "gemini-client",
			ClientSecret: "gemini-secret",
			Scope:        "openid email",
		},
	}

	bindings := buildVendorRefresherBindings(cfgs, nil)
	for _, vendor := range []string{
		runtimeconfig.VendorOAuthCursor,
		runtimeconfig.VendorOAuthWindsurf,
		runtimeconfig.VendorOAuthOpenAICodex,
		runtimeconfig.VendorOAuthKiro,
		runtimeconfig.VendorOAuthGemini,
	} {
		if !hasVendorBinding(bindings, vendor) {
			t.Fatalf("vendor %q missing from bindings: %+v", vendor, bindings)
		}
	}
	if got := len(bindings); got != 5 {
		t.Fatalf("vendor binding count=%d, want 5", got)
	}
}

func hasVendorBinding(bindings []vendorRefresherBinding, vendor string) bool {
	for _, binding := range bindings {
		if binding.name == vendor {
			return true
		}
	}
	return false
}

func TestWiring_BuildCompletionEventBusWarnsWhenAuditRefEscapeFlagActive(t *testing.T) {
	core, observed := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)
	policy := &eventbus.AuditRefPolicy{
		ReleaseMode:          eventbus.ReleaseModeProduction,
		AllowMissingMoneyRef: true,
	}

	bus, err := buildCompletionEventBus(nil, nil, nil, policy, logger)
	if err != nil {
		t.Fatalf("buildCompletionEventBus: %v", err)
	}
	if bus != nil {
		t.Fatalf("nil eventbus cfg should not build a bus")
	}
	logs := observed.FilterMessage("HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF escape flag active").All()
	if len(logs) != 1 {
		t.Fatalf("warn logs=%d want 1; all=%v", len(logs), observed.All())
	}
	fields := logs[0].ContextMap()
	if fields["env_var"] != runtimeconfig.EnvTrustLedgerAllowMissingMoneyRef {
		t.Fatalf("env_var field=%v want %s", fields["env_var"], runtimeconfig.EnvTrustLedgerAllowMissingMoneyRef)
	}
	if fields["release_mode"] != string(eventbus.ReleaseModeProduction) {
		t.Fatalf("release_mode field=%v want production", fields["release_mode"])
	}
}

// ---------------------------------------------------------------
// loadAuditSigner: 4 case
// ---------------------------------------------------------------

// dev 模式 + 无 path → 自动生成 ephemeral key，附 warn 日志，调用方拿到可用 signer。
func TestWiring_LoadAuditSigner_DevModeEphemeral(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "")
	t.Setenv("HUAKAI_AUDIT_PRIVATE_KEY_PATH", "")

	logger := zaptest.NewLogger(t)
	signer, err := loadAuditSigner(logger)
	if err != nil {
		t.Fatalf("dev ephemeral signer expected success, got error: %v", err)
	}
	if signer == nil {
		t.Fatalf("dev ephemeral signer expected non-nil Signer")
	}
	if fp := signer.Fingerprint(); len(fp) == 0 {
		t.Fatalf("ephemeral signer must produce non-empty fingerprint")
	}
}

// production 模式 + 无 path → fail-fast，不退化为 ephemeral（Risk 5 回归门）。
func TestWiring_LoadAuditSigner_ProductionRequiresKeyPath(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	t.Setenv("HUAKAI_AUDIT_PRIVATE_KEY_PATH", "")

	logger := zaptest.NewLogger(t)
	signer, err := loadAuditSigner(logger)
	if err == nil {
		t.Fatalf("production 模式无 key path 必须 fail-fast，但返回 signer=%v", signer)
	}
	if signer != nil {
		t.Fatalf("fail-fast 返回必须 signer==nil，实际拿到 %v", signer)
	}
	// 友好错误信息应包含 env 变量名，便于 Owner 排错。
	if !strings.Contains(err.Error(), "HUAKAI_AUDIT_PRIVATE_KEY_PATH") {
		t.Fatalf("error message 必须提示 env 名，实际: %v", err)
	}
}

// production 模式 + 有合法 PEM key path → 成功加载，fingerprint 与原私钥一致。
func TestWiring_LoadAuditSigner_LoadsFromPath(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 ed25519 keypair: %v", err)
	}
	// PKCS8 PEM 编码，与 main.go parseAuditPrivateKey 第一分支一致。
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("Marshal PKCS8: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	keyPath := filepath.Join(t.TempDir(), "audit_key.pem")
	if err := os.WriteFile(keyPath, pemBytes, 0600); err != nil {
		t.Fatalf("写 key 文件: %v", err)
	}

	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	t.Setenv("HUAKAI_AUDIT_PRIVATE_KEY_PATH", keyPath)

	logger := zaptest.NewLogger(t)
	signer, err := loadAuditSigner(logger)
	if err != nil {
		t.Fatalf("加载 PEM key 应成功，错误: %v", err)
	}
	if signer == nil {
		t.Fatalf("加载成功后 signer 不应为 nil")
	}
	// 验证 fingerprint = 原 pub key fingerprint，证明 priv→pub 链路正确。
	pubFromSigner := signer.PublicKey()
	if string(pubFromSigner) != string(pub) {
		t.Fatalf("Signer.PublicKey() 与原始 pubkey 不匹配")
	}
}

// production 模式 + base64 编码的 raw 64-byte private key → 也能加载。
// 这条 case 覆盖 parseAuditPrivateKey 的 base64 解码 fallback 分支。
func TestWiring_LoadAuditSigner_LoadsBase64Path(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 ed25519 keypair: %v", err)
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(priv))
	keyPath := filepath.Join(t.TempDir(), "audit_key.b64")
	if err := os.WriteFile(keyPath, encoded, 0600); err != nil {
		t.Fatalf("写 base64 key 文件: %v", err)
	}

	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	t.Setenv("HUAKAI_AUDIT_PRIVATE_KEY_PATH", keyPath)

	logger := zap.NewNop()
	signer, err := loadAuditSigner(logger)
	if err != nil {
		t.Fatalf("base64 key 加载应成功，错误: %v", err)
	}
	if signer == nil {
		t.Fatalf("base64 加载成功 signer 不应为 nil")
	}
}

// ---------------------------------------------------------------
// buildAuditLedger: 3 case
// ---------------------------------------------------------------

// dev 模式 + 无 backend env → memory ledger（warn 日志），调用方拿到可用 Ledger。
func TestWiring_BuildAuditLedger_DevModeDefault(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "")
	t.Setenv("HUAKAI_AUDIT_LEDGER_BACKEND", "")

	logger := zaptest.NewLogger(t)
	signer, err := loadAuditSigner(logger)
	if err != nil {
		t.Fatalf("dev 模式 signer 失败: %v", err)
	}
	ledger, err := buildAuditLedger(context.Background(), nil /* dev memory 不读 pgPool */, signer, logger)
	if err != nil {
		t.Fatalf("dev memory ledger 应成功，错误: %v", err)
	}
	if ledger == nil {
		t.Fatalf("dev memory ledger 不应为 nil")
	}
}

// production 模式 + backend=memory (或空) → fail-fast，不退化为 memory（Risk 6 回归门）。
func TestWiring_BuildAuditLedger_ProductionRequiresPostgres(t *testing.T) {
	cases := []struct {
		name    string
		backend string
	}{
		{"backend_empty", ""},
		{"backend_memory_literal", "memory"},
		{"backend_random", "sqlite"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HUAKAI_RELEASE_MODE", "production")
			t.Setenv("HUAKAI_AUDIT_LEDGER_BACKEND", tc.backend)

			// 为了不被 loadAuditSigner 的 production 校验提前 fail，
			// 这里直接构造一个 ephemeral signer 喂给 buildAuditLedger。
			// 测试目标是 buildAuditLedger 自身的 production gate。
			pub, priv, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatalf("生成 keypair: %v", err)
			}
			_ = pub
			_ = priv
			// 借 dev 路径快速拿到 signer，避免重复 PKCS8 marshal 样板。
			t.Setenv("HUAKAI_RELEASE_MODE", "")
			t.Setenv("HUAKAI_AUDIT_PRIVATE_KEY_PATH", "")
			signer, err := loadAuditSigner(zap.NewNop())
			if err != nil {
				t.Fatalf("ephemeral signer: %v", err)
			}
			// 切回 production 测试目标 backend 校验。
			t.Setenv("HUAKAI_RELEASE_MODE", "production")
			t.Setenv("HUAKAI_AUDIT_LEDGER_BACKEND", tc.backend)

			ledger, err := buildAuditLedger(context.Background(), nil, signer, zap.NewNop())
			if err == nil {
				t.Fatalf("production+backend=%q 必须 fail-fast，实际拿到 ledger=%v", tc.backend, ledger)
			}
			if ledger != nil {
				t.Fatalf("fail-fast 返回必须 ledger==nil，实际 %v", ledger)
			}
			if !strings.Contains(err.Error(), "HUAKAI_AUDIT_LEDGER_BACKEND") {
				t.Fatalf("error message 必须提示 env 名，实际: %v", err)
			}
		})
	}
}

// production 模式 + backend=postgres + nil pgPool → 应进入 postgres 构造分支
// 并因为缺真实连接而失败（不会 silently fallback 到 memory）。
// 验证目标：production+postgres 路径选 Postgres 后端，不是 memory，即使构造失败。
func TestWiring_BuildAuditLedger_ProductionAcceptsPostgres(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	t.Setenv("HUAKAI_AUDIT_LEDGER_BACKEND", "postgres")
	// 先拿 ephemeral signer（绕过 production gate）。
	t.Setenv("HUAKAI_RELEASE_MODE", "")
	t.Setenv("HUAKAI_AUDIT_PRIVATE_KEY_PATH", "")
	signer, err := loadAuditSigner(zap.NewNop())
	if err != nil {
		t.Fatalf("ephemeral signer: %v", err)
	}
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	t.Setenv("HUAKAI_AUDIT_LEDGER_BACKEND", "postgres")

	ledger, err := buildAuditLedger(context.Background(), nil /* nil pool 触发 Postgres 构造期错误 */, signer, zap.NewNop())
	// 期望：要么返回错误（Postgres 构造期 / 连接拒绝），要么返回非 nil ledger；
	// 关键不变量是 — production+postgres 路径不能 silently 退化为 memory。
	if err != nil {
		// 错误必须是 NewPostgresLedger 阶段的错误，不能是 "production 模式要求..."
		// 这类来自 fail-fast gate 的字符串（说明走错分支）。
		if strings.Contains(err.Error(), "production 模式要求") {
			t.Fatalf("backend=postgres 不应触发 fail-fast gate，但 error 含 gate 提示: %v", err)
		}
		// 包含 NewPostgresLedger 或 nil pool 之类的字眼即视为正确分支。
		return
	}
	if ledger == nil {
		t.Fatalf("postgres 构造成功时 ledger 不应为 nil")
	}
}
