package channelprobe

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
)

const DefaultSchedulerInterval = time.Minute

var (
	ErrNotConfigured = errors.New("channelprobe: scheduler not configured")
	ErrInvalidInput  = errors.New("channelprobe: invalid scheduler input")
)

type SchedulerTicker interface {
	C() <-chan time.Time
	Stop()
}

type ActiveChannel struct {
	ChannelID string
	Key       channelhealth.ChannelKey
}

type ActiveChannelLister interface {
	ListActiveChannels(context.Context) ([]ActiveChannel, error)
}

type HealthService interface {
	ApplySignal(context.Context, channelhealth.Signal) (channelhealth.Record, error)
}

// RampAdvancer 周期推进处于渐进放量(ramping)的渠道:用已累积的真实流量样本按
// 安全门(够样本 + 失败率低才升,失败则回滚)把放量比例 1%→10%→50%→100%→active。
// channelhealth.Service.AdvanceRamp 实现之。对非 ramping 渠道是 no-op。
// 缺此驱动时渠道冷却后进 ramping 1% 便永久卡死(无人推进);这里补上唯一的驱动者。
type RampAdvancer interface {
	AdvanceRamp(context.Context, channelhealth.ChannelKey) (channelhealth.Record, error)
}

type ProbeResult struct {
	StatusCode        int
	ErrorCode         string
	SafeErrorClass    string
	LocalGatewayError bool
	LatencyMS         int64
	At                time.Time
	RequestID         string
	RateLimitResetAt  *time.Time
}

type ActiveProbe func(context.Context, string) (ProbeResult, error)

type SchedulerConfig struct {
	Channels         ActiveChannelLister
	Health           HealthService
	ActiveProbe      ActiveProbe
	RampAdvancer     RampAdvancer
	ClassifierConfig channelhealth.ClassifierConfig
	Interval         time.Duration
	NewTicker        func(time.Duration) SchedulerTicker
}

type ChannelHealthScheduler struct {
	channels         ActiveChannelLister
	health           HealthService
	activeProbe      ActiveProbe
	rampAdvancer     RampAdvancer
	classifierConfig channelhealth.ClassifierConfig
	interval         time.Duration
	newTicker        func(time.Duration) SchedulerTicker
}

func NewChannelHealthScheduler(cfg SchedulerConfig) *ChannelHealthScheduler {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultSchedulerInterval
	}
	if cfg.NewTicker == nil {
		cfg.NewTicker = func(interval time.Duration) SchedulerTicker {
			return realSchedulerTicker{ticker: time.NewTicker(interval)}
		}
	}
	return &ChannelHealthScheduler{
		channels:         cfg.Channels,
		health:           cfg.Health,
		activeProbe:      cfg.ActiveProbe,
		rampAdvancer:     cfg.RampAdvancer,
		classifierConfig: cfg.ClassifierConfig,
		interval:         cfg.Interval,
		newTicker:        cfg.NewTicker,
	}
}

func (s *ChannelHealthScheduler) Run(ctx context.Context) error {
	// 无合成探测且无 ramp 驱动 = 无事可做,静默退出(保持"未接线即 no-op"语义)。
	if s == nil || (s.activeProbe == nil && s.rampAdvancer == nil) {
		return nil
	}
	if s.channels == nil {
		return ErrNotConfigured
	}
	// 合成探测把结果喂 ApplySignal,故启用探测时必须有 Health;纯 ramp 驱动不需要。
	if s.activeProbe != nil && s.health == nil {
		return ErrNotConfigured
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := s.newTicker(s.interval)
	if ticker == nil {
		return ErrInvalidInput
	}
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
			s.probeOnce(ctx)
		}
	}
}

func (s *ChannelHealthScheduler) probeOnce(ctx context.Context) {
	channels, err := s.channels.ListActiveChannels(ctx)
	if err != nil {
		logIfLive(ctx, "channel probe list active channels failed", err)
		return
	}
	for _, channel := range channels {
		if ctx.Err() != nil {
			return
		}
		channelID, key, ok := normalizeActiveChannel(channel)
		if !ok {
			continue
		}
		// 合成探测(配了 ActiveProbe 才跑):把探测结果喂 ApplySignal,让零流量渠道也能被主动体检。
		if s.activeProbe != nil {
			s.probeChannel(ctx, channelID, key)
		}
		// ramp 推进(配了 RampAdvancer 才跑):用已累积的真实流量样本把 ramping 渠道安全爬升,
		// 对非 ramping 渠道是 no-op。这是渠道冷却后从 1% 放量恢复的唯一驱动者。
		if s.rampAdvancer != nil {
			if _, err := s.rampAdvancer.AdvanceRamp(ctx, key); err != nil {
				logIfLive(ctx, "channel ramp advance failed", err, "channel_id", channelID)
			}
		}
	}
}

// probeChannel 对单个渠道发一次合成探测并把分类结果喂账号健康 FSM。
func (s *ChannelHealthScheduler) probeChannel(ctx context.Context, channelID string, key channelhealth.ChannelKey) {
	result, err := s.activeProbe(ctx, channelID)
	if err != nil {
		logIfLive(ctx, "channel active probe failed", err, "channel_id", channelID)
		return
	}
	classified := channelhealth.ClassifyWithConfig(channelhealth.ClassifierInput{
		StatusCode:        result.StatusCode,
		ErrorCode:         result.ErrorCode,
		SafeErrorClass:    result.SafeErrorClass,
		LocalGatewayError: result.LocalGatewayError,
		LatencyMS:         result.LatencyMS,
	}, s.classifierConfig)
	latencyMS := result.LatencyMS
	if latencyMS < 0 {
		latencyMS = 0
	}
	if _, err := s.health.ApplySignal(ctx, channelhealth.Signal{
		Key:              key,
		Class:            classified.Class,
		StatusCode:       result.StatusCode,
		LatencyMS:        latencyMS,
		At:               result.At,
		RequestID:        result.RequestID,
		RateLimitResetAt: result.RateLimitResetAt,
	}); err != nil {
		logIfLive(ctx, "channel probe apply health signal failed", err, "channel_id", channelID, "class", string(classified.Class))
	}
}

func normalizeActiveChannel(channel ActiveChannel) (string, channelhealth.ChannelKey, bool) {
	key := channel.Key
	if key.ChannelID == "" {
		key.ChannelID = key.StableChannelID()
	}
	if channel.ChannelID != "" {
		key.ChannelID = channel.ChannelID
	}
	if err := key.Validate(); err != nil {
		return "", channelhealth.ChannelKey{}, false
	}
	return key.ChannelID, key, true
}

func logIfLive(ctx context.Context, msg string, err error, args ...any) {
	if err == nil || ctx.Err() != nil {
		return
	}
	args = append(args, "error", err.Error())
	slog.WarnContext(ctx, msg, args...)
}

type realSchedulerTicker struct {
	ticker *time.Ticker
}

func (t realSchedulerTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t realSchedulerTicker) Stop() {
	t.ticker.Stop()
}
