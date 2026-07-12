package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"go.uber.org/zap"
)

// TestLoadSessionSigningKey_DevAutoGenButProductionRequires 守护 session key 软化的核心安全姿态:
// 缺显式 key 时,production 仍 fail-loud 拒启(绝不自动生成),非生产模式才自动生成临时 key;且任何
// 模式下显式配的 key 都优先原样采用。
//
// 变异检查:① 删掉 releaseModeProduction 那道分支(让 production 也自动生成)→ "production 缺 key"
// 用例由 error 变成功(RED,精确抓 production 安全降级);② 让 dev 也返回 required 错误 →
// "dev 缺 key" 用例 RED。
func TestLoadSessionSigningKey_DevAutoGenButProductionRequires(t *testing.T) {
	clearKeyEnv := func(t *testing.T) {
		for _, n := range []string{"HUAKAI_SESSION_SIGNING_KEY_B64", "HUAKAI_SESSION_HMAC_KEY_B64", "HUAKAI_SESSION_SIGNING_KEY_HEX"} {
			t.Setenv(n, "")
		}
	}

	t.Run("production缺key必须拒启", func(t *testing.T) {
		t.Setenv("HUAKAI_RELEASE_MODE", "production")
		clearKeyEnv(t)
		if _, err := loadSessionSigningKey(zap.NewNop()); err == nil {
			t.Fatal("production 缺 session key 必须 fail-loud 拒启,绝不自动生成临时 key")
		}
	})

	t.Run("dev缺key自动生成32字节", func(t *testing.T) {
		t.Setenv("HUAKAI_RELEASE_MODE", "dev")
		clearKeyEnv(t)
		key, err := loadSessionSigningKey(zap.NewNop())
		if err != nil {
			t.Fatalf("dev 缺 key 应自动生成临时 key,got err %v", err)
		}
		if len(key) != 32 {
			t.Fatalf("自动生成的临时 key 应为 32 字节,got %d", len(key))
		}
	})

	t.Run("显式key在production下优先原样采用", func(t *testing.T) {
		want := make([]byte, 32)
		if _, err := rand.Read(want); err != nil {
			t.Fatalf("生成测试 key: %v", err)
		}
		t.Setenv("HUAKAI_RELEASE_MODE", "production")
		clearKeyEnv(t)
		t.Setenv("HUAKAI_SESSION_SIGNING_KEY_B64", base64.StdEncoding.EncodeToString(want))
		got, err := loadSessionSigningKey(zap.NewNop())
		if err != nil {
			t.Fatalf("显式配 key 应直接采用,got err %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("显式 key 未被原样采用(优先级被破坏)")
		}
	})
}
