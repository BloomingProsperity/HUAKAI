// Package quotaprobe 周期采集上游 OAuth 账号的只读配额窗口。
package quotaprobe

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

const (
	DefaultInterval  = 30 * time.Minute
	probeTimeout     = 20 * time.Second
	maxJitter        = 750 * time.Millisecond
	triggerQueueSize = 128
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

type LeaderLease interface {
	TryAcquire(context.Context) (bool, func(), error)
}

type SubscriptionRecorder interface {
	RecordSubscriptionObservation(context.Context, int64, int64, int64, int32, subscriptionprofile.Observation) (subscriptionprofile.Observation, error)
}

type WorkerConfig struct {
	Accounts      AccountLister
	Vault         CredentialVault
	Fetcher       UsageFetcher
	Store         rate.SessionWindowStore
	FactStore     accountquota.Store
	Subscriptions SubscriptionRecorder
	Adapters      []VendorAdapter
	Settings      SettingsReader
	LeaderLease   LeaderLease
	Logger        *slog.Logger
	Now           func() time.Time
	Jitter        func(Account, time.Time) time.Duration
	Wait          func(context.Context, time.Duration) bool
}

type Worker struct {
	accounts      AccountLister
	vault         CredentialVault
	fetcher       UsageFetcher
	store         rate.SessionWindowStore
	factStore     accountquota.Store
	subscriptions SubscriptionRecorder
	adapters      []VendorAdapter
	settings      SettingsReader
	leaderLease   LeaderLease
	logger        *slog.Logger
	now           func() time.Time
	jitter        func(Account, time.Time) time.Duration
	wait          func(context.Context, time.Duration) bool
	mu            sync.Mutex
	done          chan struct{}
	triggers      chan Account
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
		accounts:      cfg.Accounts,
		vault:         cfg.Vault,
		fetcher:       cfg.Fetcher,
		store:         cfg.Store,
		factStore:     cfg.FactStore,
		subscriptions: cfg.Subscriptions,
		adapters:      append([]VendorAdapter(nil), cfg.Adapters...),
		settings:      cfg.Settings,
		leaderLease:   cfg.LeaderLease,
		logger:        cfg.Logger,
		now:           cfg.Now,
		jitter:        cfg.Jitter,
		wait:          cfg.Wait,
		triggers:      make(chan Account, triggerQueueSize),
	}
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	if w.done != nil {
		w.mu.Unlock()
		return
	}
	done := make(chan struct{})
	w.done = done
	w.mu.Unlock()
	go func() {
		defer close(done)
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			w.loop(ctx)
		}()
		go func() {
			defer group.Done()
			w.triggerLoop(ctx)
		}()
		group.Wait()
	}()
}

// NotifyAccountActivated 把新建或刚轮换的账号放入即时探测队列。队列满时不阻塞
// 导入请求，原有周期扫描仍会补做该账号。
func (w *Worker) NotifyAccountActivated(tenantID, providerAccountID int64) {
	if w == nil || w.triggers == nil || tenantID <= 0 || providerAccountID <= 0 {
		return
	}
	select {
	case w.triggers <- Account{TenantID: tenantID, ProviderAccountID: providerAccountID}:
	default:
		logger := w.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("quota probe 即时队列已满，等待周期扫描", "provider_account_id", providerAccountID)
	}
}

func (w *Worker) triggerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case account := <-w.triggers:
			w.runTriggeredAccount(ctx, account)
		}
	}
}

func (w *Worker) runTriggeredAccount(ctx context.Context, account Account) {
	enabled, _ := w.runtimeConfig(ctx)
	if !enabled || w.accounts == nil || w.vault == nil {
		return
	}
	release, ok := w.acquireLease(ctx)
	if !ok {
		return
	}
	defer release()
	w.probeAccount(ctx, account)
}

// Wait 等待后台循环退出。调用方应先取消传给 Start 的 context。
func (w *Worker) Wait(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	done := w.done
	w.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) loop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		w.RunOnce(ctx)
		_, interval := w.runtimeConfig(ctx)
		if !w.wait(ctx, interval) {
			return
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
	release, ok := w.acquireLease(ctx)
	if !ok {
		return
	}
	defer release()
	if w.accounts == nil || w.vault == nil {
		w.logger.WarnContext(ctx, "quota probe 依赖未完整注入", "error_class", ErrorClassDependencyNotConfigured)
		return
	}
	accounts, err := w.accounts.ListQuotaProbeAccounts(ctx)
	if err != nil {
		w.logger.WarnContext(ctx, "quota probe 列账号失败", "error_class", ErrorClassDatabaseReadFailed)
		return
	}
	for _, account := range accounts {
		if ctx.Err() != nil {
			return
		}
		w.probeAccount(ctx, account)
	}
}

