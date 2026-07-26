package signupreward

import (
	"encoding/json"
	"testing"

	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
)

func TestEventRoundTripPreservesPromisedAmount(t *testing.T) {
	event, err := NewEvent(7, 42, KindSignupBonus, 125)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	payload, err := ParseEvent(event)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if payload.TenantID != 7 || payload.UserID != 42 || payload.Kind != KindSignupBonus || payload.AmountCents != 125 {
		t.Fatalf("payload=%+v，期望完整保留 7/42/signup_bonus/125", payload)
	}
}

func TestParseEventRejectsTamperedIdentityAndMissingAmount(t *testing.T) {
	event, err := NewEvent(7, 42, KindInviteeReward, 50)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	event.ID = "signup-reward-7-99-invitee_reward"
	if _, err := ParseEvent(event); err == nil {
		t.Fatal("事件 ID 与载荷用户不一致时必须拒绝")
	}
	event.ID = "signup-reward-7-42-invitee_reward"
	event.Payload = json.RawMessage(`{"tenant_id":7,"user_id":42,"reward_kind":"invitee_reward"}`)
	if _, err := ParseEvent(event); err == nil {
		t.Fatal("缺少承诺金额的事件必须拒绝，不能按运行时配置猜金额")
	}
	event.EventType = obsdlq.EventTypeAdminAlert
	if _, err := ParseEvent(event); err == nil {
		t.Fatal("错误事件类型必须拒绝")
	}
}
