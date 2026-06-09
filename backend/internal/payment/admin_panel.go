// HUAKAI · iKun

package payment

import (
	"context"
	"strings"
	"time"
)

const (
	defaultAdminOrderLimit       = 50
	maxAdminOrderLimit           = 200
	defaultAdminOrderExportLimit = 100000
	maxAdminOrderExportLimit     = 100001
	maxDashboardDays             = 366
)

type OrderListFilter struct {
	TenantID int64
	UserID   int64
	Status   OrderStatus
	From     *time.Time
	To       *time.Time
	Limit    int
	Offset   int
}

type OrderExportFilter struct {
	TenantID int64
	Status   OrderStatus
	From     *time.Time
	To       *time.Time
	Limit    int
}

// RefundExportFilter filters for refund CSV export.
type RefundExportFilter struct {
	TenantID int64
	From     *time.Time
	To       *time.Time
	Limit    int
}

type DashboardFilter struct {
	TenantID int64
	From     time.Time
	To       time.Time
}

type DailyStats struct {
	Date        string
	OrderCount  int
	AmountCents int64
}

type DashboardStats struct {
	TotalAmountCents   int64
	TotalCount         int
	TodayCount         int
	AverageAmountCents int64
	DailySeries        []DailyStats
}

type RetryFulfillmentInput struct {
	TenantID     int64
	OrderID      int64
	ActorAdminID int64
	RequestID    string
}

type ProviderRuntimeConfigInput struct {
	ProviderKind ProviderKind
	Enabled      bool
	CheckoutURL  string
	UpdatedBy    string
}

type ProviderRuntimeConfig struct {
	ProviderKind ProviderKind
	Enabled      bool
	CheckoutURL  string
	Source       string
	UpdatedBy    string
	UpdatedAt    time.Time
}

func (s *Service) AdminListOrders(ctx context.Context, filter OrderListFilter) ([]Order, error) {
	filter, err := normalizeOrderListFilter(filter)
	if err != nil {
		return nil, err
	}
	return s.store.AdminListOrders(ctx, filter)
}

func (s *Service) ExportOrders(ctx context.Context, filter OrderExportFilter) ([]Order, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	filter, err := normalizeOrderExportFilter(filter)
	if err != nil {
		return nil, err
	}
	store, ok := s.store.(adminOrderExportStore)
	if !ok {
		return nil, ErrStoreNotConfigured
	}
	return store.AdminExportOrders(ctx, filter)
}

// ExportRefunds returns refund records for CSV export (read-only, no billing side effects).
func (s *Service) ExportRefunds(ctx context.Context, filter RefundExportFilter) ([]RefundRecord, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if filter.TenantID <= 0 {
		return nil, ErrInvalidInput
	}
	if filter.Limit <= 0 {
		filter.Limit = defaultAdminOrderExportLimit
	}
	store, ok := s.store.(adminRefundExportStore)
	if !ok {
		return nil, ErrStoreNotConfigured
	}
	return store.AdminExportRefunds(ctx, filter)
}

func (s *Service) DashboardStats(ctx context.Context, filter DashboardFilter) (DashboardStats, error) {
	filter, err := s.normalizeDashboardFilter(filter)
	if err != nil {
		return DashboardStats{}, err
	}
	return s.store.DashboardStats(ctx, filter, s.now())
}

func (s *Service) RetryFulfillment(ctx context.Context, in RetryFulfillmentInput) (FulfillResult, error) {
	if in.TenantID <= 0 || in.OrderID <= 0 {
		return FulfillResult{}, ErrInvalidInput
	}
	return s.Fulfill(ctx, FulfillInput{
		TenantID:  in.TenantID,
		OrderID:   in.OrderID,
		ActorKind: ActorKindAdmin,
		ActorID:   in.ActorAdminID,
		RequestID: in.RequestID,
	})
}

func (s *Service) GetProviderRuntimeConfig(_ context.Context, kind ProviderKind) (ProviderRuntimeConfig, error) {
	kind = ProviderKind(strings.TrimSpace(string(kind)))
	if kind != ProviderManual && kind != ProviderTaobao {
		return ProviderRuntimeConfig{}, ErrProviderUnknown
	}
	s.providerMu.RLock()
	defer s.providerMu.RUnlock()
	if cfg, ok := s.providerConfigs[kind]; ok {
		return cfg, nil
	}
	return defaultProviderRuntimeConfig(kind), nil
}

func (s *Service) SetProviderRuntimeConfig(_ context.Context, in ProviderRuntimeConfigInput) (ProviderRuntimeConfig, error) {
	cfg, err := normalizeProviderRuntimeConfigInput(in, s.now())
	if err != nil {
		return ProviderRuntimeConfig{}, err
	}
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	switch cfg.ProviderKind {
	case ProviderManual:
		if cfg.Enabled {
			s.providers[ProviderManual] = NewManualProvider()
		} else {
			delete(s.providers, ProviderManual)
		}
	case ProviderTaobao:
		if cfg.Enabled {
			s.providers[ProviderTaobao] = NewTaobaoProvider(cfg.CheckoutURL)
		} else {
			delete(s.providers, ProviderTaobao)
		}
	default:
		return ProviderRuntimeConfig{}, ErrProviderUnknown
	}
	s.providerConfigs[cfg.ProviderKind] = cfg
	return cfg, nil
}

