// Package accountadvanced 统一管理 provider account 高级配置字段的契约、校验与落库映射。
package accountadvanced

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

//go:embed fields.json
var fieldSpecsJSON []byte

// FieldSpec 是后端与前端静态 mirror 必须共同遵守的字段元数据。
type FieldSpec struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Format   string `json:"format"`
	Nullable bool   `json:"nullable"`
	Minimum  string `json:"minimum"`
	Maximum  string `json:"maximum"`
	Create   bool   `json:"create"`
	Update   bool   `json:"update"`
	Label    string `json:"label"`
	Help     string `json:"help"`
}

var fieldSpecs = mustLoadFieldSpecs()

// Specs 返回规格副本，防止调用方修改进程内唯一清单。
func Specs() []FieldSpec {
	out := make([]FieldSpec, len(fieldSpecs))
	copy(out, fieldSpecs)
	return out
}

// SpecsJSON 返回嵌入的规范 JSON 副本，供跨层一致性门使用。
func SpecsJSON() []byte {
	return append([]byte(nil), fieldSpecsJSON...)
}

// Keys 按规范顺序返回全部 JSON key。
func Keys() []string {
	out := make([]string, 0, len(fieldSpecs))
	for _, spec := range fieldSpecs {
		out = append(out, spec.Key)
	}
	return out
}

func mustLoadFieldSpecs() []FieldSpec {
	var specs []FieldSpec
	if err := json.Unmarshal(fieldSpecsJSON, &specs); err != nil {
		panic(fmt.Sprintf("账号高级字段规格无效: %v", err))
	}
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if spec.Key == "" || !spec.Create || !spec.Update {
			panic(fmt.Sprintf("账号高级字段规格不完整: %q", spec.Key))
		}
		if _, ok := seen[spec.Key]; ok {
			panic(fmt.Sprintf("账号高级字段重复: %s", spec.Key))
		}
		seen[spec.Key] = struct{}{}
	}
	return specs
}

// Optional 保留字段缺席、显式 null 与显式零值之间的差异。
type Optional[T any] struct {
	Present bool
	Null    bool
	Value   T
}

// ProxyBinding 表达账号直连、单代理和代理组三种互斥绑定状态。
type ProxyBinding struct {
	Mode         string  `json:"mode"`
	ProxyID      *int64  `json:"proxy_id,omitempty"`
	ProxyGroupID *string `json:"proxy_group_id,omitempty"`
}

// TempRule 是临时停调规则的可写契约。
type TempRule struct {
	ErrorCode       int32    `json:"error_code"`
	Keywords        []string `json:"keywords"`
	DurationMinutes int32    `json:"duration_minutes"`
	Description     string   `json:"description,omitempty"`
}

// Mutation 是 create/update 共用的已校验高级字段集合。
type Mutation struct {
	RPMLimit                   Optional[int64]
	TPMLimit                   Optional[int64]
	WindowCostLimitCents       Optional[int64]
	MaxSessions                Optional[int32]
	DisableCooling             Optional[bool]
	RefreshLeadSeconds         Optional[int32]
	ExpiresAt                  Optional[time.Time]
	TLSFingerprintRotate       Optional[bool]
	CustomErrorCodesEnabled    Optional[bool]
	CustomErrorCodes           Optional[[]int32]
	PoolMode                   Optional[bool]
	TempUnschedulableEnabled   Optional[bool]
	TempUnschedulableRulesJSON Optional[[]byte]
	ProxyBinding               Optional[ProxyBinding]
}

// Any 表示请求是否提交了至少一个高级字段。
func (m Mutation) Any() bool {
	return m.RPMLimit.Present || m.TPMLimit.Present || m.WindowCostLimitCents.Present ||
		m.MaxSessions.Present || m.DisableCooling.Present || m.RefreshLeadSeconds.Present ||
		m.ExpiresAt.Present || m.TLSFingerprintRotate.Present || m.CustomErrorCodesEnabled.Present ||
		m.CustomErrorCodes.Present || m.PoolMode.Present || m.TempUnschedulableEnabled.Present ||
		m.TempUnschedulableRulesJSON.Present || m.ProxyBinding.Present
}

