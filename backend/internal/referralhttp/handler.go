package referralhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/community/invitation"
)

type Service interface {
	ListUserReferrals(context.Context, invitation.ListUserReferralsInput) (invitation.ReferralRecordPage, error)
	ListUserReferralRewards(context.Context, invitation.ListUserReferralRewardsInput) (invitation.ReferralRewardPage, error)
	ListReferralsAdmin(context.Context, invitation.ListReferralsAdminInput) (invitation.ReferralRecordPage, error)
	ReferralOverview(context.Context, int64) (invitation.ReferralOverview, error)
}

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Deps struct {
	Service   Service
	AdminAuth AdminAuth
}

type referralListResponse struct {
	Object string             `json:"object"`
	Items  []userReferralItem `json:"items"`
	Total  int64              `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

type userReferralItem struct {
	ReferralID    int64   `json:"referral_id"`
	RefereeUserID int64   `json:"referee_user_id"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"created_at"`
	RewardedAt    *string `json:"rewarded_at,omitempty"`
}

type rewardLedgerResponse struct {
	Object         string             `json:"object"`
	Items          []rewardLedgerItem `json:"items"`
	Total          int64              `json:"total"`
	TotalRewardUSD decimal.Decimal    `json:"total_reward_usd"`
	Limit          int                `json:"limit"`
	Offset         int                `json:"offset"`
}

type rewardLedgerItem struct {
	ReferralID int64           `json:"referral_id"`
	RewardType string          `json:"reward_type"`
	AmountUSD  decimal.Decimal `json:"amount_usd"`
	CreatedAt  string          `json:"created_at"`
}

type adminReferralListResponse struct {
	Object string              `json:"object"`
	Items  []adminReferralItem `json:"items"`
	Total  int64               `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

type adminReferralItem struct {
	ID             int64  `json:"id"`
	ReferrerUserID int64  `json:"referrer_user_id"`
	RefereeUserID  int64  `json:"referee_user_id"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
}

type overviewResponse struct {
	Object         string           `json:"object"`
	CountsByStatus map[string]int64 `json:"counts_by_status"`
	TotalRewardUSD decimal.Decimal  `json:"total_reward_usd"`
	RewardCount    int64            `json:"reward_count"`
}

func NewUserReferralsHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "session bearer token is required")
			return
		}
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "referral dependency unset")
			return
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		page, err := d.Service.ListUserReferrals(r.Context(), invitation.ListUserReferralsInput{
			TenantID: ident.TenantID, ReferrerUserID: ident.UserID, Limit: limit, Offset: offset,
		})
		if err != nil {
			writeReferralError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, referralListResponse{
			Object: "referrals_list",
			Items:  userReferralItems(page.Items),
			Total:  page.Total,
			Limit:  page.Limit,
			Offset: page.Offset,
		})
	}
}

func NewUserReferralRewardsHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "session bearer token is required")
			return
		}
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "referral dependency unset")
			return
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		page, err := d.Service.ListUserReferralRewards(r.Context(), invitation.ListUserReferralRewardsInput{
			TenantID: ident.TenantID, ReferrerUserID: ident.UserID, Limit: limit, Offset: offset,
		})
		if err != nil {
			writeReferralError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rewardLedgerResponse{
			Object:         "referral_reward_ledger",
			Items:          rewardLedgerItems(page.Items),
			Total:          page.Total,
			TotalRewardUSD: page.TotalRewardUSD,
			Limit:          page.Limit,
			Offset:         page.Offset,
		})
	}
}

func NewAdminReferralsHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveAdminTenant(w, r, d)
		if !ok {
			return
		}
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "referral dependency unset")
			return
		}
		limit, offset, ok := parsePage(w, r)
		if !ok {
			return
		}
		status, ok := parseStatusFilter(w, r)
		if !ok {
			return
		}
		page, err := d.Service.ListReferralsAdmin(r.Context(), invitation.ListReferralsAdminInput{
			TenantID: tenantID, Status: status, Limit: limit, Offset: offset,
		})
		if err != nil {
			writeReferralError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, adminReferralListResponse{
			Object: "admin_referrals_list",
			Items:  adminReferralItems(page.Items),
			Total:  page.Total,
			Limit:  page.Limit,
			Offset: page.Offset,
		})
	}
}

func NewAdminReferralOverviewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveAdminTenant(w, r, d)
		if !ok {
			return
		}
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "referral dependency unset")
			return
		}
		overview, err := d.Service.ReferralOverview(r.Context(), tenantID)
		if err != nil {
			writeReferralError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, overviewResponse{
			Object:         "admin_referrals_overview",
			CountsByStatus: overview.CountsByStatus,
			TotalRewardUSD: overview.TotalRewardUSD,
			RewardCount:    overview.RewardCount,
		})
	}
}

func parsePage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit, ok := parseOptionalNonNegativeInt(w, r.URL.Query().Get("limit"), invitation.ReferralRecordsDefaultLimit, "limit")
	if !ok {
		return 0, 0, false
	}
	offset, ok := parseOptionalNonNegativeInt(w, r.URL.Query().Get("offset"), 0, "offset")
	if !ok {
		return 0, 0, false
	}
	limit, offset = normalizePage(limit, offset)
	return limit, offset, true
}

func parseOptionalNonNegativeInt(w http.ResponseWriter, raw string, fallback int, field string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", field+" must be a non-negative integer")
		return 0, false
	}
	return n, true
}

func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = invitation.ReferralRecordsDefaultLimit
	}
	if limit > invitation.ReferralRecordsMaxLimit {
		limit = invitation.ReferralRecordsMaxLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func parseStatusFilter(w http.ResponseWriter, r *http.Request) (*string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("status"))
	if raw == "" {
		return nil, true
	}
	if !invitation.ValidReferralStatus(raw) {
		writeError(w, http.StatusBadRequest, "invalid_request", "status is invalid")
		return nil, false
	}
	return &raw, true
}

func resolveAdminTenant(w http.ResponseWriter, r *http.Request, d Deps) (int64, bool) {
	if d.AdminAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin auth dependency unset")
		return 0, false
	}
	ident, err := d.AdminAuth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return 0, false
	}
	queryTenantID, hasQueryTenant, ok := parseTenantQuery(w, r)
	if !ok {
		return 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required")
			return 0, false
		}
		if hasQueryTenant && queryTenantID != ident.ScopeTenantID {
			writeError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
			return 0, false
		}
		return ident.ScopeTenantID, true
	case admin.RolePlatformAdmin:
		if !hasQueryTenant {
			writeError(w, http.StatusBadRequest, "invalid_request", "tenant_id is required")
			return 0, false
		}
		return queryTenantID, true
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return 0, false
	}
}

func parseTenantQuery(w http.ResponseWriter, r *http.Request) (int64, bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if raw == "" {
		return 0, false, true
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "tenant_id must be a positive integer")
		return 0, false, false
	}
	return tenantID, true, true
}

func userReferralItems(rows []invitation.ReferralRecord) []userReferralItem {
	items := make([]userReferralItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, userReferralItem{
			ReferralID:    row.ID,
			RefereeUserID: row.RefereeUserID,
			Status:        row.Status,
			CreatedAt:     formatTime(row.CreatedAt),
			RewardedAt:    formatOptionalTime(row.RewardedAt),
		})
	}
	return items
}

func rewardLedgerItems(rows []invitation.ReferralRewardLedgerEntry) []rewardLedgerItem {
	items := make([]rewardLedgerItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, rewardLedgerItem{
			ReferralID: row.ReferralID,
			RewardType: row.RewardType,
			AmountUSD:  row.AmountUSD,
			CreatedAt:  formatTime(row.CreatedAt),
		})
	}
	return items
}

func adminReferralItems(rows []invitation.ReferralRecord) []adminReferralItem {
	items := make([]adminReferralItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, adminReferralItem{
			ID:             row.ID,
			ReferrerUserID: row.ReferrerUserID,
			RefereeUserID:  row.RefereeUserID,
			Status:         row.Status,
			CreatedAt:      formatTime(row.CreatedAt),
		})
	}
	return items
}

func formatTime(ts time.Time) string {
	return ts.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(ts *time.Time) *string {
	if ts == nil {
		return nil
	}
	out := formatTime(*ts)
	return &out
}

func writeReferralError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, invitation.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "referral request is invalid")
	case errors.Is(err, invitation.ErrStoreNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "referral dependency unset")
	default:
		writeError(w, http.StatusServiceUnavailable, "referral_backend_error", "referral service unavailable")
	}
}

func writeAdminAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, admin.ErrAdminBackend) {
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		return
	}
	writeError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
