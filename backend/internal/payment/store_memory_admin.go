// HUAKAI · iKun

package payment

import (
	"context"
	"sort"
	"time"
)

func (m *MemoryStore) AdminListOrders(_ context.Context, filter OrderListFilter) ([]Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Order
	for _, o := range m.orders {
		if !orderMatchesListFilter(o, filter) {
			continue
		}
		out = append(out, *o)
	}
	sortOrdersForAdmin(out)
	if filter.Offset >= len(out) {
		return nil, nil
	}
	out = out[filter.Offset:]
	if len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (m *MemoryStore) DashboardStats(_ context.Context, filter DashboardFilter, now time.Time) (DashboardStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := DashboardStats{DailySeries: emptyDailySeries(filter.From, filter.To)}
	byDate := make(map[string]int, len(stats.DailySeries))
	for i := range stats.DailySeries {
		byDate[stats.DailySeries[i].Date] = i
	}
	today := startOfUTCDay(now)
	for _, o := range m.orders {
		if o == nil || o.TenantID != filter.TenantID || o.CreatedAt.Before(filter.From) || !o.CreatedAt.Before(filter.To) {
			continue
		}
		stats.TotalCount++
		stats.TotalAmountCents += o.AmountCents
		if day := startOfUTCDay(o.CreatedAt); day.Equal(today) {
			stats.TodayCount++
		}
		key := startOfUTCDay(o.CreatedAt).Format("2006-01-02")
		if idx, ok := byDate[key]; ok {
			stats.DailySeries[idx].OrderCount++
			stats.DailySeries[idx].AmountCents += o.AmountCents
		}
	}
	if stats.TotalCount > 0 {
		stats.AverageAmountCents = stats.TotalAmountCents / int64(stats.TotalCount)
	}
	return stats, nil
}

func orderMatchesListFilter(o *Order, filter OrderListFilter) bool {
	if o == nil || o.TenantID != filter.TenantID {
		return false
	}
	if filter.UserID > 0 && o.UserID != filter.UserID {
		return false
	}
	if filter.Status != "" && o.Status != filter.Status {
		return false
	}
	if filter.From != nil && o.CreatedAt.Before(*filter.From) {
		return false
	}
	if filter.To != nil && !o.CreatedAt.Before(*filter.To) {
		return false
	}
	return true
}

func sortOrdersForAdmin(orders []Order) {
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].CreatedAt.Equal(orders[j].CreatedAt) {
			return orders[i].ID > orders[j].ID
		}
		return orders[i].CreatedAt.After(orders[j].CreatedAt)
	})
}
