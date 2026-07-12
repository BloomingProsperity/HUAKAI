package hermeschat

import (
	"testing"
	"time"
)

func TestSessionBindingExpiryIsFailClosed(t *testing.T) {
	// 回归(FAIL-CLOSED):已过期的绑定必须被视为不存在,使泄露的 request_id 无法在会话
	// 窗口之后被重放。变异:若 Lookup 忽略 ExpiresAt,陈旧的绑定仍会授权。我们带过期时间
	// 绑定,把时钟拨过该时间,并断言 Lookup 未命中。
	now := time.Unix(1700000000, 0).UTC()
	clock := func() time.Time { return now }
	b := NewSessionBindings(clock)

	exp := now.Add(2 * time.Minute)
	b.Bind("req-exp", SessionOperator{TenantID: 7, ActorUserID: 42, AdminActorTokenID: 9, Role: "platform_admin", ExpiresAt: exp})

	if _, ok := b.Lookup("req-exp"); !ok {
		t.Fatalf("binding missing before expiry")
	}
	// 拨过过期时间。
	now = exp.Add(time.Second)
	if _, ok := b.Lookup("req-exp"); ok {
		t.Fatalf("expired binding still resolved — replay window not closed")
	}
}

func TestSessionBindingReleaseRemovesBinding(t *testing.T) {
	// 回归:Release 必须丢弃绑定,使其即便在过期之前也不会比其会话存活更久。变异:若
	// Release 是空操作,已结束会话的 request_id 会一直可用直到过期。
	clock := func() time.Time { return time.Unix(1700000000, 0).UTC() }
	b := NewSessionBindings(clock)
	b.Bind("req-rel", SessionOperator{TenantID: 7, ActorUserID: 42, AdminActorTokenID: 9, Role: "platform_admin", ExpiresAt: clock().Add(time.Minute)})
	b.Release("req-rel")
	if _, ok := b.Lookup("req-rel"); ok {
		t.Fatalf("binding survived Release")
	}
}

func TestSessionBindingBlankRequestIDNeverMatches(t *testing.T) {
	// 回归:空白的 request_id 绝不能绑定或匹配——纵深防御,使以空白键的 lookup 无法与
	// 以空白键的 bind 相撞。
	clock := func() time.Time { return time.Unix(1700000000, 0).UTC() }
	b := NewSessionBindings(clock)
	b.Bind("", SessionOperator{TenantID: 7, Role: "platform_admin", ExpiresAt: clock().Add(time.Minute)})
	if _, ok := b.Lookup(""); ok {
		t.Fatalf("blank request_id matched a binding")
	}
}
