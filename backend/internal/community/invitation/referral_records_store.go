package invitation

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ListUserReferrals(ctx context.Context, input ListUserReferralsInput) (ReferralRecordPage, error) {
	if s == nil || s.pool == nil {
		return ReferralRecordPage{}, ErrStoreNotConfigured
	}
	var total int64
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*)::bigint
FROM referrals
WHERE tenant_id=$1 AND referrer_user_id=$2`,
		input.TenantID, input.ReferrerUserID).Scan(&total); err != nil {
		return ReferralRecordPage{}, fmt.Errorf("invitation: count user referrals: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
SELECT
	r.id,
	r.referrer_user_id,
	r.referee_user_id,
	r.status,
	r.created_at,
	r.qualified_at,
	MAX(rr.issued_at) AS rewarded_at
FROM referrals r
LEFT JOIN referral_rewards rr
  ON rr.tenant_id=r.tenant_id AND rr.referral_id=r.id
WHERE r.tenant_id=$1 AND r.referrer_user_id=$2
GROUP BY r.id, r.referrer_user_id, r.referee_user_id, r.status, r.created_at, r.qualified_at
ORDER BY r.created_at DESC, r.id DESC
LIMIT $3 OFFSET $4`,
		input.TenantID, input.ReferrerUserID, input.Limit, input.Offset)
	if err != nil {
		return ReferralRecordPage{}, fmt.Errorf("invitation: list user referrals: %w", err)
	}
	items, err := scanReferralRecords(rows)
	if err != nil {
		return ReferralRecordPage{}, fmt.Errorf("invitation: scan user referrals: %w", err)
	}
	return ReferralRecordPage{Items: items, Total: total, Limit: input.Limit, Offset: input.Offset}, nil
}

func (s *PostgresStore) ListUserReferralRewards(ctx context.Context, input ListUserReferralRewardsInput) (ReferralRewardPage, error) {
	if s == nil || s.pool == nil {
		return ReferralRewardPage{}, ErrStoreNotConfigured
	}
	var total, totalMicros int64
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*)::bigint, COALESCE(SUM(amount_usd_micros), 0)::bigint
FROM referral_rewards
WHERE tenant_id=$1 AND referrer_user_id=$2`,
		input.TenantID, input.ReferrerUserID).Scan(&total, &totalMicros); err != nil {
		return ReferralRewardPage{}, fmt.Errorf("invitation: count user referral rewards: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, referral_id, reward_type, amount_usd_micros, issued_at
FROM referral_rewards
WHERE tenant_id=$1 AND referrer_user_id=$2
ORDER BY issued_at DESC, id DESC
LIMIT $3 OFFSET $4`,
		input.TenantID, input.ReferrerUserID, input.Limit, input.Offset)
	if err != nil {
		return ReferralRewardPage{}, fmt.Errorf("invitation: list user referral rewards: %w", err)
	}
	items, err := scanReferralRewards(rows)
	if err != nil {
		return ReferralRewardPage{}, fmt.Errorf("invitation: scan user referral rewards: %w", err)
	}
	return ReferralRewardPage{
		Items: items, Total: total, TotalRewardUSD: referralMicrosToUSD(totalMicros),
		Limit: input.Limit, Offset: input.Offset,
	}, nil
}

func (s *PostgresStore) ListReferralsAdmin(ctx context.Context, input ListReferralsAdminInput) (ReferralRecordPage, error) {
	if s == nil || s.pool == nil {
		return ReferralRecordPage{}, ErrStoreNotConfigured
	}
	status := nullableReferralStatus(input.Status)
	var total int64
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*)::bigint
FROM referrals
WHERE tenant_id=$1 AND ($2::text IS NULL OR status=$2)`,
		input.TenantID, status).Scan(&total); err != nil {
		return ReferralRecordPage{}, fmt.Errorf("invitation: count admin referrals: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, referrer_user_id, referee_user_id, status, created_at, qualified_at, NULL::timestamptz AS rewarded_at
FROM referrals
WHERE tenant_id=$1 AND ($2::text IS NULL OR status=$2)
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4`,
		input.TenantID, status, input.Limit, input.Offset)
	if err != nil {
		return ReferralRecordPage{}, fmt.Errorf("invitation: list admin referrals: %w", err)
	}
	items, err := scanReferralRecords(rows)
	if err != nil {
		return ReferralRecordPage{}, fmt.Errorf("invitation: scan admin referrals: %w", err)
	}
	return ReferralRecordPage{Items: items, Total: total, Limit: input.Limit, Offset: input.Offset}, nil
}

