package quotaprobe

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

func TestWorkerMockUpstreamUpdatesBothWindows(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	reset5h := now.Add(4 * time.Hour)
	reset7d := now.Add(6 * 24 * time.Hour)
	utilization := 37.5
	accounts := &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 101}}}
	vault := provider.NewStaticVault()
	if err := vault.Set(101,
		provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "oauth-access", Extra: map[string]string{"scope": "user:profile user:inference"}},
		provider.AccountInfo{AccountID: 101, TenantID: 7, OAuthScope: "user:profile user:inference", Platform: "anthropic", AccountType: "claude_ai_oauth"},
	); err != nil {
		t.Fatalf("准备凭据: %v", err)
	}
	fetcher := &usageFetcherStub{snapshot: UsageSnapshot{
		FiveHour: UsageWindow{Utilization: &utilization, ResetsAt: &reset5h},
		SevenDay: UsageWindow{Utilization: &utilization, ResetsAt: &reset7d},
	}}
	store := &windowStoreStub{}
	lease := &leaderLeaseStub{acquired: true}
	worker := NewWorker(WorkerConfig{
		Accounts: accounts, Vault: vault, Fetcher: fetcher, Store: store,
		Settings:    settingsStub{enabled: "true", interval: "30"},
		LeaderLease: lease,
		Now:         func() time.Time { return now },
		Jitter:      func(Account, time.Time) time.Duration { return 0 },
	})

	worker.RunOnce(context.Background())
	if fetcher.calls != 1 || fetcher.accountID != 101 || fetcher.accessToken != "oauth-access" {
		t.Fatalf("探测调用=%d account=%d token=%q", fetcher.calls, fetcher.accountID, fetcher.accessToken)
	}
	if store.calls != 1 {
		t.Fatalf("窗口写入次数=%d，期望 1", store.calls)
	}
	if lease.calls != 1 || lease.releases != 1 {
		t.Fatalf("多副本租约 acquire/release=%d/%d，期望 1/1", lease.calls, lease.releases)
	}
	update := store.update
	if update.Window5hStart == nil || !update.Window5hStart.Equal(reset5h.Add(-5*time.Hour)) ||
		update.Window5hEnd == nil || !update.Window5hEnd.Equal(reset5h) ||
		update.Window5hStatus == nil || *update.Window5hStatus != "active" ||
		update.Window5hUtilization == nil || *update.Window5hUtilization != 37.5 {
		t.Fatalf("5h 窗口写入不完整：%+v", update)
	}
	if update.Window7dStart == nil || !update.Window7dStart.Equal(reset7d.Add(-7*24*time.Hour)) ||
		update.Window7dEnd == nil || !update.Window7dEnd.Equal(reset7d) ||
		update.Window7dStatus == nil || *update.Window7dStatus != "active" ||
		update.Window7dUtilization == nil || *update.Window7dUtilization != 37.5 {
		t.Fatalf("7d 窗口写入不完整：%+v", update)
	}
	if update.ObservedAt == nil || !update.ObservedAt.Equal(now) ||
		update.ObservationSource != rate.QuotaSnapshotSourceUsageEndpoint ||
		update.ObservationOutcome != rate.QuotaSnapshotOutcomeSuccess ||
		update.ObservationErrorClass != "" {
		t.Fatalf("成功观测元数据不完整：%+v", update)
	}
}

