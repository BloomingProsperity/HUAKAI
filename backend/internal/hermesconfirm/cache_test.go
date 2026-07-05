package hermesconfirm

import (
	"testing"
	"time"
)

// TestConfirmCacheBindsOperatorToken 是 H4 审查 S1 的判别式守卫:mutating-tool 的确认
// 必须绑定到签发 dry-run 预览的那个确切 operator admin token,而不只是 tenant + tenant-
// user 上下文。若没有 TokenID 校验,在同一 (tenant, as_user_id) 上下文中操作的 operator B
// (不同的 admin token)就能消费 operator A 的预览并执行一次特权 mutation。
//
// 捕获的回归:从 Cache.Consume 删除 `entry.TokenID != tokenID` 会让错误 operator token
// 的消费成功——此测试随之变红。
func TestConfirmCacheBindsOperatorToken(t *testing.T) {
	const (
		tool      = "account_pause"
		tenantID  = int64(7)
		actorUser = int64(42)
		tokenA    = int64(100)
		tokenB    = int64(200)
		target    = int64(555)
	)
	c := NewCache()

	// Operator A(token 100)签发一个预览。
	id, err := c.Issue(PendingConfirmation{
		ToolName: tool, TenantID: tenantID, ActorID: actorUser, TokenID: tokenA, TargetID: target,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Operator B(token 200)——相同的 tool/tenant/tenant-user——绝不能消费 A 的
	// correlation_id。(而且这次尝试仍会消费掉它:单次消费。)
	if _, ok := c.Consume(id, tool, tenantID, actorUser, tokenB); ok {
		t.Fatal("operator B (different admin token) consumed operator A's confirmation — confirm is not bound to the operator token")
	}

	// 错误 token 的尝试是单次消费:即便是 A 也无法再消费它。
	if _, ok := c.Consume(id, tool, tenantID, actorUser, tokenA); ok {
		t.Fatal("correlation_id survived a failed consume — single-use is broken")
	}

	// 完整性检查:由同一个 operator token 消费的全新预览恰好成功一次。
	id2, err := c.Issue(PendingConfirmation{
		ToolName: tool, TenantID: tenantID, ActorID: actorUser, TokenID: tokenA, TargetID: target,
	})
	if err != nil {
		t.Fatalf("issue 2: %v", err)
	}
	entry, ok := c.Consume(id2, tool, tenantID, actorUser, tokenA)
	if !ok {
		t.Fatal("the issuing operator could not consume its own confirmation")
	}
	if entry.TargetID != target {
		t.Fatalf("consumed entry target=%d want %d", entry.TargetID, target)
	}
	if _, ok := c.Consume(id2, tool, tenantID, actorUser, tokenA); ok {
		t.Fatal("correlation_id was reusable after a successful consume — single-use is broken")
	}
}

func TestConfirmCacheConsumeWithStatusDistinguishesRecoverableMiss(t *testing.T) {
	// HERMES-IP-02:未知/过期 correlation_id 要能被 HTTP 层映射成“重新 dry-run/propose”
	// 的可恢复错误,而不是和错误工具/错误 operator 混成一个泛化 invalid。
	// 变异证伪:若 ConsumeWithStatus 退回 bool 或把 missing/expired/mismatch 都返回同一状态,
	// 本测试会在状态断言处变红。
	const (
		tool      = "account_pause"
		tenantID  = int64(7)
		actorUser = int64(42)
		tokenID   = int64(99)
	)
	c := NewCache()

	if _, status := c.ConsumeWithStatus("hmc_missing", tool, tenantID, actorUser, tokenID); status != ConsumeMissing {
		t.Fatalf("missing status=%s want %s", status, ConsumeMissing)
	}

	id, err := c.Issue(PendingConfirmation{ToolName: tool, TenantID: tenantID, ActorID: actorUser, TokenID: tokenID, TargetID: 5})
	if err != nil {
		t.Fatalf("issue mismatch: %v", err)
	}
	if _, status := c.ConsumeWithStatus(id, "dlq_replay", tenantID, actorUser, tokenID); status != ConsumeMismatch {
		t.Fatalf("wrong tool status=%s want %s", status, ConsumeMismatch)
	}

	base := time.Now()
	c.now = func() time.Time { return base }
	expiredID, err := c.Issue(PendingConfirmation{ToolName: tool, TenantID: tenantID, ActorID: actorUser, TokenID: tokenID, TargetID: 5})
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}
	c.now = func() time.Time { return base.Add(ConfirmTTL + time.Nanosecond) }
	if _, status := c.ConsumeWithStatus(expiredID, tool, tenantID, actorUser, tokenID); status != ConsumeExpired {
		t.Fatalf("expired status=%s want %s", status, ConsumeExpired)
	}
}
