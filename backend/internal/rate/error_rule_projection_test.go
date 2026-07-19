package rate

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeTempUnschedulableRulesForWriteAddsSafeDefaults(t *testing.T) {
	raw := []byte(`[{"rule_id":" Busy-529 ","error_code":529,"keywords":[" busy "],"duration_minutes":5}]`)
	got, err := NormalizeTempUnschedulableRulesForWrite(raw)
	if err != nil {
		t.Fatalf("NormalizeTempUnschedulableRulesForWrite: %v", err)
	}
	want := `[{"rule_id":"busy-529","error_code":529,"keywords":["busy"],"duration_minutes":5,"message_mode":"fixed","affect_health":true}]`
	if string(got) != want {
		t.Fatalf("规范化结果不一致\n got=%s\nwant=%s", got, want)
	}
}

func TestNormalizeTempUnschedulableRulesForWriteRejectsUnknownAndDuplicate(t *testing.T) {
	tests := []string{
		`[{"rule_id":"busy","error_code":529,"duration_minutes":5,"retry":true}]`,
		`[{"rule_id":"busy","error_code":529,"duration_minutes":5},{"rule_id":"busy","error_code":503,"duration_minutes":5}]`,
	}
	for _, raw := range tests {
		if _, err := NormalizeTempUnschedulableRulesForWrite([]byte(raw)); err == nil {
			t.Fatalf("非法规则未被拒绝: %s", raw)
		}
	}
}

func TestEvalAccountErrorRulesProjectsCustomClientErrorWithoutChangingCooldown(t *testing.T) {
	affectHealth := false
	clientStatus := 503
	rules := []TempUnschedulableRule{{
		RuleID: "busy-529", ErrorCode: 529, Keywords: []string{"busy"}, DurationMinutes: 7,
		ClientStatus: &clientStatus, ClientCode: "account_busy", MessageMode: "custom",
		ClientMessage: "服务繁忙，请稍后重试", AffectHealth: &affectHealth,
	}}
	dec := evalAccountErrorRules(529, []byte(`{"error":{"message":"busy"}}`), rules, nil,
		durationProvider, testDefaultCooldown, fixedNow(), false)
	if dec.StateChange != StateTempUnsched || !dec.ShouldFailover || dec.CooldownUntil.IsZero() {
		t.Fatalf("客户端投影不得削弱原冷却决策: %+v", dec)
	}
	if dec.ClientStatus != 503 || dec.ClientCode != "account_busy" || dec.ClientMessage != "服务繁忙，请稍后重试" || dec.ClientRuleID != "busy-529" {
		t.Fatalf("客户端投影错误: %+v", dec)
	}
	if !dec.SuppressHealthSignal {
		t.Fatal("affect_health=false 应请求抑制普通健康信号")
	}
}

func TestEvalAccountErrorRulesUsesFirstMatchingRule(t *testing.T) {
	firstStatus := 422
	secondStatus := 503
	affectHealth := true
	rules := []TempUnschedulableRule{
		{
			RuleID: "first", ErrorCode: 529, Keywords: []string{"busy"}, DurationMinutes: 3,
			ClientStatus: &firstStatus, ClientCode: "first_match", MessageMode: "custom",
			ClientMessage: "第一条规则", AffectHealth: &affectHealth,
		},
		{
			RuleID: "second", ErrorCode: 529, Keywords: []string{"busy"}, DurationMinutes: 9,
			ClientStatus: &secondStatus, ClientCode: "second_match", MessageMode: "custom",
			ClientMessage: "第二条规则", AffectHealth: &affectHealth,
		},
	}

	dec := evalAccountErrorRules(529, []byte(`{"error":{"message":"BUSY"}}`), rules, nil,
		durationProvider, testDefaultCooldown, fixedNow(), false)
	if dec.ClientRuleID != "first" || dec.ClientStatus != firstStatus || dec.ClientCode != "first_match" || dec.ClientMessage != "第一条规则" {
		t.Fatalf("首个匹配规则没有胜出: %+v", dec)
	}
	if got := dec.CooldownUntil.Sub(fixedNow()); got != 3*time.Minute {
		t.Fatalf("冷却时长=%s，期望首条规则的 3m", got)
	}
}

func TestEvalAccountErrorRulesUpstreamSafeMessageFailsClosed(t *testing.T) {
	affectHealth := true
	rule := TempUnschedulableRule{
		RuleID: "safe-message", ErrorCode: 503, DurationMinutes: 3,
		MessageMode: "upstream_safe", AffectHealth: &affectHealth,
	}
	safe := evalAccountErrorRules(503, []byte(`{"error":{"message":"capacity unavailable"}}`), []TempUnschedulableRule{rule}, nil,
		durationProvider, testDefaultCooldown, fixedNow(), false)
	if safe.ClientMessage != "capacity unavailable" {
		t.Fatalf("安全消息未投影: %+v", safe)
	}
	unsafe := evalAccountErrorRules(503, []byte(`{"error":{"message":"token=sk-live-1234567890"}}`), []TempUnschedulableRule{rule}, nil,
		durationProvider, testDefaultCooldown, fixedNow(), false)
	if unsafe.ClientMessage != "" || unsafe.ClientRuleID != "safe-message" {
		t.Fatalf("不安全消息应回退固定目录但保留规则身份: %+v", unsafe)
	}
}

func TestEvalAccountErrorRulesLegacyRuleKeepsCooldownWithoutNewAuthority(t *testing.T) {
	affectHealth := false
	status := 503
	legacy := TempUnschedulableRule{
		ErrorCode: 418, DurationMinutes: 5, ClientStatus: &status,
		ClientCode: "legacy_override", MessageMode: "custom", ClientMessage: "不可使用",
		AffectHealth: &affectHealth,
	}
	dec := evalAccountErrorRules(418, nil, []TempUnschedulableRule{legacy}, nil,
		durationProvider, testDefaultCooldown, fixedNow(), false)
	if dec.StateChange != StateTempUnsched || dec.CooldownUntil.IsZero() {
		t.Fatalf("旧规则冷却行为被破坏: %+v", dec)
	}
	if dec.ClientStatus != 0 || dec.ClientCode != "" || dec.ClientMessage != "" || dec.SuppressHealthSignal {
		t.Fatalf("没有稳定 rule_id 的旧数据不得获得新权限: %+v", dec)
	}
}

func TestNormalizeTempUnschedulableRulesForWriteRejectsSecretCustomMessage(t *testing.T) {
	raw := `[{"rule_id":"secret","error_code":503,"duration_minutes":5,"message_mode":"custom","client_message":"token=sk-live-1234567890"}]`
	if _, err := NormalizeTempUnschedulableRulesForWrite([]byte(raw)); err == nil || !strings.Contains(err.Error(), "不含秘密") {
		t.Fatalf("含秘密的自定义消息必须被明确拒绝，得到 %v", err)
	}
}
