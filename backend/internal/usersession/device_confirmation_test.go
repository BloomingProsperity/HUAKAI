package usersession

import (
	"context"
	"errors"
	"testing"
	"time"
)

// seedActiveFamilies 直接在 MemoryStore 里塞 n 个活跃 family (last_active_at 递增, 第 0 个最老),
// 用于把用户顶到 MaxActiveFamilies 上限以触发 confirm 流。返回最老 family 的 ID。
func seedActiveFamilies(t *testing.T, store *MemoryStore, tenantID, userID int64, n int, base time.Time) (oldestID string) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := 0; i < n; i++ {
		id := familyTestID(userID, i)
		store.families[id] = SessionFamily{
			ID: id, TenantID: tenantID, UserID: userID, Status: FamilyStatusActive,
			Generation: 1, CreatedAt: base.Add(-time.Hour),
			// i=0 最老 (last_active 最早), 故 ListActiveFamiliesForDevicePolicy 按 ASC 排序时居首。
			LastActiveAt: base.Add(time.Duration(i) * time.Minute),
			DeviceInfo:   map[string]any{},
		}
		if i == 0 {
			oldestID = id
		}
	}
	return oldestID
}

func familyTestID(userID int64, i int) string {
	return "fam-" + time.Duration(userID).String() + "-" + time.Duration(i).String()
}

func newConfirmService(store *MemoryStore, now time.Time) *Service {
	svc := NewService(store)
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	svc.MaxActiveFamilies = 2
	svc.DevicePolicy = "confirm"
	return svc
}

// countActiveFamilies 数某用户当前仍 active 的 family 个数 (腾位断言用)。
func countActiveFamilies(store *MemoryStore, tenantID, userID int64) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	c := 0
	for _, f := range store.families {
		if f.TenantID == tenantID && f.UserID == userID && f.Status == FamilyStatusActive {
			c++
		}
	}
	return c
}

// TestConfirmFlow_CreatesPendingThenConfirmFreesOldest 覆盖 confirm 流主路径:
// 达上限 → Create 返回携带 token 的类型化错误 (errors.Is 仍真) + 落 pending 记录 →
// 带 token 调 ConfirmDevice → 撤最老 family 腾位 (活跃数 2→1)。
//
// 变异 (§14): 删 ConfirmDevice 末尾的 RevokeFamily(撤最老)→ 活跃数停在 2, "want 1" 断言变红。
//   (已手动验证: 临时把 RevokeFamily 那两行删掉, 本测试 activeAfter=2 报红, 还原后绿。)
func TestConfirmFlow_CreatesPendingThenConfirmFreesOldest(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	const tenantID, userID = int64(1), int64(7)
	oldest := seedActiveFamilies(t, store, tenantID, userID, 2, base)

	svc := newConfirmService(store, base)

	// 达上限登录: 应返回 DeviceConfirmationRequiredError, 且 errors.Is(ErrDeviceConfirmationRequired) 为真。
	_, err := svc.Create(ctx, CreateInput{TenantID: tenantID, UserID: userID, IP: "10.0.0.1", UserAgent: "Chrome/1"})
	if !errors.Is(err, ErrDeviceConfirmationRequired) {
		t.Fatalf("Create err=%v want ErrDeviceConfirmationRequired", err)
	}
	var confirmErr *DeviceConfirmationRequiredError
	if !errors.As(err, &confirmErr) {
		t.Fatalf("Create err=%v want *DeviceConfirmationRequiredError", err)
	}
	if confirmErr.RawToken == "" {
		t.Fatal("DeviceConfirmationRequiredError.RawToken is empty; handler cannot send confirmation email")
	}
	// Error() 绝不泄露原文 token。
	if got := confirmErr.Error(); got != ErrDeviceConfirmationRequired.Error() {
		t.Fatalf("Error()=%q must equal sentinel text (no raw token leak)", got)
	}

	// pending 记录已落库 (按 token_hash 可取回, status=pending)。
	dc, getErr := store.GetDeviceConfirmationByTokenHash(ctx, tenantID, HashDeviceConfirmationToken(confirmErr.RawToken))
	if getErr != nil {
		t.Fatalf("GetDeviceConfirmationByTokenHash: %v", getErr)
	}
	if dc.Status != DeviceConfirmationStatusPending {
		t.Fatalf("pending record status=%q want pending", dc.Status)
	}

	if got := countActiveFamilies(store, tenantID, userID); got != 2 {
		t.Fatalf("active families before confirm=%d want 2 (nothing revoked yet)", got)
	}

	// 确认: 应撤最老 family 腾位。
	if err := svc.ConfirmDevice(ctx, tenantID, confirmErr.RawToken); err != nil {
		t.Fatalf("ConfirmDevice: %v", err)
	}
	if got := countActiveFamilies(store, tenantID, userID); got != 1 {
		t.Fatalf("active families after confirm=%d want 1 (oldest revoked to free a slot)", got)
	}
	// 被撤的必须是最老那个。
	store.mu.Lock()
	revokedOldest := store.families[oldest].Status == FamilyStatusRevoked
	store.mu.Unlock()
	if !revokedOldest {
		t.Fatalf("oldest family %s was not the one revoked", oldest)
	}
}

