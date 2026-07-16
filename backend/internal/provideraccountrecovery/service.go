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

const clearRateLimitAuditAction = "clear_provider_account_rate_limit"

var ErrPartialRecovery = errors.New("provider account recovery partially completed")

type AccountStore interface {
	ClearRateLimitWithAudit(context.Context, AccountMutation) (admindb.AdminProviderAccountRow, error)
}

type ChannelController interface {
	ClearRateLimitByProviderAccount(context.Context, int64, int64, string) (channelhealth.Record, bool, error)
}

type AccountMutation struct {
	Clear admindb.ClearProviderAccountRateLimitParams
	Audit admindb.InsertAdminAuditEventParams
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