func TestWorkerProbeFailurePersistsClassWithoutOverwritingWindows(t *testing.T) {
	now := time.Date(2026, 7, 19, 3, 30, 0, 0, time.UTC)
	vault := provider.NewStaticVault()
	if err := vault.Set(101,
		provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "oauth-access"},
		provider.AccountInfo{AccountID: 101, TenantID: 7, OAuthScope: "user:profile", Platform: "anthropic", AccountType: "claude_ai_oauth"},
	); err != nil {
		t.Fatalf("准备凭据: %v", err)
	}
	store := &windowStoreStub{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	worker := NewWorker(WorkerConfig{
		Accounts: &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 101}}},
		Vault:    vault,
		Fetcher: &usageFetcherStub{err: withErrorClass(
			ErrorClassUpstreamAuthorization,
			errors.New("sensitive-upstream-marker"),
		)},
		Store:    store,
		Logger:   logger,
		Settings: settingsStub{enabled: "true", interval: "30"},
		Now:      func() time.Time { return now },
		Jitter:   func(Account, time.Time) time.Duration { return 0 },
	})

	worker.RunOnce(context.Background())
	if store.calls != 1 {
		t.Fatalf("失败观测写入次数=%d，期望 1", store.calls)
	}
	update := store.update
	if sessionWindowUpdateHasValues(update) {
		t.Fatalf("失败观测不得改写旧窗口：%+v", update)
	}
	if update.ObservedAt == nil || !update.ObservedAt.Equal(now) ||
		update.ObservationSource != rate.QuotaSnapshotSourceUsageEndpoint ||
		update.ObservationOutcome != rate.QuotaSnapshotOutcomeFailed ||
		update.ObservationErrorClass != ErrorClassUpstreamAuthorization {
		t.Fatalf("失败观测元数据=%+v", update)
	}
	if strings.Contains(logs.String(), "sensitive-upstream-marker") {
		t.Fatalf("日志泄露了上游错误原文：%s", logs.String())
	}
	if !strings.Contains(logs.String(), `"error_class":"upstream_authorization_failed"`) {
		t.Fatalf("日志缺少稳定错误分类：%s", logs.String())
	}
}

func TestWorkerLoopRunsExactlyOncePerWaitCycle(t *testing.T) {
	now := time.Date(2026, 7, 19, 4, 0, 0, 0, time.UTC)
	reset5h := now.Add(4 * time.Hour)
	reset7d := now.Add(6 * 24 * time.Hour)
	utilization := 20.0
	vault := provider.NewStaticVault()
	if err := vault.Set(101,
		provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "oauth-access"},
		provider.AccountInfo{AccountID: 101, TenantID: 7, OAuthScope: "user:profile", Platform: "anthropic", AccountType: "claude_ai_oauth"},
	); err != nil {
		t.Fatalf("准备凭据: %v", err)
	}
	fetcher := &usageFetcherStub{snapshot: UsageSnapshot{
		FiveHour: UsageWindow{Utilization: &utilization, ResetsAt: &reset5h},
		SevenDay: UsageWindow{Utilization: &utilization, ResetsAt: &reset7d},
	}}
	waits := 0
	worker := NewWorker(WorkerConfig{
		Accounts: &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 101}}},
		Vault:    vault,
		Fetcher:  fetcher,
		Store:    &windowStoreStub{},
		Settings: settingsStub{enabled: "true", interval: "30"},
		Now:      func() time.Time { return now },
		Jitter:   func(Account, time.Time) time.Duration { return 0 },
		Wait: func(context.Context, time.Duration) bool {
			waits++
			return waits == 1
		},
	})

	worker.loop(context.Background())
	if fetcher.calls != 2 || waits != 2 {
		t.Fatalf("探测/等待次数=%d/%d，期望每个周期各一次", fetcher.calls, waits)
	}
}

func TestWorkerDisabledDoesNotListOrProbe(t *testing.T) {
	accounts := &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 101}}}
	fetcher := &usageFetcherStub{}
	store := &windowStoreStub{}
	worker := NewWorker(WorkerConfig{
		Accounts: accounts,
		Vault:    provider.NewStaticVault(),
		Fetcher:  fetcher,
		Store:    store,
		Settings: settingsStub{enabled: "false", interval: "30"},
	})

	worker.RunOnce(context.Background())
	if accounts.calls != 0 || fetcher.calls != 0 || store.calls != 0 {
		t.Fatalf("开关关闭仍触发工作：list=%d fetch=%d store=%d", accounts.calls, fetcher.calls, store.calls)
	}
}

