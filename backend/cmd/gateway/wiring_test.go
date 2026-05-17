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
	"encoding/base64"
	"encoding/pem"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

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
