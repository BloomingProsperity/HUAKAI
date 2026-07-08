// Package codexclientaccess 定义 codex-cli 全局加固层的数据契约与配置校验。
// 本包只承载策略类型与配置解析/校验;匹配逻辑与 gateway 接线由后续片实现。
// 默认空配置 = 不额外限制现有行为(与片2e 账号开关组合,账号未开则整层不生效)。
package codexclientaccess

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// AllowedClientEntry 是全局 codex 客户端加固层的客户端签名条目。
// 白名单用双因子 AND(originator 精确 + 所有 UA 子串都在,防伪造);
// 黑名单用宽 deny(任一字段命中即拒)。SkipEngineFingerprint 仅白名单条目有意义。
type AllowedClientEntry struct {
	Originator            string   `json:"originator"`
	UAContains            []string `json:"ua_contains"`
	SkipEngineFingerprint bool     `json:"skip_engine_fingerprint"`
}

// EngineFingerprintSignal 描述一个后续匹配阶段可用的引擎指纹信号。
// Required 用于后续实现 AND 勾选;Variants 表示同一信号内任一变体命中即可(行内 OR)。
type EngineFingerprintSignal struct {
	Name     string   `json:"name"`
	Header   string   `json:"header"`
	BodyPath string   `json:"body_path"`
	Variants []string `json:"variants"`
	Required bool     `json:"required"`
}

// Policy 是全局 codex 客户端加固层的策略快照。本片只建立配置结构与校验边界,
// 不改变任何请求准入行为。
type Policy struct {
	Blacklist                []AllowedClientEntry      `json:"blacklist"`
	Whitelist                []AllowedClientEntry      `json:"whitelist"`
	MinVersion               string                    `json:"min_version"`
	MaxVersion               string                    `json:"max_version"`
	AllowAppServer           bool                      `json:"allow_app_server"`
	EngineFingerprintSignals []EngineFingerprintSignal `json:"engine_fingerprint_signals"`
	ForceAllow               bool                      `json:"force_allow"`
}

// versionStringPattern:min/max 版本边界只接受 1-3 段纯数字(与引擎版本解析器同为三段粒度)。
// 不接受第 4 段或 -预发布/+构建后缀——比较层为发布粒度、会丢弃这些后缀,若放行会造成「配了却
// 不按 semver 生效」的错觉(审查 S3)。真实 codex 版本均为三段 semver。
var versionStringPattern = regexp.MustCompile(`^v?[0-9]+(?:\.[0-9]+){0,2}$`)

// ParseAllowedClientEntries 解析客户端签名 JSON 数组。空串与 [] 都表示空清单;
// 未知字段会被拒绝,避免配置拼写错误静默失效。
func ParseAllowedClientEntries(raw string) ([]AllowedClientEntry, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return []AllowedClientEntry{}, nil
	}
	if !strings.HasPrefix(value, "[") {
		return nil, fmt.Errorf("client entries must be a JSON array")
	}
	var entries []AllowedClientEntry
	if err := decodeStrictJSON(value, &entries); err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, fmt.Errorf("client entries must be a JSON array")
	}
	return entries, nil
}

// ValidateWhitelistEntries 校验白名单条目:必须同时具备 originator 与 UA 子串条件,
// 避免退化成仅凭可伪造的 originator 单因子放行(安全失败,视为非法配置)。
func ValidateWhitelistEntries(entries []AllowedClientEntry) error {
	for i, entry := range entries {
		if strings.TrimSpace(entry.Originator) == "" {
			return fmt.Errorf("whitelist entry %d originator is required", i)
		}
		if len(entry.UAContains) == 0 {
			return fmt.Errorf("whitelist entry %d ua_contains is required", i)
		}
		if err := validateUAContains("whitelist", i, entry.UAContains); err != nil {
			return err
		}
	}
	return nil
}

// ValidateBlacklistEntries 校验黑名单条目:宽 deny,允许只按 originator 或只按 UA 子串拒绝,
// 但整条全空(无 originator 也无有效 UA 子串)是永不命中的废条目,视为非法。
func ValidateBlacklistEntries(entries []AllowedClientEntry) error {
	for i, entry := range entries {
		hasOriginator := strings.TrimSpace(entry.Originator) != ""
		hasUA, err := hasNonBlankUAContains("blacklist", i, entry.UAContains)
		if err != nil {
			return err
		}
		if !hasOriginator && !hasUA {
			return fmt.Errorf("blacklist entry %d must contain originator or ua_contains", i)
		}
	}
	return nil
}

// ParseEngineFingerprintSignals 解析并校验引擎指纹信号 JSON 数组。本片只校验配置形状,
// 具体命中判断由后续片实现。
func ParseEngineFingerprintSignals(raw string) ([]EngineFingerprintSignal, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return []EngineFingerprintSignal{}, nil
	}
	if !strings.HasPrefix(value, "[") {
		return nil, fmt.Errorf("engine fingerprint signals must be a JSON array")
	}
	var signals []EngineFingerprintSignal
	if err := decodeStrictJSON(value, &signals); err != nil {
		return nil, err
	}
	if signals == nil {
		return nil, fmt.Errorf("engine fingerprint signals must be a JSON array")
	}
	if err := validateEngineFingerprintSignals(signals); err != nil {
		return nil, err
	}
	return signals, nil
}

// ValidateVersionString 校验宽松版本字符串。空串合法(= 不启用版本边界);
// 非空值只要求接近数字点分版本格式,禁过严(容忍新 codex UA 版本形态)。
func ValidateVersionString(v string) error {
	value := strings.TrimSpace(v)
	if value == "" {
		return nil
	}
	if !versionStringPattern.MatchString(value) {
		return fmt.Errorf("version must look like a dotted numeric version")
	}
	return nil
}

func decodeStrictJSON(raw string, out any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("json contains trailing data")
	}
	return nil
}

func validateUAContains(kind string, index int, values []string) error {
	for i, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s entry %d ua_contains[%d] is blank", kind, index, i)
		}
	}
	return nil
}

func hasNonBlankUAContains(kind string, index int, values []string) (bool, error) {
	if len(values) == 0 {
		return false, nil
	}
	if err := validateUAContains(kind, index, values); err != nil {
		return false, err
	}
	return true, nil
}

func validateEngineFingerprintSignals(signals []EngineFingerprintSignal) error {
	for i, signal := range signals {
		if strings.TrimSpace(signal.Name) == "" {
			return fmt.Errorf("engine fingerprint signal %d name is required", i)
		}
		if strings.TrimSpace(signal.Header) == "" && strings.TrimSpace(signal.BodyPath) == "" {
			return fmt.Errorf("engine fingerprint signal %d requires header or body_path", i)
		}
		if len(signal.Variants) == 0 {
			return fmt.Errorf("engine fingerprint signal %d variants is required", i)
		}
		for j, variant := range signal.Variants {
			if strings.TrimSpace(variant) == "" {
				return fmt.Errorf("engine fingerprint signal %d variants[%d] is blank", i, j)
			}
		}
	}
	return nil
}