func TestWorkerVendorProbeDoesNotRequireClaudeDependencies(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	vault := provider.NewStaticVault()
	if err := vault.Set(202, provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "token"}, provider.AccountInfo{AccountID: 202, TenantID: 7, Platform: "gemini"}); err != nil {
		t.Fatalf("准备凭据: %v", err)
	}
	facts := &factStoreStub{}
	worker := NewWorker(WorkerConfig{
		Accounts: &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 202}}},
		Vault:    vault, FactStore: facts, Adapters: []VendorAdapter{GeminiUnknownAdapter{}},
		Settings: settingsStub{enabled: "true", interval: "30"}, Now: func() time.Time { return now },
		Jitter: func(Account, time.Time) time.Duration { return 0 },
	})

	worker.RunOnce(context.Background())
	if facts.replaceCalls != 1 || !facts.snapshot.Complete || facts.snapshot.Facts[0].State != accountquota.StateUnknown {
		t.Fatalf("厂商采集未独立运行：%+v", facts)
	}
}

func TestWorkerCanonicalizesLegacyAntigravityQuotaVendor(t *testing.T) {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	vault := provider.NewStaticVault()
	if err := vault.Set(206,
		provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "token"},
		provider.AccountInfo{AccountID: 206, TenantID: 7, Platform: "gemini", AccountType: "antigravity"},
	); err != nil {
		t.Fatal(err)
	}
	facts := &factStoreStub{}
	worker := NewWorker(WorkerConfig{
		Accounts: &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 206}}},
		Vault:    vault, FactStore: facts,
		Adapters: []VendorAdapter{vendorAdapterStub{platform: "gemini", result: VendorResult{
			Source: accountquota.SourceUpstreamModelCatalog, Complete: true,
			Facts: []accountquota.Fact{{MetricKey: "model_quota", ModelKey: "model-a", State: accountquota.StateAvailable}},
		}}},
		Settings: settingsStub{enabled: "true", interval: "30"}, Now: func() time.Time { return now },
		Jitter: func(Account, time.Time) time.Duration { return 0 },
	})

	worker.RunOnce(context.Background())
	if facts.replaceCalls != 1 || facts.snapshot.Vendor != "antigravity" {
		t.Fatalf("兼容形态没有归一到 Antigravity 额度事实：%+v", facts.snapshot)
	}
}

func TestWorkerPartialVendorResultMergesAndMarksFailure(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	vault := provider.NewStaticVault()
	if err := vault.Set(203, provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "token"}, provider.AccountInfo{AccountID: 203, TenantID: 7, Platform: "partial"}); err != nil {
		t.Fatalf("准备凭据: %v", err)
	}
	facts := &factStoreStub{}
	worker := NewWorker(WorkerConfig{
		Accounts: &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 203}}},
		Vault:    vault, FactStore: facts, Adapters: []VendorAdapter{vendorAdapterStub{result: VendorResult{
			Source: accountquota.SourceUpstreamBilling, Complete: false, ErrorClass: ErrorClassUpstreamPartialResponse,
			Facts: []accountquota.Fact{{MetricKey: "monthly_spend", State: accountquota.StateAvailable}},
		}}},
		Settings: settingsStub{enabled: "true", interval: "30"}, Now: func() time.Time { return now },
		Jitter: func(Account, time.Time) time.Duration { return 0 },
	})

	worker.RunOnce(context.Background())
	if facts.replaceCalls != 1 || facts.snapshot.Complete || facts.failureCalls != 1 || facts.errorClass != ErrorClassUpstreamPartialResponse {
		t.Fatalf("部分结果未按合并并留错处理：%+v", facts)
	}
}