func (s *PostgresStore) GetReferralOverview(ctx context.Context, tenantID int64) (ReferralOverview, error) {
	if s == nil || s.pool == nil {
		return ReferralOverview{}, ErrStoreNotConfigured
	}
	counts := zeroReferralStatusCounts()
	var pending, qualified, rewarded, rejected int64
	if err := s.pool.QueryRow(ctx, `
SELECT
	COUNT(*) FILTER (WHERE status='pending')::bigint,
	COUNT(*) FILTER (WHERE status='qualified')::bigint,
	COUNT(*) FILTER (WHERE status='rewarded')::bigint,
	COUNT(*) FILTER (WHERE status='rejected')::bigint
FROM referrals
WHERE tenant_id=$1`,
		tenantID).Scan(&pending, &qualified, &rewarded, &rejected); err != nil {
		return ReferralOverview{}, fmt.Errorf("invitation: referral overview counts: %w", err)
	}
	counts[ReferralStatusPending] = pending
	counts[ReferralStatusQualified] = qualified
	counts[ReferralStatusRewarded] = rewarded
	counts[ReferralStatusRejected] = rejected
	var rewardCount, totalMicros int64
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(rr.id)::bigint, COALESCE(SUM(rr.amount_usd_micros), 0)::bigint
FROM referral_rewards rr
JOIN referrals r
  ON r.tenant_id=rr.tenant_id AND r.id=rr.referral_id
WHERE rr.tenant_id=$1 AND r.status='rewarded'`,
		tenantID).Scan(&rewardCount, &totalMicros); err != nil {
		return ReferralOverview{}, fmt.Errorf("invitation: referral overview rewards: %w", err)
	}
	return ReferralOverview{
		CountsByStatus: counts,
		TotalRewardUSD: referralMicrosToUSD(totalMicros),
		RewardCount:    rewardCount,
	}, nil
}

func scanReferralRecords(rows pgx.Rows) ([]ReferralRecord, error) {
	defer rows.Close()
	var out []ReferralRecord
	for rows.Next() {
		var rec ReferralRecord
		var qualifiedAt, rewardedAt sql.NullTime
		if err := rows.Scan(
			&rec.ID,
			&rec.ReferrerUserID,
			&rec.RefereeUserID,
			&rec.Status,
			&rec.CreatedAt,
			&qualifiedAt,
			&rewardedAt,
		); err != nil {
			return nil, err
		}
		rec.CreatedAt = rec.CreatedAt.UTC()
		if qualifiedAt.Valid {
			t := qualifiedAt.Time.UTC()
			rec.QualifiedAt = &t
		}
		if rewardedAt.Valid {
			t := rewardedAt.Time.UTC()
			rec.RewardedAt = &t
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanReferralRewards(rows pgx.Rows) ([]ReferralRewardLedgerEntry, error) {
	defer rows.Close()
	var out []ReferralRewardLedgerEntry
	for rows.Next() {
		var item ReferralRewardLedgerEntry
		var amountMicros int64
		if err := rows.Scan(&item.ID, &item.ReferralID, &item.RewardType, &amountMicros, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.AmountUSD = referralMicrosToUSD(amountMicros)
		item.CreatedAt = item.CreatedAt.UTC()
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func nullableReferralStatus(status *string) any {
	if status == nil {
		return nil
	}
	return *status
}

var _ referralRecordsStore = (*PostgresStore)(nil)

// ListReferralRewardsAdmin implements the tenant-scoped admin reward ledger read.
func (s *PostgresStore) ListReferralRewardsAdmin(ctx context.Context, input ListReferralRewardsAdminInput) (AdminReferralRewardPage, error) {
	if s == nil || s.pool == nil {
		return AdminReferralRewardPage{}, ErrStoreNotConfigured
	}
	var refFilter interface{}
	if input.ReferrerUserID != nil {
		refFilter = *input.ReferrerUserID
	}
	var total, totalMicros int64
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*)::bigint, COALESCE(SUM(amount_usd_micros), 0)::bigint
FROM referral_rewards
WHERE tenant_id=$1 AND ($2::bigint IS NULL OR referrer_user_id=$2)`,
		input.TenantID, refFilter).Scan(&total, &totalMicros); err != nil {
		return AdminReferralRewardPage{}, fmt.Errorf("invitation: count admin referral rewards: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, referral_id, referrer_user_id, reward_type, amount_usd_micros, issued_at
FROM referral_rewards
WHERE tenant_id=$1 AND ($2::bigint IS NULL OR referrer_user_id=$2)
ORDER BY issued_at DESC, id DESC
LIMIT $3 OFFSET $4`,
		input.TenantID, refFilter, input.Limit, input.Offset)
	if err != nil {
		return AdminReferralRewardPage{}, fmt.Errorf("invitation: list admin referral rewards: %w", err)
	}
	defer rows.Close()
	items := make([]AdminReferralRewardEntry, 0)
	for rows.Next() {
		var e AdminReferralRewardEntry
		var micros int64
		if err := rows.Scan(&e.ID, &e.ReferralID, &e.ReferrerUserID, &e.RewardType, &micros, &e.CreatedAt); err != nil {
			return AdminReferralRewardPage{}, fmt.Errorf("invitation: scan admin referral rewards: %w", err)
		}
		e.AmountUSD = referralMicrosToUSD(micros)
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return AdminReferralRewardPage{}, fmt.Errorf("invitation: iterate admin referral rewards: %w", err)
	}
	return AdminReferralRewardPage{Items: items, Total: total, TotalRewardUSD: referralMicrosToUSD(totalMicros), Limit: input.Limit, Offset: input.Offset}, nil
}
