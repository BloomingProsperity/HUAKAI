// R7.5：Anthropic Messages API metadata.user_id 重写引擎（强伪装层 6 步
// body 变换的第 5 步）。Spec：docs/specs/upstream-credential-management.md
// §Phase C 第 27 步 step 5 of 6。
//
// metadata.user_id 是 Anthropic 客户端（Claude Code）写入请求的身份指纹。
// 上游用此字段做客户端识别 + 风控。HUAKAI 强伪装在请求转发前重写这个字段
// 的三个语义组件（设备指纹 / 账号 UUID / 会话 ID），让上游看到的身份与
// HUAKAI 本次实际派发的池中账号一致，避免身份不一致触发上游风控。
//
// metadata.user_id 有两种格式：
//   - 旧格式（Claude Code < 2.1.78）："user_<64位hex>_account_<UUID|空>_session_<UUID36>"
//   - 新格式（Claude Code ≥ 2.1.78）：JSON {"device_id":"...","account_uuid":"...","session_id":"..."}
//
// HUAKAI 相对 sub2api 的差异：
//   - 替换组件由 plan 指定，不耦合到 user 表
//   - 纯函数：入参 bytes + plan → 出参 bytes + 审计信息
//   - 无法解析时回退到 plan.FallbackUserID（直接整体替换为给定字符串）
//   - 解析与格式化拆为公开 helper，可在测试 / 其它路径单独使用
package gateway

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// 出参 Reason 的封闭枚举值。
const (
	reasonMetaRewrote                  = "rewrote"                    // 已解析并按 plan 替换组件
	reasonMetaInjected                 = "injected"                   // 原本无 user_id，按 fallback 注入
	reasonMetaPreserved                = "preserved"                  // preserve 模式下检测到合法 user_id，保持
	reasonMetaUnparseableAndFallback   = "unparseable_used_fallback"  // user_id 不可解析，整体替换为 fallback
	reasonMetaUnparseableNoFallback    = "unparseable_no_fallback"    // user_id 不可解析且无 fallback，保持原值
	reasonMetaEmptyPlan                = "empty_plan"                 // plan 没有任何替换值
	reasonMetaInvalidBody              = "invalid_body"               // body 解析失败
	reasonMetaUnsupportedMetadataShape = "unsupported_metadata_shape" // metadata 字段非对象
)

// MetadataInjectMode 控制当 metadata.user_id 已存在时的处理策略。
type MetadataInjectMode int

const (
	// MetadataInjectRewrite 解析现有 user_id 并按 plan 替换组件；解析失败
	// 时回退到 FallbackUserID。默认模式。
	MetadataInjectRewrite MetadataInjectMode = iota
	// MetadataInjectPreserveExisting 仅在 metadata.user_id 不存在时注入
	// FallbackUserID；已存在则一律保持原值。
	MetadataInjectPreserveExisting
	// MetadataInjectForceFallback 不解析现有值，无条件用 FallbackUserID 覆盖。
	MetadataInjectForceFallback
)

// MetadataUserIDPlan 描述一次 metadata.user_id 重写计划。
type MetadataUserIDPlan struct {
	// Mode 决定替换策略。
	Mode MetadataInjectMode
	// DeviceID 替换原 user_id 中的设备指纹（rewrite 模式）。空串表示不
	// 改这一组件，沿用原值。
	DeviceID string
	// AccountUUID 替换原 user_id 中的账号 UUID。空串表示不改。
	AccountUUID string
	// SessionID 替换原 user_id 中的会话 ID。空串表示不改。
	SessionID string
	// UseNewFormat 决定写回 user_id 时使用 JSON 形态（true）还是 legacy
	// 拼接字符串形态（false）。调用方通常根据 UA 版本决定（≥ 2.1.78 → true）。
	UseNewFormat bool
	// FallbackUserID 是当原 user_id 不存在或不可解析时整体写入的 user_id
	// 字符串。Inject 路径与 unparseable 路径都用它。
	FallbackUserID string
}

// MetadataUserIDResult 携带重写结果与元信息。
type MetadataUserIDResult struct {
	Body    []byte
	Applied bool
	Reason  string
	// ParsedBefore 是改写前的原始组件（解析成功时填）。
	ParsedBefore *ParsedUserID
	// FinalUserID 是写回 body 的最终 user_id 字符串（仅 Applied=true 时有值）。
	FinalUserID string
}