func TestWorkerBindsVendorSubscriptionToResolvedCredentialVersion(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	vault := provider.NewStaticVault()
	if err := vault.Set(204,
		provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "token"},
		provider.AccountInfo{
			AccountID: 204, TenantID: 7, Platform: "partial", AccountType: "oauth",
			AccountCredentialID: 901, CredentialVersion: 4,
		},
	); err != nil {
		t.Fatalf("准备凭据: %v", err)
	}
	facts := &factStoreStub{}
	subscriptions := &subscriptionRecorderStub{}
	worker := NewWorker(WorkerConfig{
		Accounts: &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 204}}},
		Vault:    vault, FactStore: facts, Subscriptions: subscriptions,
		Adapters: []VendorAdapter{vendorAdapterStub{result: VendorResult{
			Source: accountquota.SourceUpstreamBilling, Complete: true,
			Facts: []accountquota.Fact{{MetricKey: "monthly_spend", State: accountquota.StateAvailable}},
			Subscription: subscriptionprofile.FromRaw(
				subscriptionprofile.VendorGrok, "SuperGrok",
				subscriptionprofile.SourceProviderAPI, subscriptionprofile.TrustVerifiedAPI,
				subscriptionprofile.VerificationVerified, "", "",
			),
		}}},
		Settings: settingsStub{enabled: "true", interval: "30"}, Now: func() time.Time { return now },
		Jitter: func(Account, time.Time) time.Duration { return 0 },
	})

	worker.RunOnce(context.Background())
	if facts.replaceCalls != 1 || subscriptions.calls != 1 || subscriptions.tenantID != 7 ||
		subscriptions.accountID != 204 || subscriptions.credentialID != 901 || subscriptions.version != 4 ||
		subscriptions.observation.Label() != "grok:supergrok" {
		t.Fatalf("套餐没有绑定到实际探测凭据：facts=%+v subscription=%+v", facts, subscriptions)
	}
}

func TestWorkerVendorSessionProjectionUpdatesOperationalWindows(t *testing.T) {
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	reset5h := now.Add(3 * time.Hour)
	reset7d := now.Add(5 * 24 * time.Hour)
	used5h, used7d := 35.0, 68.0
	vault := provider.NewStaticVault()
	if err := vault.Set(205,
		provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "token"},
		provider.AccountInfo{AccountID: 205, TenantID: 7, Platform: "partial", AccountCredentialID: 902, CredentialVersion: 2},
	); err != nil {
		t.Fatal(err)
	}
	facts := &factStoreStub{}
	windows := &windowStoreStub{}
	worker := NewWorker(WorkerConfig{
		Accounts: &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 205}}},
		Vault:    vault, FactStore: facts, Store: windows,
		Adapters: []VendorAdapter{vendorAdapterStub{result: VendorResult{
			Source: accountquota.SourceUpstreamUsage, Complete: true,
			Facts: []accountquota.Fact{{MetricKey: "five_hour", State: accountquota.StateAvailable}},
			Session: &UsageSnapshot{
				FiveHour: UsageWindow{Utilization: &used5h, ResetsAt: &reset5h},
				SevenDay: UsageWindow{Utilization: &used7d, ResetsAt: &reset7d},
			},
		}}},
		Settings: settingsStub{enabled: "true", interval: "30"}, Now: func() time.Time { return now },
		Jitter: func(Account, time.Time) time.Duration { return 0 },
	})

	worker.RunOnce(context.Background())
	if facts.replaceCalls != 1 || windows.calls != 1 || windows.update.Window5hUtilization == nil ||
		*windows.update.Window5hUtilization != 35 || windows.update.Window7dUtilization == nil ||
		*windows.update.Window7dUtilization != 68 || windows.update.ObservationOutcome != rate.QuotaSnapshotOutcomeSuccess {
		t.Fatalf("厂商额度没有同步到运维窗口：facts=%+v windows=%+v", facts, windows)
	}
}