func (w *Worker) acquireLease(ctx context.Context) (func(), bool) {
	if w.leaderLease == nil {
		return func() {}, true
	}
	acquired, release, err := w.leaderLease.TryAcquire(ctx)
	if err != nil {
		w.logger.WarnContext(ctx, "quota probe 获取多副本租约失败", "error_class", ErrorClassCoordinationDependencyFailed)
		return nil, false
	}
	if !acquired {
		return nil, false
	}
	if release == nil {
		w.logger.WarnContext(ctx, "quota probe 多副本租约返回无效释放函数", "error_class", ErrorClassCoordinationContractInvalid)
		return nil, false
	}
	return release, true
}

func (w *Worker) probeAccount(ctx context.Context, account Account) {
	credential, info, err := w.vault.Resolve(ctx, account.TenantID, account.ProviderAccountID)
	if err != nil {
		w.logger.WarnContext(ctx, "quota probe 解析凭据失败", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassCredentialResolutionFailed)
		return
	}
	if info.Platform != "anthropic" || info.AccountType != "claude_ai_oauth" || credential.Type != provider.CredentialTypeOAuthAccessToken {
		w.probeVendorAccount(ctx, account, credential, info)
		return
	}
	if w.fetcher == nil || w.store == nil {
		w.logger.WarnContext(ctx, "quota probe Claude 依赖未完整注入", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassDependencyNotConfigured)
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
		errorClass := usageErrorClass(err)
		w.recordFailedObservation(ctx, account, errorClass)
		w.logger.WarnContext(ctx, "quota probe 上游探测失败", "provider_account_id", account.ProviderAccountID, "error_class", errorClass)
		return
	}
	update := sessionWindowUpdate(account.ProviderAccountID, snapshot, w.now())
	if !sessionWindowUpdateHasValues(update) {
		w.recordFailedObservation(ctx, account, ErrorClassUpstreamResponseIncomplete)
		w.logger.WarnContext(ctx, "quota probe 响应没有有效窗口", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassUpstreamResponseIncomplete)
		return
	}
	observedAt := w.now().UTC()
	update.ObservedAt = &observedAt
	update.ObservationSource = rate.QuotaSnapshotSourceUsageEndpoint
	update.ObservationOutcome = quotaObservationOutcome(update)
	if err := w.store.UpdateProviderAccountSessionWindows(ctx, update); err != nil {
		w.logger.WarnContext(ctx, "quota probe 写窗口失败", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassDatabaseWriteFailed)
	}
	w.writeAnthropicFacts(ctx, account, snapshot, observedAt)
}

func (w *Worker) probeVendorAccount(ctx context.Context, account Account, credential provider.Credential, info provider.AccountInfo) {
	if w.factStore == nil {
		return
	}
	var adapter VendorAdapter
	for _, candidate := range w.adapters {
		if candidate != nil && candidate.Supports(credential, info) {
			adapter = candidate
			break
		}
	}
	if adapter == nil {
		return
	}
	if delay := w.jitter(account, w.now()); delay > 0 && !w.wait(ctx, delay) {
		return
	}
	observedAt := w.now().UTC()
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	result, err := adapter.Fetch(probeCtx, account.ProviderAccountID, credential, info, observedAt)
	cancel()
	snapshot := accountquota.Snapshot{TenantID: account.TenantID, ProviderAccountID: account.ProviderAccountID, Vendor: quotaFactVendor(info), Source: result.Source, ObservedAt: observedAt, Complete: result.Complete, Facts: result.Facts}
	if err != nil {
		snapshot.Source = adapter.Source()
		if writeErr := w.factStore.RecordFailure(ctx, snapshot, usageErrorClass(err)); writeErr != nil {
			w.logger.WarnContext(ctx, "quota probe 写厂商失败观测失败", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassDatabaseWriteFailed)
		}
		w.logger.WarnContext(ctx, "quota probe 厂商只读接口失败", "provider_account_id", account.ProviderAccountID, "error_class", usageErrorClass(err))
		return
	}
	if err := w.factStore.ReplaceSnapshot(ctx, snapshot); err != nil {
		w.logger.WarnContext(ctx, "quota probe 写厂商额度失败", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassDatabaseWriteFailed)
		return
	}
	if result.ErrorClass != "" {
		if err := w.factStore.RecordFailure(ctx, snapshot, result.ErrorClass); err != nil {
			w.logger.WarnContext(ctx, "quota probe 写厂商部分失败观测失败", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassDatabaseWriteFailed)
		}
	}
	if result.Session != nil && w.store != nil {
		update := sessionWindowUpdate(account.ProviderAccountID, *result.Session, observedAt)
		if sessionWindowUpdateHasValues(update) {
			update.ObservedAt = &observedAt
			update.ObservationSource = rate.QuotaSnapshotSourceUsageEndpoint
			update.ObservationOutcome = quotaObservationOutcome(update)
			if err := w.store.UpdateProviderAccountSessionWindows(ctx, update); err != nil {
				w.logger.WarnContext(ctx, "quota probe 写会话额度窗口失败", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassDatabaseWriteFailed)
			}
		}
	}
	if !result.Subscription.Empty() && w.subscriptions != nil {
		if info.AccountCredentialID <= 0 || info.CredentialVersion <= 0 {
			w.logger.WarnContext(ctx, "quota probe 缺少套餐观测的凭据版本", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassCredentialResolutionFailed)
			return
		}
		if _, err := w.subscriptions.RecordSubscriptionObservation(
			ctx, account.TenantID, account.ProviderAccountID, info.AccountCredentialID,
			int32(info.CredentialVersion), result.Subscription,
		); err != nil {
			w.logger.WarnContext(ctx, "quota probe 写套餐观测失败", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassDatabaseWriteFailed)
		}
	}
}

