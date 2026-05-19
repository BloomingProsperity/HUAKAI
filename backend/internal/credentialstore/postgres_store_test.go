package credentialstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestResolveActiveRejectsCrossTenantCredentialJoin(t *testing.T) {
	db := &credentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, _ ...interface{}) pgx.Row {
			if !strings.Contains(sql, "pa.tenant_id = ac.tenant_id") {
				t.Fatalf("ResolveActive SQL missing tenant equality join:\n%s", sql)
			}
			return credentialStoreRowStub{err: pgx.ErrNoRows}
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.ResolveActive(context.Background(), 42)
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("ResolveActive err=%v want %v", err, ErrCredentialNotFound)
	}
}

func TestCreateRejectsProviderAccountTenantMismatchBeforeInsert(t *testing.T) {
	insertSeen := false
	db := &credentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, _ ...interface{}) pgx.Row {
			if strings.Contains(sql, "INSERT INTO account_credentials") {
				insertSeen = true
			}
			return credentialStoreRowStub{err: pgx.ErrNoRows}
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.Create(context.Background(), CreateCredentialInput{
		TenantID:          7,
		ProviderAccountID: 99,
		Vendor:            VendorOpenAI,
		AuthMode:          AuthModeAPIKey,
		Payload:           []byte(`{"api_key":"sk-test"}`),
	})
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("Create err=%v want %v", err, ErrCredentialNotFound)
	}
	if insertSeen {
		t.Fatal("Create attempted account_credentials insert after provider account tenant mismatch")
	}
}

func mustTestKeyProvider(t *testing.T) KeyProvider {
	t.Helper()
	provider, err := NewStaticKeyProvider("test-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key provider: %v", err)
	}
	return provider
}

type credentialStoreDBStub struct {
	queryRow func(context.Context, string, ...interface{}) pgx.Row
}

func (s *credentialStoreDBStub) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (s *credentialStoreDBStub) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (s *credentialStoreDBStub) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	if s.queryRow == nil {
		return credentialStoreRowStub{err: pgx.ErrNoRows}
	}
	return s.queryRow(ctx, sql, args...)
}

type credentialStoreRowStub struct {
	err error
}

func (r credentialStoreRowStub) Scan(...interface{}) error {
	if r.err != nil {
		return r.err
	}
	return nil
}
