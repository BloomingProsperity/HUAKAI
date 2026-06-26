// Package bodyfeatures 从原始客户端请求体中提取请求级别的能力信号,
// 以便 Router 能要求池中账号具备这些能力
// (capability_flags @> required_capabilities)。
//
// 唯一入口 Detect 与协议无关:同一个请求 handler 同时支撑
// OpenAI Chat Completions、Anthropic Messages 和 OpenAI Responses,
// 因此扫描能识别这三种 body 形状。它刻意做得很防御 —— 任何畸形、null、
// 空或类型错误的 body 都会全部返回 false(不施加能力约束,也绝不 panic),
// 这样一次解析故障永远不会缩小可用账号集合,也不会让分发路径崩溃。
package bodyfeatures

import (
	"encoding/json"
	"strings"
)

// Detect 扫描原始请求体,报告该请求实际需要哪些可路由的能力。这些布尔值
// 与 Router 发出、并打在账号上的能力标记 token 一一对应:
//
//	vision -> "vision", tools -> "tools", json -> "json", audio -> "audio".
//
// 检测是保守的:只有真正存在的信号才会翻转某个标志(非空的
// tools/functions 数组、带有可用 URL 的真实 image part、json_object/json_schema
// 的 response format、input_audio content part 或带 "audio" 的 modality 请求)。
// 未知或为空的信号保持 false,以免对请求施加过度约束。
func Detect(body []byte) (vision, tools, json, audio bool) {
	if len(body) == 0 {
		return false, false, false, false
	}
	var doc looseRequest
	if err := unmarshal(body, &doc); err != nil {
		return false, false, false, false
	}
	vision = detectVision(doc)
	tools = detectTools(doc)
	json = detectJSON(doc)
	audio = detectAudio(doc)
	return vision, tools, json, audio
}

// unmarshal 是对标准库解码器的一层薄封装,让本包对(<=1MB 的)body 只做
// 一次宽容解析;调用方在重试循环之前只跑一次 Detect,因为能力在多次重试
// 之间是稳定的。
func unmarshal(body []byte, v *looseRequest) error {
	return json.Unmarshal(body, v)
}

// looseRequest 只保留三种协议中承载能力信号的字段,把 content 留作
// RawMessage,这样逐 part 的遍历会推迟到某个标志真正被需要判定时再进行。
type looseRequest struct {
	// OpenAI Chat / Anthropic Messages
	Messages []looseMessage `json:"messages"`
	// OpenAI Responses(input 可能是字符串,也可能是 part 数组)
	Input json.RawMessage `json:"input"`

	// 三种形状下的 tools / function-call 信号
	Tools     json.RawMessage `json:"tools"`
	Functions json.RawMessage `json:"functions"`

	// 结构化输出信号
	ResponseFormat json.RawMessage `json:"response_format"`
	Text           json.RawMessage `json:"text"`

	// audio 输出信号:OpenAI Chat 在顶层带一个 modalities 数组,其元素是
	// 输出模式(text / audio / ...)。出现 "audio" 元素表示该请求要求模型
	// 产出音频,这需要一个 audio 类账号。
	Modalities json.RawMessage `json:"modalities"`
}

type looseMessage struct {
	// Content 可能是字符串(OpenAI Chat 简单形式),也可能是带类型的 part
	// 数组(OpenAI Chat 多模态 / Anthropic content blocks)。
	Content json.RawMessage `json:"content"`
}

// --- vision -----------------------------------------------------------------

// detectVision 报告是否有任何 message 或 Responses-input part 携带需要
// vision 类账号的媒体载荷。它覆盖 OpenAI Chat content parts
// (image_url / file / video_url)、Anthropic image blocks(type "image"
// 且带 source)以及 OpenAI Responses input_image parts(data-URI 的
// image_url)。audio parts 由 detectAudio 单独分类。
func detectVision(doc looseRequest) bool {
	for _, msg := range doc.Messages {
		if contentHasVisionPart(msg.Content) {
			return true
		}
	}
	return contentHasVisionPart(doc.Input)
}