func (w *Worker) writeAnthropicFacts(ctx context.Context, account Account, snapshot UsageSnapshot, observedAt time.Time) {
	if w.factStore == nil {
		return
	}
	facts := make([]accountquota.Fact, 0, 2)
	for _, window := range []struct {
		key   string
		usage UsageWindow
	}{{"five_hour", snapshot.FiveHour}, {"seven_day", snapshot.SevenDay}} {
		if window.usage.Utilization == nil || window.usage.ResetsAt == nil {
			continue
		}
		used := *window.usage.Utilization
		remaining := 100 - used
		state := accountquota.StateAvailable
		if remaining <= 0 {
			state = accountquota.StateExhausted
		}
		facts = append(facts, accountquota.Fact{MetricKey: window.key, State: state, UtilizationPercent: &used, RemainingPercent: &remaining, ResetsAt: window.usage.ResetsAt, ValidUntil: validUntil(observedAt)})
	}
	if len(facts) == 0 {
		return
	}
	if err := w.factStore.ReplaceSnapshot(ctx, accountquota.Snapshot{TenantID: account.TenantID, ProviderAccountID: account.ProviderAccountID, Vendor: "anthropic", Source: accountquota.SourceUpstreamUsage, ObservedAt: observedAt, Complete: true, Facts: facts}); err != nil {
		w.logger.WarnContext(ctx, "quota probe 写 Claude 统一额度失败", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassDatabaseWriteFailed)
	}
}

func (w *Worker) recordFailedObservation(ctx context.Context, account Account, errorClass string) {
	observedAt := w.now().UTC()
	update := rate.SessionWindowUpdate{
		ProviderAccountID:     account.ProviderAccountID,
		ObservedAt:            &observedAt,
		ObservationSource:     rate.QuotaSnapshotSourceUsageEndpoint,
		ObservationOutcome:    rate.QuotaSnapshotOutcomeFailed,
		ObservationErrorClass: strings.TrimSpace(errorClass),
	}
	if err := w.store.UpdateProviderAccountSessionWindows(ctx, update); err != nil {
		w.logger.WarnContext(ctx, "quota probe 写失败观测失败",
			"provider_account_id", account.ProviderAccountID,
			"error_class", ErrorClassDatabaseWriteFailed,
		)
	}
	if w.factStore != nil {
		if err := w.factStore.RecordFailure(ctx, accountquota.Snapshot{
			TenantID: account.TenantID, ProviderAccountID: account.ProviderAccountID,
			Vendor: "anthropic", Source: accountquota.SourceUpstreamUsage, ObservedAt: observedAt,
		}, errorClass); err != nil {
			w.logger.WarnContext(ctx, "quota probe 写 Claude 统一失败观测失败", "provider_account_id", account.ProviderAccountID, "error_class", ErrorClassDatabaseWriteFailed)
		}
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

func quotaObservationOutcome(update rate.SessionWindowUpdate) string {
	if update.Window5hEnd != nil && update.Window5hUtilization != nil &&
		update.Window7dEnd != nil && update.Window7dUtilization != nil {
		return rate.QuotaSnapshotOutcomeSuccess
	}
	return rate.QuotaSnapshotOutcomePartial
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
