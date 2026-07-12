// Package quotaprobe 周期采集上游 OAuth 账号的只读配额窗口。
package quotaprobe

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
)

const (
	DefaultInterval = 30 * time.Minute
	probeTimeout    = 20 * time.Second
	maxJitter       = 750 * time.Millisecond
)

type Account struct {
	TenantID          int64
	ProviderAccountID int64
}

type AccountLister interface {
	ListQuotaProbeAccounts(context.Context) ([]Account, error)
}

type CredentialVault interface {
	Resolve(context.Context, int64, int64) (provider.Credential, provider.AccountInfo, error)
}

type SettingsReader interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type UsageWindow struct {
	Utilization *float64
	ResetsAt    *time.Time
}

type UsageSnapshot struct {
	FiveHour UsageWindow
	SevenDay UsageWindow
}

type UsageFetcher interface {
	FetchUsage(context.Context, int64, string) (UsageSnapshot, error)
}

type WorkerConfig struct {
	Accounts AccountLister
	Vault    CredentialVault
	Fetcher  UsageFetcher
	Store    rate.SessionWindowStore
	Settings SettingsReader
	Logger   *slog.Logger
	Now      func() time.Time
	Jitter   func(Account, time.Time) time.Duration
	Wait     func(context.Context, time.Duration) bool
}

type Worker struct {
	accounts AccountLister
	vault    CredentialVault
	fetcher  UsageFetcher
	store    rate.SessionWindowStore
	settings SettingsReader
	logger   *slog.Logger
	now      func() time.Time
	jitter   func(Account, time.Time) time.Duration
	wait     func(context.Context, time.Duration) bool
}

func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Jitter == nil {
		cfg.Jitter = accountJitter
	}
	if cfg.Wait == nil {
		cfg.Wait = waitContext
	}
	return &Worker{
		accounts: cfg.Accounts,
		vault:    cfg.Vault,
		fetcher:  cfg.Fetcher,
		store:    cfg.Store,
		settings: cfg.Settings,
		logger:   cfg.Logger,
		now:      cfg.Now,
		jitter:   cfg.Jitter,
		wait:     cfg.Wait,
	}
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go w.loop(ctx)
}

