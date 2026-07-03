package mimicry

import (
	"context"
	"log/slog"
	"time"
)

// egressSidecarComponent 是 go↔rust 出口边界全部结构化日志的固定 component 值:
// 运维按 component=egress_sidecar 即可过滤出出口衔接的所有事件,不与其它子系统混。
const egressSidecarComponent = "egress_sidecar"

// 出口边界的分层 phase(把 go↔rust 衔接拆细:握手层 / 帧传输层 各自独立标记,
// 一条日志一眼看出卡在哪一层,便于运维定位与后续维护)。
const (
	sidecarPhaseDial         = "dial"          // 握手层:拨本地 unix socket
	sidecarPhaseWriteControl = "write_control" // 帧传输层:发控制帧
	sidecarPhaseReadAck      = "read_ack"      // 帧传输层:收 ACK 帧
	sidecarPhaseEstablished  = "established"   // 握手层:隧道建立成功
	sidecarPhaseRejected     = "rejected"      // 握手层:sidecar 受理连接但显式拒绝请求
)

// 出口错误分类:与 transport factory 的 TransportErrorClass 字符串同口径,跨层一致。
const (
	sidecarErrClassUnavailable = "sidecar_unavailable"         // 拨号/发帧/收帧失败:sidecar 进程不可用
	sidecarErrClassProfile     = "sidecar_profile_unavailable" // sidecar 受理了连接但拒绝请求(profile 等)
)

// sidecarDialObserver 记录一次 go↔rust 出口拨号的分层可观测事件。此前 DialTLS 是黑盒
// (fail-closed 静默返回 err,运维看不到出口降级);这里把拨号/发帧/收帧/建立/拒绝分层
// 结构化输出。任何失败一律 Warn 可见(出口降级=拒服务,必须看得见),成功建立走 Debug
// 不刷屏。logger 为 nil 时兜底 slog.Default()(片 D 门面:统一 JSON + /loglevel + 脱敏)。
type sidecarDialObserver struct {
	logger *slog.Logger
	// correlationID 随控制帧过河给 Rust sidecar,令 go↔rust 两侧日志用同一 id 可关联
	// (跨边界追一次出口握手)。片 G 全链 request_id 落地后可由真 request_id 播种。
	correlationID string
	profileID     string
	host          string
	port          int
	forceH1       bool
	proxied       bool // 只记"是否走代理";代理凭据(Username/Password)绝不进日志
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

// rejected 记录 sidecar 受理了连接但显式拒绝请求(profile 不受理等),Warn 级。
func (o *sidecarDialObserver) rejected(ctx context.Context, reason string) {
	recordEgressDialResult(egressDialResultRejected)
	attrs := append(o.base(sidecarPhaseRejected), "error_class", sidecarErrClassProfile, "reject_reason", reason)
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
