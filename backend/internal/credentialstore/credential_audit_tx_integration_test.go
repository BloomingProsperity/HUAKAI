//go:build integration_pg

package credentialstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type credentialAuditTxFixture struct {
	tenantID          int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
}

type credentialAuditSnapshot struct {
	State              string
	Version            int32
	PayloadFingerprint string
	RefreshFingerprint string
	LastOutcome        string
	FailureClass       string
	FailureCount       int32
}

func TestCredentialAuditTxCreateFailureRollsBackCredential(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture := seedCredentialAuditTxFixture(t, ctx, pool, "create-fail")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)
	cleanupRejector := installCredentialAuditRejector(t, ctx, pool, CredentialEventCreated)
	defer cleanupRejector()

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	_, err := store.Create(ctx, CreateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeAPIKey, Payload: []byte(`{"api_key":"sk-create-fail"}`),
		ActorID: "owner",
	})
	if err == nil {
		t.Fatal("Create audit failure returned nil error")
	}
	if got := countCredentialRows(t, ctx, pool, fixture.tenantID, fixture.providerAccountID); got != 0 {
		t.Fatalf("Create audit failure left %d credential rows, want 0", got)
	}
}

func TestCredentialAuditTxRotateFailurePreservesPreviousVersion(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture, meta := seedCredentialWithStore(t, ctx, pool, "rotate-fail")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)
	before := credentialAuditSnapshotForID(t, ctx, pool, meta.ID)
	cleanupRejector := installCredentialAuditRejector(t, ctx, pool, CredentialEventRotated)
	defer cleanupRejector()

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	_, err := store.Rotate(ctx, RotateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID, CredentialID: meta.ID,
		Payload: []byte(`{"api_key":"sk-rotate-fail-next"}`), ActorID: "owner",
	})
	if err == nil {
		t.Fatal("Rotate audit failure returned nil error")
	}
	after := credentialAuditSnapshotForID(t, ctx, pool, meta.ID)
	if after.Version != before.Version || after.PayloadFingerprint != before.PayloadFingerprint || after.RefreshFingerprint != before.RefreshFingerprint {
		t.Fatalf("Rotate audit failure changed row: before=%+v after=%+v", before, after)
	}
}

func TestCredentialAuditTxDeleteFailureKeepsCredentialVisible(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture, meta := seedCredentialWithStore(t, ctx, pool, "delete-fail")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)
	before := credentialAuditSnapshotForID(t, ctx, pool, meta.ID)
	cleanupRejector := installCredentialAuditRejector(t, ctx, pool, CredentialEventDeleted)
	defer cleanupRejector()

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	err := store.Delete(ctx, fixture.tenantID, fixture.providerAccountID, meta.ID, "owner")
	if err == nil {
		t.Fatal("Delete audit failure returned nil error")
	}
	after := credentialAuditSnapshotForID(t, ctx, pool, meta.ID)
	if after.State != before.State || after.Version != before.Version {
		t.Fatalf("Delete audit failure changed visible row: before=%+v after=%+v", before, after)
	}
	if got := countVisibleCredentialRows(t, ctx, pool, meta.ID); got != 1 {
		t.Fatalf("Delete audit failure visible rows=%d want 1", got)
	}
}

func TestCredentialAuditTxRefreshSuccessFailureRollsBackTokenVersion(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture, meta := seedCredentialWithStore(t, ctx, pool, "refresh-ok-fail")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)
	rec := credentialRecordForID(t, ctx, pool, meta.ID)
	before := credentialAuditSnapshotForID(t, ctx, pool, meta.ID)
	cleanupRejector := installCredentialAuditRejector(t, ctx, pool, CredentialEventRefreshSucceeded)
	defer cleanupRejector()

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	err := store.SaveRefreshSuccess(ctx, rec, []byte(`{"api_key":"sk-refresh-success-next"}`), time.Now().Add(time.Hour), "refresh_succeeded")
	if err == nil {
		t.Fatal("SaveRefreshSuccess audit failure returned nil error")
	}
	after := credentialAuditSnapshotForID(t, ctx, pool, meta.ID)
	if after.Version != before.Version || after.PayloadFingerprint != before.PayloadFingerprint || after.LastOutcome != before.LastOutcome {
		t.Fatalf("Refresh success audit failure changed row: before=%+v after=%+v", before, after)
	}
}

func TestCredentialAuditTxRefreshFailureRollsBackHealthFields(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture, meta := seedCredentialWithStore(t, ctx, pool, "refresh-fail-fail")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)
	rec := credentialRecordForID(t, ctx, pool, meta.ID)
	before := credentialAuditSnapshotForID(t, ctx, pool, meta.ID)
	cleanupRejector := installCredentialAuditRejector(t, ctx, pool, CredentialEventRefreshFailed)
	defer cleanupRejector()

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	err := store.SaveRefreshFailure(ctx, rec, "invalid_grant", time.Now().Add(time.Minute))
	if err == nil {
		t.Fatal("SaveRefreshFailure audit failure returned nil error")
	}
	after := credentialAuditSnapshotForID(t, ctx, pool, meta.ID)
	if after.State != before.State || after.FailureClass != before.FailureClass || after.FailureCount != before.FailureCount || after.LastOutcome != before.LastOutcome {
		t.Fatalf("Refresh failure audit failure changed row: before=%+v after=%+v", before, after)
	}
}

