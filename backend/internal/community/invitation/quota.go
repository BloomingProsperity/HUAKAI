package invitation

import (
	"context"
	"time"
)

func monthStartUTC(now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func checkTenantMonthlyQuota(ctx context.Context, store Store, tenantID int64, now time.Time) error {
	count, err := store.CountTenantInvitationsSince(ctx, tenantID, monthStartUTC(now))
	if err != nil {
		return err
	}
	if count >= MonthlyTenantQuota {
		return ErrQuotaExceeded
	}
	return nil
}
