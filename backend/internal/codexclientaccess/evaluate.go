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
// force 旁路 → 黑名单 OR 拒 → 身份候选(官方 UA / 官方 originator / 白名单 AND / app-server 开闸)。
// 版本门(片2f-3)与引擎指纹门(片2f-4)本片留占位默认放行;默认空策略等价于「仅官方客户端放行」。
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

	// 片2f-3:官方候选版本 [min,max] 校验。
	// 片2f-4:引擎指纹 AND 硬门(白名单 SkipEngineFingerprint 例外)。
	return Decision{Allow: true, Reason: reason}
}
