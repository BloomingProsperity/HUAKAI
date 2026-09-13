package credentialstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestInspectRefreshModeQueryDoesNotUseDuePredicateOrDecrypt(t *testing.T) {
	var seen string
	db := &credentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, args ...interface{}) pgx.Row {
			seen = sql
			if len(args) != 2 || args[0] != int64(77) || args[1] != int64(9) {
				t.Fatalf("必须按 (account, tenant) 绑定: %#v", args)
			}
			return credentialStoreRowStub{err: pgx.ErrNoRows}
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())
	vendor, mode, err := store.InspectRefreshMode(context.Background(), 9, 77)
	if !errors.Is(err, ErrCredentialNotFound) || vendor != "" || mode != "" {
		t.Fatalf("无行必须是 ErrCredentialNotFound: vendor=%q mode=%q err=%v", vendor, mode, err)
	}
	for _, forbidden := range []string{"refresh_before_at", "encrypted_payload", "plaintext"} {
		if strings.Contains(seen, forbidden) {
			t.Fatalf("模式检查不得包含 %q:\n%s", forbidden, seen)
		}
	}
	for _, required := range []string{
		"ac.tenant_id = $2",
		"ac.deleted_at IS NULL",
		"pa.deleted_at IS NULL",
		"ac.vendor",
		"ac.auth_mode",
	} {
		if !strings.Contains(seen, required) {
			t.Fatalf("模式检查缺少 %q:\n%s", required, seen)
		}
	}
}
