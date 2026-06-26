package credentialstore

import (
	"context"
	"time"
)

// IneffectiveRefreshBackoff 是当一次 refresh "成功" 但产出的 token 仍立即到期需要再次
// refresh(无效刷新),或当本不需要 refresh 时,所施加的节流时长。它防止对上游 provider
// 形成密集的 refresh 风暴。
const IneffectiveRefreshBackoff = 30 * time.Second

// ineffectiveRefreshNextAttempt 返回一次 refresh 成功后要持久化的 next_attempt_at 值。
// 若刚计算出的 refreshBeforeAt 已 <= now,说明本次 refresh 是无效的(上游返回了一个
// 接近过期的 token),因此我们对下一次尝试做节流。否则原样返回正常值(normalNext)
// ——这是 DEFAULT/SAFE 路径,绝不可改动。
func ineffectiveRefreshNextAttempt(refreshBeforeAt, now time.Time, normalNext time.Time) time.Time {
	if !refreshBeforeAt.After(now) {
		// token 仍立即到期需要 refresh:节流。
		return now.Add(IneffectiveRefreshBackoff)
	}
	return normalNext
}

// SetNextAttemptThrottle 在不改变 credential 记录的 state、failure_class 或 failure_count
// 的前提下设置其 next_attempt_at。它在 ErrNoRefreshRequired 路径上被调用,以防止密集的
// 重试循环。
func (s *Store) SetNextAttemptThrottle(ctx context.Context, rec CredentialRecord, nextAttemptAt time.Time) error {
	if err := s.validateReady(); err != nil {
		return err
	}
	const q = `
UPDATE account_credentials
SET next_attempt_at = $1,
    updated_at = NOW()
WHERE id = $2
  AND tenant_id = $3
  AND provider_account_id = $4
  AND deleted_at IS NULL
  AND credential_version = $5`
	tag, err := s.db.Exec(ctx, q, nullableTime(nextAttemptAt), rec.ID, rec.TenantID, rec.ProviderAccountID, rec.CredentialVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCredentialNotFound
	}
	return nil
}
