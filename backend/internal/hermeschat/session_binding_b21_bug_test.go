package hermeschat

import (
	"testing"
	"time"
)

// TestSessionBindingB21ConflictDoesNotCrossOperatorRole 是 B21 [S3] 的判别测试。
//
// bug:SessionBindings.Bind 以 request_id 为唯一键、无条件 last-writer-wins。request_id
// 受客户端影响(X-Request-Id 头)。若同租户、同 ?as_user_id 的两个 admin operator 用同一
// request_id 并发开聊,后 prepare 的绑定会覆盖先前的。随后 operator A 的 runner 用 A 自己
// 有效铸造的 internal_token(tenant,user,request_id)回调 tool-execute 时,resolveOperator
// 的一致性检查只比对 tenant/user(两者相同),于是返回了【另一个】operator 的 Role +
// AdminActorTokenID —— 跨越了 role 下限与归属。
//
// 正确行为:同一 request_id 被【不同 operator 身份】(不同 AdminActorTokenID / Role)再次
// 绑定时,绝不能让该 request_id 解析出【第二个】operator 的身份。fail-closed 的实现可以拒绝
// 覆盖(first-writer-wins)或作废该键(poison);本测试只断言安全不变式——绝不会解析到冒名
// operator 的 admin actor / role。
//
// 当前 last-writer-wins 下:Lookup 返回 opB(AdminActorTokenID=222, role=tenant_operator)
// => RED。
func TestSessionBindingB21ConflictDoesNotCrossOperatorRole(t *testing.T) {
	clock := func() time.Time { return time.Unix(1700000000, 0).UTC() }
	b := NewSessionBindings(clock)
	exp := clock().Add(2 * time.Minute)

	// 同租户、同 as_user_id —— 这正是 resolveOperator 一致性检查会放行的组合。
	// 两个 operator 的区别在 admin actor token id 与 role(即被越权跨越的字段)。
	opA := SessionOperator{TenantID: 7, ActorUserID: 42, AdminActorTokenID: 111, Role: "platform_admin", ExpiresAt: exp}
	opB := SessionOperator{TenantID: 7, ActorUserID: 42, AdminActorTokenID: 222, Role: "tenant_operator", ExpiresAt: exp}

	sharedRequestID := "client-supplied-x-request-id"
	b.Bind(sharedRequestID, opA) // operator A 先 prepare
	b.Bind(sharedRequestID, opB) // operator B 用同一 request_id 并发 prepare

	got, ok := b.Lookup(sharedRequestID)
	if ok {
		if got.AdminActorTokenID == opB.AdminActorTokenID {
			t.Fatalf("冲突绑定用 opB 覆盖了 opA:request_id 解析出冒名 admin_actor_token_id=%d(应为 %d 或未命中)——operator 归属被跨越", got.AdminActorTokenID, opA.AdminActorTokenID)
		}
		if got.Role != opA.Role {
			t.Fatalf("冲突绑定跨越了 role:解析出 role=%q(应为 %q 或未命中)——RBAC role 下限被跨越", got.Role, opA.Role)
		}
	}
}

// TestSessionBindingB21IdempotentRebindSameOperator 守卫修复不过度:同一 operator 身份用
// 同一 request_id 再次绑定(例如同会话重试 prepare)必须仍然成功,不应被冲突逻辑误伤。
func TestSessionBindingB21IdempotentRebindSameOperator(t *testing.T) {
	clock := func() time.Time { return time.Unix(1700000000, 0).UTC() }
	b := NewSessionBindings(clock)
	exp := clock().Add(2 * time.Minute)

	op := SessionOperator{TenantID: 7, ActorUserID: 42, AdminActorTokenID: 111, Role: "platform_admin", ExpiresAt: exp}
	rid := "same-session-retry"
	b.Bind(rid, op)
	b.Bind(rid, op) // 同一身份重绑 —— 幂等,应保留

	got, ok := b.Lookup(rid)
	if !ok {
		t.Fatalf("同一 operator 的幂等重绑被误伤,绑定丢失")
	}
	if got.AdminActorTokenID != op.AdminActorTokenID || got.Role != op.Role {
		t.Fatalf("幂等重绑后身份被破坏:got token=%d role=%q", got.AdminActorTokenID, got.Role)
	}
}
