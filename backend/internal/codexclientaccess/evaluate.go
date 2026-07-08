package codexclientaccess

import "net/http"

// Decision.Reason 取值:供审计日志与 403 拒因归类。
const (
	ReasonForceAllow                = "force_allow"
	ReasonBlacklisted               = "blacklisted"
	ReasonMatchedOfficialUA         = "matched_official_ua"
	ReasonMatchedOfficialOriginator = "matched_official_originator"
	ReasonMatchedWhitelist          = "matched_whitelist"
	ReasonMatchedAppServer          = "matched_app_server"
	ReasonVersionUndetectable       = "version_undetectable"
	ReasonVersionTooLow             = "version_too_low"
	ReasonVersionTooHigh            = "version_too_high"
	ReasonNotMatched                = "not_matched"
)

// Candidate 是一次请求参与 Codex 客户端访问评估的运行时身份候选。
// 指纹门(片2f-4)所需的 Header/BodyFieldNames 到时再加。
type Candidate struct {
	UserAgent  string
	Originator string
}

// CandidateFromRequest 从 HTTP 请求提取 Codex 客户端访问评估需要的字段。
func CandidateFromRequest(r *http.Request) Candidate {
	if r == nil {
		return Candidate{}
	}
	return Candidate{
		UserAgent:  r.UserAgent(),
		Originator: r.Header.Get("originator"),
	}
}

// Decision 是 Codex 客户端访问策略的单次评估结果。
type Decision struct {
	Allow  bool
	Reason string
}

// Evaluate 按全局 Codex 客户端访问策略评估请求身份。门控顺序(每步可短路):
// force 旁路 → 黑名单 OR 拒 → 身份候选(官方 UA / 官方 originator / 白名单 AND / app-server 开闸)
// → 官方候选版本门 → 引擎指纹门占位(片2f-4)。默认空策略 = 仅官方客户端放行。
// 版本门只约束官方候选;白名单和 app-server 候选可能没有可解析引擎版本,整块跳过。
// 真实官方客户端(codex_cli_rs/0.141.0 等)UA 天然可解析,min/max 空时默认行为不变;
// originator 命中但 UA 不带可解析三段版本时拒绝,避免 originator 单因子伪造。
func Evaluate(policy Policy, cand Candidate) Decision {
	if policy.ForceAllow {
		return Decision{Allow: true, Reason: ReasonForceAllow}
	}
	if matchDenyEntries(cand.UserAgent, cand.Originator, policy.Blacklist) {
		return Decision{Allow: false, Reason: ReasonBlacklisted}
	}

	reason := ""
	switch {
	case IsOfficialCodexUserAgent(cand.UserAgent):
		reason = ReasonMatchedOfficialUA
	case IsOfficialCodexOriginator(cand.Originator):
		reason = ReasonMatchedOfficialOriginator
	default:
		if entry, ok := matchClientEntry(cand.UserAgent, cand.Originator, policy.Whitelist); ok {
			// 片2f-4 会用命中条目的 SkipEngineFingerprint 决定是否跳过指纹硬门。
			_ = entry.SkipEngineFingerprint
			reason = ReasonMatchedWhitelist
		} else if policy.AllowAppServer {
			reason = ReasonMatchedAppServer
		} else {
			return Decision{Allow: false, Reason: ReasonNotMatched}
		}
	}

	// 版本门按匹配路径分域(比 sub2 无条件 undetectable 更安全,不误拒已双因子确立身份的官方 UA):
	//  - matched_official_originator:originator 头单因子可伪造,无论是否配版本边界都要求 UA 是可解析
	//    的 codex 形态版本作第二因子(闭合 originator 单因子伪造)。
	//  - matched_official_ua:strict UA 匹配已确立身份,仅当运维配了 min/max 才要求可解析版本;未配
	//    边界时不因头部无三段版本(空格家族 "Codex Desktop"、UA 尾括号兜底等形态)误拒。
	switch reason {
	case ReasonMatchedOfficialOriginator:
		ver, ok := ParseEngineVersion(cand.UserAgent)
		if !ok {
			return Decision{Allow: false, Reason: ReasonVersionUndetectable}
		}
		if bound := versionBoundReason(ver, policy); bound != "" {
			return Decision{Allow: false, Reason: bound}
		}
	case ReasonMatchedOfficialUA:
		if policy.MinVersion != "" || policy.MaxVersion != "" {
			ver, ok := ParseEngineVersion(cand.UserAgent)
			if !ok {
				return Decision{Allow: false, Reason: ReasonVersionUndetectable}
			}
			if bound := versionBoundReason(ver, policy); bound != "" {
				return Decision{Allow: false, Reason: bound}
			}
		}
	}

	// 片2f-4:引擎指纹 AND 硬门(白名单 SkipEngineFingerprint 例外)。
	return Decision{Allow: true, Reason: reason}
}

// versionBoundReason 对已解析版本按 [min,max] 判定越界拒因;都在界内返回空串。
func versionBoundReason(ver string, policy Policy) string {
	if policy.MinVersion != "" && CompareVersions(ver, policy.MinVersion) < 0 {
		return ReasonVersionTooLow
	}
	if policy.MaxVersion != "" && CompareVersions(ver, policy.MaxVersion) > 0 {
		return ReasonVersionTooHigh
	}
	return ""
}
