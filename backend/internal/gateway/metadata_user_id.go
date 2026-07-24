// R7.5：Anthropic Messages API metadata.user_id 重写引擎（强伪装层 6 步
// body 变换的第 5 步）。当前合同见 docs/HUAKAI工程设计手册.md §6。
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
// 设计要点（HUAKAI 自有重写引擎；仅描述本仓机制）：
//   - 替换组件由 plan 指定，与 user 表解耦
//   - 纯函数形态：入参 bytes + plan → 出参 bytes + 审计信息
//   - 不可解析时整体回退到 plan.FallbackUserID
//   - parse / format / version 三个 helper 独立导出，供引擎与测试复用
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

// ParsedUserID 还原 metadata.user_id 的三个语义组件加形态标记。组件集合
// (设备指纹 / 账号 / 会话)由线协议固定;IsNewFormat 记录原值是 JSON
// 还是 legacy 拼接,供写回时选用同一形态。
type ParsedUserID struct {
	DeviceID    string // legacy 下为 64 位 hex,JSON 下为客户端任意非空指纹
	AccountUUID string // 账号 UUID,允许缺省为空
	SessionID   string // legacy 下为 36 字符 UUID,JSON 下为任意非空会话标识
	IsNewFormat bool   // 原值为 JSON 形态时为 true
}

// newFormatUserID 是 JSON 形态 user_id 的字段投影,三键名固定于线协议。
type newFormatUserID struct {
	DeviceID    string `json:"device_id"`
	AccountUUID string `json:"account_uuid"`
	SessionID   string `json:"session_id"`
}

// MetadataNewFormatMinVersion:自此 Claude Code CLI 版本起,user_id 改用 JSON
// 形态。
const MetadataNewFormatMinVersion = "2.1.78"

// legacy 形态:user_<设备64hex>_account_<账号UUID或空>_session_<会话UUID>。
// 由 hex / uuid 子片段拼装而成;会话与账号按 UUID 结构(8-4-4-4-12)校验,
// 比纯长度匹配更严、能拒掉畸形指纹,账号另允许整体缺省为空串。
const (
	hexRun   = `[0-9a-fA-F]`
	uuidPart = hexRun + `{8}-` + hexRun + `{4}-` + hexRun + `{4}-` + hexRun + `{4}-` + hexRun + `{12}`
)

var legacyMetadataUserIDPattern = regexp.MustCompile(
	`^user_(` + hexRun + `{64})_account_(` + uuidPart + `|)_session_(` + uuidPart + `)$`)

// ParseMetadataUserID 把 metadata.user_id 还原为组件视图;JSON 与 legacy 两种
// 形态都接受,无法归类时返回 nil。形态由首个非空白字符是否为 '{' 分派。
func ParseMetadataUserID(raw string) *ParsedUserID {
	v := strings.TrimSpace(raw)
	switch {
	case v == "":
		return nil
	case strings.HasPrefix(v, "{"):
		return parseNewFormatUserID(v)
	default:
		return parseLegacyUserID(v)
	}
}

// parseNewFormatUserID 解析 JSON 形态;device_id 或 session_id 缺失即判无效
// (账号允许为空)。
func parseNewFormatUserID(v string) *ParsedUserID {
	var p newFormatUserID
	if json.Unmarshal([]byte(v), &p) != nil {
		return nil
	}
	if p.DeviceID == "" || p.SessionID == "" {
		return nil
	}
	out := assembleParsedUserID(p.DeviceID, p.AccountUUID, p.SessionID, true)
	return &out
}

// parseLegacyUserID 解析 legacy 拼接形态;三个捕获组缺一即判无效。
func parseLegacyUserID(v string) *ParsedUserID {
	groups := legacyMetadataUserIDPattern.FindStringSubmatch(v)
	if len(groups) != 4 {
		return nil
	}
	out := assembleParsedUserID(groups[1], groups[2], groups[3], false)
	return &out
}

// assembleParsedUserID 把三组件加形态标记装进 ParsedUserID(两个解析路径共用,
// 避免各自重复 struct 字面量)。
func assembleParsedUserID(device, account, session string, jsonForm bool) ParsedUserID {
	return ParsedUserID{
		DeviceID:    device,
		AccountUUID: account,
		SessionID:   session,
		IsNewFormat: jsonForm,
	}
}

// FormatMetadataUserID 把三组件按指定形态拼回 user_id;useNewFormat=true 输出
// JSON,否则输出 legacy 拼接串。
func FormatMetadataUserID(deviceID, accountUUID, sessionID string, useNewFormat bool) string {
	if !useNewFormat {
		return fmt.Sprintf("user_%s_account_%s_session_%s", deviceID, accountUUID, sessionID)
	}
	encoded, err := json.Marshal(newFormatUserID{
		DeviceID:    deviceID,
		AccountUUID: accountUUID,
		SessionID:   sessionID,
	})
	if err != nil {
		return ""
	}
	return string(encoded)
}

// IsNewMetadataFormatVersion 判断给定 Claude Code CLI 版本是否使用 JSON 形态
// user_id(>= MetadataNewFormatMinVersion);空串按旧版处理。
func IsNewMetadataFormatVersion(version string) bool {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return false
	}
	return compareSemver(trimmed, MetadataNewFormatMinVersion) >= 0
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
		// 回退：字符串比较
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
		// 解析失败 / 无 user_id：用回退值整体替换。
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
