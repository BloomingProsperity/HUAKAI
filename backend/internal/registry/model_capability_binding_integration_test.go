//go:build integration_pg

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestModelCapabilityBindingWriteLogAndSnapshotCommitAtomically(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	modelID := f.seedModel(modelOpts{canonicalID: "capability-log-" + f.suffix})
	f.setSnapshot(7)

	actor := "admin_token:capability-" + f.suffix
	requestID := "req-capability-" + f.suffix
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
DELETE FROM admin_audit_events
WHERE actor_id = $1 OR request_id = $2
`, actor, requestID)
	})

	got, err := NewPostgresRegistry(pool, nil).UpsertModelCapabilityBinding(ctx, UpsertModelCapabilityBindingParams{
		TenantID:   f.tenantID,
		Scope:      "tenant",
		ModelID:    modelID,
		Capability: "vision",
		Enabled:    true,
		Source:     "operator",
		Actor:      actor,
		ActorRole:  "platform_admin",
		RequestID:  requestID,
	})
	if err != nil {
		t.Fatalf("UpsertModelCapabilityBinding: %v", err)
	}
	if got.ModelID != modelID || got.TenantID == nil || *got.TenantID != f.tenantID || !got.Enabled {
		t.Fatalf("binding=%+v want committed tenant binding", got)
	}
	if version := readSnapVer(t, ctx, pool, f.tenantID); version != 8 {
		t.Fatalf("snapshot version=%d want 8；去掉同事务快照推进时本断言必须变红", version)
	}

	var (
		logTenantID int64
		logActor    string
		logRole     string
		action      string
		targetType  string
		targetID    int64
		category    string
		payloadRaw  []byte
	)
	if err := pool.QueryRow(ctx, `
SELECT tenant_id, actor_id, actor_role, action, target_type, target_id, log_category, payload
FROM admin_audit_events
WHERE request_id = $1
`, requestID).Scan(&logTenantID, &logActor, &logRole, &action, &targetType, &targetID, &category, &payloadRaw); err != nil {
		t.Fatalf("read operation log: %v", err)
	}
	if logTenantID != f.tenantID || logActor != actor || logRole != "platform_admin" ||
		action != "update_model_capability_binding" || targetType != "model_capability_binding" ||
		targetID != modelID || category != "operation" {
		t.Fatalf("operation log mismatch tenant=%d actor=%q role=%q action=%q target=%q/%d category=%q",
			logTenantID, logActor, logRole, action, targetType, targetID, category)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("decode operation payload: %v", err)
	}
	if payload["capability"] != "vision" || payload["scope"] != "tenant" || payload["enabled"] != true {
		t.Fatalf("operation payload=%v want capability/scope/enabled summary", payload)
	}
}

func TestModelCapabilityBindingRejectsCrossTenantModel(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	var otherModelID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO models (
    tenant_id, scope, canonical_id, protocol_family, default_provider_model_id,
    default_context_window, default_request_timeout_ms, pricing_class, model_owner
) VALUES ($1, 'tenant', $2, 'openai_chat', $3, 128000, 30000, 'standard', 'test')
RETURNING id
`, f.otherTenantID, "other/capability-"+f.suffix, "other-capability-"+f.suffix).Scan(&otherModelID); err != nil {
		t.Fatalf("seed other tenant model: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM models WHERE id = $1`, otherModelID)
	})

	_, err := NewPostgresRegistry(pool, nil).UpsertModelCapabilityBinding(ctx, UpsertModelCapabilityBindingParams{
		TenantID:   f.tenantID,
		Scope:      "tenant",
		ModelID:    otherModelID,
		Capability: "vision",
		Enabled:    true,
		Source:     "operator",
		Actor:      "admin_token:cross-tenant-" + f.suffix,
		ActorRole:  "platform_admin",
		RequestID:  "req-cross-tenant-" + f.suffix,
	})
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("cross-tenant model err=%v want ErrUnknownModel", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM model_registry_capabilities
WHERE tenant_id = $1 AND model_id = $2
`, f.tenantID, otherModelID).Scan(&count); err != nil {
		t.Fatalf("count cross-tenant binding: %v", err)
	}
	if count != 0 {
		t.Fatalf("cross-tenant binding rows=%d want 0", count)
	}
}

func TestModelCapabilityBindingLogFailureRollsBackBindingAndSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	modelID := f.seedModel(modelOpts{canonicalID: "capability-rollback-" + f.suffix})
	f.setSnapshot(11)
	requestID := "req-capability-rollback-" + f.suffix
	actor := "admin_token:rollback-" + f.suffix

	compactSuffix := strings.ReplaceAll(f.suffix, "-", "")
	functionName := pgx.Identifier{"huakai_fail_capability_log_" + compactSuffix}.Sanitize()
	triggerName := pgx.Identifier{"huakai_fail_capability_log_trigger_" + compactSuffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'forced model capability log failure';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER %s
BEFORE INSERT ON admin_audit_events
FOR EACH ROW
WHEN (NEW.request_id = '%s')
EXECUTE FUNCTION %s();
`, functionName, triggerName, requestID, functionName)); err != nil {
		t.Fatalf("install log failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON admin_audit_events; DROP FUNCTION IF EXISTS %s();",
			triggerName, functionName,
		))
	})

	_, err := NewPostgresRegistry(pool, nil).UpsertModelCapabilityBinding(ctx, UpsertModelCapabilityBindingParams{
		TenantID:   f.tenantID,
		Scope:      "tenant",
		ModelID:    modelID,
		Capability: "vision",
		Enabled:    false,
		Source:     "operator",
		Actor:      actor,
		ActorRole:  "platform_admin",
		RequestID:  requestID,
	})
	if err == nil || !strings.Contains(err.Error(), "insert model capability binding log") {
		t.Fatalf("log failure err=%v want wrapped operation-log failure", err)
	}
	var bindingCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM model_registry_capabilities
WHERE tenant_id = $1 AND model_id = $2 AND capability = 'vision'
`, f.tenantID, modelID).Scan(&bindingCount); err != nil {
		t.Fatalf("count binding after rollback: %v", err)
	}
	if bindingCount != 0 {
		t.Fatalf("日志失败后 binding rows=%d want 0；业务写必须随事务回滚", bindingCount)
	}
	if version := readSnapVer(t, ctx, pool, f.tenantID); version != 11 {
		t.Fatalf("日志失败后 snapshot version=%d want 11", version)
	}
}
