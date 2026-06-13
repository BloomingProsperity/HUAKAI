package userauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// selfAccountStubStore 是只实现自助账户 service 所需窄接口(GetUserByID +
// UpdateOwnPassword + SoftDeleteUser)的最小桩,用来隔离 service 编排逻辑。
type selfAccountStubStore struct {
	Store // 嵌入 nil Store 满足全接口;未实现的方法不会被自助账户路径调用。

	user          User
	getErr        error
	updatedHash   string
	updateCalls   int
	updateUserOut User
	softDelCalls  int
	softDelErr    error
	softDelUserID int64
}

func (s *selfAccountStubStore) GetUserByID(_ context.Context, _, _ int64) (User, error) {
	if s.getErr != nil {
		return User{}, s.getErr
	}
	return s.user, nil
}

func (s *selfAccountStubStore) UpdateOwnPassword(_ context.Context, _, _ int64, passwordHash string) (User, error) {
	s.updateCalls++
	s.updatedHash = passwordHash
	return s.updateUserOut, nil
}

func (s *selfAccountStubStore) SoftDeleteUser(_ context.Context, _, userID int64, _ time.Time) (User, error) {
	s.softDelCalls++
	s.softDelUserID = userID
	if s.softDelErr != nil {
		return User{}, s.softDelErr
	}
	return User{ID: userID, Status: UserStatusDeleted}, nil
}

func testServiceWithStore(store Store) *Service {
	svc := NewService(store)
	// 小成本 argon2,加速测试(仍真实校验 / 真实 hash)。
	svc.PasswordPolicy = PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	return svc
}

// 改密-旧密校验:旧密对 → UpdateOwnPassword 被调,写入的 hash 能 VerifyPassword(新密)通过、
// (旧密)失败。旧密错 → ErrInvalidCredentials 且 UpdateOwnPassword NOT 被调。
// MUTATION: service 跳过 verifyPasswordFn(任何旧密都放行)→ wrong-old 用例不再返
// ErrInvalidCredentials → 红;且 update 被调 → updateCalls 断言红。
func TestChangeOwnPasswordVerifiesOldPassword(t *testing.T) {
	ctx := context.Background()
	policy := PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	oldHash, err := HashPassword("correct-old", policy)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	// 旧密错:拒绝,不触碰 update。
	storeWrong := &selfAccountStubStore{user: User{ID: 42, TenantID: 7, PasswordHash: oldHash, Status: UserStatusActive}}
	svcWrong := testServiceWithStore(storeWrong)
	if _, err := svcWrong.ChangeOwnPassword(ctx, 7, 42, "WRONG-old", "brand-new"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong old password err=%v want ErrInvalidCredentials", err)
	}
	if storeWrong.updateCalls != 0 {
		t.Fatalf("UpdateOwnPassword calls=%d want 0 on wrong old password; MUTATION: skipping verify lets update through", storeWrong.updateCalls)
	}

	// 旧密对:改密,写入新 hash。
	storeOK := &selfAccountStubStore{user: User{ID: 42, TenantID: 7, PasswordHash: oldHash, Status: UserStatusActive}}
	storeOK.updateUserOut = User{ID: 42, TenantID: 7}
	svcOK := testServiceWithStore(storeOK)
	if _, err := svcOK.ChangeOwnPassword(ctx, 7, 42, "correct-old", "brand-new-secret"); err != nil {
		t.Fatalf("correct old password ChangeOwnPassword: %v", err)
	}
	if storeOK.updateCalls != 1 {
		t.Fatalf("UpdateOwnPassword calls=%d want 1", storeOK.updateCalls)
	}
	// 写入的 hash 必须是新密的 hash:新密能验过、旧密验不过(判别 fixture:新旧明文不同)。
	if ok, _ := VerifyPassword(storeOK.updatedHash, "brand-new-secret"); !ok {
		t.Fatalf("new password does not verify against stored hash; MUTATION: hashing old password instead would fail here")
	}
	if ok, _ := VerifyPassword(storeOK.updatedHash, "correct-old"); ok {
		t.Fatalf("old password still verifies against new hash; MUTATION: no-op update leaves old hash")
	}
}

// 改密-social-only 账号(无本地口令)不放行:PasswordHash 空 → ErrInvalidCredentials,update NOT 被调。
// MUTATION: 删掉空 hash 守卫 → VerifyPassword 对空 hash 走 parse,返回 (false, err) 仍拒,
// 但若 service 误把空 hash 当「无需校验」放行就会 update → updateCalls 断言抓住。
func TestChangeOwnPasswordRejectsSocialOnly(t *testing.T) {
	ctx := context.Background()
	store := &selfAccountStubStore{user: User{ID: 42, TenantID: 7, PasswordHash: "", Status: UserStatusActive}}
	svc := testServiceWithStore(store)
	if _, err := svc.ChangeOwnPassword(ctx, 7, 42, "anything", "brand-new-secret"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("social-only change err=%v want ErrInvalidCredentials", err)
	}
	if store.updateCalls != 0 {
		t.Fatalf("UpdateOwnPassword calls=%d want 0 for social-only account", store.updateCalls)
	}
}

// 改密-空新密 → ErrInvalidInput,update NOT 被调(service 层早返,与 handler 层双保险)。
func TestChangeOwnPasswordRejectsEmptyNew(t *testing.T) {
	ctx := context.Background()
	policy := PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	oldHash, _ := HashPassword("correct-old", policy)
	store := &selfAccountStubStore{user: User{ID: 42, TenantID: 7, PasswordHash: oldHash, Status: UserStatusActive}}
	svc := testServiceWithStore(store)
	if _, err := svc.ChangeOwnPassword(ctx, 7, 42, "correct-old", "   "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty new password err=%v want ErrInvalidInput", err)
	}
	if store.updateCalls != 0 {
		t.Fatalf("UpdateOwnPassword calls=%d want 0 on empty new password", store.updateCalls)
	}
}

// 删号-service 透传到 store(末位 admin 保护在 store 层判定):store 返 ErrLastAdmin → 透传;
// store 成功 → 透传 User。断言 SoftDeleteUser 收到 session userID。
// MUTATION: service 吞掉 ErrLastAdmin(返 nil)→ 第一段断言红。
func TestSoftDeleteSelfDelegatesToStore(t *testing.T) {
	ctx := context.Background()

	storeLast := &selfAccountStubStore{softDelErr: ErrLastAdmin}
	svcLast := testServiceWithStore(storeLast)
	if _, err := svcLast.SoftDeleteSelf(ctx, 7, 42); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last-admin SoftDeleteSelf err=%v want ErrLastAdmin", err)
	}

	storeOK := &selfAccountStubStore{}
	svcOK := testServiceWithStore(storeOK)
	out, err := svcOK.SoftDeleteSelf(ctx, 7, 42)
	if err != nil {
		t.Fatalf("SoftDeleteSelf: %v", err)
	}
	if storeOK.softDelCalls != 1 || storeOK.softDelUserID != 42 {
		t.Fatalf("SoftDeleteUser call mismatch: calls=%d userID=%d", storeOK.softDelCalls, storeOK.softDelUserID)
	}
	if out.Status != UserStatusDeleted {
		t.Fatalf("returned user status=%q want deleted", out.Status)
	}
}