// ParsedUserID 是 metadata.user_id 解析后的语义组件视图。
type ParsedUserID struct {
	// DeviceID 设备指纹（旧格式 64 hex；新格式可任意非空）。
	DeviceID string
	// AccountUUID 账号 UUID（可为空）。
	AccountUUID string
	// SessionID 会话 ID（旧格式 UUID36；新格式可任意非空）。
	SessionID string
	// IsNewFormat 表示原值是否为 JSON 形态。
	IsNewFormat bool
}

// jsonUserIDPayload 是新格式 user_id 的 JSON 投影。
type jsonUserIDPayload struct {
	DeviceID    string `json:"device_id"`
	AccountUUID string `json:"account_uuid"`
	SessionID   string `json:"session_id"`
}

// MetadataNewFormatMinVersion 是切换到 JSON 形态 user_id 的最低 Claude Code
// CLI 版本。
const MetadataNewFormatMinVersion = "2.1.78"

// legacyMetadataUserIDPattern 匹配旧格式 user_id：
//
//	user_<64hex>_account_<可选UUID>_session_<UUID36>
var legacyMetadataUserIDPattern = regexp.MustCompile(`^user_([a-fA-F0-9]{64})_account_([a-fA-F0-9-]*)_session_([a-fA-F0-9-]{36})$`)

// ParseMetadataUserID 解析 metadata.user_id 字符串，自动识别 JSON / legacy
// 两种形态。返回 nil 表示无法识别。
func ParseMetadataUserID(raw string) *ParsedUserID {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if trimmed[0] == '{' {
		var p jsonUserIDPayload
		if err := json.Unmarshal([]byte(trimmed), &p); err != nil {
			return nil
		}
		if p.DeviceID == "" || p.SessionID == "" {
			return nil
		}
		return &ParsedUserID{
			DeviceID:    p.DeviceID,
			AccountUUID: p.AccountUUID,
			SessionID:   p.SessionID,
			IsNewFormat: true,
		}
	}
	groups := legacyMetadataUserIDPattern.FindStringSubmatch(trimmed)
	if groups == nil {
		return nil
	}
	return &ParsedUserID{
		DeviceID:    groups[1],
		AccountUUID: groups[2],
		SessionID:   groups[3],
		IsNewFormat: false,
	}
}

// FormatMetadataUserID 按指定形态把三组件拼装回 user_id 字符串。
// useNewFormat=true 输出 JSON；否则输出 legacy 拼接形态。
func FormatMetadataUserID(deviceID, accountUUID, sessionID string, useNewFormat bool) string {
	if useNewFormat {
		b, _ := json.Marshal(jsonUserIDPayload{
			DeviceID:    deviceID,
			AccountUUID: accountUUID,
			SessionID:   sessionID,
		})
		return string(b)
	}
	return "user_" + deviceID + "_account_" + accountUUID + "_session_" + sessionID
}

// IsNewMetadataFormatVersion 判断给定 Claude Code CLI 版本号是否使用 JSON
// 形态 user_id（≥ 2.1.78）。空串视为旧格式。
func IsNewMetadataFormatVersion(version string) bool {
	v := strings.TrimSpace(version)
	if v == "" {
		return false
	}
	return compareSemver(v, MetadataNewFormatMinVersion) >= 0
}

// compareSemver 比较两个 X.Y.Z 形式的版本号。返回 -1 / 0 / 1。
// 非数字段或位数差异退化为字典序比较。
func compareSemver(a, b string) int {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		ai, aok := atoiSafe(getOrEmpty(pa, i))
		bi, bok := atoiSafe(getOrEmpty(pb, i))
		if aok && bok {
			if ai != bi {
				if ai < bi {
					return -1
				}
				return 1
			}
			continue
		}
		// fallback：字符串比较
		ax, bx := getOrEmpty(pa, i), getOrEmpty(pb, i)
		if ax != bx {
			if ax < bx {
				return -1
			}
			return 1
		}
	}
	return 0
}

func getOrEmpty(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

func atoiSafe(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		n = n*10 + int(ch-'0')
	}
	return n, true
}