// Parse 从完整请求对象中提取并校验高级字段；其他请求字段不会进入本包。
func Parse(data []byte) (Mutation, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return Mutation{}, fmt.Errorf("请求必须是 JSON 对象")
	}
	var out Mutation
	for _, spec := range fieldSpecs {
		raw, ok := object[spec.Key]
		if !ok {
			continue
		}
		if err := parseField(&out, spec, raw); err != nil {
			return Mutation{}, fmt.Errorf("%s: %w", spec.Key, err)
		}
	}
	return out, nil
}

func parseField(out *Mutation, spec FieldSpec, raw json.RawMessage) error {
	isNull := bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	if isNull && !spec.Nullable {
		return fmt.Errorf("不允许 null")
	}
	switch spec.Key {
	case "rpm_limit":
		return parseInt64Optional(raw, spec, &out.RPMLimit)
	case "tpm_limit":
		return parseInt64Optional(raw, spec, &out.TPMLimit)
	case "window_cost_limit_cents":
		return parseInt64Optional(raw, spec, &out.WindowCostLimitCents)
	case "max_sessions":
		return parseInt32Optional(raw, spec, &out.MaxSessions)
	case "disable_cooling":
		return parseBoolOptional(raw, &out.DisableCooling)
	case "refresh_lead_seconds":
		return parseInt32Optional(raw, spec, &out.RefreshLeadSeconds)
	case "expires_at":
		return parseTimeOptional(raw, &out.ExpiresAt)
	case "tls_fingerprint_rotate":
		return parseBoolOptional(raw, &out.TLSFingerprintRotate)
	case "custom_error_codes_enabled":
		return parseBoolOptional(raw, &out.CustomErrorCodesEnabled)
	case "custom_error_codes":
		return parseErrorCodesOptional(raw, &out.CustomErrorCodes)
	case "pool_mode":
		return parseBoolOptional(raw, &out.PoolMode)
	case "temp_unschedulable_enabled":
		return parseBoolOptional(raw, &out.TempUnschedulableEnabled)
	case "temp_unschedulable_rules":
		return parseRulesOptional(raw, &out.TempUnschedulableRulesJSON)
	case "proxy_binding":
		return parseProxyOptional(raw, &out.ProxyBinding)
	default:
		return fmt.Errorf("后端没有该字段的解析器")
	}
}

func parseInt64Optional(raw json.RawMessage, spec FieldSpec, out *Optional[int64]) error {
	out.Present = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		out.Null = true
		return nil
	}
	value, err := parseJSONInteger(raw, 64)
	if err != nil {
		return err
	}
	min, _ := strconv.ParseInt(spec.Minimum, 10, 64)
	max, _ := strconv.ParseInt(spec.Maximum, 10, 64)
	if value < min || value > max {
		return fmt.Errorf("须在 %d 到 %d 之间", min, max)
	}
	out.Value = value
	return nil
}

func parseInt32Optional(raw json.RawMessage, spec FieldSpec, out *Optional[int32]) error {
	out.Present = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		out.Null = true
		return nil
	}
	value, err := parseJSONInteger(raw, 32)
	if err != nil {
		return err
	}
	min, _ := strconv.ParseInt(spec.Minimum, 10, 32)
	max, _ := strconv.ParseInt(spec.Maximum, 10, 32)
	if value < min || value > max {
		return fmt.Errorf("须在 %d 到 %d 之间", min, max)
	}
	out.Value = int32(value)
	return nil
}

func parseJSONInteger(raw []byte, bits int) (int64, error) {
	text := string(bytes.TrimSpace(raw))
	if text == "" || strings.ContainsAny(text, ".eE\"") {
		return 0, fmt.Errorf("须为整数")
	}
	value, err := strconv.ParseInt(text, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("须为可表示的整数")
	}
	return value, nil
}

func parseBoolOptional(raw json.RawMessage, out *Optional[bool]) error {
	out.Present = true
	if err := json.Unmarshal(raw, &out.Value); err != nil {
		return fmt.Errorf("须为布尔值")
	}
	return nil
}

