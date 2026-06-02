// Package subscriptionhttp 暴露订阅计划和用户订阅订单 HTTP endpoint。
//
// 本包只做 HTTP 解析、租户范围校验和响应映射；订阅状态机在
// internal/subscription，支付入账在 internal/payment。
package subscriptionhttp

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/paymenthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

const maxBodyBytes = 64 << 10

type SubscriptionService interface {
	CreatePlan(context.Context, subscription.PlanInput) (subscription.Plan, error)
	ListPlans(context.Context, int64, bool) ([]subscription.Plan, error)
	GetPlan(context.Context, int64, int64) (subscription.Plan, error)
	UpdatePlan(context.Context, subscription.PlanPatch) (subscription.Plan, error)
	ArchivePlan(context.Context, int64, int64) error
	CreateOrder(context.Context, subscription.CreateOrderInput) (subscription.Order, error)
	ListUserSubscriptions(context.Context, subscription.ListUserSubscriptionsInput) ([]subscription.UserSubscription, error)
	ExpireDueSubscriptions(context.Context, subscription.ExpireDueInput) (int, error)
	ResetDueSubscriptions(context.Context, subscription.ResetDueInput) (int, error)
}

type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Deps struct {
	Service   SubscriptionService
	AdminAuth adminAuth
	Providers map[string]paymenthttp.ProviderBinding
}

func MountUserRoutes(r chi.Router, d Deps) {
	r.Get("/v1/users/me/subscriptions", newListUserSubscriptionsHandler(d))
	r.Post("/v1/users/me/subscription-orders", newCreateSubscriptionOrderHandler(d))
}

func MountAdminPlanRoutes(r chi.Router, d Deps) {
	r.Post("/", newCreatePlanHandler(d))
	r.Get("/", newListPlansHandler(d))
	r.Get("/{id}", newGetPlanHandler(d))
	r.Patch("/{id}", newUpdatePlanHandler(d))
	r.Delete("/{id}", newArchivePlanHandler(d))
}