// TestConfirmFlow_ReplayIsIdempotentAndDoesNotFreeTwice 覆盖重放幂等:
// 同一 token 二次 ConfirmDevice 必须不再撤 family。第一次撤最老 (2→1),
// 第二次应被 MarkDeviceConfirmationConfirmed 的条件 UPDATE 挡住 (返回已用语义), 活跃数维持 1。
//
// 防重放是 defense-in-depth 双层: ① ConfirmDevice 里的 `dc.Status != pending` 顺序守卫;
// ② MarkDeviceConfirmationConfirmed 的 `dc.Status != pending` 条件 UPDATE (并发竞态根)。
// 变异 (§14): 同时拆掉这两层 (ConfirmDevice 守卫改 `if false && ...` + Memory MarkConfirmed 去掉
//   pending 条件无条件返回 true)→ 第二次确认会再撤一个 family, 活跃数从 2 掉到 1, 二次 err=nil,
//   "want already-consumed sentinel" 断言变红。只拆任一层另一层仍兜住 (本测试仍绿), 证明两层都在生效。
//   (已手动验证: 两层都拆 → 二次 err=nil 报红; 只拆 ConfirmDevice 守卫 → 仍绿; 全还原 → 绿。)
func TestConfirmFlow_ReplayIsIdempotentAndDoesNotFreeTwice(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 29, 11, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	const tenantID, userID = int64(1), int64(8)
	// 起始塞 3 个活跃 (上限设 2), 这样若重放误撤两次, 活跃数会从 2 掉到 0, 与正确的 1 区分明显。
	seedActiveFamilies(t, store, tenantID, userID, 3, base)
	svc := newConfirmService(store, base)

	_, err := svc.Create(ctx, CreateInput{TenantID: tenantID, UserID: userID, IP: "10.0.0.2", UserAgent: "Chrome/1"})
	var confirmErr *DeviceConfirmationRequiredError
	if !errors.As(err, &confirmErr) {
		t.Fatalf("Create err=%v want *DeviceConfirmationRequiredError", err)
	}

	if err := svc.ConfirmDevice(ctx, tenantID, confirmErr.RawToken); err != nil {
		t.Fatalf("first ConfirmDevice: %v", err)
	}
	firstActive := countActiveFamilies(store, tenantID, userID)
	if firstActive != 2 {
		t.Fatalf("active after first confirm=%d want 2 (one of three revoked)", firstActive)
	}

	// 二次确认: token 已被消费 → 必须返回"已用"语义 (顺序重放走 status!=pending 守卫 →
	// ErrDeviceConfirmationNotFound; 并发竞态走 MarkConfirmed 命中 0 行 → ErrRefreshReplay,
	// 二者同为"已消费"且 handler 都映射到 401), 且绝不再撤 family。
	secondErr := svc.ConfirmDevice(ctx, tenantID, confirmErr.RawToken)
	if !errors.Is(secondErr, ErrDeviceConfirmationNotFound) && !errors.Is(secondErr, ErrRefreshReplay) {
		t.Fatalf("replay ConfirmDevice err=%v want already-consumed sentinel (NotFound or Replay)", secondErr)
	}
	if got := countActiveFamilies(store, tenantID, userID); got != firstActive {
		t.Fatalf("active after replay=%d want %d (replay must not revoke again)", got, firstActive)
	}
}