func parseTimeOptional(raw json.RawMessage, out *Optional[time.Time]) error {
	out.Present = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		out.Null = true
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("须为 RFC3339 时间或 null")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return fmt.Errorf("须为 RFC3339 时间或 null")
	}
	out.Value = parsed.UTC()
	return nil
}

func parseErrorCodesOptional(raw json.RawMessage, out *Optional[[]int32]) error {
	out.Present = true
	var values []int64
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return fmt.Errorf("须为整数数组")
	}
	out.Value = make([]int32, 0, len(values))
	for _, value := range values {
		if value < 100 || value > 599 {
			return fmt.Errorf("每个错误码须在 100 到 599 之间")
		}
		out.Value = append(out.Value, int32(value))
	}
	return nil
}

func parseRulesOptional(raw json.RawMessage, out *Optional[[]byte]) error {
	out.Present = true
	var rules []TempRule
	if err := json.Unmarshal(raw, &rules); err != nil || rules == nil {
		return fmt.Errorf("须为规则数组")
	}
	for i := range rules {
		if rules[i].ErrorCode < 100 || rules[i].ErrorCode > 599 {
			return fmt.Errorf("第 %d 条规则的 error_code 须在 100 到 599 之间", i+1)
		}
		if rules[i].DurationMinutes <= 0 || int64(rules[i].DurationMinutes) > math.MaxInt32 {
			return fmt.Errorf("第 %d 条规则的 duration_minutes 须为正整数", i+1)
		}
		rules[i].Keywords = cleanStrings(rules[i].Keywords)
		rules[i].Description = strings.TrimSpace(rules[i].Description)
	}
	normalized, err := json.Marshal(rules)
	if err != nil {
		return fmt.Errorf("规则无法编码")
	}
	out.Value = normalized
	return nil
}