// contentHasVisionPart 遍历一个 content 值,它可能是裸字符串、带类型的
// part 数组或垃圾数据。只有数组才能承载媒体 part;每次查找都有 ok 守卫,
// 因此对抗性的形状会一路落到 false。
func contentHasVisionPart(raw json.RawMessage) bool {
	parts, ok := asArray(raw)
	if !ok {
		return false
	}
	for _, part := range parts {
		obj, ok := asObject(part)
		if !ok {
			continue
		}
		if partIsVision(obj) {
			return true
		}
	}
	return false
}

// partIsVision 对单个 content part 进行分类。part 的 "type" token 横跨多种
// 协议:OpenAI Chat 用 image_url/file/video_url;Anthropic 用 image(且带
// 非空的 source);OpenAI Responses 用 input_image。没有可用 URL 的 image
// part 会被跳过,以避免空图误报。
func partIsVision(obj map[string]json.RawMessage) bool {
	partType := stringField(obj, "type")
	switch partType {
	case "image_url":
		return imageURLPresent(obj["image_url"])
	case "input_image":
		// Responses 的 input_image 在 part 层级携带 data-URI/url。
		return nonEmptyString(obj["image_url"]) && !isEmptyDataURI(stringValue(obj["image_url"]))
	case "image":
		// Anthropic image block 必须带 source 对象/值才算真图。
		return present(obj["source"])
	case "file", "input_file", "video_url":
		// 文档/视频媒体属于 vision 类媒体内容;只有承载字段确实存在
		// (而非空占位)时才计入。
		return present(obj["file"]) || present(obj["file_url"]) ||
			present(obj["video_url"]) || present(obj["image_url"]) ||
			nonEmptyString(obj["file_id"])
	default:
		return false
	}
}

// imageURLPresent 处理 OpenAI Chat 的 image_url part,其载荷是一个对象
// {url:...}(或宽容地接受裸字符串),并跳过空值或空 base64 data URI,
// 以免占位 part 对路由造成过度约束。
func imageURLPresent(raw json.RawMessage) bool {
	if !present(raw) {
		return false
	}
	if s, ok := tryString(raw); ok {
		return s != "" && !isEmptyDataURI(s)
	}
	obj, ok := asObject(raw)
	if !ok {
		return false
	}
	url := stringField(obj, "url")
	return url != "" && !isEmptyDataURI(url)
}

// --- audio ------------------------------------------------------------------

// detectAudio 报告该请求是否需要 audio 类账号。有两个独立信号会翻转它:
// (1)任何 message 或 Responses-input part 是携带真实音频载荷的 input_audio
// content part(OpenAI Chat 音频输入);(2)顶层 modalities 数组把 "audio"
// 列为输出模式(OpenAI Chat 音频输出)。两者任一即足够。与其它检测器一样,
// 每种形状都有 comma-ok 守卫,因此畸形 body 会得到 false,而非 panic 或
// 虚假约束。
func detectAudio(doc looseRequest) bool {
	for _, msg := range doc.Messages {
		if contentHasAudioPart(msg.Content) {
			return true
		}
	}
	if contentHasAudioPart(doc.Input) {
		return true
	}
	return modalitiesHaveAudio(doc.Modalities)
}

// contentHasAudioPart 遍历一个 content 值,它可能是裸字符串、带类型的
// part 数组或垃圾数据。只有数组才能承载媒体 part;每次查找都有 ok 守卫,
// 因此对抗性的形状会一路落到 false。与 contentHasVisionPart 对称。
func contentHasAudioPart(raw json.RawMessage) bool {
	parts, ok := asArray(raw)
	if !ok {
		return false
	}
	for _, part := range parts {
		obj, ok := asObject(part)
		if !ok {
			continue
		}
		if partIsAudio(obj) {
			return true
		}
	}
	return false
}

// partIsAudio 对单个 content part 进行分类。input_audio part 在 part 层级
// 把音频片段放在一个 input_audio 对象下(带 data/format);没有可用
// input_audio 载荷的 part 会被跳过,以避免空音频误报,与 partIsVision 的
// 空图守卫对称。
func partIsAudio(obj map[string]json.RawMessage) bool {
	if stringField(obj, "type") != "input_audio" {
		return false
	}
	return present(obj["input_audio"])
}