func (w *Worker) loop(ctx context.Context) {
	for {
		_, interval := w.runtimeConfig(ctx)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			w.RunOnce(ctx)
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) {
	if w == nil {
		return
	}
	enabled, _ := w.runtimeConfig(ctx)
	if !enabled {
		return
	}
	if w.accounts == nil || w.vault == nil || w.fetcher == nil || w.store == nil {
		w.logger.WarnContext(ctx, "quota probe 依赖未完整注入")
		return
	}
	accounts, err := w.accounts.ListQuotaProbeAccounts(ctx)
	if err != nil {
		w.logger.WarnContext(ctx, "quota probe 列账号失败", "error", err.Error())
		return
	}
	for _, account := range accounts {
		if ctx.Err() != nil {
			return
		}
		w.probeAccount(ctx, account)
	}
}

func (w *Worker) probeAccount(ctx context.Context, account Account) {
	credential, info, err := w.vault.Resolve(ctx, account.TenantID, account.ProviderAccountID)
	if err != nil {
		w.logger.WarnContext(ctx, "quota probe 解析凭据失败", "provider_account_id", account.ProviderAccountID, "error", err.Error())
		return
	}
	if info.Platform != "anthropic" || info.AccountType != "claude_ai_oauth" || credential.Type != provider.CredentialTypeOAuthAccessToken {
		return
	}
	if !hasProfileScope(info.OAuthScope) {
		w.logger.InfoContext(ctx, "quota probe 跳过账号",
			"provider_account_id", account.ProviderAccountID,
			"skip_reason", "missing_profile_scope",
		)
		return
	}
	accessToken := strings.TrimSpace(credential.Value)
	if accessToken == "" {
		w.logger.WarnContext(ctx, "quota probe 跳过账号", "provider_account_id", account.ProviderAccountID, "skip_reason", "missing_access_token")
		return
	}
	if delay := w.jitter(account, w.now()); delay > 0 && !w.wait(ctx, delay) {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	snapshot, err := w.fetcher.FetchUsage(probeCtx, account.ProviderAccountID, accessToken)
	cancel()
	if err != nil {
		w.logger.WarnContext(ctx, "quota probe 上游探测失败", "provider_account_id", account.ProviderAccountID, "error", err.Error())
		return
	}
	update := sessionWindowUpdate(account.ProviderAccountID, snapshot, w.now())
	if !sessionWindowUpdateHasValues(update) {
		w.logger.WarnContext(ctx, "quota probe 响应没有有效窗口", "provider_account_id", account.ProviderAccountID)
		return
	}
	if err := w.store.UpdateProviderAccountSessionWindows(ctx, update); err != nil {
		w.logger.WarnContext(ctx, "quota probe 写窗口失败", "provider_account_id", account.ProviderAccountID, "error", err.Error())
	}
}

func (w *Worker) runtimeConfig(ctx context.Context) (bool, time.Duration) {
	enabled := true
	minutes := int(DefaultInterval / time.Minute)
	if w != nil && w.settings != nil {
		if setting, err := w.settings.Get(ctx, platformsettings.KeyQuotaProbeEnabled); err == nil {
			enabled = strings.EqualFold(strings.TrimSpace(setting.Value), "true")
		}
		if setting, err := w.settings.Get(ctx, platformsettings.KeyQuotaProbeIntervalMinutes); err == nil {
			if parsed, parseErr := strconv.Atoi(strings.TrimSpace(setting.Value)); parseErr == nil {
				minutes = parsed
			}
		}
	}
	if minutes < platformsettings.MinQuotaProbeIntervalMinutes {
		minutes = platformsettings.MinQuotaProbeIntervalMinutes
	}
	if minutes > platformsettings.MaxQuotaProbeIntervalMinutes {
		minutes = platformsettings.MaxQuotaProbeIntervalMinutes
	}
	return enabled, time.Duration(minutes) * time.Minute
}

func hasProfileScope(raw string) bool {
	for _, scope := range strings.FieldsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || r == ','
	}) {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope == "profile" || strings.HasSuffix(scope, ":profile") {
			return true
		}
	}
	return false
}

func sessionWindowUpdate(accountID int64, snapshot UsageSnapshot, now time.Time) rate.SessionWindowUpdate {
	update := rate.SessionWindowUpdate{ProviderAccountID: accountID}
	setUsageWindow(&update.Window5hStart, &update.Window5hEnd, &update.Window5hStatus, &update.Window5hUtilization, snapshot.FiveHour, 5*time.Hour, now)
	setUsageWindow(&update.Window7dStart, &update.Window7dEnd, &update.Window7dStatus, &update.Window7dUtilization, snapshot.SevenDay, 7*24*time.Hour, now)
	return update
}

func setUsageWindow(start, end **time.Time, status **string, utilization **float64, window UsageWindow, duration time.Duration, now time.Time) {
	if window.ResetsAt == nil || window.ResetsAt.IsZero() || window.Utilization == nil {
		return
	}
	value := *window.Utilization
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return
	}
	windowEnd := window.ResetsAt.UTC()
	windowStart := windowEnd.Add(-duration)
	windowStatus := "active"
	if windowEnd.Before(now.UTC()) {
		windowStatus = "expired"
	}
	*start = &windowStart
	*end = &windowEnd
	*status = &windowStatus
	*utilization = &value
}

func sessionWindowUpdateHasValues(update rate.SessionWindowUpdate) bool {
	return update.Window5hEnd != nil || update.Window7dEnd != nil
}

func accountJitter(account Account, now time.Time) time.Duration {
	value := uint64(account.ProviderAccountID) ^ uint64(now.UnixNano())
	value ^= value >> 33
	value *= 0xff51afd7ed558ccd
	value ^= value >> 33
	return time.Duration(value % uint64(maxJitter))
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
