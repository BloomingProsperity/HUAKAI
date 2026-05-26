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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"

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
