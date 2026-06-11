// response_translator.go — Replicate prediction 响应 → OpenAI images 响应的
// 纯函数翻译。计费正确性的第二道闸:只有 status=succeeded 且 output 含至少
// 一个 URL 才产出成功形;starting/processing/failed/canceled、error 非空、
// output 为空一律返回 error,调用层按上游失败处理(abort,绝不 settle 计费)。
package replicate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type predictionResponse struct {
	Status string          `json:"status"`
	Output json.RawMessage `json:"output"`
	Error  json.RawMessage `json:"error"`
}

type imageResponseEntry struct {
	URL string `json:"url"`
}

type imageResponse struct {
	Created int64                `json:"created"`
	Data    []imageResponseEntry `json:"data"`
}

// TranslateImageResponse 把 Replicate prediction JSON 翻译为 OpenAI images
// 响应 {created,data:[{url}...]}。output 接受 string 或 []string 两形态。
// now 注入产生 created 时间戳(测试可控)。
func TranslateImageResponse(raw []byte, now func() time.Time) ([]byte, error) {
	var pred predictionResponse
	if err := json.Unmarshal(raw, &pred); err != nil {
		return nil, fmt.Errorf("replicate: prediction 响应不是合法 JSON: %w", err)
	}
	if detail := errorDetail(pred.Error); detail != "" {
		return nil, fmt.Errorf("replicate: prediction error: %s", detail)
	}
	if pred.Status != "succeeded" {
		// starting/processing 也按失败处理:Prefer: wait 超时窗口内没完成,
		// 没有任何已生成产物可交付——接受这些状态等于对未生成的图计费。
		return nil, fmt.Errorf("replicate: prediction status=%q 未完成或失败,不可交付", pred.Status)
	}
	urls, err := outputURLs(pred.Output)
	if err != nil {
		return nil, err
	}
	out := imageResponse{Created: now().UTC().Unix()}
	for _, u := range urls {
		out.Data = append(out.Data, imageResponseEntry{URL: u})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("replicate: 响应序列化失败: %w", err)
	}
	return body, nil
}

// outputURLs 解析 output 的两种合法形态:单个 URL 字符串,或 URL 字符串数组。
// 空值/空数组/含非字符串元素 → error(succeeded 却无可交付产物 = 上游异常,
// fail-loud 不计费)。
func outputURLs(raw json.RawMessage) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("replicate: prediction succeeded 但 output 为空")
	}
	var single string
	if err := json.Unmarshal(trimmed, &single); err == nil {
		if single == "" {
			return nil, errors.New("replicate: prediction succeeded 但 output 为空")
		}
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(trimmed, &many); err != nil {
		return nil, fmt.Errorf("replicate: output 形态既非 string 也非 string 数组: %s", trimmed)
	}
	urls := make([]string, 0, len(many))
	for _, u := range many {
		if u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return nil, errors.New("replicate: prediction succeeded 但 output 为空")
	}
	return urls, nil
}

// errorDetail 把 error 字段(string 或任意 JSON 对象)归一成非空 detail 文本;
// null/空白返回 ""。
func errorDetail(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		if s == "" {
			return ""
		}
		return s
	}
	return string(trimmed)
}
