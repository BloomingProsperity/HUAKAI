// HUAKAI · iKun

package payment

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *PostgresStore) AdminListOrders(ctx context.Context, filter OrderListFilter) ([]Order, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreNotConfigured
	}
	where, args := adminOrderWhere(filter)
	args = append(args, filter.Limit, filter.Offset)
	limitPos, offsetPos := len(args)-1, len(args)
	rows, err := s.pool.Query(ctx, `SELECT`+orderSelectColumns+`
FROM payment_orders `+where+`
ORDER BY created_at DESC, id DESC LIMIT $`+fmt.Sprint(limitPos)+` OFFSET $`+fmt.Sprint(offsetPos), args...)
	if err != nil {
		return nil, fmt.Errorf("payment: admin list orders: %w", err)
	}
	defer rows.Close()
	var out []Order
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, order)
	}
	return out, rows.Err()
}

func (s *PostgresStore) DashboardStats(ctx context.Context, filter DashboardFilter, now time.Time) (DashboardStats, error) {
	if s == nil || s.pool == nil {
		return DashboardStats{}, ErrStoreNotConfigured
	}
	stats := DashboardStats{DailySeries: emptyDailySeries(filter.From, filter.To)}
	where, args := dashboardWhere(filter)
	todayStart := startOfUTCDay(now)
	todayEnd := todayStart.AddDate(0, 0, 1)
	args = append(args, todayStart, todayEnd)
	todayStartPos, todayEndPos := len(args)-1, len(args)
	if err := s.pool.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_cents), 0)::bigint,
       COUNT(*)::int,
       COUNT(*) FILTER (WHERE created_at >= $`+fmt.Sprint(todayStartPos)+` AND created_at < $`+fmt.Sprint(todayEndPos)+`)::int
FROM payment_orders `+where, args...).Scan(&stats.TotalAmountCents, &stats.TotalCount, &stats.TodayCount); err != nil {
		return DashboardStats{}, fmt.Errorf("payment: dashboard stats aggregate: %w", err)
	}
	if stats.TotalCount > 0 {
		stats.AverageAmountCents = stats.TotalAmountCents / int64(stats.TotalCount)
	}
	if err := s.fillDashboardSeries(ctx, filter, &stats); err != nil {
		return DashboardStats{}, err
	}
	return stats, nil
}

func (s *PostgresStore) fillDashboardSeries(ctx context.Context, filter DashboardFilter, stats *DashboardStats) error {
	where, args := dashboardWhere(filter)
	rows, err := s.pool.Query(ctx, `
SELECT date_trunc('day', created_at AT TIME ZONE 'UTC') AS day,
       COUNT(*)::int,
       COALESCE(SUM(amount_cents), 0)::bigint
FROM payment_orders `+where+`
GROUP BY day ORDER BY day`, args...)
	if err != nil {
		return fmt.Errorf("payment: dashboard daily series: %w", err)
	}
	defer rows.Close()
	byDate := make(map[string]int, len(stats.DailySeries))
	for i := range stats.DailySeries {
		byDate[stats.DailySeries[i].Date] = i
	}
	for rows.Next() {
		var day time.Time
		var count int
		var amount int64
		if err := rows.Scan(&day, &count, &amount); err != nil {
			return err
		}
		if idx, ok := byDate[startOfUTCDay(day).Format("2006-01-02")]; ok {
			stats.DailySeries[idx].OrderCount = count
			stats.DailySeries[idx].AmountCents = amount
		}
	}
	return rows.Err()
}

func adminOrderWhere(filter OrderListFilter) (string, []any) {
	clauses := []string{"tenant_id=$1"}
	args := []any{filter.TenantID}
	if filter.UserID > 0 {
		args = append(args, filter.UserID)
		clauses = append(clauses, "user_id=$"+fmt.Sprint(len(args)))
	}
	if filter.Status != "" {
		args = append(args, string(filter.Status))
		clauses = append(clauses, "status=$"+fmt.Sprint(len(args)))
	}
	if filter.From != nil {
		args = append(args, *filter.From)
		clauses = append(clauses, "created_at >= $"+fmt.Sprint(len(args)))
	}
	if filter.To != nil {
		args = append(args, *filter.To)
		clauses = append(clauses, "created_at < $"+fmt.Sprint(len(args)))
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func dashboardWhere(filter DashboardFilter) (string, []any) {
	return "WHERE tenant_id=$1 AND created_at >= $2 AND created_at < $3", []any{filter.TenantID, filter.From, filter.To}
}
