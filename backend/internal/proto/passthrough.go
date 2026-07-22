// passthrough.go — Upgrade U7-A atomic：upstream 字段透传容器
//
// 解决问题：HUAKAI 上游适配器（OpenAI/Anthropic/Gemini/Bedrock）当前用
// typed struct + json.Unmarshal，未识别字段静默丢失。vendor 加新字段（如
// system_fingerprint / cache_creation_input_tokens / service_tier 等）时，
// HUAKAI 客户端永远看不到。本类型 + helpers 让任意未识别字段以 RawMessage
// 形式被携带过整个 canonical pipeline，最终序列化时合并回输出。
//
// HUAKAI 升级点：每个 extra field 都可走 FieldMatrix 查询 verdict
// （preserve / transform / drop），运维可观测。
//
// 使用模式（adapter 端）：
//
//	var env proto.PassthroughEnvelope
//	var typed openAIChatCompletionChunk
//	if err := proto.UnmarshalWithExtras(raw, &typed, &env); err != nil {
//	    return ...
//	}
//	// typed 装 known fields；env.Extra 装未识别字段（map[string]RawMessage）
//	canonicalEvt.Passthrough = &env  // 由 ClientAdapter 在响应序列化时合并
//
// 使用模式（client adapter 端）：
//
//	typedJSON, _ := json.Marshal(clientChunk)
//	merged, _ := proto.MergeExtrasInto(typedJSON, evt.Passthrough)
//	// merged 含 typed fields + 上游未识别字段；同名键 typed 优先
package proto

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

// PassthroughEnvelope 是 unknown-field 透传容器。
//
// Extra 是上游 JSON 解析后剔除 typed struct 已声明字段（按 json tag）剩余
// 的字段集。值保留为 json.RawMessage 防止嵌套结构被压平。
//
// nil envelope 与空 envelope 行为等价（merge / unmarshal 不 panic）。
type PassthroughEnvelope struct {
	// Extra 持有 unknown 字段的 raw JSON。key 是上游原始 JSON 字段名。
	// 序列化时由 MergeExtrasInto 合并回输出（typed field 同名时 typed 优先）。
	Extra map[string]json.RawMessage
}

// UnmarshalWithExtras 把 raw JSON 解码到 typed struct + 把未识别字段抓到
// dst.Extra。
//
// 算法（two-pass）：
//  1. 先 json.Unmarshal(raw, typed)：装满 typed struct 的 known 字段
//  2. 再 json.Unmarshal(raw, &all)：把 raw 解到 map[string]RawMessage 拿全字段
//  3. 反射 typed 类型拿 json tag 集合 (cached)
//  4. all 减去 known set → 余下 = Extra
//
// 为什么不一次性扫描：Go 标准 json 包不暴露 "未匹配字段" 钩子（除非用
// json.Decoder.DisallowUnknownFields 让解析失败）。two-pass 是 stdlib-only
// 干净实现，性能开销可接受（unmarshal twice on small per-event JSON）。
//
// dst 为 nil 时，unknown 字段被丢弃（行为退化为普通 unmarshal）。
// typed 为 nil 时，所有字段都进 Extra（罕见但合法）。
func UnmarshalWithExtras(raw []byte, typed any, dst *PassthroughEnvelope) error {
	if len(raw) == 0 {
		return nil
	}

	// 第 1 遍：typed unmarshal
	if typed != nil {
		if err := json.Unmarshal(raw, typed); err != nil {
			return fmt.Errorf("proto.UnmarshalWithExtras: typed: %w", err)
		}
	}
	if dst == nil {
		return nil
	}

	// 第 2 遍：完整 raw → map
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		// 顶层不是 JSON object（数组 / 标量） — typed 已尝试解，extras 不适用
		return nil
	}

	// known 字段集合（typed struct 的 json tag）
	known := knownJSONFields(typed)

	// 余下 = unknown
	for k, v := range all {
		if _, isKnown := known[k]; isKnown {
			continue
		}
		if dst.Extra == nil {
			dst.Extra = make(map[string]json.RawMessage, len(all))
		}
		dst.Extra[k] = v
	}
	return nil
}

