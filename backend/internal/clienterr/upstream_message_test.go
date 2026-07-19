package clienterr

import "testing"

func TestSafeUpstreamMessageReadsKnownFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "嵌套错误消息", body: `{"error":{"message":"  capacity   unavailable  "}}`, want: "capacity unavailable"},
		{name: "顶层错误消息", body: `{"message":"try later"}`, want: "try later"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SafeUpstreamMessage([]byte(tt.body)); got != tt.want {
				t.Fatalf("SafeUpstreamMessage()=%q，期望 %q", got, tt.want)
			}
		})
	}
}

func TestSafeUpstreamMessageFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "非 JSON", body: `upstream says try later`},
		{name: "未知字段", body: `{"detail":"try later"}`},
		{name: "秘密字段", body: `{"error":{"message":"access_token=secret-value"}}`},
		{name: "裸凭据", body: `{"message":"key sk-live-1234567890 rejected"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SafeUpstreamMessage([]byte(tt.body)); got != "" {
				t.Fatalf("不安全消息必须回退固定目录，得到 %q", got)
			}
		})
	}
}

func TestSafeConfiguredMessageRejectsTextThatNeedsRedaction(t *testing.T) {
	if got, ok := SafeConfiguredMessage("服务繁忙，请稍后重试"); !ok || got != "服务繁忙，请稍后重试" {
		t.Fatalf("安全自定义消息被拒绝：got=%q ok=%v", got, ok)
	}
	if got, ok := SafeConfiguredMessage("token=sk-live-1234567890"); ok || got != "" {
		t.Fatalf("含秘密的自定义消息不得落库：got=%q ok=%v", got, ok)
	}
}