// modalitiesHaveAudio 报告顶层 modalities 数组是否列出了 "audio" 输出模式。
// 对非数组形状(string / object / null)以及非字符串元素都很宽容,直接跳过。
func modalitiesHaveAudio(raw json.RawMessage) bool {
	mods, ok := asArray(raw)
	if !ok {
		return false
	}
	for _, m := range mods {
		if stringValue(m) == "audio" {
			return true
		}
	}
	return false
}

// --- tools -------------------------------------------------------------------

// detectTools 报告该请求是否提供了可调用的 tools。它把非空的顶层 tools[]
// (OpenAI Chat / Anthropic / Responses)或旧的 functions[](OpenAI)视为
// 信号;空数组不算信号。
func detectTools(doc looseRequest) bool {
	return nonEmptyArray(doc.Tools) || nonEmptyArray(doc.Functions)
}

// --- json --------------------------------------------------------------------

// detectJSON 报告该请求是否要求结构化输出。OpenAI Chat/Responses 用
// response_format.type 取值 {json_object, json_schema};OpenAI Responses
// 还会把 format 嵌在 text.format.type 之下。"text" 格式类型(或任何其它
// 取值)都不属于结构化输出请求。
func detectJSON(doc looseRequest) bool {
	if formatTypeIsJSON(doc.ResponseFormat) {
		return true
	}
	// Responses 的 text.format.{type|json_schema}。
	if obj, ok := asObject(doc.Text); ok {
		if formatTypeIsJSON(obj["format"]) {
			return true
		}
	}
	return false
}

// formatTypeIsJSON 检查 response_format / text.format 对象,报告其 "type"
// 是否选择了 JSON 输出。对非对象形状很宽容。
func formatTypeIsJSON(raw json.RawMessage) bool {
	obj, ok := asObject(raw)
	if !ok {
		return false
	}
	switch stringField(obj, "type") {
	case "json_object", "json_schema":
		return true
	default:
		return false
	}
}

// --- low-level tolerant helpers ---------------------------------------------

// asArray 把 raw 解码为由 raw 元素组成的 JSON 数组,对 null、字符串、数字、
// 对象或畸形输入返回 ok=false。
func asArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	if !present(raw) {
		return nil, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	return arr, true
}

// asObject 把 raw 解码为 JSON 对象 map,对任何非对象形状返回 ok=false,
// 这样调用方可以对每次字段访问做 comma-ok 守卫。
func asObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if !present(raw) {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	if obj == nil {
		return nil, false
	}
	return obj, true
}

// nonEmptyArray 报告 raw 是否解码为至少含一个元素的数组。空数组和非数组
// 都不算信号。
func nonEmptyArray(raw json.RawMessage) bool {
	arr, ok := asArray(raw)
	return ok && len(arr) > 0
}

// stringField 把 obj[key] 读为字符串,缺失或非字符串时返回 ""。
func stringField(obj map[string]json.RawMessage, key string) string {
	return stringValue(obj[key])
}

// stringValue 把 raw 读为字符串,对任何非字符串形状返回 ""。
func stringValue(raw json.RawMessage) string {
	s, _ := tryString(raw)
	return s
}

// tryString 报告 raw 是否为 JSON 字符串,并返回其值。
func tryString(raw json.RawMessage) (string, bool) {
	if !present(raw) {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// nonEmptyString 报告 raw 是否为非空的 JSON 字符串。
func nonEmptyString(raw json.RawMessage) bool {
	s, ok := tryString(raw)
	return ok && s != ""
}

// present 报告 raw 是否携带一个可与 JSON null 或缺失区分开的值。
func present(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// isEmptyDataURI 报告 s 是否为载荷为空的 base64 data URI,这样占位 image
// part 就不会被误当成真图。
func isEmptyDataURI(s string) bool {
	if !strings.HasPrefix(s, "data:") {
		return false
	}
	rest := strings.TrimPrefix(s, "data:")
	idx := strings.Index(rest, ";")
	if idx < 0 {
		return false
	}
	rest = rest[idx+1:]
	if !strings.HasPrefix(rest, "base64,") {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(rest, "base64,")) == ""
}
