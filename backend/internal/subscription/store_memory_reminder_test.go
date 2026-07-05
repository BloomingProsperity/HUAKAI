// HUAKAI · iKun

package subscription

import (
	"context"
	"sort"
	"time"
)

func (m *memoryStore) ListDueReminder(_ context.Context, now time.Time, within time.Duration, after ReminderCursor, limit int) ([]ReminderCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	if within <= 0 {
		within = 7 * 24 * time.Hour
	}
	upper := now.Add(within)
	var out []ReminderCandidate
	for k, sub := range m.subs {
		if sub.Status != StatusActive {
			continue
		}
		// 镜像 PG: expires_at 在 (now, now+within]。
		if !sub.ExpiresAt.After(now) || sub.ExpiresAt.After(upper) {
			continue
		}
		// 镜像 PG 行值游标 (expires_at, id) > (after.ExpiresAt, after.ID)。
		if !afterCursor(sub.ExpiresAt, sub.ID, after) {
			continue
		}
		uk := userKey{k.tenant, sub.UserID}
		if !m.users[uk] { // 镜像 INNER JOIN users (已删/不存在用户跳过)
			continue
		}
		out = append(out, ReminderCandidate{
			TenantID:       k.tenant,
			SubscriptionID: sub.ID,
			UserID:         sub.UserID,
			ExpiresAt:      sub.ExpiresAt.UTC(),
			RecipientEmail: m.userEmails[uk],
			PlanName:       m.plans[planKey{k.tenant, sub.PlanID}].Name,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
			return out[i].ExpiresAt.Before(out[j].ExpiresAt)
		}
		return out[i].SubscriptionID < out[j].SubscriptionID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// afterCursor 判断 (expiresAt, id) 是否严格大于游标 (镜像 PG 行值比较)。
func afterCursor(expiresAt time.Time, id int64, after ReminderCursor) bool {
	if expiresAt.After(after.ExpiresAt) {
		return true
	}
	if expiresAt.Equal(after.ExpiresAt) {
		return id > after.ID
	}
	return false
}

func (m *memoryStore) SentReminderKeys(_ context.Context, tenantID, subscriptionID int64) (map[string]struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]struct{})
	for k := range m.reminders {
		if k.tenant == tenantID && k.sub == subscriptionID {
			out[k.key] = struct{}{}
		}
	}
	return out, nil
}

func (m *memoryStore) RecordReminder(_ context.Context, rec reminderRecord) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := reminderMemKey{rec.TenantID, rec.SubscriptionID, rec.ReminderKey}
	if _, exists := m.reminders[k]; exists {
		return false, nil // ON CONFLICT DO NOTHING
	}
	m.reminders[k] = rec
	return true, nil
}
