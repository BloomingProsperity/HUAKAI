package hermes

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteProfile_BlockedByInUseSettings(t *testing.T) {
	// Regression: deleting a profile still referenced by settings must not corrupt enabled Hermes users.
	store := &hermesStoreSpy{profileInUse: true, deleteRows: 1}
	service := NewService(store)

	err := service.DeleteProfile(context.Background(), 99, 7)

	// Mutation check: 删除 deleteProfileWithStore 的 ProfileInUse guard,此断言会得到 nil 并失败；删掉本断言 mutant 才会 PASS。
	if !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("err=%v want ErrProfileInUse", err)
	}
	if store.deleteCalled {
		t.Fatalf("DeleteProfile called while profile is still referenced by settings")
	}
}

func TestDeleteProfile_AllowsUnused(t *testing.T) {
	// Regression: the in-use guard must not block cleanup of an unused tenant-scoped profile.
	store := &hermesStoreSpy{profileInUse: false, deleteRows: 1}
	service := NewService(store)

	err := service.DeleteProfile(context.Background(), 99, 7)

	// Mutation check: 翻转 ProfileInUse 条件为 !inUse,unused profile 会错误返回 ErrProfileInUse 并失败。
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
