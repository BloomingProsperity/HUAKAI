package passkey

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// cloneOnUpdateStore 嵌入真 MemoryStore,但把 UpdateCredentialUsage 强制返回
// ErrCloneDetected(模拟并发竞态下 store CAS 失败),并记录 FlagCredentialCloneWarning
// 是否被 service 调用。其余方法委派给嵌入的 Store(注册凭据正常落地)。
type cloneOnUpdateStore struct {
	Store
	flagged bool
}

func (s *cloneOnUpdateStore) UpdateCredentialUsage(context.Context, int64, []byte, uint32, bool, time.Time) (CredentialRecord, error) {
	return CredentialRecord{}, ErrCloneDetected
}

func (s *cloneOnUpdateStore) FlagCredentialCloneWarning(ctx context.Context, tenantID int64, credentialID []byte, now time.Time) error {
	s.flagged = true
	return s.Store.FlagCredentialCloneWarning(ctx, tenantID, credentialID, now)
}

// TestPasskeyLoginFlagsCloneWarningOnStoreCAS 守护 S2 修复:当 store 的 CAS 在并发
// 竞态下返回 ErrCloneDetected(应用层 signCountRegressed 已放行该请求),service 必须
// 与单请求路径一致地置位 clone_warning,否则这条防克隆信号在竞态路径下丢失。
// 变异证伪:删掉 LoginFinish 里 `if errors.Is(err, ErrCloneDetected) { Flag }`,
// 则 store.flagged 仍为 false → 末断言转红。
func TestPasskeyLoginFlagsCloneWarningOnStoreCAS(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 6, 10, 9, 0, 0, time.UTC)
	engine := &fakeEngine{loginCredentialID: []byte("registered-cred"), assertedSignCount: 2}
	user := testUser(1, 101, "alice@example.test")
	users := fakeUsers{rows: map[userKey]userauth.User{{tenantID: 1, userID: 101}: user}}
	store := &cloneOnUpdateStore{Store: NewMemoryStore()}
	svc := NewService(store, users, StaticConfigSource(testConfig()), WithCeremonyEngine(engine), WithNow(func() time.Time { return now }))

	rb, err := svc.RegisterBegin(ctx, RegisterBeginInput{TenantID: 1, User: user, Name: "MacBook"})
	if err != nil {
		t.Fatalf("RegisterBegin: %v", err)
	}
	registered, err := svc.RegisterFinish(ctx, RegisterFinishInput{TenantID: 1, User: user, SessionID: rb.SessionID, CredentialJSON: []byte(`{"id":"registered-cred"}`), Name: "MacBook"})
	if err != nil || !bytes.Equal(registered.CredentialID, []byte("registered-cred")) {
		t.Fatalf("RegisterFinish: %v cred=%x", err, registered.CredentialID)
	}

	lb, err := svc.LoginBegin(ctx, LoginBeginInput{TenantID: 1})
	if err != nil {
		t.Fatalf("LoginBegin: %v", err)
	}
	// 应用层 signCountRegressed 放行(asserted=2 > stored),但 store CAS 返回 Clone。
	_, err = svc.LoginFinish(ctx, LoginFinishInput{TenantID: 1, SessionID: lb.SessionID, CredentialJSON: []byte(`{"id":"registered-cred"}`)})
	if !errors.Is(err, ErrCloneDetected) {
		t.Fatalf("LoginFinish err=%v want ErrCloneDetected", err)
	}
	if !store.flagged {
		t.Fatal("store 返回 ErrCloneDetected 时 service 必须置位 clone_warning(调 FlagCredentialCloneWarning),却没调用")
	}
}
