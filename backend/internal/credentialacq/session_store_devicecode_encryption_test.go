package credentialacq

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// capturingSessionDB wraps the standard test fake and records the raw bytes that
// SetAuthPayload writes into the device_code_payload jsonb column ($3), so a test
// can assert what actually lands "at rest".
type capturingSessionDB struct {
	*testSessionDB
	lastAuthPayloadRaw []byte
}

func (db *capturingSessionDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "SET auth_type = $2::oauth_acquisition_auth_type") && len(args) >= 3 {
		db.lastAuthPayloadRaw = append([]byte(nil), bytesArg(args[2])...)
	}
	return db.testSessionDB.Exec(ctx, sql, args...)
}

// TestSetAuthPayloadEncryptsDeviceCodeAtRest is a discriminating security test:
// the device_code (an RFC 8628 bearer secret exchangeable for a token) must NOT be
// persisted as plaintext in the device_code_payload jsonb column. It also verifies the
// polling read path still recovers the original payload (round-trip via decryption).
func TestSetAuthPayloadEncryptsDeviceCodeAtRest(t *testing.T) {
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	keys, err := credentialstore.NewStaticKeyProvider("test-v1", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	db := &capturingSessionDB{testSessionDB: newTestSessionDB(now)}
	store := NewPostgresSessionStoreWithKeys(db, keys).WithNow(func() time.Time { return now })

	created, err := store.Create(context.Background(), Session{
		ID: "flow-dc-enc", TenantID: 42, ProviderAccountID: 7,
		Vendor: "openai", AuthMode: "codex_cli_oauth", Kind: FlowKindOAuth, Status: StatusWaitingForUser,
		ActorID: "admin-1", ActorRole: "platform_admin",
		ClientIdentitySource: ClientSourcePublicCLI,
		RedactedContext:      map[string]any{},
		ExpiresAt:            now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const deviceCodeSecret = "SECRET-DEVICE-CODE-abc123-bearer"
	payload := map[string]any{
		"auth_type":        string(AuthTypeDeviceCode),
		"device_code":      deviceCodeSecret,
		"user_code":        "USER-CODE",
		"verification_uri": "https://device.example.test",
		"expires_in":       900,
		"interval":         5,
		"issued_at":        now.Format(time.RFC3339Nano),
		"token_url":        "https://device.example.test/token",
		"client_id":        "client-xyz",
	}
	if err := store.SetAuthPayload(context.Background(), created.ID, AuthTypeDeviceCode, payload); err != nil {
		t.Fatalf("SetAuthPayload: %v", err)
	}

	// SAFE BEHAVIOR: the bearer secret must never appear in the bytes written to the DB column.
	if len(db.lastAuthPayloadRaw) == 0 {
		t.Fatal("no device_code_payload bytes captured")
	}
	if bytes.Contains(db.lastAuthPayloadRaw, []byte(deviceCodeSecret)) {
		t.Fatalf("device_code bearer secret persisted as plaintext at rest: %s", db.lastAuthPayloadRaw)
	}

	// POLL READ PATH: reload must transparently decrypt back to the original payload.
	reloaded, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := stringFromPayload(reloaded.DeviceCodePayload, "device_code"); got != deviceCodeSecret {
		t.Fatalf("reloaded device_code = %q; want %q", got, deviceCodeSecret)
	}
	if got := stringFromPayload(reloaded.DeviceCodePayload, "token_url"); got != "https://device.example.test/token" {
		t.Fatalf("reloaded token_url = %q; want token url round-trip", got)
	}
}
