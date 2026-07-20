package usersession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSessionRefreshRotationAndReplayRevokesFamily(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 8, 30, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SessionTTL = time.Minute
	svc.RefreshTTL = time.Hour
	svc.SigningKey = testSigningKey()

	issued, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 42, IP: "10.1.2.3", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if issued.Generation != 1 || issued.RefreshToken == "" || issued.SessionToken == "" {
		t.Fatalf("bad issued tokens: %+v", issued)
	}
	now = now.Add(time.Minute)
	rotated, err := svc.Refresh(ctx, RefreshInput{TenantID: 1, UserID: 42, RefreshToken: issued.RefreshToken, IP: "10.1.9.9", UserAgent: "Chrome/2"})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if rotated.Generation != 2 || rotated.RefreshToken == issued.RefreshToken {
		t.Fatalf("rotation did not advance: old=%q new=%q gen=%d", issued.RefreshToken, rotated.RefreshToken, rotated.Generation)
	}
	if _, err := svc.Refresh(ctx, RefreshInput{TenantID: 1, UserID: 42, RefreshToken: issued.RefreshToken, IP: "10.1.9.9", UserAgent: "Chrome/2"}); !errors.Is(err, ErrRefreshReplay) {
		t.Fatalf("replay error = %v, want ErrRefreshReplay", err)
	}
	families, err := svc.List(ctx, 1, 42)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(families) != 1 || families[0].Status != FamilyStatusRevoked {
		t.Fatalf("family not revoked after replay: %+v", families)
	}
}

func TestCreateEnforcesDeviceLimitAtomically(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	svc := NewService(store)
	svc.SigningKey = testSigningKey()
	svc.MaxActiveFamilies = 3
	svc.DevicePolicy = "deny"

	const requests = 24
	var wg sync.WaitGroup
	results := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 88, IP: "10.1.1.1", UserAgent: "Chrome/1"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	succeeded := 0
	limited := 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrDeviceLimitExceeded):
			limited++
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	if succeeded != 3 || limited != requests-3 {
		t.Fatalf("succeeded=%d limited=%d，期望 3/%d", succeeded, limited, requests-3)
	}
	families, err := store.ListActiveFamiliesForDevicePolicy(ctx, 1, 88, requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 3 {
		t.Fatalf("active families=%d，期望严格等于上限 3", len(families))
	}
	if len(store.tokens) != 3 || len(store.sessionTokens) != 3 {
		t.Fatalf("refresh/session tokens=%d/%d，不能留下半截会话", len(store.tokens), len(store.sessionTokens))
	}
}

func TestSessionAnomalyHighRevokesFamily(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 8, 45, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	issued, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 7, IP: "10.1.2.3", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := svc.Refresh(ctx, RefreshInput{TenantID: 1, UserID: 7, RefreshToken: issued.RefreshToken, IP: "172.16.2.3", UserAgent: "Firefox/1"}); !errors.Is(err, ErrAnomalyRejected) {
		t.Fatalf("anomaly refresh error = %v, want ErrAnomalyRejected", err)
	}
	families, err := svc.List(ctx, 1, 7)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := families[0].RevokedReason; got != "ip_and_ua_changed" {
		t.Fatalf("revoked reason = %q", got)
	}
}

func TestSessionRefreshRejectsCrossUserRefreshToken(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 8, 50, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()

	if _, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 101, IP: "10.1.1.1", UserAgent: "Chrome/1"}); err != nil {
		t.Fatalf("Create caller: %v", err)
	}
	target, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 202, IP: "10.2.1.1", UserAgent: "Firefox/1"})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}

	now = now.Add(time.Minute)
	if _, err := svc.Refresh(ctx, RefreshInput{
		TenantID: 1, UserID: 101, RefreshToken: target.RefreshToken,
		IP: "10.1.1.1", UserAgent: "Chrome/1",
	}); !errors.Is(err, ErrSessionUserMismatch) {
		t.Fatalf("cross-user refresh error = %v, want ErrSessionUserMismatch", err)
	}
	if _, err := svc.Refresh(ctx, RefreshInput{
		TenantID: 1, UserID: 202, RefreshToken: target.RefreshToken,
		IP: "10.2.1.1", UserAgent: "Firefox/1",
	}); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("target user's stolen refresh family should be revoked: %v", err)
	}
	families, err := svc.List(ctx, 1, 202)
	if err != nil {
		t.Fatalf("List target families: %v", err)
	}
	if len(families) != 1 || families[0].Status != FamilyStatusRevoked || families[0].RevokedReason != "refresh_token_cross_user_attempt" {
		t.Fatalf("target family not revoked for cross-user attempt: %+v", families)
	}
}

