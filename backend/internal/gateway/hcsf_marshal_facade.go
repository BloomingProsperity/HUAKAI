package gateway

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway/codexreqctl"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway/hcsfmarshal"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// MarshalToProviderRequest 把 HCSF graph 投影为请求 body，且不静默丢弃 capability。
func MarshalToProviderRequest(env *proto.HCSF, endpointFamily string) ([]byte, error) {
	if env == nil {
		return nil, errors.New("gateway: nil HCSF envelope")
	}
	codexreqctl.AddUnsupportedRequestControlLosses(env, endpointFamily)
	shape := hcsfProviderRequestModelFamily(endpointFamily)
	if !hcsfmarshal.SupportsShape(shape) {
		return nil, fmt.Errorf("gateway: unsupported HCSF endpoint family %q", endpointFamily)
	}
	return hcsfmarshal.Marshal(env, shape)
}

// HCSFRequestMarshalShape 返回当前生产路径实际登记的请求投影形态。
func HCSFRequestMarshalShape(endpointFamily string) (string, bool) {
	shape := hcsfProviderRequestModelFamily(endpointFamily)
	if !hcsfmarshal.SupportsShape(shape) && hcsfProviderRequestUsesNativeRawBody(endpointFamily, endpointFamily) {
		return "native_raw", true
	}
	return shape, hcsfmarshal.SupportsShape(shape)
}

// injectRequestControls 保持同包测试使用的窄入口，实际实现位于独立编组包。
func injectRequestControls(raw []byte, env *proto.HCSF, family string) ([]byte, error) {
	return hcsfmarshal.InjectRequestControls(raw, env, family)
}

// OpenAIResponsesTextFromChatResponseFormatRaw 将聊天协议的原始响应格式转换为 Responses 文本格式。
func OpenAIResponsesTextFromChatResponseFormatRaw(raw json.RawMessage) (map[string]any, bool) {
	return hcsfmarshal.OpenAIResponsesTextFromChatResponseFormatRaw(raw)
}
