package hermesconfirm

import (
	"context"
	"testing"
	"time"
)

func TestConfirmCache绑定真实管理员来源和ID(t *testing.T) {
	const (
		tool     = "account_pause"
		tenantID = int64(7)
		actorA   = int64(100)
		actorB   = int64(200)
		target   = int64(555)
	)
	c := NewCache()
	pending := testPending(tool, tenantID, "token", actorA, target, "active")

	id, err := c.Issue(context.Background(), pending)
	if err != nil {
		t.Fatalf("签发确认: %v", err)
	}
	wrongActor := pending
	wrongActor.ActorID = actorB
	if _, ok := c.Consume(context.Background(), id, wrongActor); ok {
		t.Fatal("另一位令牌管理员消费了不属于自己的确认")
	}
	if _, ok := c.Consume(context.Background(), id, pending); ok {
		t.Fatal("绑定不匹配后确认仍可复用")
	}

	id2, err := c.Issue(context.Background(), pending)
	if err != nil {
		t.Fatalf("再次签发确认: %v", err)
	}
	wrongSource := pending
	wrongSource.ActorSource = "session"
	if _, ok := c.Consume(context.Background(), id2, wrongSource); ok {
		t.Fatal("相同数值 ID 的会话管理员串用了令牌管理员确认")
	}

	sessionPending := testPending(tool, tenantID, "session", actorA, target, "active")
	id3, err := c.Issue(context.Background(), sessionPending)
	if err != nil {
		t.Fatalf("签发会话确认: %v", err)
	}
	entry, ok := c.Consume(context.Background(), id3, sessionPending)
	if !ok || entry.TargetID != target {
		t.Fatalf("签发者不能消费自己的确认: ok=%v entry=%+v", ok, entry)
	}
	if _, ok := c.Consume(context.Background(), id3, sessionPending); ok {
		t.Fatal("成功消费后的确认仍可复用")
	}
}

func TestConfirmCache区分可恢复缺失和绑定冲突(t *testing.T) {
	const (
		tool     = "account_pause"
		tenantID = int64(7)
		actorID  = int64(99)
	)
	c := NewCache()
	pending := testPending(tool, tenantID, "token", actorID, 5, "active")

	if _, status, err := c.ConsumeWithStatus(context.Background(), "hmc_missing", pending); err != nil || status != ConsumeMissing {
		t.Fatalf("缺失状态=%s，期望 %s", status, ConsumeMissing)
	}

	id, err := c.Issue(context.Background(), pending)
	if err != nil {
		t.Fatalf("签发冲突确认: %v", err)
	}
	wrongTool := pending
	wrongTool.ToolName = "dlq_replay"
	if _, status, err := c.ConsumeWithStatus(context.Background(), id, wrongTool); err != nil || status != ConsumeMismatch {
		t.Fatalf("错误工具状态=%s，期望 %s", status, ConsumeMismatch)
	}

	base := time.Now()
	c.now = func() time.Time { return base }
	expiredID, err := c.Issue(context.Background(), pending)
	if err != nil {
		t.Fatalf("签发过期确认: %v", err)
	}
	c.now = func() time.Time { return base.Add(ConfirmTTL + time.Nanosecond) }
	if _, status, err := c.ConsumeWithStatus(context.Background(), expiredID, pending); err != nil || status != ConsumeExpired {
		t.Fatalf("过期状态=%s，期望 %s", status, ConsumeExpired)
	}
}

func TestConfirmCache换参或状态漂移均拒绝且销毁确认(t *testing.T) {
	ctx := context.Background()
	c := NewCache()
	original := testPending("renew_trigger", 7, "token", 99, 5, "active")

	argsChanged := original
	argsChanged.ArgsDigest, _ = DigestArguments(map[string]any{"account_id": 5, "credentials": map[string]any{"access_token": "changed"}})
	id, err := c.Issue(ctx, original)
	if err != nil {
		t.Fatalf("签发换参测试确认: %v", err)
	}
	if _, status, err := c.ConsumeWithStatus(ctx, id, argsChanged); err != nil || status != ConsumeMismatch {
		t.Fatalf("换参状态=%s err=%v，期望 mismatch", status, err)
	}
	if _, status, err := c.ConsumeWithStatus(ctx, id, original); err != nil || status != ConsumeMissing {
		t.Fatalf("换参冲突后确认仍可复用: status=%s err=%v", status, err)
	}

	planChanged := original
	planChanged.PlanDigest, _ = DigestPlan("provider_account", 5, "lock:5", map[string]any{"state": "rate_limited"})
	id, err = c.Issue(ctx, original)
	if err != nil {
		t.Fatalf("签发状态漂移测试确认: %v", err)
	}
	if _, status, err := c.ConsumeWithStatus(ctx, id, planChanged); err != nil || status != ConsumeMismatch {
		t.Fatalf("状态漂移=%s err=%v，期望 mismatch", status, err)
	}
}

func TestDigestArguments不受对象字段顺序影响(t *testing.T) {
	left, err := DigestArguments(map[string]any{"account_id": 5, "credentials": map[string]any{"refresh_token": "r", "access_token": "a"}})
	if err != nil {
		t.Fatalf("摘要左侧参数: %v", err)
	}
	right, err := DigestArguments(map[string]any{"credentials": map[string]any{"access_token": "a", "refresh_token": "r"}, "account_id": 5})
	if err != nil {
		t.Fatalf("摘要右侧参数: %v", err)
	}
	if left != right {
		t.Fatal("相同 JSON 对象因字段顺序不同得到不同摘要")
	}
}

func testPending(tool string, tenantID int64, source string, actorID, targetID int64, state string) PendingConfirmation {
	argsDigest, _ := DigestArguments(map[string]any{"account_id": targetID})
	planDigest, _ := DigestPlan("provider_account", targetID, "lock", map[string]any{"state": state})
	return PendingConfirmation{
		ToolName: tool, TenantID: tenantID, ActorSource: source, ActorID: actorID,
		TargetID: targetID, ArgsDigest: argsDigest, PlanDigest: planDigest,
	}
}
