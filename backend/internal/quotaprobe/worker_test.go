package quotaprobe

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/rate"
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
	worker := NewWorker(WorkerConfig{
		Accounts: accounts, Vault: vault, Fetcher: fetcher, Store: store,
		Settings: settingsStub{enabled: "true", interval: "30"},
		Now:      func() time.Time { return now },
		Jitter:   func(Account, time.Time) time.Duration { return 0 },
	})

	worker.RunOnce(context.Background())
	if fetcher.calls != 1 || fetcher.accountID != 101 || fetcher.accessToken != "oauth-access" {
		t.Fatalf("探测调用=%d account=%d token=%q", fetcher.calls, fetcher.accountID, fetcher.accessToken)
	}
	if store.calls != 1 {
		t.Fatalf("窗口写入次数=%d，期望 1", store.calls)
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
	calls       int
	accountID   int64
	accessToken string
}

func (s *usageFetcherStub) FetchUsage(_ context.Context, accountID int64, accessToken string) (UsageSnapshot, error) {
	s.calls++
	s.accountID = accountID
	s.accessToken = accessToken
	return s.snapshot, s.err
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

func (s settingsStub) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	value := s.enabled
	if key == platformsettings.KeyQuotaProbeIntervalMinutes {
		value = s.interval
	}
	return platformsettings.StoredSetting{Key: key, Value: value}, nil
}
