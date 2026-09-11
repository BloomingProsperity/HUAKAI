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

// FromEnvelope 从缓冲响应、信封透传、流式事件由近到远取最后一次非空实档。
func FromEnvelope(env *proto.HCSF) string {
	if env == nil {
		return ""
	}
	if env.BufferedResponse != nil {
		if got := fromPassthrough(env.BufferedResponse.Passthrough); got != "" {
			return got
		}
	}
	if got := fromPassthrough(env.Passthrough); got != "" {
		return got
	}
	for i := len(env.StreamEvents) - 1; i >= 0; i-- {
		if got := fromPassthrough(env.StreamEvents[i].Passthrough); got != "" {
			return got
		}
	}
	return ""
}

// FromSSE 从 SSE 数据行由近到远取最后一次顶层处理档。
func FromSSE(raw []byte) string {
	found := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if got := FromJSON([]byte(payload)); got != "" {
			found = got
		}
	}
	if found != "" {
		return found
	}
	return FromJSON(raw)
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