func parseProxyOptional(raw json.RawMessage, out *Optional[ProxyBinding]) error {
	out.Present = true
	var binding ProxyBinding
	if err := json.Unmarshal(raw, &binding); err != nil {
		return fmt.Errorf("须为代理绑定对象")
	}
	binding.Mode = strings.TrimSpace(binding.Mode)
	switch binding.Mode {
	case "direct":
		binding.ProxyID, binding.ProxyGroupID = nil, nil
	case "proxy":
		if binding.ProxyID == nil || *binding.ProxyID <= 0 {
			return fmt.Errorf("mode=proxy 需正整数 proxy_id")
		}
		binding.ProxyGroupID = nil
	case "group":
		group := ""
		if binding.ProxyGroupID != nil {
			group = strings.TrimSpace(*binding.ProxyGroupID)
		}
		if group == "" {
			return fmt.Errorf("mode=group 需非空 proxy_group_id")
		}
		binding.ProxyID, binding.ProxyGroupID = nil, &group
	default:
		return fmt.Errorf("mode 须为 direct/proxy/group")
	}
	out.Value = binding
	return nil
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// ApplyCreate 把同一 Mutation 映射到 create 参数；缺席字段保持 SQL 默认语义。
func ApplyCreate(m Mutation, out *admindb.InsertProviderAccountParams) {
	if m.RPMLimit.Present {
		out.RPMLimit = &m.RPMLimit.Value
	}
	if m.TPMLimit.Present {
		out.TPMLimit = &m.TPMLimit.Value
	}
	if m.WindowCostLimitCents.Present {
		out.WindowCostLimitCents = &m.WindowCostLimitCents.Value
	}
	if m.MaxSessions.Present {
		out.MaxSessions = &m.MaxSessions.Value
	}
	if m.DisableCooling.Present {
		out.DisableCooling = &m.DisableCooling.Value
	}
	if m.RefreshLeadSeconds.Present && !m.RefreshLeadSeconds.Null {
		out.RefreshLeadSeconds = &m.RefreshLeadSeconds.Value
	}
	if m.ExpiresAt.Present && !m.ExpiresAt.Null {
		out.ExpiresAt = pgtype.Timestamptz{Time: m.ExpiresAt.Value, Valid: true}
	}
	if m.TLSFingerprintRotate.Present {
		out.TLSFingerprintRotate = &m.TLSFingerprintRotate.Value
	}
	if m.CustomErrorCodesEnabled.Present {
		out.CustomErrorCodesEnabled = &m.CustomErrorCodesEnabled.Value
	}
	if m.CustomErrorCodes.Present {
		out.CustomErrorCodes = m.CustomErrorCodes.Value
	}
	if m.PoolMode.Present {
		out.PoolMode = &m.PoolMode.Value
	}
	if m.TempUnschedulableEnabled.Present {
		out.TempUnschedulableEnabled = &m.TempUnschedulableEnabled.Value
	}
	if m.TempUnschedulableRulesJSON.Present {
		out.TempUnschedulableRulesJSON = m.TempUnschedulableRulesJSON.Value
	}
	if m.ProxyBinding.Present {
		out.ProxyID = m.ProxyBinding.Value.ProxyID
		out.ProxyGroupID = m.ProxyBinding.Value.ProxyGroupID
	}
}

// ApplyUpdate 把同一 Mutation 映射到部分更新参数；只有 Present 字段会设置 Set-flag 或值。
func ApplyUpdate(m Mutation, out *admindb.UpdateAdminProviderAccountParams) {
	if m.RPMLimit.Present {
		out.RPMLimit = &m.RPMLimit.Value
	}
	if m.TPMLimit.Present {
		out.TPMLimit = &m.TPMLimit.Value
	}
	if m.WindowCostLimitCents.Present {
		out.WindowCostLimitCents = &m.WindowCostLimitCents.Value
	}
	if m.MaxSessions.Present {
		out.MaxSessions = &m.MaxSessions.Value
	}
	if m.DisableCooling.Present {
		out.DisableCooling = &m.DisableCooling.Value
	}
	if m.RefreshLeadSeconds.Present {
		out.SetRefreshLeadSeconds = true
		if !m.RefreshLeadSeconds.Null {
			out.RefreshLeadSeconds = &m.RefreshLeadSeconds.Value
		}
	}
	if m.ExpiresAt.Present {
		out.SetExpiresAt = true
		if !m.ExpiresAt.Null {
			out.ExpiresAt = pgtype.Timestamptz{Time: m.ExpiresAt.Value, Valid: true}
		}
	}
	if m.TLSFingerprintRotate.Present {
		out.TLSFingerprintRotate = &m.TLSFingerprintRotate.Value
	}
	if m.CustomErrorCodesEnabled.Present {
		out.CustomErrorCodesEnabled = &m.CustomErrorCodesEnabled.Value
	}
	if m.CustomErrorCodes.Present {
		out.SetCustomErrorCodes, out.CustomErrorCodes = true, m.CustomErrorCodes.Value
	}
	if m.PoolMode.Present {
		out.PoolMode = &m.PoolMode.Value
	}
	if m.TempUnschedulableEnabled.Present {
		out.TempUnschedulableEnabled = &m.TempUnschedulableEnabled.Value
	}
	if m.TempUnschedulableRulesJSON.Present {
		out.SetTempUnschedulableRules = true
		out.TempUnschedulableRulesJSON = m.TempUnschedulableRulesJSON.Value
	}
	if m.ProxyBinding.Present {
		out.SetProxyID, out.SetProxyGroupID = true, true
		out.ProxyID = m.ProxyBinding.Value.ProxyID
		out.ProxyGroupID = m.ProxyBinding.Value.ProxyGroupID
	}
}

// BindingFromColumns 把兼容的两列读模型规范化为单一代理绑定对象。
func BindingFromColumns(proxyID *int64, proxyGroupID *string) ProxyBinding {
	if proxyID != nil {
		return ProxyBinding{Mode: "proxy", ProxyID: proxyID}
	}
	if proxyGroupID != nil && strings.TrimSpace(*proxyGroupID) != "" {
		group := strings.TrimSpace(*proxyGroupID)
		return ProxyBinding{Mode: "group", ProxyGroupID: &group}
	}
	return ProxyBinding{Mode: "direct"}
}
