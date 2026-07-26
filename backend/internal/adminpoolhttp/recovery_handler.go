package adminpoolhttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/provideraccountrecovery"
)

// 账号恢复端点簇:窄口 clear-rate-limit(只清限流冷却,渠道走 ramping 渐进)与
// 全口 recover(复位 health_state + 清限流五轴 + 渠道强制满血)并存,各管一头。

type AdminPoolAccountRateLimitRecovery interface {
	ClearRateLimit(context.Context, provideraccountrecovery.ClearRateLimitInput) (provideraccountrecovery.ClearRateLimitResult, error)
	RecoverAccountState(context.Context, provideraccountrecovery.RecoverAccountInput) (provideraccountrecovery.RecoverAccountResult, error)
}

type providerAccountRateLimitRecoveryResponse struct {
	AccountBackoffCleared bool                      `json:"account_backoff_cleared"`
	ChannelRecordFound    bool                      `json:"channel_record_found"`
	ChannelChanged        bool                      `json:"channel_changed"`
	ChannelState          channelhealth.HealthState `json:"channel_state,omitempty"`
}

type providerAccountRecoveryPartialError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type providerAccountRecoveryPartialResponse struct {
	Error                  providerAccountRecoveryPartialError `json:"error"`
	TenantID               int64                               `json:"tenant_id"`
	AccountID              int64                               `json:"account_id"`
	Operation              string                              `json:"operation"`
	AccountBackoffCleared  bool                                `json:"account_backoff_cleared"`
	AccountStateRecovered  bool                                `json:"account_state_recovered"`
	ChannelRecoveryPending bool                                `json:"channel_recovery_pending"`
	Retryable              bool                                `json:"retryable"`
}

func newClearProviderAccountRateLimitHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		if d.RateLimitRecovery == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account rate-limit recovery dependency unset")
			return
		}
		id, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		result, err := d.RateLimitRecovery.ClearRateLimit(r.Context(), provideraccountrecovery.ClearRateLimitInput{
			TenantID: tenantID, AccountID: id,
			ActorID: ident.AuditActor(), ActorRole: ident.Role,
			RequestID: middleware.GetReqID(r.Context()),
		})
		if err != nil {
			if errors.Is(err, provideraccountrecovery.ErrPartialRecovery) {
				writeProviderAccountRecoveryPartial(
					w,
					result,
					"clear_rate_limit",
					false,
					"account backoff cleared, but channel rate-limit recovery failed; retry the operation",
				)
				return
			}
			writeProviderAccountReadError(w, err, "provider_account_clear_rate_limit_failed")
			return
		}
		response, err := providerAccountDTO(result.Account)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_quota_projection_invalid", "provider account quota projection is invalid")
			return
		}
		recovery := &providerAccountRateLimitRecoveryResponse{
			AccountBackoffCleared: true,
			ChannelRecordFound:    result.Channel != nil,
			ChannelChanged:        result.ChannelChanged,
		}
		if result.Channel != nil {
			recovery.ChannelState = result.Channel.State
		}
		response.RateLimitRecovery = recovery
		writeAuditJSON(w, http.StatusOK, response)
	}
}

// newRecoverProviderAccountStateHandler 运维"完整恢复账号"端点:把 health_state 复位 healthy
// (消终态 revoked 无恢复路径)+ 清限流五轴 + 渠道强制回 active 满血,一口收齐各分裂恢复口。
func newRecoverProviderAccountStateHandler(d AdminPoolAccountDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountAdmin(w, r, d)
		if !ok {
			return
		}
		if d.RateLimitRecovery == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account recovery dependency unset")
			return
		}
		id, ok := parseAdminPoolID(w, r)
		if !ok {
			return
		}
		result, err := d.RateLimitRecovery.RecoverAccountState(r.Context(), provideraccountrecovery.RecoverAccountInput{
			TenantID: tenantID, AccountID: id,
			ActorID: ident.AuditActor(), ActorRole: ident.Role,
			RequestID: middleware.GetReqID(r.Context()),
		})
		if err != nil {
			if errors.Is(err, provideraccountrecovery.ErrPartialRecovery) {
				writeProviderAccountRecoveryPartial(
					w,
					result,
					"recover_account_state",
					true,
					"account state recovered, but channel force-active failed; retry the operation",
				)
				return
			}
			writeProviderAccountReadError(w, err, "provider_account_recover_failed")
			return
		}
		response, err := providerAccountDTO(result.Account)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "provider_account_quota_projection_invalid", "provider account quota projection is invalid")
			return
		}
		recovery := &providerAccountRateLimitRecoveryResponse{
			AccountBackoffCleared: true,
			ChannelRecordFound:    result.Channel != nil,
			ChannelChanged:        result.ChannelChanged,
		}
		if result.Channel != nil {
			recovery.ChannelState = result.Channel.State
		}
		response.RateLimitRecovery = recovery
		writeAuditJSON(w, http.StatusOK, response)
	}
}

func writeProviderAccountRecoveryPartial(
	w http.ResponseWriter,
	result provideraccountrecovery.ClearRateLimitResult,
	operation string,
	accountStateRecovered bool,
	message string,
) {
	writeAuditJSON(w, http.StatusServiceUnavailable, providerAccountRecoveryPartialResponse{
		Error: providerAccountRecoveryPartialError{
			Code:    "provider_account_recovery_partial",
			Message: message,
		},
		TenantID:               result.Account.TenantID,
		AccountID:              result.Account.ID,
		Operation:              operation,
		AccountBackoffCleared:  true,
		AccountStateRecovered:  accountStateRecovered,
		ChannelRecoveryPending: true,
		Retryable:              true,
	})
}
