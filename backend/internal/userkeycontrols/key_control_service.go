package userkeycontrols

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeyipallow"
	"github.com/BloomingProsperity/HUAKAI/internal/apikeymodelallow"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

var maxNumeric20x8 = decimal.RequireFromString("999999999999.99999999")

type KeyControlService struct {
	store  controlsStore
	logger *slog.Logger
	now    func() time.Time
}

func NewKeyControlService(pool *pgxpool.Pool, logger *slog.Logger) *KeyControlService {
	if logger == nil {
		logger = slog.Default()
	}
	return &KeyControlService{store: NewPostgresStore(pool), logger: logger, now: time.Now}
}

func NewControlsService(store controlsStore, logger *slog.Logger) *KeyControlService {
	if logger == nil {
		logger = slog.Default()
	}
	return &KeyControlService{store: store, logger: logger, now: time.Now}
}

func newServiceForTest(store controlsStore, now func() time.Time) *KeyControlService {
	if now == nil {
		now = time.Now
	}
	return &KeyControlService{store: store, logger: slog.Default(), now: now}
}

func (s *KeyControlService) SetKeyQuota(ctx context.Context, req SetKeyQuotaRequest) (SetKeyQuotaResult, error) {
	if s == nil || s.store == nil {
		return SetKeyQuotaResult{}, fmt.Errorf("%w: store unset", ErrServiceMisconfig)
	}
	if err := validateQuotaRequest(req); err != nil {
		return SetKeyQuotaResult{}, err
	}
	req = normalizeQuotaRequest(req, s.now().UTC())
	var out SetKeyQuotaResult
	err := s.store.WithTx(ctx, func(txCtx context.Context, tx controlsStore) error {
		row, err := tx.UpsertKeyQuotaPolicy(txCtx, quotaPolicyWrite{
			TenantID:      req.TenantID,
			UserID:        req.UserID,
			APIKeyID:      req.APIKeyID,
			ScopeID:       strconv.FormatInt(req.APIKeyID, 10),
			Metric:        req.Metric,
			WindowKind:    req.WindowKind,
			WindowSeconds: req.WindowSeconds,
			LimitUSD:      req.LimitUSD,
			Mode:          req.Mode,
			ValidFrom:     s.now().UTC(),
			Actor:         actorFor(req),
		})
		if err != nil {
			return err
		}
		affected, err := tx.SetAPIKeyQuotaPolicyID(txCtx, quotaPolicyLink{
			TenantID: req.TenantID,
			UserID:   req.UserID,
			APIKeyID: req.APIKeyID,
			PolicyID: row.ID,
		})
		if err != nil {
			return fmt.Errorf("%w: link quota policy: %v", ErrBackend, err)
		}
		if affected == 0 {
			return ErrKeyNotFound
		}
		out = quotaResult(req.APIKeyID, row)
		return nil
	})
	if err != nil {
		return SetKeyQuotaResult{}, err
	}
	return out, nil
}

func (s *KeyControlService) GetKeyQuota(ctx context.Context, tenantID, userID, apiKeyID int64) (KeyQuotaView, error) {
	if s == nil || s.store == nil {
		return KeyQuotaView{}, fmt.Errorf("%w: store unset", ErrServiceMisconfig)
	}
	if tenantID <= 0 || userID <= 0 || apiKeyID <= 0 {
		return KeyQuotaView{}, ErrQuotaPolicyNotFound
	}
	row, err := s.store.GetAPIKeyQuotaPolicy(ctx, tenantID, userID, apiKeyID)
	if err != nil {
		if isNoRows(err) {
			return KeyQuotaView{}, ErrQuotaPolicyNotFound
		}
		return KeyQuotaView{}, fmt.Errorf("%w: get quota policy: %v", ErrBackend, err)
	}
	return quotaResult(apiKeyID, row), nil
}

func (s *KeyControlService) SetKeyIPAllowlist(ctx context.Context, req SetKeyIPAllowlistRequest) (SetKeyIPAllowlistResult, error) {
	if s == nil || s.store == nil {
		return SetKeyIPAllowlistResult{}, fmt.Errorf("%w: store unset", ErrServiceMisconfig)
	}
	if req.TenantID <= 0 || req.UserID <= 0 || req.APIKeyID <= 0 {
		return SetKeyIPAllowlistResult{}, ErrKeyNotFound
	}
	normalized, err := apikeyipallow.Normalize(req.IPAllowlist)
	if err != nil {
		return SetKeyIPAllowlistResult{}, ErrInvalidIPAllowlist
	}
	affected, err := s.store.SetAPIKeyIPAllowlist(ctx, ipAllowlistAssignment{
		TenantID:    req.TenantID,
		UserID:      req.UserID,
		APIKeyID:    req.APIKeyID,
		IPAllowlist: apikeyipallow.StorageText(normalized),
	})
	if err != nil {
		return SetKeyIPAllowlistResult{}, fmt.Errorf("%w: set ip allowlist: %v", ErrBackend, err)
	}
	if affected == 0 {
		return SetKeyIPAllowlistResult{}, ErrKeyNotFound
	}
	return SetKeyIPAllowlistResult{APIKeyID: req.APIKeyID, IPAllowlist: normalized}, nil
}

