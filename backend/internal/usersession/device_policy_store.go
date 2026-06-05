package usersession

import (
	"context"
	"sort"
)

func (s *PostgresStore) ListActiveFamiliesForDevicePolicy(
	ctx context.Context,
	tenantID, userID int64,
	limit int,
) ([]SessionFamily, error) {
	if s == nil || s.db == nil {
		return nil, ErrStoreNotConfigured
	}
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
SELECT id::text, tenant_id, user_id, status, generation, created_at, last_active_at,
       device_info, ip_baseline, revoked_at, revoked_reason
FROM session_families
WHERE tenant_id = $1 AND user_id = $2 AND status IN ('active', 'suspicious')
ORDER BY last_active_at ASC
LIMIT $3
`, tenantID, userID, limit)
	if err != nil {
		if s.cache != nil {
			return s.cache.ListActiveFamiliesForDevicePolicy(ctx, tenantID, userID, limit)
		}
		return nil, err
	}
	defer rows.Close()
	var out []SessionFamily
	for rows.Next() {
		family, err := scanFamily(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, family)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *MemoryStore) ListActiveFamiliesForDevicePolicy(
	_ context.Context,
	tenantID, userID int64,
	limit int,
) ([]SessionFamily, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	if limit <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionFamily, 0)
	for _, family := range s.families {
		if family.TenantID != tenantID || family.UserID != userID {
			continue
		}
		if family.Status != FamilyStatusActive && family.Status != FamilyStatusSuspicious {
			continue
		}
		out = append(out, family)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastActiveAt.Before(out[j].LastActiveAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
