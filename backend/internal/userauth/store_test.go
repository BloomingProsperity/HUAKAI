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

func TestInviteCodeStatusReadsAuthInviteWithoutConsuming(t *testing.T) {
	ctx := context.Background()
	rawCode := "hki_readonly_test"
	expires := time.Now().UTC().Add(time.Hour)
	fake := &fakeInviteStatusDB{
		status:     "active",
		usedCount:  0,
		maxUses:    1,
		validUntil: &expires,
	}

	status, err := NewPostgresStore(fake).InviteCodeStatus(ctx, 7, rawCode)
	if err != nil {
		t.Fatalf("InviteCodeStatus: %v", err)
	}
	if status != InviteCodeStatusValid {
		t.Fatalf("InviteCodeStatus=%q want %q", status, InviteCodeStatusValid)
	}
	if fake.execCalled || fake.queryCalled {
		t.Fatalf("InviteCodeStatus must be QueryRow-only; exec=%v query=%v", fake.execCalled, fake.queryCalled)
	}
	if strings.Contains(strings.ToLower(fake.queryRowSQL), "update") ||
		strings.Contains(strings.ToLower(fake.queryRowSQL), "pg_advisory") {
		t.Fatalf("InviteCodeStatus query consumed or locked invite:\n%s", fake.queryRowSQL)
	}
	if len(fake.queryRowArgs) != 2 || fake.queryRowArgs[0] != int64(7) || fake.queryRowArgs[1] != HashInviteCode(rawCode) {
		t.Fatalf("InviteCodeStatus args=%#v want tenant + hashed code", fake.queryRowArgs)
	}
}

func TestInviteCodeStatusClassifiesInactiveRows(t *testing.T) {
	ctx := context.Background()
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)
	cases := []struct {
		name       string
		rowErr     error
		rowStatus  string
		usedCount  int
		maxUses    int
		validUntil *time.Time
		want       InviteCodeStatus
	}{
		{name: "missing", rowErr: pgx.ErrNoRows, want: InviteCodeStatusNotFound},
		{name: "disabled", rowStatus: "disabled", maxUses: 1, validUntil: &future, want: InviteCodeStatusDisabled},
		{name: "exhausted_status", rowStatus: "exhausted", usedCount: 1, maxUses: 1, validUntil: &future, want: InviteCodeStatusUsedOrExhausted},
		{name: "exhausted_count", rowStatus: "active", usedCount: 1, maxUses: 1, validUntil: &future, want: InviteCodeStatusUsedOrExhausted},
		{name: "expired_status", rowStatus: "expired", maxUses: 1, validUntil: &future, want: InviteCodeStatusExpired},
		{name: "expired_time", rowStatus: "active", maxUses: 1, validUntil: &past, want: InviteCodeStatusExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeInviteStatusDB{
				err:        tc.rowErr,
				status:     tc.rowStatus,
				usedCount:  tc.usedCount,
				maxUses:    tc.maxUses,
				validUntil: tc.validUntil,
			}
			got, err := NewPostgresStore(fake).InviteCodeStatus(ctx, 7, "hki_classify")
			if err != nil {
				t.Fatalf("InviteCodeStatus: %v", err)
			}
			if got != tc.want {
				t.Fatalf("InviteCodeStatus=%q want %q", got, tc.want)
			}
		})
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

type fakeInviteStatusDB struct {
	queryRowSQL  string
	queryRowArgs []interface{}
	queryCalled  bool
	execCalled   bool
	err          error
	status       string
	usedCount    int
	maxUses      int
	validUntil   *time.Time
}

func (f *fakeInviteStatusDB) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	f.execCalled = true
	return pgconn.CommandTag{}, errors.New("unexpected Exec in fake invite status DB")
}

func (f *fakeInviteStatusDB) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	f.queryCalled = true
	return nil, errors.New("unexpected Query in fake invite status DB")
}

func (f *fakeInviteStatusDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	f.queryRowSQL = sql
	f.queryRowArgs = append([]interface{}(nil), args...)
	return fakeInviteStatusRow{fake: f}
}

type fakeInviteStatusRow struct {
	fake *fakeInviteStatusDB
}

func (r fakeInviteStatusRow) Scan(dest ...interface{}) error {
	if r.fake.err != nil {
		return r.fake.err
	}
	*dest[0].(*string) = r.fake.status
	*dest[1].(*int) = r.fake.usedCount
	*dest[2].(*int) = r.fake.maxUses
	if r.fake.validUntil != nil {
		*dest[3].(*pgtype.Timestamptz) = pgtype.Timestamptz{Time: *r.fake.validUntil, Valid: true}
	} else {
		*dest[3].(*pgtype.Timestamptz) = pgtype.Timestamptz{}
	}
	return nil
}
