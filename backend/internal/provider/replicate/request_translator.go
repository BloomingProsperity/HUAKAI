// request_translator.go — OpenAI images 形请求 → Replicate prediction body 的
// 纯函数翻译。不做 IO、不读凭据;BuildRequest 在组装出站请求时调用。
//
// Replicate 的请求 body 形态是 {"input":{...}},input 内字段 model-specific。
// 这里只翻译跨模型通用的 OpenAI images 字段(prompt/n/size/quality),未知顶层
// 字段一律丢弃(不可猜测 vendor 语义);需要 model-specific 参数的客户端可显式
// 传顶层 "input" 对象,翻译后原样合并且客户端值优先。
package replicate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrB64JSONResponseFormat 表示客户端要求 response_format=b64_json。Replicate
// 输出是文件 URL;b64 需要出站后追加下载子请求(adapter 契约禁止 adapter 内
// 发子请求),v1 范围外 → 调用层应以 400 显式拒绝,不得静默降级成 url。
var ErrB64JSONResponseFormat = errors.New("replicate: response_format b64_json 不支持(输出为 URL;b64 需下载子请求,v1 范围外)")

// 自定义尺寸的 snap 步进与 clamp 边界(像素)。多数 Replicate 文生图模型要求
// 宽高为 32 的倍数且在有限区间内;超界请求与其原样透传换上游 422,不如入站
// 就贴齐到最近合法值。
const (
	dimensionStep = 32
	dimensionMin  = 256
	dimensionMax  = 1440
)

// billingAxisInputKeys 是计费承重轴:出站量与计费量必须由 OpenAI 顶层字段
// (n→num_outputs、size→aspect_ratio/width/height)单一掌控。客户端顶层
// "input" 对象不得覆盖这些 key——否则可发 n=1 + input.num_outputs=4,按 1 张
// 计费却出 4 张图,同时绕过 image_amount_range 与 quota 预扣(漏钱 + 绕配额)。
// 客户端 input 仍可调非计费轴的 model-specific 参数(guidance/steps/seed 等)。
var billingAxisInputKeys = map[string]bool{
	"num_outputs":  true,
	"width":        true,
	"height":       true,
	"aspect_ratio": true,
	"size":         true,
}

type openAIImageRequest struct {
	Prompt         *string                    `json:"prompt"`
	N              *int                       `json:"n"`
	Size           string                     `json:"size"`
	Quality        string                     `json:"quality"`
	ResponseFormat string                     `json:"response_format"`
	Input          map[string]json.RawMessage `json:"input"`
}

// TranslateImageRequest 把 OpenAI images JSON body 翻译为 Replicate 的
// {"input":{...}} body。映射:
//   - prompt           → input.prompt(必填)
//   - n                → input.num_outputs
//   - size 1024x1024   → input.aspect_ratio "1:1"
//   - size 1792x1024   → input.aspect_ratio "16:9"
//   - size 1024x1792   → input.aspect_ratio "9:16"
//   - size 其它 WxH    → input.aspect_ratio "custom" + width/height
//     (32 步进 snap、clamp [256,1440])
//   - quality hd/high  → input.prompt_upsampling true（提示词增强）
//   - 顶层 "input" 对象 → 合并 model-specific 调参,但计费承重轴
//     (num_outputs/width/height/aspect_ratio/size)被剔除,由顶层字段单一掌控
//
// response_format=b64_json 返回 ErrB64JSONResponseFormat;其余未知顶层字段丢弃。
func TranslateImageRequest(inbound []byte) ([]byte, error) {
	var req openAIImageRequest
	if err := json.Unmarshal(inbound, &req); err != nil {
		return nil, fmt.Errorf("replicate: 入站 body 不是合法 JSON: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(req.ResponseFormat), "b64_json") {
		return nil, ErrB64JSONResponseFormat
	}
	if req.Prompt == nil || strings.TrimSpace(*req.Prompt) == "" {
		return nil, errors.New("replicate: prompt 不能为空")
	}
	input := map[string]any{"prompt": *req.Prompt}
	if req.N != nil {
		if *req.N <= 0 {
			return nil, fmt.Errorf("replicate: n=%d 非法", *req.N)
		}
		input["num_outputs"] = *req.N
	}
	if size := strings.TrimSpace(req.Size); size != "" {
		if err := applySize(input, size); err != nil {
			return nil, err
		}
	}
	switch strings.ToLower(strings.TrimSpace(req.Quality)) {
	case "hd", "high":
		input["prompt_upsampling"] = true
	}
	// 客户端显式 input 逐 key 合并(model-specific 调参);但计费承重轴
	// (num_outputs/width/height/aspect_ratio/size)一律剔除,只能由顶层
	// n/size 决定——否则出站量与计费量脱钩,可计 1 张出 N 张绕配额(漏钱)。
	for key, raw := range req.Input {
		if billingAxisInputKeys[key] {
			continue
		}
		input[key] = raw
	}
	body, err := json.Marshal(map[string]any{"input": input})
	if err != nil {
		return nil, fmt.Errorf("replicate: 请求 body 序列化失败: %w", err)
	}
	return body, nil
}

// applySize 把 OpenAI "WxH" 尺寸映射进 input。标准三档走 aspect_ratio 快捷值;
// 其余可解析尺寸走 custom + 贴齐宽高;不可解析 fail-loud(静默丢尺寸会让计费
// 档位与实际产出不一致)。
func applySize(input map[string]any, size string) error {
	switch size {
	case "1024x1024":
		input["aspect_ratio"] = "1:1"
		return nil
	case "1792x1024":
		input["aspect_ratio"] = "16:9"
		return nil
	case "1024x1792":
		input["aspect_ratio"] = "9:16"
		return nil
	}
	width, height, err := parseSize(size)
	if err != nil {
		return err
	}
	input["aspect_ratio"] = "custom"
	input["width"] = snapDimension(width)
	input["height"] = snapDimension(height)
	return nil
}

func parseSize(size string) (int, int, error) {
	parts := strings.SplitN(strings.ToLower(size), "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("replicate: size %q 不是 WxH 形", size)
	}
	width, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("replicate: size %q 不是 WxH 形", size)
	}
	return width, height, nil
}

// snapDimension 把像素值贴到最近的 32 倍数并 clamp 到 [256,1440]。
func snapDimension(v int) int {
	snapped := (v + dimensionStep/2) / dimensionStep * dimensionStep
	if snapped < dimensionMin {
		return dimensionMin
	}
	if snapped > dimensionMax {
		return dimensionMax
	}
	return snapped
}
