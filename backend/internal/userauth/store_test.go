package userauth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestAuthOAuthFlowEncryptsPKCEVerifierAtRest(t *testing.T) {
	keys, err := credentialstore.NewStaticKeyProvider("pkce-test-v1", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	store := NewPostgresStore(nil).WithKeyProvider(keys)
	challenge := OAuthFlowChallenge{
		ID: "00000000-0000-0000-0000-000000000123", TenantID: 1,
		Provider: SocialProviderGoogle, PKCEVerifier: "raw-pkce-verifier",
		StateHash: []byte("state-hash"),
	}
	ciphertext, err := store.encryptPKCEVerifier(context.Background(), challenge)
	if err != nil {
		t.Fatalf("encryptPKCEVerifier: %v", err)
	}
	if len(ciphertext) == 0 {
		t.Fatal("encrypted PKCE payload missing")
	}
	if bytes.Contains(ciphertext, []byte(challenge.PKCEVerifier)) {
		t.Fatal("encrypted PKCE storage leaked raw verifier bytes")
	}
	flow := OAuthFlowSession{
		ID: challenge.ID, TenantID: challenge.TenantID, Provider: challenge.Provider,
		StateHash: challenge.StateHash, PKCEVerifierCiphertext: ciphertext,
	}
	verifier, err := store.decryptPKCEVerifier(context.Background(), flow)
	if err != nil {
		t.Fatalf("decryptPKCEVerifier: %v", err)
	}
	if verifier != challenge.PKCEVerifier {
		t.Fatalf("PKCE verifier=%q want %q", verifier, challenge.PKCEVerifier)
	}
	legacy := OAuthFlowSession{PKCEVerifier: "legacy-plain"}
	verifier, err = store.decryptPKCEVerifier(context.Background(), legacy)
	if err != nil {
		t.Fatalf("legacy plaintext compatibility: %v", err)
	}
	if verifier != "legacy-plain" {
		t.Fatalf("legacy verifier=%q want legacy-plain", verifier)
	}
}

func TestRedeemInviteUsesCommunityInvitationWhenAuthInviteMissing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	rawCode := "ABCD1234ABCD"
	codeHash := HashInviteCode(rawCode)
	fake := &fakeInviteRedemptionDB{
		communityActive:       true,
		communityInvitationID: 55,
		communityTenantID:     1,
		communityInviterID:    9001,
		communityMaxUses:      3,
		communityUsedCount:    1,
		communityCreatedAt:    now.Add(-time.Hour),
	}

	invite, err := redeemInviteWithDB(ctx, fake, 1, codeHash, rawCode, now)
	if err != nil {
		t.Fatalf("redeemInviteWithDB community fallback: %v", err)
	}
	if !fake.authInviteUpdateTried || !fake.authInviteExistsChecked || !fake.communityUpdateTried || !fake.inviteCodeUpserted {
		t.Fatalf("expected auth miss check + community update + compatibility upsert; fake=%+v", fake)
	}
	if invite.Code != codeHash || invite.CommunityInvitationID != 55 || invite.CreatedBy != 9001 || invite.UsedCount != 1 || invite.Status != "active" {
		t.Fatalf("community invitation redemption mismatch: %+v", invite)
	}
	if len(fake.upsertArgs) != 9 || fake.upsertArgs[0] != codeHash || fake.upsertArgs[1] != int64(1) || fake.upsertArgs[2] != int64(9001) {
		t.Fatalf("compatibility invite upsert args mismatch: %+v", fake.upsertArgs)
	}
}

func TestRedeemInviteDoesNotFallbackWhenAuthInviteRowExists(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 9, 15, 0, 0, time.UTC)
	rawCode := "ABCD1234WXYZ"
	codeHash := HashInviteCode(rawCode)
	fake := &fakeInviteRedemptionDB{
		authInviteExists:      true,
		communityActive:       true,
		communityInvitationID: 55,
		communityTenantID:     1,
		communityInviterID:    9001,
		communityMaxUses:      3,
		communityUsedCount:    1,
		communityCreatedAt:    now.Add(-time.Hour),
	}

	_, err := redeemInviteWithDB(ctx, fake, 1, codeHash, rawCode, now)
	if !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("redeemInviteWithDB with existing invalid auth invite = %v, want ErrInviteInvalid", err)
	}
	if !fake.authInviteUpdateTried || !fake.authInviteExistsChecked {
		t.Fatalf("auth invite miss/existence checks not exercised: %+v", fake)
	}
	if fake.communityUpdateTried || fake.inviteCodeUpserted {
		t.Fatalf("existing auth invite row must not be bypassed through community fallback: %+v", fake)
	}
}

type fakeInviteRedemptionDB struct {
	authInviteUpdateTried   bool
	authInviteExists        bool
	authInviteExistsChecked bool
	communityUpdateTried    bool
	communityActive         bool
	communityInvitationID   int64
	communityTenantID       int64
	communityInviterID      int64
	communityMaxUses        int
	communityUsedCount      int
	communityExpiresAt      *time.Time
	communityCreatedAt      time.Time
	inviteCodeUpserted      bool
	upsertArgs              []interface{}
}

func (f *fakeInviteRedemptionDB) Exec(_ context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "INSERT INTO invite_codes") {
		f.inviteCodeUpserted = true
		f.upsertArgs = append([]interface{}(nil), args...)
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.CommandTag{}, errors.New("unexpected Exec in fake invite redemption DB")
}

func (f *fakeInviteRedemptionDB) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query in fake invite redemption DB")
}

func (f *fakeInviteRedemptionDB) QueryRow(_ context.Context, sql string, _ ...interface{}) pgx.Row {
	switch {
	case strings.Contains(sql, "UPDATE invite_codes"):
		f.authInviteUpdateTried = true
		return fakeInviteRow{err: pgx.ErrNoRows}
	case strings.Contains(sql, "FROM invite_codes") && strings.Contains(sql, "SELECT EXISTS"):
		f.authInviteExistsChecked = true
		return fakeInviteRow{scan: func(dest ...interface{}) error {
			*dest[0].(*bool) = f.authInviteExists
			return nil
		}}
	case strings.Contains(sql, "UPDATE invitations"):
		f.communityUpdateTried = true
		if !f.communityActive {
			return fakeInviteRow{err: pgx.ErrNoRows}
		}
		return fakeInviteRow{scan: func(dest ...interface{}) error {
			*dest[0].(*int64) = f.communityInvitationID
			*dest[1].(*int64) = f.communityTenantID
			*dest[2].(*int64) = f.communityInviterID
			*dest[3].(*int) = f.communityMaxUses
			*dest[4].(*int) = f.communityUsedCount
			if f.communityExpiresAt != nil {
				*dest[5].(*pgtype.Timestamptz) = pgtype.Timestamptz{Time: *f.communityExpiresAt, Valid: true}
			} else {
				*dest[5].(*pgtype.Timestamptz) = pgtype.Timestamptz{}
			}
			*dest[6].(*time.Time) = f.communityCreatedAt
			return nil
		}}
	case strings.Contains(sql, "FROM invitations") && strings.Contains(sql, "SELECT EXISTS"):
		return fakeInviteRow{scan: func(dest ...interface{}) error {
			*dest[0].(*bool) = f.communityActive
			return nil
		}}
	default:
		return fakeInviteRow{err: errors.New("unexpected QueryRow in fake invite redemption DB")}
	}
}

type fakeInviteRow struct {
	err  error
	scan func(...interface{}) error
}

func (r fakeInviteRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	if r.scan == nil {
		return errors.New("fake invite row has no scan function")
	}
	return r.scan(dest...)
}
