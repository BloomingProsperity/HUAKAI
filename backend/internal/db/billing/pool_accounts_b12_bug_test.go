package billing

import (
	"os"
	"strings"
	"testing"
)

// TestB12_ListEligibleAccountsByPoolGroupIsReadOnly 断言 chat-completions 热路径的
// 选号查询 ListEligibleAccountsByPoolGroup 必须是纯读(SELECT-only),不得内嵌
// 数据修改 CTE(UPDATE provider_accounts ... SET health_state='healthy')。
//
// bug B12 [S3]: 之前该 :many 选号查询在 WHERE 里通过 `normalized_health` 写 CTE
// 对到期的 throttled/cooldown/revoked 行做 UPDATE(self-heal-on-read)。后果:
//  1. 选号读永远无法走只读副本(它其实是写);
//  2. 某账号 cooldown 到期瞬间,同租户/池组的并发选号会全部发同一条 UPDATE,
//     在最热路径上短暂在行锁上串行化;
//  3. 每次恢复都产生 WAL/updated_at 写放大。
//
// 正确行为:到期恢复语义用只读谓词表达(与 gates.providerAccountHealthEligible
// 一致),把 health_state 的落盘留给非热路径。删除写 CTE 后本测试 GREEN。
func TestB12_ListEligibleAccountsByPoolGroupIsReadOnly(t *testing.T) {
	generatedSQL := strings.Join(strings.Fields(listEligibleAccountsByPoolGroup), " ")

	// 主断言:生成查询里不得出现对 provider_accounts 的 UPDATE。
	// 变异:保留 normalized_health 写 CTE,本断言立即变红。
	if idx := strings.Index(strings.ToUpper(generatedSQL), "UPDATE PROVIDER_ACCOUNTS"); idx != -1 {
		t.Fatalf("热路径选号查询不得内嵌 UPDATE provider_accounts(写 CTE);发现于偏移 %d: %s", idx, generatedSQL)
	}

	// 只读恢复谓词必须存在,保证删除写 CTE 后到期账号仍被纳入候选。
	const recoveryPredicate = "pa.health_state IN ('throttled', 'revoked', 'cooldown') AND pa.health_state_until IS NOT NULL AND pa.health_state_until <= NOW()"
	if !strings.Contains(generatedSQL, recoveryPredicate) {
		t.Fatalf("生成查询缺少只读到期恢复谓词 %q: %s", recoveryPredicate, generatedSQL)
	}

	// 同样校验源 SQL,防止手写生成查询与 sqlc 源漂移。
	raw, err := os.ReadFile("../../../sql/queries/pool_accounts.sql")
	if err != nil {
		t.Fatalf("读取 pool_accounts.sql: %v", err)
	}
	const marker = "-- name: ListEligibleAccountsByPoolGroup :many"
	_, queryTail, found := strings.Cut(string(raw), marker)
	if !found {
		t.Fatalf("pool_accounts.sql 缺少 ListEligibleAccountsByPoolGroup 查询")
	}
	queryBody, _, _ := strings.Cut(queryTail, "\n-- name:")
	sourceSQL := strings.Join(strings.Fields(queryBody), " ")
	if idx := strings.Index(strings.ToUpper(sourceSQL), "UPDATE PROVIDER_ACCOUNTS"); idx != -1 {
		t.Fatalf("源查询不得内嵌 UPDATE provider_accounts(写 CTE);发现于偏移 %d: %s", idx, sourceSQL)
	}
}
