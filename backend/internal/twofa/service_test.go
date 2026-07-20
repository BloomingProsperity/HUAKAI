package twofa

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type atomicMemoryStore struct {
	*MemoryStore
}

func (s *atomicMemoryStore) RecordFailure(_ context.Context, tenantID, userID int64, maxFailedAttempts int, lockUntil, now time.Time) (int, bool, error) {
	if s == nil {
		return 0, false, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := userKey(tenantID, userID)
	settings, ok := s.settings[key]
	if !ok {
		return 0, false, ErrNotSetup
	}
	settings.FailedAttempts++
	locked := settings.FailedAttempts >= maxFailedAttempts
	if locked {
		settings.LockedUntil = cloneTimePtr(&lockUntil)
	}
	settings.UpdatedAt = now
	s.settings[key] = settings
	return settings.FailedAttempts, locked, nil
}

func TestSetupEncryptsSecretAndEnableRequiresCurrentTOTP(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	svc := NewService(store, mustKeyProvider(t), WithNow(func() time.Time { return now }))

	setup, err := svc.Setup(ctx, SetupInput{TenantID: 1, UserID: 1001, AccountName: "alice@example.test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if setup.Secret == "" || len(setup.BackupCodes) != DefaultBackupCodeCount {
		t.Fatalf("setup response missing secret or backup codes: %+v", setup)
	}
	if !strings.Contains(setup.QRData, "otpauth://totp/") || !strings.Contains(setup.QRData, "issuer=HUAKAI") || !strings.Contains(setup.QRData, setup.Secret) {
		t.Fatalf("qr_data=%q missing otpauth issuer or one-time setup secret", setup.QRData)
	}
	stored, found, err := store.GetSettings(ctx, 1, 1001)
	if err != nil || !found {
		t.Fatalf("stored settings found=%v err=%v", found, err)
	}
	if bytes.Contains(stored.SecretEnc, []byte(setup.Secret)) {
		t.Fatalf("encrypted secret contains setup secret plaintext: %q", string(stored.SecretEnc))
	}
	if stored.Enabled {
		t.Fatal("Setup must not enable 2FA before a correct TOTP proof")
	}

	code := codeFromSetupSecret(t, setup.Secret, now)
	status, err := svc.Enable(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: code})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !status.Enabled || status.BackupCodesRemaining != DefaultBackupCodeCount {
		t.Fatalf("status after enable=%+v", status)
	}
}

func TestBackupCodeLoginCanBeUsedOnlyOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 12, 10, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), mustKeyProvider(t), WithNow(func() time.Time { return now }))
	setup := setupAndEnable(t, ctx, svc, now)
	backup := setup.BackupCodes[0]

	result, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: backup})
	if err != nil {
		t.Fatalf("first backup VerifyLogin: %v", err)
	}
	if result.Method != MethodBackupCode || result.BackupCodesRemaining != DefaultBackupCodeCount-1 {
		t.Fatalf("first backup result=%+v", result)
	}
	_, err = svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: backup})
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("second backup VerifyLogin err=%v want ErrInvalidCode", err)
	}
}

func TestFailedAttemptsLockTOTPUntilLockWindowExpires(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 12, 20, 0, 0, time.UTC)
	svc := NewService(
		NewMemoryStore(),
		mustKeyProvider(t),
		WithNow(func() time.Time { return now }),
		WithMaxFailedAttempts(3),
		WithLockDuration(10*time.Minute),
	)
	setup := setupAndEnable(t, ctx, svc, now)
	valid := codeFromSetupSecret(t, setup.Secret, now)

	for i := 1; i <= 2; i++ {
		_, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: "000000"})
		if !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("wrong attempt %d err=%v want ErrInvalidCode", i, err)
		}
	}
	_, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: "000000"})
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("third wrong attempt err=%v want ErrLocked", err)
	}
	_, err = svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: valid})
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("valid code while locked err=%v want ErrLocked", err)
	}
	now = now.Add(11 * time.Minute)
	valid = codeFromSetupSecret(t, setup.Secret, now)
	if _, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: valid}); err != nil {
		t.Fatalf("valid code after lock expiry: %v", err)
	}
}