func TestCredentialAuditTxSetStateDiscriminatesActionsAndPayload(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixtureA, metaA := seedCredentialWithStore(t, ctx, pool, "state-activate")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixtureA)
	fixtureB, metaB := seedCredentialWithStore(t, ctx, pool, "state-revoke")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixtureB)
	mustSetCredentialStateDirect(t, ctx, pool, metaA.ID, StateRevoked)

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	if err := store.SetState(ctx, fixtureA.tenantID, fixtureA.providerAccountID, metaA.ID, StateActive, "owner"); err != nil {
		t.Fatalf("SetState revoked -> active: %v", err)
	}
	if err := store.SetState(ctx, fixtureB.tenantID, fixtureB.providerAccountID, metaB.ID, StateRevoked, "owner"); err != nil {
		t.Fatalf("SetState active -> revoked: %v", err)
	}
	eventA, payloadA := latestCredentialAuditEvent(t, ctx, pool, metaA.ID)
	eventB, payloadB := latestCredentialAuditEvent(t, ctx, pool, metaB.ID)
	if eventA != CredentialEventStateActivated {
		t.Fatalf("revoked -> active event=%q want %q payload=%s", eventA, CredentialEventStateActivated, payloadA)
	}
	if eventB != CredentialEventStateRevoked {
		t.Fatalf("active -> revoked event=%q want %q payload=%s", eventB, CredentialEventStateRevoked, payloadB)
	}
	if eventA == eventB {
		t.Fatal("SetState paired fixtures produced identical event_type; fixed credential_disabled regression would not be caught")
	}
	assertStatePayload(t, payloadA, StateRevoked, StateActive)
	assertStatePayload(t, payloadB, StateActive, StateRevoked)
}

func TestCredentialAuditTxSetStateFailureRollsBackState(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture, meta := seedCredentialWithStore(t, ctx, pool, "state-audit-fail")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)
	cleanupRejector := installCredentialAuditRejector(t, ctx, pool, CredentialEventStateAttention)
	defer cleanupRejector()

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	err := store.SetState(ctx, fixture.tenantID, fixture.providerAccountID, meta.ID, StateOperatorAttention, "owner")
	if err == nil {
		t.Fatal("SetState audit failure returned nil error")
	}
	after := credentialAuditSnapshotForID(t, ctx, pool, meta.ID)
	if after.State != StateActive {
		t.Fatalf("SetState audit failure state=%q want %q", after.State, StateActive)
	}
}

func TestCredentialAuditTxRotateSuccessCommitsMutationAndAudit(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture, meta := seedCredentialWithStore(t, ctx, pool, "rotate-ok")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	rotated, err := store.Rotate(ctx, RotateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID, CredentialID: meta.ID,
		Payload: []byte(`{"api_key":"sk-rotate-ok-next"}`), ActorID: "owner",
	})
	if err != nil {
		t.Fatalf("Rotate success: %v", err)
	}
	if rotated.Version != meta.Version+1 {
		t.Fatalf("rotated version=%d want %d", rotated.Version, meta.Version+1)
	}
	if got := countCredentialAuditEvents(t, ctx, pool, meta.ID, CredentialEventRotated); got != 1 {
		t.Fatalf("credential_rotated audit rows=%d want 1", got)
	}
}

func openCredentialAuditTxPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping credential audit tx integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pg ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}

func seedCredentialWithStore(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (credentialAuditTxFixture, CredentialMetadata) {
	t.Helper()
	fixture := seedCredentialAuditTxFixture(t, ctx, pool, suffix)
	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	meta, err := store.Create(ctx, CreateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeAPIKey, Payload: []byte(`{"api_key":"sk-` + suffix + `"}`),
		ActorID: "owner",
	})
	if err != nil {
		cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)
		t.Fatalf("seed credential Create: %v", err)
	}
	return fixture, meta
}

func seedCredentialAuditTxFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) credentialAuditTxFixture {
	t.Helper()
	suffix = fmt.Sprintf("%s-%d", suffix, time.Now().UnixNano())
	var f credentialAuditTxFixture
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "credential-audit-"+suffix).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, $3, 'openai_chat') RETURNING id`, f.tenantID, "openai-"+suffix, "OpenAI "+suffix).Scan(&f.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, f.tenantID, "pool-"+suffix).Scan(&f.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, f.tenantID, f.poolGroupID, "channel-"+suffix).Scan(&f.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, credentials)
		VALUES ($1, $2, $3, $4, 'api_key', '{}'::jsonb)
		RETURNING id`, f.tenantID, f.providerID, f.channelID, "account-"+suffix).Scan(&f.providerAccountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	return f
}

func cleanupCredentialAuditTxFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f credentialAuditTxFixture) {
	t.Helper()
	_, _ = pool.Exec(ctx, `DELETE FROM credential_audit_events WHERE tenant_id = $1`, f.tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM account_credentials WHERE tenant_id = $1`, f.tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id = $1`, f.tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE tenant_id = $1`, f.tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id = $1`, f.tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE tenant_id = $1`, f.tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, f.tenantID)
}

func installCredentialAuditRejector(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventType string) func() {
	t.Helper()
	suffix := strings.NewReplacer("-", "_", ":", "_").Replace(fmt.Sprintf("%s_%d", eventType, time.Now().UnixNano()))
	fn := pgx.Identifier{"public", "huakai_test_reject_credential_audit_" + suffix}.Sanitize()
	trigger := pgx.Identifier{"huakai_test_reject_credential_audit_" + suffix}.Sanitize()
	eventLiteral := strings.ReplaceAll(eventType, "'", "''")
	createFn := fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.event_type = '%s' THEN
				RAISE EXCEPTION 'forced credential audit failure for %%', NEW.event_type;
			END IF;
			RETURN NEW;
		END;
		$$`, fn, eventLiteral)
	if _, err := pool.Exec(ctx, createFn); err != nil {
		t.Fatalf("create audit rejector function: %v", err)
	}
	createTrigger := fmt.Sprintf(`
		CREATE TRIGGER %s
		BEFORE INSERT ON credential_audit_events
		FOR EACH ROW EXECUTE FUNCTION %s()`, trigger, fn)
	if _, err := pool.Exec(ctx, createTrigger); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
		t.Fatalf("create audit rejector trigger: %v", err)
	}
	return func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON credential_audit_events`, trigger))
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
	}
}

func countCredentialRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, providerAccountID int64) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM account_credentials WHERE tenant_id=$1 AND provider_account_id=$2`, tenantID, providerAccountID).Scan(&count); err != nil {
		t.Fatalf("count credential rows: %v", err)
	}
	return count
}

func countVisibleCredentialRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, credentialID int64) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM account_credentials WHERE id=$1 AND deleted_at IS NULL`, credentialID).Scan(&count); err != nil {
		t.Fatalf("count visible credential rows: %v", err)
	}
	return count
}

func countCredentialAuditEvents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, credentialID int64, eventType string) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM credential_audit_events WHERE account_credential_id=$1 AND event_type=$2`, credentialID, eventType).Scan(&count); err != nil {
		t.Fatalf("count credential audit events: %v", err)
	}
	return count
}

func credentialAuditSnapshotForID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, credentialID int64) credentialAuditSnapshot {
	t.Helper()
	var s credentialAuditSnapshot
	if err := pool.QueryRow(ctx, `
		SELECT state, credential_version, COALESCE(payload_fingerprint, ''), COALESCE(refresh_token_fingerprint, ''),
		       COALESCE(last_refresh_outcome, ''), COALESCE(failure_class, ''), failure_count
		FROM account_credentials WHERE id=$1 AND deleted_at IS NULL`, credentialID).Scan(
		&s.State, &s.Version, &s.PayloadFingerprint, &s.RefreshFingerprint, &s.LastOutcome, &s.FailureClass, &s.FailureCount,
	); err != nil {
		t.Fatalf("credential snapshot: %v", err)
	}
	return s
}

func credentialRecordForID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, credentialID int64) CredentialRecord {
	t.Helper()
	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	var tenantID, providerAccountID int64
	if err := pool.QueryRow(ctx, `SELECT tenant_id, provider_account_id FROM account_credentials WHERE id=$1`, credentialID).Scan(&tenantID, &providerAccountID); err != nil {
		t.Fatalf("credential ids: %v", err)
	}
	rec, err := store.getRecord(ctx, tenantID, providerAccountID, credentialID, false)
	if err != nil {
		t.Fatalf("get credential record: %v", err)
	}
	return rec
}

func mustSetCredentialStateDirect(t *testing.T, ctx context.Context, pool *pgxpool.Pool, credentialID int64, state string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE account_credentials SET state=$1, updated_at=NOW() WHERE id=$2`, state, credentialID); err != nil {
		t.Fatalf("direct state update: %v", err)
	}
}

func latestCredentialAuditEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, credentialID int64) (string, []byte) {
	t.Helper()
	var eventType string
	var payload []byte
	if err := pool.QueryRow(ctx, `
		SELECT event_type, payload
		FROM credential_audit_events
		WHERE account_credential_id=$1
		ORDER BY occurred_at DESC, id DESC
		LIMIT 1`, credentialID).Scan(&eventType, &payload); err != nil {
		t.Fatalf("latest credential audit event: %v", err)
	}
	return eventType, payload
}

func assertStatePayload(t *testing.T, raw []byte, oldState, newState string) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("audit payload json: %v; raw=%s", err, raw)
	}
	if got := fmt.Sprint(payload["old_state"]); got != oldState {
		t.Fatalf("old_state=%q want %q payload=%s", got, oldState, raw)
	}
	if got := fmt.Sprint(payload["new_state"]); got != newState {
		t.Fatalf("new_state=%q want %q payload=%s", got, newState, raw)
	}
}
