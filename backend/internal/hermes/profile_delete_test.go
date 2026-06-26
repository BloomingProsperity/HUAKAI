package hermes

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteProfile_BlockedByInUseSettings(t *testing.T) {
	// 回归测试：删除仍被 settings 引用的 profile 不能破坏已启用 Hermes 的用户。
	store := &hermesStoreSpy{profileInUse: true, deleteRows: 1}
	service := NewService(store)

	err := service.DeleteProfile(context.Background(), 99, 7)

	// 变异检查：删除 deleteProfileWithStore 的 ProfileInUse 守卫,此断言会得到 nil 并失败；删掉本断言变异体才会 PASS。
	if !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("err=%v want ErrProfileInUse", err)
	}
	if store.deleteCalled {
		t.Fatalf("DeleteProfile called while profile is still referenced by settings")
	}
}

func TestDeleteProfile_AllowsUnused(t *testing.T) {
	// 回归测试：in-use 守卫不能阻止清理未被使用的、租户级的 profile。
	store := &hermesStoreSpy{profileInUse: false, deleteRows: 1}
	service := NewService(store)

	err := service.DeleteProfile(context.Background(), 99, 7)

	// 变异检查：翻转 ProfileInUse 条件为 !inUse,未使用的 profile 会错误返回 ErrProfileInUse 并失败。
	if err != nil {
		t.Fatalf("DeleteProfile unused: %v", err)
	}
	if !store.profileInUseCalled || store.profileInUseArg.TenantID != 7 || store.profileInUseArg.ProfileID != 99 {
		t.Fatalf("profile usage check arg=(tenant:%d profile:%d) called=%v want (7,99)",
			store.profileInUseArg.TenantID, store.profileInUseArg.ProfileID, store.profileInUseCalled)
	}
	if !store.deleteCalled || store.deleteArg.TenantID != 7 || store.deleteArg.ID != 99 {
		t.Fatalf("delete arg=(tenant:%d profile:%d) called=%v want (7,99)",
			store.deleteArg.TenantID, store.deleteArg.ID, store.deleteCalled)
	}
}
