package mimicry

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const egressSidecarComponent = "egress_sidecar"

const (
	sidecarPhaseDial         = "dial"
	sidecarPhaseWriteControl = "write_control"
	sidecarPhaseReadAck      = "read_ack"
	sidecarPhaseEstablished  = "established"
	sidecarPhaseRejected     = "rejected"
)

const (
	sidecarErrClassUnavailable = "sidecar_unavailable"
	sidecarErrClassProfile     = "sidecar_profile_invalid"
	sidecarErrClassProxy       = "proxy_failure"
	sidecarErrClassUpstream    = "upstream_failure"
	sidecarErrClassRequest     = "request_invalid"
)

type sidecarDialObserver struct {
	logger        *slog.Logger
	correlationID string
	profileID     string
	host          string
	port          int
	forceH1       bool
	proxied       bool
	start         time.Time
}

func newSidecarDialObserver(logger *slog.Logger, correlationID, host string, port int, profileID string, forceH1, proxied bool, start time.Time) *sidecarDialObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &sidecarDialObserver{
		logger:        logger,
		correlationID: correlationID,
		profileID:     profileID,
		host:          host,
		port:          port,
		forceH1:       forceH1,
		proxied:       proxied,
		start:         start,
	}
}

// base 返回每条出口日志都带的公共字段。correlation_id 是跨边界关联键(与 Rust sidecar
// tracing 同 id);target_host/port 是上游 API 地址(非机密);proxied 只记布尔——绝不记
// 代理 host/凭据,防出口代理密码泄进日志。
func (o *sidecarDialObserver) base(phase string) []any {
	return []any{
		"component", egressSidecarComponent,
		"phase", phase,
		"correlation_id", o.correlationID,
		"profile_id", o.profileID,
		"target_host", o.host,
		"target_port", o.port,
		"force_h1", o.forceH1,
		"proxied", o.proxied,
		"elapsed_ms", time.Since(o.start).Milliseconds(),
	}
}

// failed 记录拨号/发帧/收帧任一层的失败(sidecar 不可用),Warn 级——这是 fail-closed
// 降级点,出口这一刻转不出去,运维必须立刻看见。同点递增 A2 指标(result 由 phase 映射),
// 保证同一次失败在日志(phase)与指标(result)两侧口径一致、永不背离。
func (o *sidecarDialObserver) failed(ctx context.Context, phase string, err error) {
	recordEgressDialResult(egressDialResultForPhase(phase))
	attrs := append(o.base(phase), "error_class", sidecarErrClassUnavailable, "error", sidecarErrText(err))
	o.logger.WarnContext(ctx, "出口 sidecar 衔接失败(fail-closed,不回退直连)", attrs...)
}

func (o *sidecarDialObserver) rejected(ctx context.Context, err error) {
	recordEgressDialResult(egressDialResultRejected)
	class, code := sidecarRejectClass(err)
	attrs := append(o.base(sidecarPhaseRejected), "error_class", class, "error_code", code, "reject_reason", sidecarErrText(err))
	o.logger.WarnContext(ctx, "出口 sidecar 拒绝拨号请求", attrs...)
}

// established 记录隧道建立成功,Debug 级(热路径不刷屏)。control_frame_bytes=控制帧字节数,
// 给帧传输层一个真实观测量。同点递增 A2 成功计数(result=ok),作为出口成功率分子。
func (o *sidecarDialObserver) established(ctx context.Context, controlFrameBytes int) {
	recordEgressDialResult(egressDialResultOK)
	attrs := append(o.base(sidecarPhaseEstablished), "control_frame_bytes", controlFrameBytes)
	o.logger.DebugContext(ctx, "出口 sidecar 隧道建立", attrs...)
}

func sidecarErrText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func sidecarRejectClass(err error) (string, string) {
	var sidecarErr *SidecarError
	if !errors.As(err, &sidecarErr) {
		return sidecarErrClassUnavailable, ""
	}
	switch sidecarErr.Code {
	case SidecarErrorProfileUnknown, SidecarErrorProfileInvalid:
		return sidecarErrClassProfile, sidecarErr.Code
	case SidecarErrorProxyInvalid, SidecarErrorProxyConnect:
		return sidecarErrClassProxy, sidecarErr.Code
	case SidecarErrorUpstreamDNS, SidecarErrorConnectionRefused, SidecarErrorNetworkUnreachable,
		SidecarErrorUpstreamConnect, SidecarErrorUpstreamTimeout, SidecarErrorTLSHandshake:
		return sidecarErrClassUpstream, sidecarErr.Code
	case SidecarErrorTargetInvalid:
		return sidecarErrClassRequest, sidecarErr.Code
	default:
		return sidecarErrClassUnavailable, sidecarErr.Code
	}
}