func TestAT_SESSION_001_002_SessionTokenExpiryAndRefresh(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	svc.SessionTTL = 2 * time.Minute
	svc.RefreshTTL = time.Hour
	issued, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 10, IP: "10.1.1.1", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.1.2", "Chrome/2"); err != nil {
		t.Fatalf("Validate before expiry: %v", err)
	}
	now = now.Add(time.Minute)
	refreshed, err := svc.Refresh(ctx, RefreshInput{TenantID: 1, UserID: 10, RefreshToken: issued.RefreshToken, IP: "10.1.1.2", UserAgent: "Chrome/2"})
	if err != nil {
		t.Fatalf("Refresh before expiry: %v", err)
	}
	now = now.Add(90 * time.Second)
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.1.2", "Chrome/2"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("old session token after expiry = %v, want ErrTokenExpired", err)
	}
	if _, err := svc.Validate(ctx, refreshed.SessionToken, "10.1.1.2", "Chrome/2"); err != nil {
		t.Fatalf("new session token should validate: %v", err)
	}
}

func TestAT_SESSION_001_004_PerTokenFamilyAndUserInvalidation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 12, 30, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	first, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 21, IP: "10.1.1.1", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	second, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 21, IP: "10.2.1.1", UserAgent: "Firefox/1"})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if _, err := svc.Revoke(ctx, RevokeInput{TenantID: 1, SessionToken: first.SessionToken}); err != nil {
		t.Fatalf("Revoke session token: %v", err)
	}
	if _, err := svc.Validate(ctx, first.SessionToken, "10.1.1.1", "Chrome/1"); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("revoked session token validate = %v, want ErrFamilyRevoked", err)
	}
	if _, err := svc.Validate(ctx, second.SessionToken, "10.2.1.1", "Firefox/1"); err != nil {
		t.Fatalf("second family should survive token revoke: %v", err)
	}
	if _, err := svc.Revoke(ctx, RevokeInput{TenantID: 1, UserID: 21, FamilyID: second.Family.ID}); err != nil {
		t.Fatalf("Revoke family: %v", err)
	}
	if _, err := svc.Validate(ctx, second.SessionToken, "10.2.1.1", "Firefox/1"); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("family revoked validate = %v, want ErrFamilyRevoked", err)
	}
	third, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 21, IP: "10.3.1.1", UserAgent: "Safari/1"})
	if err != nil {
		t.Fatalf("Create third: %v", err)
	}
	if _, err := svc.Revoke(ctx, RevokeInput{TenantID: 1, UserID: 21}); err != nil {
		t.Fatalf("Revoke user: %v", err)
	}
	if _, err := svc.Validate(ctx, third.SessionToken, "10.3.1.1", "Safari/1"); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("user-wide revoke validate = %v, want ErrFamilyRevoked", err)
	}
}

// TestRevokeOthersKeepsCurrentFamilyAndRevokesOtherFamilies 守护 2FA 的 UX
// 契约: 敏感状态变更应当踢掉其它浏览器, 而不是刚刚证明了持有权的
// 那一个。变异检查: 改用 RevokeUser 来实现, 那么对当前 session 的校验
// 会变红, 同时对其它 session 的断言仍证明这不是一个 no-op。
func TestRevokeOthersKeepsCurrentFamilyAndRevokesOtherFamilies(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 4, 11, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()

	current, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 21, IP: "10.1.1.1", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create current: %v", err)
	}
	other, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 21, IP: "10.2.1.1", UserAgent: "Firefox/1"})
	if err != nil {
		t.Fatalf("Create other: %v", err)
	}
	anotherUser, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 99, IP: "10.3.1.1", UserAgent: "Safari/1"})
	if err != nil {
		t.Fatalf("Create another user: %v", err)
	}

	count, err := svc.RevokeOthers(ctx, RevokeOthersInput{
		TenantID: 1, UserID: 21, CurrentFamilyID: current.Family.ID, Reason: "two_factor_state_changed",
	})
	if err != nil {
		t.Fatalf("RevokeOthers: %v", err)
	}
	if count != 1 {
		t.Fatalf("revoked families=%d want 1", count)
	}
	if _, err := svc.Validate(ctx, current.SessionToken, "10.1.1.1", "Chrome/1"); err != nil {
		t.Fatalf("current session must survive revoke-others: %v", err)
	}
	if _, err := svc.Validate(ctx, other.SessionToken, "10.2.1.1", "Firefox/1"); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("other session validate=%v want ErrFamilyRevoked", err)
	}
	if _, err := svc.Validate(ctx, anotherUser.SessionToken, "10.3.1.1", "Safari/1"); err != nil {
		t.Fatalf("another user session must survive: %v", err)
	}
}

func TestAT_SESSION_001_006_MultiDevicePolicy(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 13, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	svc.MaxActiveFamilies = 1
	if _, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 31, IP: "10.1.1.1", UserAgent: "Chrome/1"}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if _, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 31, IP: "10.2.1.1", UserAgent: "Firefox/1"}); !errors.Is(err, ErrDeviceLimitExceeded) {
		t.Fatalf("deny policy error = %v, want ErrDeviceLimitExceeded", err)
	}

	svc.DevicePolicy = "revoke_oldest"
	now = now.Add(time.Minute)
	next, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 31, IP: "10.2.1.1", UserAgent: "Firefox/1"})
	if err != nil {
		t.Fatalf("revoke_oldest create: %v", err)
	}
	families, err := svc.List(ctx, 1, 31)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	active := 0
	for _, family := range families {
		if family.Status == FamilyStatusActive {
			active++
		}
	}
	if active != 1 || next.Family.Status != FamilyStatusActive {
		t.Fatalf("revoke_oldest active count=%d next=%+v families=%+v", active, next.Family, families)
	}

	svc.DevicePolicy = "confirm"
	if _, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 31, IP: "10.3.1.1", UserAgent: "Safari/1"}); !errors.Is(err, ErrDeviceConfirmationRequired) {
		t.Fatalf("confirm policy error = %v, want ErrDeviceConfirmationRequired", err)
	}
}

