package subscription

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 扫描和 PG 错误判定独立放置，避免业务状态机文件膨胀。
type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPlan(row rowScanner) (Plan, error) {
	var plan Plan
	var unit, reset string
	if err := row.Scan(
		&plan.ID, &plan.TenantID, &plan.Code, &plan.Name, &plan.Description, &plan.Enabled,
		&plan.Price, &plan.CurrencyCode, &unit, &plan.DurationValue, &plan.DurationSeconds,
		&plan.QuotaLimit, &reset, &plan.QuotaResetIntervalSeconds,
		&plan.MaxPurchasesPerUser, &plan.SortOrder, &plan.CreatedAt, &plan.UpdatedAt, &plan.ArchivedAt,
	); err != nil {
		return Plan{}, err
	}
	plan.CurrencyCode = strings.TrimSpace(plan.CurrencyCode)
	plan.DurationUnit = DurationUnit(unit)
	plan.QuotaResetPeriod = ResetPeriod(reset)
	plan.CreatedAt = plan.CreatedAt.UTC()
	plan.UpdatedAt = plan.UpdatedAt.UTC()
	return plan, nil
}

func scanOrder(row rowScanner) (Order, error) {
	var order Order
	var status, unit, reset string
	if err := row.Scan(
		&order.ID, &order.TenantID, &order.UserID, &order.PlanID, &order.RechargeOrderID,
		&order.TradeNo, &status, &order.Price, &order.CurrencyCode, &order.Provider,
		&order.PlanCode, &order.PlanName, &unit, &order.DurationValue, &order.DurationSeconds,
		&order.QuotaLimit, &reset, &order.QuotaResetIntervalSeconds,
		&order.CreatedAt, &order.PaidAt, &order.ActivatedAt, &order.UpdatedAt,
	); err != nil {
		return Order{}, err
	}
	order.Status = OrderStatus(status)
	order.CurrencyCode = strings.TrimSpace(order.CurrencyCode)
	order.Provider = strings.ToLower(strings.TrimSpace(order.Provider))
	order.DurationUnit = DurationUnit(unit)
	order.QuotaResetPeriod = ResetPeriod(reset)
	order.CreatedAt = order.CreatedAt.UTC()
	order.UpdatedAt = order.UpdatedAt.UTC()
	return order, nil
}

func scanUserSubscription(row rowScanner) (UserSubscription, error) {
	var sub UserSubscription
	var status, reset string
	if err := row.Scan(
		&sub.ID, &sub.TenantID, &sub.UserID, &sub.PlanID, &sub.SourceOrderID, &status,
		&sub.QuotaLimit, &sub.QuotaUsed, &reset, &sub.QuotaResetIntervalSeconds,
		&sub.StartedAt, &sub.CurrentPeriodStartedAt, &sub.NextQuotaResetAt, &sub.ExpiresAt,
		&sub.CreatedAt, &sub.UpdatedAt,
	); err != nil {
		return UserSubscription{}, err
	}
	sub.Status = SubscriptionStatus(status)
	sub.QuotaResetPeriod = ResetPeriod(reset)
	sub.CreatedAt = sub.CreatedAt.UTC()
	sub.UpdatedAt = sub.UpdatedAt.UTC()
	return sub, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

var _ Store = (*PostgresStore)(nil)