func (s *KeyControlService) GetKeyIPAllowlist(ctx context.Context, tenantID, userID, apiKeyID int64) (KeyIPAllowlistView, error) {
	if s == nil || s.store == nil {
		return KeyIPAllowlistView{}, fmt.Errorf("%w: store unset", ErrServiceMisconfig)
	}
	if tenantID <= 0 || userID <= 0 || apiKeyID <= 0 {
		return KeyIPAllowlistView{}, ErrKeyNotFound
	}
	row, err := s.store.GetAPIKeyIPAllowlist(ctx, tenantID, userID, apiKeyID)
	if err != nil {
		if isNoRows(err) {
			return KeyIPAllowlistView{}, ErrKeyNotFound
		}
		return KeyIPAllowlistView{}, fmt.Errorf("%w: get ip allowlist: %v", ErrBackend, err)
	}
	entries := []string{}
	if row.IPAllowlist != nil {
		entries, err = apikeyipallow.NormalizeCSV(*row.IPAllowlist)
		if err != nil {
			return KeyIPAllowlistView{}, ErrInvalidIPAllowlist
		}
	}
	return KeyIPAllowlistView{APIKeyID: row.APIKeyID, IPAllowlist: entries}, nil
}

func (s *KeyControlService) SetKeyModelAllowlist(ctx context.Context, req SetKeyModelAllowlistRequest) (SetKeyModelAllowlistResult, error) {
	if s == nil || s.store == nil {
		return SetKeyModelAllowlistResult{}, fmt.Errorf("%w: store unset", ErrServiceMisconfig)
	}
	if req.TenantID <= 0 || req.UserID <= 0 || req.APIKeyID <= 0 {
		return SetKeyModelAllowlistResult{}, ErrKeyNotFound
	}
	normalized := apikeymodelallow.Normalize(req.AllowedModels)
	affected, err := s.store.SetAPIKeyModelAllowlist(ctx, modelAllowlistAssignment{
		TenantID:      req.TenantID,
		UserID:        req.UserID,
		APIKeyID:      req.APIKeyID,
		AllowedModels: apikeymodelallow.StorageText(normalized),
	})
	if err != nil {
		return SetKeyModelAllowlistResult{}, fmt.Errorf("%w: set model allowlist: %v", ErrBackend, err)
	}
	if affected == 0 {
		return SetKeyModelAllowlistResult{}, ErrKeyNotFound
	}
	return SetKeyModelAllowlistResult{APIKeyID: req.APIKeyID, AllowedModels: normalized}, nil
}

func (s *KeyControlService) GetKeyModelAllowlist(ctx context.Context, tenantID, userID, apiKeyID int64) (KeyModelAllowlistView, error) {
	if s == nil || s.store == nil {
		return KeyModelAllowlistView{}, fmt.Errorf("%w: store unset", ErrServiceMisconfig)
	}
	if tenantID <= 0 || userID <= 0 || apiKeyID <= 0 {
		return KeyModelAllowlistView{}, ErrKeyNotFound
	}
	row, err := s.store.GetAPIKeyModelAllowlist(ctx, tenantID, userID, apiKeyID)
	if err != nil {
		if isNoRows(err) {
			return KeyModelAllowlistView{}, ErrKeyNotFound
		}
		return KeyModelAllowlistView{}, fmt.Errorf("%w: get model allowlist: %v", ErrBackend, err)
	}
	entries := []string{}
	if row.AllowedModels != nil {
		entries = apikeymodelallow.NormalizeCSV(*row.AllowedModels)
	}
	return KeyModelAllowlistView{APIKeyID: row.APIKeyID, AllowedModels: entries}, nil
}

func validateQuotaRequest(req SetKeyQuotaRequest) error {
	if req.TenantID <= 0 || req.UserID <= 0 || req.APIKeyID <= 0 {
		return ErrKeyNotFound
	}
	if req.LimitUSD.IsNegative() || req.LimitUSD.GreaterThan(maxNumeric20x8) || req.LimitUSD.Exponent() < -8 {
		return ErrInvalidQuota
	}
	metric := req.Metric
	if metric == "" {
		metric = quota.MetricCostUSD
	}
	switch metric {
	case quota.MetricCostUSD, quota.MetricRequests:
	default:
		return ErrInvalidQuota
	}
	kind := req.WindowKind
	if kind == "" {
		kind = quota.WindowCalendarDay
	}
	switch kind {
	case quota.WindowCalendarDay, quota.WindowCalendarWeek, quota.WindowCalendarMonth:
		if req.WindowSeconds != 0 {
			return ErrInvalidQuota
		}
	case quota.WindowFixed:
		if req.WindowSeconds <= 0 {
			return ErrInvalidQuota
		}
	default:
		return ErrInvalidQuota
	}
	mode := req.Mode
	if mode == "" {
		mode = quota.ModeEnforce
	}
	switch mode {
	case quota.ModeEnforce, quota.ModeObserve, quota.ModeManualFirst, quota.ModeDisabled:
		return nil
	default:
		return ErrInvalidQuota
	}
}

func normalizeQuotaRequest(req SetKeyQuotaRequest, now time.Time) SetKeyQuotaRequest {
	if req.Metric == "" {
		req.Metric = quota.MetricCostUSD
	}
	if req.WindowKind == "" {
		req.WindowKind = quota.WindowCalendarDay
	}
	if req.Mode == "" {
		req.Mode = quota.ModeEnforce
	}
	return req
}

func quotaResult(apiKeyID int64, row quotaPolicyRow) SetKeyQuotaResult {
	return SetKeyQuotaResult{
		APIKeyID:      apiKeyID,
		PolicyID:      row.ID,
		LimitUSD:      row.LimitUSD,
		ScopeKind:     row.ScopeKind,
		ScopeID:       row.ScopeID,
		Metric:        row.Metric,
		WindowKind:    row.WindowKind,
		WindowSeconds: row.WindowSeconds,
		Mode:          row.Mode,
		ValidFrom:     row.ValidFrom,
	}
}

func actorFor(req SetKeyQuotaRequest) string {
	if req.RequestID != "" {
		return "userkeycontrols:" + req.RequestID
	}
	return fmt.Sprintf("user:%d", req.UserID)
}
