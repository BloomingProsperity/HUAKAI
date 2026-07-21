package imageshttp

import (
	"encoding/json"
	"fmt"
	"strings"
)

func isGPTImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gpt-image-") || model == "chatgpt-image-latest"
}

// normalizeGPTImageRequest 把旧客户端的 b64_json 请求投影到当前协议。
// 当前模型本身返回 Base64，因此删除已退役字段不改变客户端语义。
func normalizeGPTImageRequest(body []byte, model, responseFormat string) ([]byte, error) {
	if !isGPTImageModel(model) || strings.TrimSpace(responseFormat) == "" {
		return body, nil
	}
	if !strings.EqualFold(strings.TrimSpace(responseFormat), "b64_json") {
		return nil, fmt.Errorf("gpt image response_format %q 不受支持", responseFormat)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	delete(fields, "response_format")
	return json.Marshal(fields)
}
