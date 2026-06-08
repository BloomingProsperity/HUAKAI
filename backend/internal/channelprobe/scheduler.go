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
	ClassifierConfig channelhealth.ClassifierConfig
	Interval         time.Duration
	NewTicker        func(time.Duration) SchedulerTicker
}

type ChannelHealthScheduler struct {
	channels         ActiveChannelLister
	health           HealthService
	activeProbe      ActiveProbe
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
		classifierConfig: cfg.ClassifierConfig,
		interval:         cfg.Interval,
		newTicker:        cfg.NewTicker,
	}
}

func (s *ChannelHealthScheduler) Run(ctx context.Context) error {
	if s == nil || s.activeProbe == nil {
		return nil
	}
	if s.channels == nil || s.health == nil {
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
		result, err := s.activeProbe(ctx, channelID)
		if err != nil {
			logIfLive(ctx, "channel active probe failed", err, "channel_id", channelID)
			continue
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
		_, err = s.health.ApplySignal(ctx, channelhealth.Signal{
			Key:              key,
			Class:            classified.Class,
			StatusCode:       result.StatusCode,
			LatencyMS:        latencyMS,
			At:               result.At,
			RequestID:        result.RequestID,
			RateLimitResetAt: result.RateLimitResetAt,
		})
		if err != nil {
			logIfLive(ctx, "channel probe apply health signal failed", err, "channel_id", channelID, "class", string(classified.Class))
		}
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
