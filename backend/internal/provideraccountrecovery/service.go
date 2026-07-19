package provideraccountrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

const (
	clearRateLimitAuditAction = "clear_provider_account_rate_limit"
	recoverAccountAuditAction = "recover_provider_account_state"
)

var ErrPartialRecovery = errors.New("provider account recovery partially completed")

type AccountStore interface {
	ClearRateLimitWithAudit(context.Context, AccountMutation) (admindb.AdminProviderAccountRow, error)
	RecoverAccountStateWithAudit(context.Context, AccountRecoverMutation) (admindb.AdminProviderAccountRow, error)
}

type ChannelController interface {
	ClearRateLimitByProviderAccount(context.Context, int64, int64, string) (channelhealth.Record, bool, error)
	ForceActiveByProviderAccount(context.Context, int64, int64, string, string) (channelhealth.Record, bool, error)
}

type AccountMutation struct {
	Clear admindb.ClearProviderAccountRateLimitParams
	Audit admindb.InsertAdminAuditEventParams
}

type AccountRecoverMutation struct {
	Recover admindb.RecoverProviderAccountStateParams
	Audit   admindb.InsertAdminAuditEventParams
}

type ClearRateLimitInput struct {
	TenantID  int64
	AccountID int64
	ActorID   string
	ActorRole string
	RequestID string
	Reason    string
}

type ClearRateLimitResult struct {
	Account        admindb.AdminProviderAccountRow
	Channel        *channelhealth.Record
	ChannelChanged bool
}

type Service struct {
	accounts AccountStore
	channels ChannelController
}

func NewService(accounts AccountStore, channels ChannelController) *Service {
	return &Service{accounts: accounts, channels: channels}
}

func (s *Service) ClearRateLimit(ctx context.Context, in ClearRateLimitInput) (ClearRateLimitResult, error) {
	if s == nil || s.accounts == nil || s.channels == nil {
		return ClearRateLimitResult{}, errors.New("provider account recovery service is not configured")
	}
	if in.TenantID <= 0 {
		return ClearRateLimitResult{}, errors.New("tenant_id must be positive")
	}
	if in.AccountID <= 0 {
		return ClearRateLimitResult{}, errors.New("provider_account_id must be positive")
	}
	in.ActorID = strings.TrimSpace(in.ActorID)
	in.ActorRole = strings.TrimSpace(in.ActorRole)
	if in.ActorID == "" || in.ActorRole == "" {
		return ClearRateLimitResult{}, errors.New("actor identity is required")
	}

	payload, err := json.Marshal(map[string]any{
		"tenant_id": in.TenantID,
		"cleared":   true,
	})
	if err != nil {
		return ClearRateLimitResult{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "清除 provider account rate limit"
	}
	targetID := in.AccountID
	requestID := strings.TrimSpace(in.RequestID)
	actorID := in.ActorID
	account, err := s.accounts.ClearRateLimitWithAudit(ctx, AccountMutation{
		Clear: admindb.ClearProviderAccountRateLimitParams{
			ID: in.AccountID, TenantID: in.TenantID, ActorID: &actorID,
		},
		Audit: admindb.InsertAdminAuditEventParams{
			TenantID: &in.TenantID, ActorID: in.ActorID, ActorRole: in.ActorRole,
			Action: clearRateLimitAuditAction, TargetType: "provider_account", TargetID: &targetID,
			RequestID: &requestID, Reason: &reason, Payload: payload,
		},
	})
	if err != nil {
		return ClearRateLimitResult{}, err
	}

	result := ClearRateLimitResult{Account: account}
	channel, changed, err := s.channels.ClearRateLimitByProviderAccount(ctx, in.TenantID, in.AccountID, in.ActorID)
	if errors.Is(err, channelhealth.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("%w: clear channel rate-limit cooldown: %w", ErrPartialRecovery, err)
	}
	result.Channel = &channel
	result.ChannelChanged = changed
	return result, nil
}

// RecoverAccountInput/Result 复用清限流的入参/出参形态(租户/账号/操作者/原因 + 账号行 + 渠道结果),
// 避免重复结构体。
type RecoverAccountInput = ClearRateLimitInput
type RecoverAccountResult = ClearRateLimitResult

// RecoverAccountState 是运维"完整恢复账号"原语:一次调用把 health_state 复位 healthy(消终态
// revoked 无恢复路径)、清限流五轴、并把渠道强制回 active 满流量——各恢复口各清一半的分裂
// 由此一口收齐。与 ClearRateLimit(轻量、只清限流冷却、渠道走 ramping 渐进)并存:
// 窄口日常清冷却、全口救终态,两原语各管一头。
func (s *Service) RecoverAccountState(ctx context.Context, in RecoverAccountInput) (RecoverAccountResult, error) {
	if s == nil || s.accounts == nil || s.channels == nil {
		return RecoverAccountResult{}, errors.New("provider account recovery service is not configured")
	}
	if in.TenantID <= 0 {
		return RecoverAccountResult{}, errors.New("tenant_id must be positive")
	}
	if in.AccountID <= 0 {
		return RecoverAccountResult{}, errors.New("provider_account_id must be positive")
	}
	in.ActorID = strings.TrimSpace(in.ActorID)
	in.ActorRole = strings.TrimSpace(in.ActorRole)
	if in.ActorID == "" || in.ActorRole == "" {
		return RecoverAccountResult{}, errors.New("actor identity is required")
	}

	payload, err := json.Marshal(map[string]any{
		"tenant_id": in.TenantID,
		"recovered": true,
	})
	if err != nil {
		return RecoverAccountResult{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "完整恢复 provider account 状态"
	}
	targetID := in.AccountID
	requestID := strings.TrimSpace(in.RequestID)
	actorID := in.ActorID
	account, err := s.accounts.RecoverAccountStateWithAudit(ctx, AccountRecoverMutation{
		Recover: admindb.RecoverProviderAccountStateParams{
			ID: in.AccountID, TenantID: in.TenantID, ActorID: &actorID,
		},
		Audit: admindb.InsertAdminAuditEventParams{
			TenantID: &in.TenantID, ActorID: in.ActorID, ActorRole: in.ActorRole,
			Action: recoverAccountAuditAction, TargetType: "provider_account", TargetID: &targetID,
			RequestID: &requestID, Reason: &reason, Payload: payload,
		},
	})
	if err != nil {
		return RecoverAccountResult{}, err
	}

	result := RecoverAccountResult{Account: account}
	channel, changed, err := s.channels.ForceActiveByProviderAccount(ctx, in.TenantID, in.AccountID, in.ActorID, reason)
	if errors.Is(err, channelhealth.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("%w: force channel active: %w", ErrPartialRecovery, err)
	}
	result.Channel = &channel
	result.ChannelChanged = changed
	return result, nil
}