func (s *Service) resolveProvider(kind ProviderKind) (Provider, error) {
	s.providerMu.RLock()
	defer s.providerMu.RUnlock()
	return s.providers.resolve(kind)
}

func (s *Service) refreshProviderRuntimeConfigs() {
	s.providerConfigs[ProviderManual] = ProviderRuntimeConfig{
		ProviderKind: ProviderManual,
		Enabled:      s.providers[ProviderManual] != nil,
		Source:       "runtime",
	}
	cfg := ProviderRuntimeConfig{ProviderKind: ProviderTaobao, Enabled: false, Source: "runtime"}
	if p, ok := s.providers[ProviderTaobao].(taobaoProvider); ok {
		cfg.Enabled = true
		cfg.CheckoutURL = p.checkoutURL
	}
	s.providerConfigs[ProviderTaobao] = cfg
}

func normalizeOrderListFilter(filter OrderListFilter) (OrderListFilter, error) {
	if filter.TenantID <= 0 {
		return OrderListFilter{}, ErrInvalidInput
	}
	if filter.UserID < 0 || filter.Offset < 0 {
		return OrderListFilter{}, ErrInvalidInput
	}
	if filter.Status != "" && !validOrderStatus(filter.Status) {
		return OrderListFilter{}, ErrInvalidInput
	}
	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		return OrderListFilter{}, ErrInvalidInput
	}
	filter.Limit = normalizeAdminLimit(filter.Limit)
	return filter, nil
}

func normalizeOrderExportFilter(filter OrderExportFilter) (OrderExportFilter, error) {
	if filter.TenantID <= 0 {
		return OrderExportFilter{}, ErrInvalidInput
	}
	if filter.Status != "" && !validOrderStatus(filter.Status) {
		return OrderExportFilter{}, ErrInvalidInput
	}
	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		return OrderExportFilter{}, ErrInvalidInput
	}
	filter.Limit = normalizeAdminExportLimit(filter.Limit)
	return filter, nil
}

func (s *Service) normalizeDashboardFilter(filter DashboardFilter) (DashboardFilter, error) {
	if filter.TenantID <= 0 {
		return DashboardFilter{}, ErrInvalidInput
	}
	if filter.To.IsZero() {
		filter.To = startOfUTCDay(s.now()).AddDate(0, 0, 1)
	}
	if filter.From.IsZero() {
		filter.From = startOfUTCDay(filter.To).AddDate(0, 0, -6)
	}
	filter.From = startOfUTCDay(filter.From)
	filter.To = startOfUTCDay(filter.To)
	if !filter.From.Before(filter.To) || int(filter.To.Sub(filter.From).Hours()/24) > maxDashboardDays {
		return DashboardFilter{}, ErrInvalidInput
	}
	return filter, nil
}

func normalizeAdminLimit(limit int) int {
	if limit <= 0 {
		return defaultAdminOrderLimit
	}
	if limit > maxAdminOrderLimit {
		return maxAdminOrderLimit
	}
	return limit
}

func normalizeAdminExportLimit(limit int) int {
	if limit <= 0 {
		return defaultAdminOrderExportLimit
	}
	if limit > maxAdminOrderExportLimit {
		return maxAdminOrderExportLimit
	}
	return limit
}

func validOrderStatus(status OrderStatus) bool {
	switch status {
	case StatusPending, StatusPaid, StatusRecharging, StatusCompleted, StatusRefunded, StatusExpired, StatusCancelled, StatusFailed:
		return true
	default:
		return false
	}
}

func normalizeProviderRuntimeConfigInput(in ProviderRuntimeConfigInput, now time.Time) (ProviderRuntimeConfig, error) {
	kind := ProviderKind(strings.TrimSpace(string(in.ProviderKind)))
	if kind != ProviderManual && kind != ProviderTaobao {
		return ProviderRuntimeConfig{}, ErrProviderUnknown
	}
	checkoutURL := strings.TrimSpace(in.CheckoutURL)
	if kind == ProviderTaobao && in.Enabled && checkoutURL == "" {
		return ProviderRuntimeConfig{}, ErrInvalidInput
	}
	if kind == ProviderManual && checkoutURL != "" {
		return ProviderRuntimeConfig{}, ErrInvalidInput
	}
	return ProviderRuntimeConfig{
		ProviderKind: kind,
		Enabled:      in.Enabled,
		CheckoutURL:  checkoutURL,
		Source:       "runtime",
		UpdatedBy:    strings.TrimSpace(in.UpdatedBy),
		UpdatedAt:    now.UTC(),
	}, nil
}

func defaultProviderRuntimeConfig(kind ProviderKind) ProviderRuntimeConfig {
	return ProviderRuntimeConfig{ProviderKind: kind, Enabled: kind == ProviderManual, Source: "runtime"}
}

func startOfUTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func emptyDailySeries(from, to time.Time) []DailyStats {
	var out []DailyStats
	for day := startOfUTCDay(from); day.Before(to); day = day.AddDate(0, 0, 1) {
		out = append(out, DailyStats{Date: day.Format("2006-01-02")})
	}
	return out
}
