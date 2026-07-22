// Package outboundbody 是网关出站 body 改写的单一入口：汇总 OfficialDirect /
// system 三块伪装 / metadata.user_id 身份改写，避免 clientgate 与 identityRewrite
// 各写一套门控。
//
// 分层约定：
//   - clientgate 只读 claudecodecloak.Enabled() 决定第三方是否放行，不调用 Apply
//   - 热路径唯一调用 Apply / BuildPlan（经 gatewayhttp 薄委托）
//   - system 三块算法在 claudecodecloak；user_id 在 mimicryidentity
//   - gateway.ApplyMimicryPlan 的 SystemRewrite 仍是 binding 级前缀，不承担 OAuth 反转伪装
package outboundbody

import (
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/claudecodecloak"
	"github.com/BloomingProsperity/HUAKAI/internal/officialclient"
)

// Reason 是 clientgate / 审计可稳定引用的放行原因（避免散落魔法串）。
const ReasonAllowBodyCloak = "claude_oauth_body_cloak"

// Plan 描述一次出站 body 应执行哪些变换。零值 = 不改写。
type Plan struct {
	// SkipAll 为 true 时 Apply 原样返回 body 拷贝（OfficialDirect）。
	SkipAll bool
	// SystemCloak 为 true 时施加 claudecodecloak system 三块。
	SystemCloak bool
	// IdentityUserID 为 true 时尝试 mimicryidentity user_id 改写（内部仍 fail-open）。
	IdentityUserID bool

	AccountID         int64
	ExternalAccountID string
	AccountType       string
	ClientSessionID   string
	CLIVersion        string
}

// Input 是 BuildPlan 的入参，全部由调用方从 chatExecution / 请求上下文填好。
type Input struct {
	OfficialDirect    bool
	ProtocolFamily    string
	AccountType       string
	AccountID         int64
	ExternalAccountID string
	ClientSessionID   string
	CLIVersion        string
}

// BuildPlan 据账号与官方直发标志决定出站变换。开关只在此读取一次。
func BuildPlan(in Input) Plan {
	if in.OfficialDirect {
		return Plan{SkipAll: true}
	}
	if !isAnthropicMessagesFamily(in.ProtocolFamily) {
		return Plan{}
	}
	p := Plan{
		AccountID:         in.AccountID,
		ExternalAccountID: in.ExternalAccountID,
		AccountType:       in.AccountType,
		ClientSessionID:   in.ClientSessionID,
		CLIVersion:        in.CLIVersion,
		// Anthropic messages 形态下总是尝试 user_id；内部对非反转/缺 id/关开关 fail-open。
		IdentityUserID: true,
	}
	if officialclient.IsReverseAccountType(in.AccountType) && claudecodecloak.Enabled() {
		p.SystemCloak = true
	}
	return p
}

// ThirdPartyAdmission 供 clientgate 使用：Anthropic 反转号上非官方客户端
// 在 body 伪装开启时是否应 Allow（而非 403）。不碰 body、不调用 Apply。
func ThirdPartyAdmissionAllowed() bool {
	return claudecodecloak.Enabled()
}

func isAnthropicMessagesFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case "anthropic_messages", "anthropic_claude_session":
		return true
	default:
		return false
	}
}