func TestAT_SESSION_001_005_AuthoritativeDenialBeatsStalePositiveCache(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 13, 15, 0, 0, time.UTC)
	store := &authoritativeDenyStore{MemoryStore: NewMemoryStore()}
	svc := NewService(store)
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	issued, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 35, IP: "10.1.1.1", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.1.1", "Chrome/1"); err != nil {
		t.Fatalf("initial validate: %v", err)
	}
	store.denySessionLookup = true
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.1.1", "Chrome/1"); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("authoritative denial = %v, want ErrFamilyRevoked", err)
	}
}

type authoritativeDenyStore struct {
	*MemoryStore
	denySessionLookup bool
}

func (s *authoritativeDenyStore) LookupSessionToken(ctx context.Context, tokenHash []byte) (SessionRecord, error) {
	if s.denySessionLookup {
		return SessionRecord{}, ErrFamilyRevoked
	}
	return s.MemoryStore.LookupSessionToken(ctx, tokenHash)
}

func TestAT_SESSION_001_007_ResetRevocationRejectsOldTokens(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 13, 30, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	issued, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 41, IP: "10.1.1.1", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Revoke(ctx, RevokeInput{TenantID: 1, UserID: 41, Reason: "password_reset"}); err != nil {
		t.Fatalf("Revoke user: %v", err)
	}
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.1.1", "Chrome/1"); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("old session after reset = %v, want ErrFamilyRevoked", err)
	}
	if _, err := svc.Refresh(ctx, RefreshInput{TenantID: 1, UserID: 41, RefreshToken: issued.RefreshToken, IP: "10.1.1.1", UserAgent: "Chrome/1"}); !errors.Is(err, ErrFamilyRevoked) {
		t.Fatalf("old refresh after reset = %v, want ErrFamilyRevoked", err)
	}
	fresh, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 41, IP: "10.1.1.1", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("new login after reset: %v", err)
	}
	if _, err := svc.Validate(ctx, fresh.SessionToken, "10.1.1.1", "Chrome/1"); err != nil {
		t.Fatalf("fresh session validate: %v", err)
	}
}

func TestAT_SESSION_001_005_PostgresDenialInvalidatesStaleRefreshCache(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 16, 14, 0, 0, 0, time.UTC)
	store := NewPostgresStore(staleDenyDB{})
	family := SessionFamily{
		ID: "00000000-0000-0000-0000-000000000005", TenantID: 1, UserID: 51,
		Status: FamilyStatusActive, Generation: 1, CreatedAt: now, LastActiveAt: now,
		DeviceInfo: map[string]any{}, IPBaseline: "10.1",
	}
	token := RefreshToken{
		ID: "00000000-0000-0000-0000-000000000006", TenantID: 1, FamilyID: family.ID,
		TokenHash: HashRefreshToken("stale-refresh"), Generation: 1, Status: RefreshTokenStatusActive,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	store.cache.mu.Lock()
	store.cache.families[family.ID] = family
	store.cache.tokens[token.ID] = token
	store.cache.byHash[hashKey(token.TokenHash)] = token.ID
	store.cache.mu.Unlock()

	if _, err := store.LookupRefreshToken(ctx, token.TokenHash); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("authoritative postgres denial = %v, want ErrTokenNotFound", err)
	}
	if _, err := store.cache.LookupRefreshToken(ctx, token.TokenHash); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("stale cache was not invalidated: %v", err)
	}

	sessionToken := SessionToken{
		ID: "00000000-0000-0000-0000-000000000007", TenantID: 1, FamilyID: family.ID,
		TokenHash: HashSessionToken("stale-session"), Generation: 1,
		ExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}
	store.cache.mu.Lock()
	store.cache.families[family.ID] = family
	store.cache.sessionTokens[sessionToken.ID] = sessionToken
	store.cache.sessionByHash[hashKey(sessionToken.TokenHash)] = sessionToken.ID
	store.cache.mu.Unlock()
	if _, err := store.LookupSessionToken(ctx, sessionToken.TokenHash); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("authoritative session denial = %v, want ErrTokenNotFound", err)
	}
	if _, err := store.cache.LookupSessionToken(ctx, sessionToken.TokenHash); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("stale session cache was not invalidated: %v", err)
	}
}

func testSigningKey() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}

type staleDenyDB struct{}

func (staleDenyDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (staleDenyDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, pgx.ErrNoRows
}

func (staleDenyDB) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return staleDenyRow{err: pgx.ErrNoRows}
}

type staleDenyRow struct {
	err error
}

func (r staleDenyRow) Scan(...interface{}) error {
	return r.err
}
