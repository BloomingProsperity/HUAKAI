// Package clientgate 决定入站请求的客户端准入:对反转/订阅号按 vendor 默认策略或账号级
// CodexCLIOnly 校验官方客户端;codex 反转号 + CodexCLIOnly 开启时改走 codexclientaccess 全局
// 加固层(黑白名单/app-server/force 策略)。抽出为独立子包便于纯决策单测,gatewayhttp 侧只
// 负责按判决释放预扣 + 渲染 403。
package clientgate

import (
	"context"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/codexclientaccess"
	"github.com/BloomingProsperity/HUAKAI/internal/officialclient"
	"github.com/BloomingProsperity/HUAKAI/internal/outboundbody"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// ReasonOfficialClientRequired 是片2e 官方客户端门拒绝时的审计/错误原因标签。
const ReasonOfficialClientRequired = "official_client_required"

// Decision 是 clientgate 对调用方的封闭结果。S1 不定义 RewriteRequired：
// Anthropic 反转账号:真 Claude Code→OfficialDirect；body 伪装开时第三方→Allow；伪装关时 Reject。其它账号可 Allow。
type Decision string

const (
	DecisionAllow          Decision = "allow"
	DecisionOfficialDirect Decision = "official_direct"
	DecisionReject         Decision = "reject"
)

type Result struct {
	Decision Decision
	Reason   string
	Body     []byte
}

// SettingsGetter 是 clientgate 读取平台设置的最小接口;*platformsettings.Service 及 gatewayhttp
// 的 platformSettingsReader 均满足。nil 表示设置未接线,调用方回退默认(仅官方客户端放行)。
type SettingsGetter interface {
	Get(ctx context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

// CodexAccessApplies 判定本次请求是否走 codex 全局加固层:openai/codex/chatgpt 反转号 +
// 账号 CodexCLIOnly 开启才生效;其余走片2e 原官方客户端门。
func CodexAccessApplies(accountType, platform string, codexCLIOnly bool) bool {
	return officialclient.IsReverseAccountType(accountType) &&
		codexclientaccess.IsCodexVendor(platform) &&
		codexCLIOnly
}

// Decide 执行既有无 body 客户端决策；Anthropic 严格直发由 DecideWithBody 在它之前处理。
func Decide(ctx context.Context, getter SettingsGetter, accountType, platform string, codexCLIOnly bool, r *http.Request) (bool, string) {
	if CodexAccessApplies(accountType, platform, codexCLIOnly) {
		decision := codexclientaccess.Evaluate(LoadCodexPolicy(ctx, getter), codexclientaccess.CandidateFromRequest(r))
		if decision.Allow {
			return false, ""
		}
		return true, "codex_client_access:" + decision.Reason
	}

	identity, _ := clientid.Detect(clientid.SignalFromRequest(r))
	if reject, _ := officialclient.GateDecision(accountType, platform, identity, codexCLIOnly); !reject {
		return false, ""
	}
	return true, ReasonOfficialClientRequired
}

// DecideWithBody 在既有策略之前为 Anthropic 反转账号启用严格官方直发。
// 真 Claude Code 形态 → OfficialDirect(跳过 body 伪装)。
// 非官方客户端:
//   - HUAKAI_CLAUDE_OAUTH_BODY_CLOAK 默认开 → Allow,由出站 claudecodecloak 伪装 body
//   - 显式 false → 保持 Reject(仅官方客户端,历史严格门)
func DecideWithBody(ctx context.Context, getter SettingsGetter, accountType, platform string, codexCLIOnly bool, r *http.Request, body []byte) Result {
	if officialclient.RequiresStrictAnthropicDirect(accountType, platform, codexCLIOnly) {
		strict := officialclient.DecideAnthropicOfficialDirect(r, body)
		if strict.Decision == officialclient.DirectDecisionOfficialDirect {
			return Result{Decision: DecisionOfficialDirect, Body: strict.Body}
		}
		// 兼容伪装模式:放行第三方;出站 body 由 outboundbody 统一改写。
		if outboundbody.ThirdPartyAdmissionAllowed() {
			return Result{Decision: DecisionAllow, Reason: outboundbody.ReasonAllowBodyCloak}
		}
		return Result{Decision: DecisionReject, Reason: ReasonOfficialClientRequired}
	}
	if reject, reason := Decide(ctx, getter, accountType, platform, codexCLIOnly, r); reject {
		return Result{Decision: DecisionReject, Reason: reason}
	}
	return Result{Decision: DecisionAllow}
}

// LoadCodexPolicy 从平台设置读取 codex 全局加固层策略快照。任一键读失败/解析失败/校验失败 →
// 该字段回退默认(名单/信号空、版本无界、app-server 关、force 关)= 仅官方客户端放行,语义同
// 片2e;官方判定集合按 strict 前缀 + originator 重定义(见 codexclientaccess,与 clientid 宽松
// 判定不逐输入等价)。整体绝不因读取失败误放行或误拒。版本/指纹字段本片先填充,由片2f-3/2f-4 消费。
func LoadCodexPolicy(ctx context.Context, getter SettingsGetter) codexclientaccess.Policy {
	var policy codexclientaccess.Policy

	if value, ok := settingValue(ctx, getter, platformsettings.KeyCodexClientAccessBlacklist); ok {
		if entries, err := codexclientaccess.ParseAllowedClientEntries(value); err == nil {
			if err := codexclientaccess.ValidateBlacklistEntries(entries); err == nil {
				policy.Blacklist = entries
			}
		}
	}
	if value, ok := settingValue(ctx, getter, platformsettings.KeyCodexClientAccessWhitelist); ok {
		if entries, err := codexclientaccess.ParseAllowedClientEntries(value); err == nil {
			if err := codexclientaccess.ValidateWhitelistEntries(entries); err == nil {
				policy.Whitelist = entries
			}
		}
	}
	if value, ok := settingValue(ctx, getter, platformsettings.KeyCodexClientAccessMinVersion); ok {
		value = strings.TrimSpace(value)
		if value == "" || codexclientaccess.ValidateVersionString(value) == nil {
			policy.MinVersion = value
		}
	}
	if value, ok := settingValue(ctx, getter, platformsettings.KeyCodexClientAccessMaxVersion); ok {
		value = strings.TrimSpace(value)
		if value == "" || codexclientaccess.ValidateVersionString(value) == nil {
			policy.MaxVersion = value
		}
	}
	if value, ok := settingValue(ctx, getter, platformsettings.KeyCodexClientAccessAllowAppServer); ok {
		policy.AllowAppServer = strings.TrimSpace(value) == "true"
	}
	if value, ok := settingValue(ctx, getter, platformsettings.KeyCodexClientAccessEngineFingerprintSignals); ok {
		if signals, err := codexclientaccess.ParseEngineFingerprintSignals(value); err == nil {
			policy.EngineFingerprintSignals = signals
		}
	}
	if value, ok := settingValue(ctx, getter, platformsettings.KeyCodexClientAccessForceAllow); ok {
		policy.ForceAllow = strings.TrimSpace(value) == "true"
	}

	return policy
}

func settingValue(ctx context.Context, getter SettingsGetter, key platformsettings.SettingKey) (string, bool) {
	if getter == nil {
		return "", false
	}
	stored, err := getter.Get(ctx, key)
	if err != nil {
		return "", false
	}
	return stored.Value, true
}
