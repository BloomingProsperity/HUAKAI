package credentialstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestListIdentityInventoryIsTenantScopedAndPlaintextFree(t *testing.T) {
	db := &identityInventoryDBStub{}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	rows, err := store.ListIdentityInventory(context.Background(), 7, VendorOpenAI)
	if err != nil {
		t.Fatalf("ListIdentityInventory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d want 1", len(rows))
	}
	got := rows[0]
	if got.ProviderAccountID != 77 || got.ProviderAccountName != "account-a" ||
		got.ExternalSubjectID != "subject-a" ||
		got.ExternalIdentitySource != "chatgpt_jwt_claim" ||
		got.CredentialMaterialFingerprint != "fingerprint-a" {
		t.Fatalf("row=%+v", got)
	}
	if db.args[0] != int64(7) || db.args[1] != VendorOpenAI {
		t.Fatalf("args=%v", db.args)
	}
	for _, required := range []string{
		"ac.tenant_id = $1", "pa.tenant_id = $1",
		"ac.deleted_at IS NULL", "pa.deleted_at IS NULL",
		"DISTINCT ON (ac.provider_account_id, ac.vendor, ac.auth_mode)",
		"WHEN 'active' THEN 0", "ac.updated_at DESC", "ac.id DESC",
		"external_subject_id", "external_identity_source", "credential_material_fingerprint",
	} {
		if !strings.Contains(db.sql, required) {
			t.Fatalf("SQL 缺少 %q:\n%s", required, db.sql)
		}
	}
	for _, forbidden := range []string{"encrypted_payload", "nonce", "key_id", "payload_fingerprint", "refresh_token_fingerprint"} {
		if strings.Contains(db.sql, forbidden) {
			t.Fatalf("identity inventory 不得读取 %q:\n%s", forbidden, db.sql)
		}
	}
}

func TestListIdentityInventoryRequiresTenant(t *testing.T) {
	store := NewStore(&identityInventoryDBStub{}, mustTestKeyProvider(t), DefaultHandlerRegistry())
	if _, err := store.ListIdentityInventory(context.Background(), 0, ""); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("err=%v want ErrInvalidPayload", err)
	}
}

type identityInventoryDBStub struct {
	sql  string
	args []any
}

func (s *identityInventoryDBStub) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (s *identityInventoryDBStub) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	s.sql = sql
	s.args = append([]any(nil), args...)
	return &identityInventoryRowsStub{}, nil
}

func (s *identityInventoryDBStub) QueryRow(context.Context, string, ...any) pgx.Row {
	return credentialStoreRowStub{err: pgx.ErrNoRows}
}

type identityInventoryRowsStub struct {
	read bool
}

func (r *identityInventoryRowsStub) Close()                                       {}
func (r *identityInventoryRowsStub) Err() error                                   { return nil }
func (r *identityInventoryRowsStub) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *identityInventoryRowsStub) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *identityInventoryRowsStub) Next() bool {
	if r.read {
		return false
	}
	r.read = true
	return true
}
func (r *identityInventoryRowsStub) Scan(dest ...any) error {
	values := []any{
		int64(77), " account-a ", VendorOpenAI, AuthModeCodexCLIOAuth, StateActive,
		"workspace-a", "subject-a", "a@example.com", "chatgpt_jwt_claim", "fingerprint-a",
	}
	if len(dest) != len(values) {
		return errors.New("scan destination count mismatch")
	}
	for index := range values {
		switch out := dest[index].(type) {
		case *int64:
			*out = values[index].(int64)
		case *string:
			*out = values[index].(string)
		default:
			return errors.New("unexpected scan type")
		}
	}
	return nil
}
func (r *identityInventoryRowsStub) Values() ([]any, error) {
	return nil, errors.New("unexpected Values")
}
func (r *identityInventoryRowsStub) RawValues() [][]byte { return nil }
func (r *identityInventoryRowsStub) Conn() *pgx.Conn     { return nil }
