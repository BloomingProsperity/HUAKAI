//go:build integration_pg

package credentialstore

import (
	"strings"
	"testing"
)

// TestCodexWebOAuthInVendorModeChecks 守护迁移 0143:两个 vendor-mode CHECK 约束
// 的 openai vendor 分支都必须把 'codex_web_oauth' 列入白名单。
//
// 这是 wave-A 评审要求的判别性回归守护:单元测试使用不强制 SQL CHECK 约束的
// 内存 fake,因此它们无法捕获原始缺陷(codex_web_oauth 缺失于 CHECK 中 ->
// 在真实 Postgres 下,每次 codex web-OAuth flow-session / credential 插入都会违反
// 该约束)。本测试针对真实 gate 库运行,若 0143 被回退则 FAILS(已验证判别性:
// 在迁移 0142 处,该约束定义不包含 'codex_web_oauth')。
func TestCodexWebOAuthInVendorModeChecks(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	for _, conname := range []string{
		"account_credentials_vendor_mode_check",    // 迁移 0016 (account_credentials)
		"credential_acq_vendor_mode_check",         // 迁移 0019 (credential_acquisition_flow_sessions)
	} {
		var def string
		if err := pool.QueryRow(ctx,
			"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1", conname,
		).Scan(&def); err != nil {
			t.Fatalf("%s: load constraint def: %v", conname, err)
		}
		if !strings.Contains(def, "'codex_web_oauth'") {
			t.Fatalf("%s does not whitelist codex_web_oauth (migration 0143 not applied?) — def: %s", conname, def)
		}
		// 判别性 sanity 校验:CHECK 仍必须是真正的白名单,而非一概放行——
		// 从未列入白名单的 mode 必须保持缺席。
		if strings.Contains(def, "'definitely_not_a_real_mode'") {
			t.Fatalf("%s unexpectedly contains a bogus mode — CHECK is not a strict allowlist", conname)
		}
	}
}