func TestLoginChallengeRequiresValidSignatureAndCode(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 3, 12, 30, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), mustKeyProvider(t), WithNow(func() time.Time { return now }))
	setup := setupAndEnable(t, ctx, svc, now)

	challenge, err := svc.StartLoginChallenge(ctx, 1, 1001)
	if err != nil {
		t.Fatalf("StartLoginChallenge: %v", err)
	}
	if challenge.ID == "" || !challenge.ExpiresAt.After(now) {
		t.Fatalf("challenge=%+v", challenge)
	}
	tampered := challenge.ID[:len(challenge.ID)-1] + "x"
	_, err = svc.VerifyLoginChallenge(ctx, ChallengeVerifyInput{ChallengeID: tampered, Code: codeFromSetupSecret(t, setup.Secret, now)})
	if !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("tampered challenge err=%v want ErrChallengeInvalid", err)
	}
	// 错误验证码: 失败也必须回填 challenge 解出的身份, 否则调用方失败/锁定审计恒记 0/0 无法归因。
	// mutation: VerifyLoginChallenge 失败分支退回裸 `return VerifyResult{}, err` → 断言红。
	failed, err := svc.VerifyLoginChallenge(ctx, ChallengeVerifyInput{ChallengeID: challenge.ID, Code: "000000"})
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code err=%v want ErrInvalidCode", err)
	}
	if failed.TenantID != 1 || failed.UserID != 1001 {
		t.Fatalf("失败结果身份 = (%d,%d), want (1,1001) —— 审计无法归因", failed.TenantID, failed.UserID)
	}
	result, err := svc.VerifyLoginChallenge(ctx, ChallengeVerifyInput{ChallengeID: challenge.ID, Code: codeFromSetupSecret(t, setup.Secret, now)})
	if err != nil {
		t.Fatalf("VerifyLoginChallenge: %v", err)
	}
	if result.TenantID != 1 || result.UserID != 1001 || result.Method != MethodTOTP {
		t.Fatalf("challenge result=%+v", result)
	}
}

func setupAndEnable(t *testing.T, ctx context.Context, svc *Service, now time.Time) SetupResult {
	t.Helper()
	setup, err := svc.Setup(ctx, SetupInput{TenantID: 1, UserID: 1001, AccountName: "alice@example.test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := svc.Enable(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: codeFromSetupSecret(t, setup.Secret, now)}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	return setup
}

func codeFromSetupSecret(t *testing.T, encoded string, now time.Time) string {
	t.Helper()
	secret, err := DecodeSecret(encoded)
	if err != nil {
		t.Fatalf("DecodeSecret: %v", err)
	}
	code, err := GenerateTOTP(secret, now, DefaultTOTPDigits, DefaultTOTPStep)
	if err != nil {
		t.Fatalf("GenerateTOTP: %v", err)
	}
	return code
}

func mustKeyProvider(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("twofa-test-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key provider: %v", err)
	}
	return keys
}

// TestVerifyLoginRejectsTOTPReplayWithinWindow 守审计 wy94u3tn9 的 S1:同一枚 TOTP 码在其
// ±窗口有效期内被重复提交,第 1 次成功后第 2 次必须按 ErrCodeReused 拒绝(RFC 6238 §5.2)。
// 此前 recordSuccess 只写 last_used_at、不记已消费时间步,故同码可重复登录。
// 判别(变异):把 VerifyLogin 的 TOTP 分支退回"只 VerifyTOTP + recordSuccess(不记 step)",
// 同码第 2 次又会成功 → 本测试断言 ErrCodeReused 变红。
func TestVerifyLoginRejectsTOTPReplayWithinWindow(t *testing.T) {
	ctx := context.Background()
	// 启用消费当前时间步;登录发生在 +60s 的全新时间步,避免被启用码占用的步误挡。
	clock := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), mustKeyProvider(t), WithNow(func() time.Time { return clock }))
	setup := setupAndEnable(t, ctx, svc, clock)
	clock = clock.Add(60 * time.Second)

	code := codeFromSetupSecret(t, setup.Secret, clock)
	if _, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: code}); err != nil {
		t.Fatalf("首次 TOTP 登录应成功: %v", err)
	}
	_, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: code})
	if !errors.Is(err, ErrCodeReused) {
		t.Fatalf("同码重放第 2 次应得 ErrCodeReused,实际 err=%v", err)
	}
	// 重放拒绝不计失败次数(不锁合法用户在网络重试里重复提交同一码)。
	status, err := svc.Status(ctx, 1, 1001)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.LockedUntil != nil {
		t.Fatalf("重放拒绝不应触发锁定,locked_until=%v", status.LockedUntil)
	}
}