// TestConfirmDevice_ExpiredTokenRejected 覆盖过期拒绝: pending 但已过期的 token 必须被挡,
// 且不撤任何 family。
//
// 变异 (§14): 删 ConfirmDevice 里的 `!dc.ExpiresAt.After(now)` 过期检查 → 过期 token 会被放行去腾位,
//   "want ErrTokenExpired" 断言变红 (返回 nil) 且活跃数会掉到 1。
//   (已手动验证: 临时注释掉过期分支, err=nil 报红; 还原后绿。)
func TestConfirmDevice_ExpiredTokenRejected(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	const tenantID, userID = int64(1), int64(9)
	seedActiveFamilies(t, store, tenantID, userID, 2, base)

	svc := NewService(store)
	svc.SigningKey = testSigningKey()
	svc.MaxActiveFamilies = 2
	svc.DevicePolicy = "confirm"
	svc.DeviceConfirmationTTL = time.Hour
	// 生成 token 时 now=base。
	svc.Now = func() time.Time { return base }

	_, err := svc.Create(ctx, CreateInput{TenantID: tenantID, UserID: userID, IP: "10.0.0.3", UserAgent: "Chrome/1"})
	var confirmErr *DeviceConfirmationRequiredError
	if !errors.As(err, &confirmErr) {
		t.Fatalf("Create err=%v want *DeviceConfirmationRequiredError", err)
	}

	// 把时钟推到 TTL 之后 (2 小时 > 1 小时 TTL), 确认必须被拒。
	svc.Now = func() time.Time { return base.Add(2 * time.Hour) }
	if err := svc.ConfirmDevice(ctx, tenantID, confirmErr.RawToken); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("ConfirmDevice with expired token err=%v want ErrTokenExpired", err)
	}
	if got := countActiveFamilies(store, tenantID, userID); got != 2 {
		t.Fatalf("active families after expired confirm=%d want 2 (nothing revoked)", got)
	}
}

// TestConfirmDevice_WrongTokenRejected 覆盖 token hash 比对: 用一个未注册的 token 调确认必须被挡,
// 且不撤任何 family。这是 hash 比对正确性的护栏。
//
// 变异 (§14): 把 ConfirmDevice 里 HashDeviceConfirmationToken(token) 换成固定常量 (使任意 token 都
//   命中同一条记录)→ 错误 token 会被放行, "want ErrDeviceConfirmationNotFound" 断言变红。
//   (已手动验证: 临时把传入 hash 改成 pending 记录的真 hash, err=nil 报红; 还原后绿。)
func TestConfirmDevice_WrongTokenRejected(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 29, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	const tenantID, userID = int64(1), int64(10)
	seedActiveFamilies(t, store, tenantID, userID, 2, base)
	svc := newConfirmService(store, base)

	if _, err := svc.Create(ctx, CreateInput{TenantID: tenantID, UserID: userID, IP: "10.0.0.4", UserAgent: "Chrome/1"}); !errors.Is(err, ErrDeviceConfirmationRequired) {
		t.Fatalf("Create err=%v want ErrDeviceConfirmationRequired", err)
	}

	// 一个没注册过的随机 token: hash 对不上任何 pending 记录 → 必须 ErrDeviceConfirmationNotFound。
	wrong, _, genErr := GenerateDeviceConfirmationToken()
	if genErr != nil {
		t.Fatalf("GenerateDeviceConfirmationToken: %v", genErr)
	}
	if err := svc.ConfirmDevice(ctx, tenantID, wrong); !errors.Is(err, ErrDeviceConfirmationNotFound) {
		t.Fatalf("ConfirmDevice with wrong token err=%v want ErrDeviceConfirmationNotFound", err)
	}
	if got := countActiveFamilies(store, tenantID, userID); got != 2 {
		t.Fatalf("active families after wrong-token confirm=%d want 2 (nothing revoked)", got)
	}
}

// TestDevicePolicyDormantByDefault 覆盖休眠不变量: MaxActiveFamilies=0 时, 即便用户已有一堆活跃
// family 且 DevicePolicy=confirm, Create 也必须照常成功签发、绝不落 pending 记录、绝不撤 family。
// 这是"默认零生产行为变更"的护栏。
//
// 变异 (§14): 把 enforceDevicePolicy 里 `s.MaxActiveFamilies <= 0` 的早返回删掉 → 休眠态会误入
//   confirm 分支落 pending/返回错误, "Create should succeed" 断言变红。
//   (已手动验证: 临时把早返回的 <=0 改成 <0, max=0 时进了 confirm 分支, Create 返回错误报红; 还原后绿。)
func TestDevicePolicyDormantByDefault(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 6, 29, 14, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	const tenantID, userID = int64(1), int64(11)
	seedActiveFamilies(t, store, tenantID, userID, 5, base)

	svc := NewService(store)
	svc.SigningKey = testSigningKey()
	svc.Now = func() time.Time { return base }
	// 默认: MaxActiveFamilies 留 0, DevicePolicy 即便误设 confirm 也应被休眠短路。
	svc.DevicePolicy = "confirm"

	tokens, err := svc.Create(ctx, CreateInput{TenantID: tenantID, UserID: userID, IP: "10.0.0.5", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create should succeed when device policy dormant (max=0): %v", err)
	}
	if tokens.SessionToken == "" {
		t.Fatal("dormant policy must still issue a session token")
	}
	// 不得新增任何 active family 被撤 (原 5 个仍在 + 新建 1 个 = 6)。
	if got := countActiveFamilies(store, tenantID, userID); got != 6 {
		t.Fatalf("active families=%d want 6 (5 seeded + 1 new, none revoked)", got)
	}
}
