package servicetier

import (
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const wireField = "service_tier"

// FromJSON 读取请求或响应 JSON 顶层处理档。缺席或非字符串视为未提供。
func FromJSON(raw []byte) string {
	if len(bytesTrimSpace(raw)) == 0 {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return FromExtras(obj)
}

// FromExtras 从透传剩余字段读取处理档。
func FromExtras(extra map[string]json.RawMessage) string {
	if len(extra) == 0 {
		return ""
	}
	raw, ok := extra[wireField]
	if !ok {
		for key, value := range extra {
			if strings.EqualFold(strings.TrimSpace(key), wireField) {
				raw = value
				ok = true
				break
			}
		}
	}
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return normalizeLiteral(s)
	}
	return ""
}

// FromEnvelope 只采信缓冲终态或带用量/终止语义的事件。中间事件上的处理档
// 可能只是请求回声，不能当实档结算。
func FromEnvelope(env *proto.HCSF) string {
	if env == nil {
		return ""
	}
	if env.BufferedResponse != nil {
		if got := fromPassthrough(env.BufferedResponse.Passthrough); got != "" {
			return got
		}
	}
	found := ""
	for _, ev := range env.StreamEvents {
		if got := AuthoritativeFromEvent(ev); got != "" {
			found = got
		}
	}
	return found
}

// AuthoritativeFromEvent 仅在终止帧或带用量的事件上读取处理档。
func AuthoritativeFromEvent(evt proto.CanonicalEvent) string {
	if !eventLaneAuthoritative(evt) {
		return ""
	}
	return fromPassthrough(evt.Passthrough)
}

func eventLaneAuthoritative(evt proto.CanonicalEvent) bool {
	if evt.Type == "message_stop" {
		return true
	}
	return evt.Usage != nil
}

// FromSSE 只采信带用量或终态类型的数据行，避免把中间回声当实档。
func FromSSE(raw []byte) string {
	found := ""
	sawAuthoritative := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if !payloadLaneAuthoritative([]byte(payload)) {
			continue
		}
		if got := FromJSON([]byte(payload)); got != "" {
			found = got
			sawAuthoritative = true
		}
	}
	if sawAuthoritative {
		return found
	}
	// 非 SSE 的整包 JSON（补全缓冲体）仍可读；整段 SSE 回声不得回落到任意一行。
	if payloadLaneAuthoritative(raw) {
		return FromJSON(raw)
	}
	return ""
}

func payloadLaneAuthoritative(raw []byte) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	if _, ok := obj["usage"]; ok {
		return true
	}
	var typ string
	if err := json.Unmarshal(obj["type"], &typ); err != nil {
		return false
	}
	switch strings.TrimSpace(typ) {
	case "message_stop", "response.completed", "response.incomplete":
		return true
	default:
		return false
	}
}

func fromPassthrough(env *proto.PassthroughEnvelope) string {
	if env == nil {
		return ""
	}
	return FromExtras(env.Extra)
}

func bytesTrimSpace(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}
