// HUAKAI · iKun

package paymenthttp

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// Service 是 handler 依赖的支付能力子集 (由 *payment.Service 实现)。
type Service interface {
	CreateOrder(context.Context, payment.CreateOrderInput) (payment.CreateOrderResult, error)
	AdminConfirmPaid(context.Context, payment.AdminConfirmPaidInput) (payment.FulfillResult, error)
	AdminListOrders(context.Context, payment.OrderListFilter) ([]payment.Order, error)
	DashboardStats(context.Context, payment.DashboardFilter) (payment.DashboardStats, error)
	GetOrder(context.Context, int64, int64) (payment.Order, error)
	ListAuditEvents(context.Context, int64, int64) ([]payment.AuditEvent, error)
	GetBalance(context.Context, int64, int64) (payment.Balance, error)
	ListOrders(context.Context, int64, int64, int) ([]payment.Order, error)
	RetryFulfillment(context.Context, payment.RetryFulfillmentInput) (payment.FulfillResult, error)
	GetProviderRuntimeConfig(context.Context, payment.ProviderKind) (payment.ProviderRuntimeConfig, error)
	SetProviderRuntimeConfig(context.Context, payment.ProviderRuntimeConfigInput) (payment.ProviderRuntimeConfig, error)
	CancelOrder(context.Context, payment.CancelOrderInput) (payment.Order, error)
	RefundOrder(context.Context, payment.RefundOrderInput) (payment.RefundResult, error)
}

type ProviderRuntimeConfigService interface {
	GetProviderRuntimeConfig(context.Context, payment.ProviderKind) (payment.ProviderRuntimeConfig, error)
	SetProviderRuntimeConfig(context.Context, payment.ProviderRuntimeConfigInput) (payment.ProviderRuntimeConfig, error)
}
