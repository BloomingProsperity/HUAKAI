package registry

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestSanitizeAliasImportRowError 守 bulk-import per-row 错误 sanitize(防把 DB 内部经 HTTP 200 响应泄给客户端)。
// 判别:
//   - ErrUnknownModel(含包裹)是安全用户向 sentinel → 给清晰文案 model_not_found(帮运维改输入)。
//   - 后端 DB/事务错误(ErrRegistryBackend 包裹的 raw pgx 串: 表名/约束/SQLSTATE)→ 收敛成通用码 import_row_failed,
//     且返回文案【绝不含】任何原始 DB 内部串。
//
// mutation: 把 sanitizeAliasImportRowError 退回 err.Error() → leaky 断言(返回不含 idx_/SQLSTATE 等)转红。
func TestSanitizeAliasImportRowError(t *testing.T) {
	ctx := context.Background()

	if got := sanitizeAliasImportRowError(ctx, 0, "gpt-a", ErrUnknownModel); got != "model_not_found" {
		t.Fatalf("ErrUnknownModel → %q, want model_not_found", got)
	}
	// 包裹后仍经 errors.Is 识别为安全 sentinel。
	wrapped := fmt.Errorf("%w: upsert tenant alias context", ErrUnknownModel)
	if got := sanitizeAliasImportRowError(ctx, 1, "gpt-b", wrapped); got != "model_not_found" {
		t.Fatalf("wrapped ErrUnknownModel → %q, want model_not_found", got)
	}

	// 后端 DB 错误: 模拟 raw pgx 唯一约束违反(含表/约束/SQLSTATE 等绝不可外泄的内部细节)。
	leaky := fmt.Errorf("%w: upsert tenant alias: ERROR: duplicate key value violates unique constraint \"idx_model_alias_internal\" (SQLSTATE 23505)", ErrRegistryBackend)
	got := sanitizeAliasImportRowError(ctx, 2, "gpt-c", leaky)
	if got != "import_row_failed" {
		t.Fatalf("backend DB error → %q, want import_row_failed", got)
	}
	for _, internal := range []string{"idx_model_alias_internal", "duplicate key", "SQLSTATE", "constraint", "ERROR:", "23505"} {
		if strings.Contains(got, internal) {
			t.Fatalf("sanitized message leaks DB internal %q: %q", internal, got)
		}
	}

	// 任意非 sentinel 错误同样收敛(default 安全)。
	if got := sanitizeAliasImportRowError(ctx, 3, "gpt-d", fmt.Errorf("some raw internal: %s", "secret_table_x")); got != "import_row_failed" || strings.Contains(got, "secret_table_x") {
		t.Fatalf("generic backend error → %q, want sanitized import_row_failed without raw", got)
	}
}

// TestNormalizeModelAliasImportErrorsAreSafe 固化两路径设计: 校验失败(normalize)的错误【有意】绕过
// sanitizeAliasImportRowError 直接回客户端, 因其是安全的用户向文案(帮运维改输入)。本测守该前提 ——
// 若未来有人往 normalizeModelAliasImport 塞 DB/内部错误, 这些断言转红提醒(那时就该改走 sanitize)。
func TestNormalizeModelAliasImportErrorsAreSafe(t *testing.T) {
	cases := []ModelAliasImport{
		{Scope: "weird", ModelID: 1, Alias: "a"},                    // 非法 scope
		{Scope: "tenant", ModelID: 1, Alias: "a"},                   // tenant 缺 tenant_id
		{Scope: "global", ModelID: 0, Alias: "a"},                   // model_id 非正
		{Scope: "global", ModelID: 1, Alias: "   "},                 // alias 空
		{Scope: "global", ModelID: 1, Alias: "a", Status: "paused"}, // 非法 status
	}
	for i, in := range cases {
		row := in
		_, err := normalizeModelAliasImport(&row)
		if err == nil {
			t.Fatalf("case %d: expected validation error, got nil", i)
		}
		msg := err.Error()
		for _, internal := range []string{"SQLSTATE", "constraint", "registry: backend", "pgx", "duplicate key"} {
			if strings.Contains(msg, internal) {
				t.Fatalf("case %d: validation message leaks DB internal %q: %q", i, internal, msg)
			}
		}
	}
}