// MergeExtrasInto 把 env.Extra 合并到 typedJSON 输出。
//
// 规则：
//   - typedJSON 必须是 JSON object 形态；不是 object 时原样返回（数组 / 标量）
//   - typed field 与 Extra key 冲突时 typed 优先（去重，避免覆盖已知字段）
//   - env nil 或 Extra empty → 原样返回 typedJSON
//   - 输出顺序：先 typed keys 后 Extra keys（稳定顺序便于测试）
//
// 性能注：本函数 marshal 一次（合并后），适合每个 streaming event 的 fast path。
func MergeExtrasInto(typedJSON []byte, env *PassthroughEnvelope) ([]byte, error) {
	if env == nil || len(env.Extra) == 0 {
		return typedJSON, nil
	}
	if len(typedJSON) == 0 {
		// 极端情况：典型空 typed 但有 extras（罕见）
		return marshalMap(nil, env.Extra)
	}

	// 解 typedJSON 到 map 检查形态
	var typedMap map[string]json.RawMessage
	if err := json.Unmarshal(typedJSON, &typedMap); err != nil {
		// 不是 object（数组 / 标量）—— extras 不能合并到非 object，原样返回
		return typedJSON, nil
	}

	return marshalMap(typedMap, env.Extra)
}

// attachPassthroughToEvents 把同一个上游 JSON chunk 的 unknown 字段复制到
// 该 chunk 产生的每条 canonical event。复制 map/value，避免下游修改一条
// event 时影响同 chunk 的其它 event。
func AttachPassthroughToEvents(events []CanonicalEvent, env PassthroughEnvelope) []CanonicalEvent {
	if len(events) == 0 || len(env.Extra) == 0 {
		return events
	}
	for i := range events {
		events[i].Passthrough = clonePassthroughEnvelope(&env)
	}
	return events
}

func attachRequestPassthroughFields(env *HCSF, raw []byte, fields ...string) {
	if env == nil || len(raw) == 0 || len(fields) == 0 {
		return
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return
	}
	for _, field := range fields {
		value, ok := all[field]
		if !ok {
			continue
		}
		attachRequestPassthroughField(env, field, value)
	}
}

func attachRequestPassthroughField(env *HCSF, field string, value json.RawMessage) {
	if env == nil || field == "" {
		return
	}
	if env.Passthrough == nil {
		env.Passthrough = &PassthroughEnvelope{}
	}
	if env.Passthrough.Extra == nil {
		env.Passthrough.Extra = map[string]json.RawMessage{}
	}
	copied := append(json.RawMessage(nil), value...)
	env.Passthrough.Extra[field] = copied
}

func clonePassthroughEnvelope(env *PassthroughEnvelope) *PassthroughEnvelope {
	if env == nil || len(env.Extra) == 0 {
		return nil
	}
	extra := make(map[string]json.RawMessage, len(env.Extra))
	for k, v := range env.Extra {
		raw := make([]byte, len(v))
		copy(raw, v)
		extra[k] = json.RawMessage(raw)
	}
	return &PassthroughEnvelope{Extra: extra}
}

// marshalMap 合并 typedMap + extras（typed 优先），按稳定 key 顺序输出。
// 使用 map[string]json.RawMessage 而非自定义 struct 避免值再次 marshal。
func marshalMap(typedMap, extras map[string]json.RawMessage) ([]byte, error) {
	// 合并到 single map（typed 优先）
	merged := make(map[string]json.RawMessage, len(typedMap)+len(extras))
	for k, v := range typedMap {
		merged[k] = v
	}
	for k, v := range extras {
		if _, exists := merged[k]; exists {
			continue // typed 优先
		}
		merged[k] = v
	}
	// json.Marshal map 默认按 key 字典序，稳定
	return json.Marshal(merged)
}

// knownJSONFields 反射 typed struct 类型，返回 json tag 字段集合。
// 内置 cache（typeCache）按 reflect.Type 哈希避免热路径重复反射。
func knownJSONFields(typed any) map[string]struct{} {
	if typed == nil {
		return nil
	}
	t := reflect.TypeOf(typed)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	if cached, ok := typeCache.Load(t); ok {
		return cached.(map[string]struct{})
	}

	out := make(map[string]struct{}, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := jsonFieldName(f)
		if name == "" || name == "-" {
			continue
		}
		out[name] = struct{}{}
	}
	typeCache.Store(t, out)
	return out
}

// jsonFieldName 返回 struct field 的有效 JSON 名（tag 优先，否则 field 名）。
// 处理 `json:",omitempty"` / `json:"-"` / 嵌入字段。
func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "-"
	}
	if tag == "" {
		// 无 tag：Go json 默认用 exported field name
		return f.Name
	}
	// 取逗号前部分
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return tag[:i]
		}
	}
	return tag
}

// typeCache 缓存 reflect.Type → known JSON field set。
// 全局单例避免每次 unmarshal 重复反射。
var typeCache sync.Map