// TestVerifyLoginAllowsNextTimeStepAfterConsume 守"防重放不误伤合法连续登录":消费时间步 N 后,
// 更晚时间步的新码计数器严格更大,必须被接受(否则正常的下一次登录会被误判为重放)。
// 判别(变异):让 VerifyTOTPStep 恒返回 0(或不按 candidateAt 计步)→ 更晚的码也被算成同一步、
// 被当作重放拒绝 → 本用例变红。
// 注:重放的**权威边界**由存储层条件 UPDATE(last_used_step < $4)守住,已由
// TestMarkTOTPSuccessConditionalGuard 在 store 层精确覆盖"同步/更早/更大步";service 层
// 快速路径(step <= *LastUsedStep)只是冗余的提前拒绝优化,其 off-by-one 不单独由本用例守。
func TestVerifyLoginAllowsNextTimeStepAfterConsume(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), mustKeyProvider(t), WithNow(func() time.Time { return clock }))
	setup := setupAndEnable(t, ctx, svc, clock)
	clock = clock.Add(60 * time.Second)
	if _, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: codeFromSetupSecret(t, setup.Secret, clock)}); err != nil {
		t.Fatalf("首次登录应成功: %v", err)
	}
	clock = clock.Add(60 * time.Second)
	if _, err := svc.VerifyLogin(ctx, VerifyInput{TenantID: 1, UserID: 1001, Code: codeFromSetupSecret(t, setup.Secret, clock)}); err != nil {
		t.Fatalf("更晚时间步的新码应被接受,实际 err=%v", err)
	}
}

// TestMarkTOTPSuccessConditionalGuard 直接守存储层的原子防重放条件(并发竞态兜底):仅当
// consumedStep 严格大于已存值时才记录并返回 stored=true。判别(变异):删掉 MemoryStore
// MarkTOTPSuccess 里的 `consumedStep <= *LastUsedStep → return false` 守卫(改为无条件记录),
// 则"同 step 第 2 次"和"更早 step"都会错误地返回 stored=true → 本测试变红。
func TestMarkTOTPSuccessConditionalGuard(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	if err := store.SaveSetup(ctx, Settings{TenantID: 1, UserID: 1001, SecretEnc: []byte("x"), CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatalf("SaveSetup: %v", err)
	}
	mustStored := func(step int64, want bool) {
		t.Helper()
		stored, err := store.MarkTOTPSuccess(ctx, 1, 1001, step, now)
		if err != nil {
			t.Fatalf("MarkTOTPSuccess(step=%d): %v", step, err)
		}
		if stored != want {
			t.Fatalf("MarkTOTPSuccess(step=%d) stored=%v want %v", step, stored, want)
		}
	}
	mustStored(100, true)  // 首次消费
	mustStored(100, false) // 同步重放 → 拒
	mustStored(99, false)  // 更早步 → 拒
	mustStored(101, true)  // 更大步 → 纳
}

func TestRecordFailureCountsConcurrentAttemptsAtomically(t *testing.T) {
	ctx := context.Background()
	store := &atomicMemoryStore{MemoryStore: NewMemoryStore()}
	now := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	if err := store.SaveSetup(ctx, Settings{TenantID: 1, UserID: 1001, SecretEnc: []byte("x"), CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}

	const attempts = 20
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := store.RecordFailure(ctx, 1, 1001, 5, now.Add(15*time.Minute), now)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}
	}
	settings, found, err := store.GetSettings(ctx, 1, 1001)
	if err != nil || !found {
		t.Fatalf("GetSettings found=%v err=%v", found, err)
	}
	if settings.FailedAttempts != attempts {
		t.Fatalf("failed attempts=%d，期望并发请求全部计入 %d", settings.FailedAttempts, attempts)
	}
	if settings.LockedUntil == nil {
		t.Fatal("达到阈值后必须进入锁定")
	}
}

// TestVerifyTOTPStepReturnsMatchedCounter 守 VerifyTOTPStep 返回的就是匹配到的时间步计数器
// (counter = at.Unix()/step),这是防重放比较的基准量;并守 VerifyTOTP 委托后 ok 行为不变。
// 判别(变异):让 VerifyTOTPStep 返回 0(或不按 candidateAt 计步)→ 计数器断言变红。
func TestVerifyTOTPStepReturnsMatchedCounter(t *testing.T) {
	secret := bytes.Repeat([]byte{0x1f}, secretBytes)
	at := time.Date(2026, 6, 4, 9, 0, 7, 0, time.UTC)
	cfg := TOTPConfig{Digits: DefaultTOTPDigits, Step: DefaultTOTPStep, Window: DefaultTOTPWindow}
	code, err := GenerateTOTP(secret, at, DefaultTOTPDigits, DefaultTOTPStep)
	if err != nil {
		t.Fatalf("GenerateTOTP: %v", err)
	}
	step, ok := VerifyTOTPStep(secret, code, at, cfg)
	if !ok {
		t.Fatal("当前时刻生成的码应匹配")
	}
	if want := at.Unix() / int64(DefaultTOTPStep.Seconds()); step != want {
		t.Fatalf("匹配时间步=%d want %d", step, want)
	}
	if VerifyTOTP(secret, code, at, cfg) != ok {
		t.Fatal("VerifyTOTP 委托后 ok 应与 VerifyTOTPStep 一致")
	}
}