// RewriteMetadataUserID 按 plan 改写 body 中 metadata.user_id 字段。永不修改
// 入参切片：未变动时返回 body 的拷贝，变动时返回重新序列化的字节。
func RewriteMetadataUserID(body []byte, plan MetadataUserIDPlan) (MetadataUserIDResult, error) {
	if !planHasAnyAction(plan) {
		return metaUnchanged(body, reasonMetaEmptyPlan), nil
	}
	root, err := decodeMetaBody(body)
	if err != nil {
		return metaUnchanged(body, reasonMetaInvalidBody), err
	}

	metaRaw, hasMeta := root["metadata"]
	var metaObj rawObject
	if hasMeta {
		if isJSONNull(metaRaw) {
			hasMeta = false
		} else {
			obj, err := decodeRawObject(metaRaw)
			if err != nil {
				return metaUnchanged(body, reasonMetaUnsupportedMetadataShape), nil
			}
			metaObj = obj
		}
	}
	if metaObj == nil {
		metaObj = rawObject{}
	}

	existingRaw, hasUserID := metaObj["user_id"]
	var existing string
	if hasUserID {
		if s, ok := decodeRawString(existingRaw); ok {
			existing = s
		} else {
			// user_id 字段存在但不是字符串 — 视为不可解析。
			hasUserID = false
		}
	}

	var parsed *ParsedUserID
	if existing != "" {
		parsed = ParseMetadataUserID(existing)
	}

	switch plan.Mode {
	case MetadataInjectPreserveExisting:
		if parsed != nil {
			return metaUnchanged(body, reasonMetaPreserved), nil
		}
		// 无 user_id 或不可解析 → 走注入路径。
		if plan.FallbackUserID == "" {
			return metaUnchanged(body, reasonMetaUnparseableNoFallback), nil
		}
		return writeMetaUserID(root, metaObj, plan.FallbackUserID, reasonMetaInjected, nil)
	case MetadataInjectForceFallback:
		if plan.FallbackUserID == "" {
			return metaUnchanged(body, reasonMetaEmptyPlan), nil
		}
		return writeMetaUserID(root, metaObj, plan.FallbackUserID, reasonMetaRewrote, parsed)
	default: // MetadataInjectRewrite
		if parsed != nil {
			final := FormatMetadataUserID(
				pickComponent(plan.DeviceID, parsed.DeviceID),
				pickComponent(plan.AccountUUID, parsed.AccountUUID),
				pickComponent(plan.SessionID, parsed.SessionID),
				plan.UseNewFormat,
			)
			return writeMetaUserID(root, metaObj, final, reasonMetaRewrote, parsed)
		}
		// 解析失败 / 无 user_id：用 fallback 整体替换。
		if plan.FallbackUserID == "" {
			if hasUserID {
				return metaUnchanged(body, reasonMetaUnparseableNoFallback), nil
			}
			return metaUnchanged(body, reasonMetaEmptyPlan), nil
		}
		reason := reasonMetaUnparseableAndFallback
		if !hasUserID {
			reason = reasonMetaInjected
		}
		return writeMetaUserID(root, metaObj, plan.FallbackUserID, reason, nil)
	}
}

// planHasAnyAction 检查 plan 是否携带至少一项改动来源。
func planHasAnyAction(p MetadataUserIDPlan) bool {
	return p.DeviceID != "" || p.AccountUUID != "" || p.SessionID != "" || p.FallbackUserID != ""
}

// pickComponent 优先用 override；override 为空时沿用 original。
func pickComponent(override, original string) string {
	if override != "" {
		return override
	}
	return original
}

// writeMetaUserID 把 final user_id 写入 metadata 对象，再写回 root，最后
// 重新序列化 body。
func writeMetaUserID(root rawObject, metaObj rawObject, final string, reason string, parsed *ParsedUserID) (MetadataUserIDResult, error) {
	encoded, err := json.Marshal(final)
	if err != nil {
		return MetadataUserIDResult{}, fmt.Errorf("metadata user_id rewrite: marshal user_id: %w", err)
	}
	metaObj["user_id"] = encoded
	metaRaw, err := json.Marshal(metaObj)
	if err != nil {
		return MetadataUserIDResult{}, fmt.Errorf("metadata user_id rewrite: marshal metadata: %w", err)
	}
	root["metadata"] = metaRaw
	out, err := json.Marshal(root)
	if err != nil {
		return MetadataUserIDResult{}, fmt.Errorf("metadata user_id rewrite: marshal body: %w", err)
	}
	return MetadataUserIDResult{
		Body:         out,
		Applied:      true,
		Reason:       reason,
		ParsedBefore: parsed,
		FinalUserID:  final,
	}, nil
}

// decodeMetaBody 解析 body 为顶层 raw 对象。
func decodeMetaBody(body []byte) (rawObject, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("metadata user_id rewrite: empty body")
	}
	var root rawObject
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("metadata user_id rewrite: invalid body: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("metadata user_id rewrite: invalid body: expected JSON object")
	}
	return root, nil
}

// metaUnchanged 返回 body 的拷贝。
func metaUnchanged(body []byte, reason string) MetadataUserIDResult {
	return MetadataUserIDResult{Body: append([]byte(nil), body...), Reason: reason}
}
