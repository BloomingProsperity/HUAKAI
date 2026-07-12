package usersession

import (
	"context"
	"net"
	"strings"
)

type DriftLevel string

const (
	DriftNone   DriftLevel = "none"
	DriftLow    DriftLevel = "low"
	DriftMedium DriftLevel = "medium"
	DriftHigh   DriftLevel = "high"
)

type DriftResult struct {
	Level  DriftLevel `json:"level"`
	Reason string     `json:"reason,omitempty"`
}

// SessionDriftEvent 一次会话上下文漂移的观测事件。Medium(仅 IP 变)/Low(仅 UA 变)
// 是 token 被盗用/重放最常见形态的弱信号, 只算不消费则检测全盲; High 也进同一信号流
// (撤销行为不变), 供告警侧看到完整梯度。IP/UA 记 class 而非原文 (不落 PII 进日志)。
type SessionDriftEvent struct {
	TenantID   int64
	UserID     int64
	FamilyID   string
	Level      DriftLevel
	Reason     string
	Source     string // "validate" / "refresh"
	IPClass    string
	UAClass    string
	BaselineIP string // 家族基线 IP class
	BaselineUA string // 家族基线 UA class
}

// DriftObserver 消费漂移事件 (结构化日志/指标/告警)。纯观测: 不得阻塞、不返回错误,
// 对 Validate/Refresh 的放行与拒绝零影响。
type DriftObserver interface {
	ObserveSessionDrift(ctx context.Context, ev SessionDriftEvent)
}

func DetectDrift(family SessionFamily, ip, userAgent string) DriftResult {
	ipClass := IPClass(ip)
	uaClass := UserAgentClass(userAgent)
	baselineIP := strings.TrimSpace(family.IPBaseline)
	baselineUA := ""
	if family.DeviceInfo != nil {
		if v, ok := family.DeviceInfo["ua_class"].(string); ok {
			baselineUA = v
		}
	}
	ipChanged := baselineIP != "" && ipClass != "" && baselineIP != ipClass
	uaChanged := baselineUA != "" && uaClass != "" && baselineUA != uaClass
	switch {
	case ipChanged && uaChanged:
		return DriftResult{Level: DriftHigh, Reason: "ip_and_ua_changed"}
	case ipChanged:
		return DriftResult{Level: DriftMedium, Reason: "ip_changed"}
	case uaChanged:
		return DriftResult{Level: DriftLow, Reason: "ua_changed"}
	default:
		return DriftResult{Level: DriftNone}
	}
}

func IPClass(ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return ""
	}
	if v4 := parsed.To4(); v4 != nil {
		return net.IPv4(v4[0], v4[1], 0, 0).String() + "/16"
	}
	v6 := parsed.To16()
	if v6 == nil {
		return ""
	}
	return net.IP(v6[:8]).String() + "::/64"
}

func UserAgentClass(ua string) string {
	ua = strings.ToLower(strings.TrimSpace(ua))
	switch {
	case ua == "":
		return ""
	case strings.Contains(ua, "firefox"):
		return "firefox"
	case strings.Contains(ua, "edg/"):
		return "edge"
	case strings.Contains(ua, "chrome"):
		return "chrome"
	case strings.Contains(ua, "safari"):
		return "safari"
	default:
		fields := strings.Fields(ua)
		if len(fields) == 0 {
			return "unknown"
		}
		return fields[0]
	}
}