func TestWorkerLeaseLoserOrFailureDoesNotProbe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lease *leaderLeaseStub
	}{
		{name: "其它副本持有租约", lease: &leaderLeaseStub{}},
		{name: "协调存储不可用", lease: &leaderLeaseStub{err: errors.New("postgres unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 101}}}
			worker := NewWorker(WorkerConfig{
				Accounts: accounts, Vault: provider.NewStaticVault(), Fetcher: &usageFetcherStub{}, Store: &windowStoreStub{},
				Settings: settingsStub{enabled: "true", interval: "30"}, LeaderLease: tc.lease,
			})

			worker.RunOnce(context.Background())
			if accounts.calls != 0 {
				t.Fatalf("未取得唯一执行权仍扫描了账号：calls=%d", accounts.calls)
			}
		})
	}
}

func TestWorkerStartImmediatelyCollectsBeforeFirstInterval(t *testing.T) {
	now := time.Date(2026, 7, 19, 3, 0, 0, 0, time.UTC)
	reset5h := now.Add(4 * time.Hour)
	reset7d := now.Add(6 * 24 * time.Hour)
	utilization := 25.0
	vault := provider.NewStaticVault()
	if err := vault.Set(101,
		provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "oauth-access"},
		provider.AccountInfo{AccountID: 101, TenantID: 7, OAuthScope: "user:profile user:inference", Platform: "anthropic", AccountType: "claude_ai_oauth"},
	); err != nil {
		t.Fatalf("准备凭据: %v", err)
	}
	called := make(chan struct{}, 1)
	worker := NewWorker(WorkerConfig{
		Accounts: &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 101}}},
		Vault:    vault,
		Fetcher: &usageFetcherStub{called: called, snapshot: UsageSnapshot{
			FiveHour: UsageWindow{Utilization: &utilization, ResetsAt: &reset5h},
			SevenDay: UsageWindow{Utilization: &utilization, ResetsAt: &reset7d},
		}},
		Store:       &windowStoreStub{},
		Settings:    settingsStub{enabled: "true", interval: "30"},
		LeaderLease: &leaderLeaseStub{acquired: true},
		Now:         func() time.Time { return now },
		Jitter:      func(Account, time.Time) time.Duration { return 0 },
	})
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	select {
	case <-called:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("worker 启动后仍在空等首个 30 分钟周期")
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := worker.Wait(waitCtx); err != nil {
		t.Fatalf("等待 worker 退出: %v", err)
	}
}

func TestWorkerMissingProfileScopeSkipsWithReason(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	vault := provider.NewStaticVault()
	if err := vault.Set(101,
		// 合并后的 Extra 即使伪造 profile，也不得冒充凭据自身获批的 scope。
		provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "oauth-access", Extra: map[string]string{"scope": "user:profile"}},
		provider.AccountInfo{AccountID: 101, TenantID: 7, Platform: "anthropic", AccountType: "claude_ai_oauth"},
	); err != nil {
		t.Fatalf("准备凭据: %v", err)
	}
	fetcher := &usageFetcherStub{}
	store := &windowStoreStub{}
	worker := NewWorker(WorkerConfig{
		Accounts: &accountListerStub{accounts: []Account{{TenantID: 7, ProviderAccountID: 101}}},
		Vault:    vault, Fetcher: fetcher, Store: store, Logger: logger,
		Settings: settingsStub{enabled: "true", interval: "30"},
		Jitter:   func(Account, time.Time) time.Duration { return 0 },
	})

	worker.RunOnce(context.Background())
	if fetcher.calls != 0 || store.calls != 0 {
		t.Fatalf("无 profile scope 不得探测或写入：fetch=%d store=%d", fetcher.calls, store.calls)
	}
	if !strings.Contains(logs.String(), `"skip_reason":"missing_profile_scope"`) {
		t.Fatalf("跳过日志缺少稳定原因：%s", logs.String())
	}
}

func TestWorkerWaitBlocksUntilStartContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := NewWorker(WorkerConfig{Settings: settingsStub{enabled: "true", interval: "30"}})
	worker.Start(ctx)

	waitDone := make(chan error, 1)
	go func() {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
		defer waitCancel()
		waitDone <- worker.Wait(waitCtx)
	}()
	select {
	case err := <-waitDone:
		t.Fatalf("context 取消前 Wait 已返回: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context 取消后 worker 未退出")
	}
}

type accountListerStub struct {
	accounts []Account
	calls    int
}

func (s *accountListerStub) ListQuotaProbeAccounts(context.Context) ([]Account, error) {
	s.calls++
	return append([]Account(nil), s.accounts...), nil
}

type usageFetcherStub struct {
	snapshot    UsageSnapshot
	err         error
	called      chan struct{}
	calls       int
	accountID   int64
	accessToken string
}

func (s *usageFetcherStub) FetchUsage(_ context.Context, accountID int64, accessToken string) (UsageSnapshot, error) {
	s.calls++
	s.accountID = accountID
	s.accessToken = accessToken
	if s.called != nil {
		select {
		case s.called <- struct{}{}:
		default:
		}
	}
	return s.snapshot, s.err
}

type leaderLeaseStub struct {
	acquired bool
	err      error
	calls    int
	releases int
}

func (s *leaderLeaseStub) TryAcquire(context.Context) (bool, func(), error) {
	s.calls++
	if s.err != nil || !s.acquired {
		return false, nil, s.err
	}
	return true, func() { s.releases++ }, nil
}

type windowStoreStub struct {
	calls  int
	update rate.SessionWindowUpdate
}

func (s *windowStoreStub) UpdateProviderAccountSessionWindows(_ context.Context, update rate.SessionWindowUpdate) error {
	s.calls++
	s.update = update
	return nil
}

type settingsStub struct {
	enabled  string
	interval string
}

type factStoreStub struct {
	replaceCalls int
	failureCalls int
	snapshot     accountquota.Snapshot
	errorClass   string
}

type subscriptionRecorderStub struct {
	calls        int
	tenantID     int64
	accountID    int64
	credentialID int64
	version      int32
	observation  subscriptionprofile.Observation
}

func (s *subscriptionRecorderStub) RecordSubscriptionObservation(
	_ context.Context,
	tenantID, accountID, credentialID int64,
	version int32,
	observation subscriptionprofile.Observation,
) (subscriptionprofile.Observation, error) {
	s.calls++
	s.tenantID = tenantID
	s.accountID = accountID
	s.credentialID = credentialID
	s.version = version
	s.observation = observation
	return observation, nil
}

func (s *factStoreStub) ReplaceSnapshot(_ context.Context, snapshot accountquota.Snapshot) error {
	s.replaceCalls++
	s.snapshot = snapshot
	return nil
}

func (s *factStoreStub) RecordFailure(_ context.Context, snapshot accountquota.Snapshot, errorClass string) error {
	s.failureCalls++
	s.snapshot = snapshot
	s.errorClass = errorClass
	return nil
}

type vendorAdapterStub struct {
	platform string
	result   VendorResult
	err      error
}

func (s vendorAdapterStub) Supports(_ provider.Credential, info provider.AccountInfo) bool {
	platform := s.platform
	if platform == "" {
		platform = "partial"
	}
	return info.Platform == platform
}

func (s vendorAdapterStub) Source() accountquota.Source { return accountquota.SourceUpstreamBilling }

func (s vendorAdapterStub) Fetch(context.Context, int64, provider.Credential, provider.AccountInfo, time.Time) (VendorResult, error) {
	return s.result, s.err
}

func (s settingsStub) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	value := s.enabled
	if key == platformsettings.KeyQuotaProbeIntervalMinutes {
		value = s.interval
	}
	return platformsettings.StoredSetting{Key: key, Value: value}, nil
}
