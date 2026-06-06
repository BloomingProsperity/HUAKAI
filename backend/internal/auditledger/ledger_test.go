package auditledger

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestNoopLedger_AllNoop(t *testing.T) {
	l := NoopLedger{}
	ctx := context.Background()
	in := LedgerEntry{RequestID: "req_001"}
	out, err := l.Append(ctx, mustPrepareForAppend(t, ctx, in))
	if err != nil {
		t.Errorf("Append err: %v", err)
	}
	if out.RequestID != "req_001" {
		t.Errorf("Append must return input unchanged")
	}
	if _, err := l.GetByRequestID(ctx, "req_001"); !errors.Is(err, ErrLedgerEntryNotFound) {
		t.Errorf("Noop Get must be ErrLedgerEntryNotFound")
	}
	root, _ := l.LatestMerkleRoot(ctx)
	if root != ZeroRoot {
		t.Errorf("Noop root must be ZeroRoot")
	}
	if l.Size(ctx) != 0 {
		t.Errorf("Noop Size must be 0")
	}
}

func mustPrepareForAppend(t testing.TB, ctx context.Context, entry LedgerEntry) PreparedEntry {
	t.Helper()
	prepared, err := PrepareEntry(ctx, entry)
	if err != nil {
		t.Fatalf("PrepareEntry: %v", err)
	}
	return prepared
}

func TestNewMemoryLedger_NilSignerRejected(t *testing.T) {
	if _, err := NewMemoryLedger(nil); !errors.Is(err, ErrSignerNil) {
		t.Errorf("expected ErrSignerNil, got %v", err)
	}
}

func TestMemoryLedger_AppendComputesFields(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	entry := LedgerEntry{
		LedgerID:  "lid_1",
		RequestID: "req_1",
		TenantID:  42,
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-05-13T10:00:00Z"}},
	}
	out, err := l.Append(ctx, mustPrepareForAppend(t, ctx, entry))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if out.Timestamp == "" {
		t.Error("Timestamp must be auto-filled")
	}
	if out.PrevMerkleRoot != ZeroRoot {
		t.Error("first entry PrevMerkleRoot must be ZeroRoot")
	}
	if out.MerkleRoot == ZeroRoot {
		t.Error("MerkleRoot must be non-zero")
	}
	if out.PubkeyFingerprint != signer.Fingerprint() {
		t.Errorf("PubkeyFingerprint mismatch: got %q want %q", out.PubkeyFingerprint, signer.Fingerprint())
	}
	if out.Signature == "" {
		t.Error("Signature must be non-empty")
	}
	// 解 base64 signature 应得 64 bytes ed25519。
	sig, err := base64.StdEncoding.DecodeString(out.Signature)
	if err != nil || len(sig) != 64 {
		t.Errorf("Signature decode wrong: err=%v len=%d", err, len(sig))
	}
}

func TestMemoryLedger_ChainContinuity(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	e1, _ := l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{LedgerID: "1", RequestID: "r1"}))
	e2, _ := l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{LedgerID: "2", RequestID: "r2"}))

	if e2.PrevMerkleRoot != e1.MerkleRoot {
		t.Errorf("e2.PrevMerkleRoot != e1.MerkleRoot")
	}
	if l.Size(ctx) != 2 {
		t.Errorf("size: %d", l.Size(ctx))
	}
	root, _ := l.LatestMerkleRoot(ctx)
	if root != e2.MerkleRoot {
		t.Errorf("latest root mismatch")
	}
}

func TestMemoryLedger_GetByRequestID(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{LedgerID: "1", RequestID: "rA"}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{LedgerID: "2", RequestID: "rB"}))

	got, err := l.GetByRequestID(ctx, "rB")
	if err != nil {
		t.Fatalf("Get rB: %v", err)
	}
	if got.RequestID != "rB" {
		t.Errorf("got RequestID=%q want rB", got.RequestID)
	}

	if _, err := l.GetByRequestID(ctx, "missing"); !errors.Is(err, ErrLedgerEntryNotFound) {
		t.Errorf("expected not found, got %v", err)
	}
}

func TestAT_SECURITY_W1_B14_MemoryLedgerTenantScopedLookup(t *testing.T) {
	// Risk killed: a request_id lookup must include tenant scope so a caller
	// cannot read another tenant's ledger entry by guessing request_id.
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	entry, err := l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_scope_a", TenantID: 7}))
	if err != nil {
		t.Fatalf("append A: %v", err)
	}
	if _, err := l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_scope_b", TenantID: 8})); err != nil {
		t.Fatalf("append B: %v", err)
	}
	got, err := l.GetByRequestIDAndTenantScope(ctx, "req_scope_a", TenantScopeRef(7))
	if err != nil {
		t.Fatalf("scoped lookup A: %v", err)
	}
	if got.LedgerID != entry.LedgerID {
		t.Fatalf("got ledger %q want %q", got.LedgerID, entry.LedgerID)
	}
	if _, err := l.GetByRequestIDAndTenantScope(ctx, "req_scope_a", TenantScopeRef(8)); !errors.Is(err, ErrLedgerEntryNotFound) {
		t.Fatalf("wrong tenant scope must not read A, got %v", err)
	}
	if _, err := l.GetByRequestIDAndTenantScope(ctx, "req_scope_a", ""); !errors.Is(err, ErrLedgerEntryNotFound) {
		t.Fatalf("empty tenant scope must not read A, got %v", err)
	}
}

func TestMemoryLedger_ListByRangeTenantScopedAndBounded(t *testing.T) {
	// Mutation: remove the tenant_scope_ref comparison inside ListByRange; this
	// test fails because tenant B's request appears in tenant A's export rows.
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_a_before", TenantID: 7, Timestamp: "2026-06-01T00:00:00Z"}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_a_in_1", TenantID: 7, Timestamp: "2026-06-02T00:00:00Z"}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_a_in_2", TenantID: 7, Timestamp: "2026-06-03T00:00:00Z"}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_b_in", TenantID: 8, Timestamp: "2026-06-02T12:00:00Z"}))

	from := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 3, 23, 59, 59, 0, time.UTC)
	rows, err := l.ListByRange(ctx, TenantScopeRef(7), from, to, 10)
	if err != nil {
		t.Fatalf("ListByRange: %v", err)
	}
	got := ledgerRequestIDs(rows)
	want := []string{"req_a_in_1", "req_a_in_2"}
	if !equalStrings(got, want) {
		t.Fatalf("range request ids=%v want %v", got, want)
	}

	limited, err := l.ListByRange(ctx, TenantScopeRef(7), from, to, 1)
	if err != nil {
		t.Fatalf("ListByRange limited: %v", err)
	}
	if got := ledgerRequestIDs(limited); !equalStrings(got, []string{"req_a_in_1"}) {
		t.Fatalf("limited range request ids=%v want [req_a_in_1]", got)
	}
}

func TestMemoryLedger_ListByRequestIDsTenantScoped(t *testing.T) {
	// Mutation: drop tenant filtering in ListByRequestIDs; this test fails
	// because req_b leaks into tenant A's selected proof entries.
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_a_1", TenantID: 7}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_a_2", TenantID: 7}))
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{RequestID: "req_b", TenantID: 8}))

	rows, err := l.ListByRequestIDs(ctx, TenantScopeRef(7), []string{"req_b", "req_a_2", "missing", "req_a_1"}, 10)
	if err != nil {
		t.Fatalf("ListByRequestIDs: %v", err)
	}
	got := ledgerRequestIDs(rows)
	want := []string{"req_a_1", "req_a_2"}
	if !equalStrings(got, want) {
		t.Fatalf("request id rows=%v want %v", got, want)
	}
}

func ledgerRequestIDs(entries []LedgerEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.RequestID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMemoryLedger_SnapshotIsDeepCopy(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	entry, _ := l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{LedgerID: "1", RequestID: "r1"}))
	snap1 := l.Snapshot()
	_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{LedgerID: "2", RequestID: "r2"}))
	snap2 := l.Snapshot()

	if len(snap1) != 1 || len(snap2) != 2 {
		t.Errorf("snapshots don't reflect appends: %d / %d", len(snap1), len(snap2))
	}
	// 篡改 snap1 不应影响内部 chain。
	snap1[0].LedgerID = "polluted"
	got, _ := l.GetByRequestID(ctx, "r1")
	if got.LedgerID != entry.LedgerID {
		t.Error("snapshot mutation polluted internal chain")
	}
}

func TestMemoryLedger_VerifyChainHappyPath(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, _ = l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{LedgerID: itoa(i), RequestID: "r" + itoa(i)}))
	}
	snap := l.Snapshot()
	if err := VerifyChain(snap); err != nil {
		t.Errorf("verify chain failed: %v", err)
	}
}

func TestMemoryLedger_VerifySignaturesIndependently(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	entry, _ := l.Append(ctx, mustPrepareForAppend(t, ctx, LedgerEntry{LedgerID: "1", RequestID: "r1"}))

	// 模拟 user 用公开 pubkey 独立 verify entry signature
	sig, err := base64.StdEncoding.DecodeString(entry.Signature)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	eh, _ := EntryHash(&entry)
	if err := sign.Verify(signer.PublicKey(), eh[:], sig); err != nil {
		t.Errorf("independent verify failed: %v", err)
	}
}

func TestPrepareEntry_FieldLevelRedactionReturnsSanitizedPreparedEntry(t *testing.T) {
	// Risk killed: PrepareEntry must keep the sanitized payload when a redactor
	// reports field-level loss, not silently pass raw sensitive fields to Append.
	// Mutation self-check: bypass sanitizeLedgerEntry and this test fails because
	// forbidden_marker remains in HopChain.Detail.
	const marker = "w4a-prepare-field-marker-sk-never-persist"
	previousRedactor := ledgerRedactor
	ledgerRedactor = func() ledgerPayloadRedactor {
		return fieldLevelLedgerPayloadRedactor{blockedField: "forbidden_marker"}
	}
	defer func() { ledgerRedactor = previousRedactor }()

	prepared, err := PrepareEntry(context.Background(), LedgerEntry{
		LedgerID:       "raw-ledger-id-must-not-survive",
		Timestamp:      "2026-05-22T10:00:00Z",
		RequestID:      "req_prepare_field_level",
		TenantID:       7,
		TenantScopeRef: TenantScopeRef(7),
		HopChain: []proto.HopAttestation{{
			Hop:       proto.HopProvider,
			HopKind:   "provider",
			Provider:  "openai",
			Endpoint:  "https://api.openai.example/v1/chat/completions",
			Timestamp: "2026-05-22T10:00:00Z",
			Detail:    json.RawMessage(`{"safe_metric":"kept","forbidden_marker":"` + marker + `"}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
		},
		PubkeyFingerprint: "raw-fingerprint-must-not-survive",
		Signature:         "raw-signature-must-not-survive",
	})
	if err != nil {
		t.Fatalf("PrepareEntry: %v", err)
	}
	if prepared.requestID != "req_prepare_field_level" || prepared.tenantID != 7 || prepared.createdAt != "2026-05-22T10:00:00Z" {
		t.Fatalf("prepared scalar fields mismatch: %+v", prepared)
	}
	if prepared.tenantScopeRef != TenantScopeRef(7) {
		t.Fatalf("tenant scope ref not retained after field redaction: %q", prepared.tenantScopeRef)
	}
	assertFieldLevelRedactedProviderHop(t, prepared.hopChain, marker)
	assertFieldLevelRedactedModelChain(t, prepared.modelChain)
}

func TestPrepareEntry_RedactionFailureReturnsSentinelPreparedEntry(t *testing.T) {
	// Risk killed: if redaction is unusable, PrepareEntry must produce a safe
	// redaction_dropped intent with nil error so callers can still append it.
	// Mutation self-check: returning the raw entry on ErrLedgerSanitizeUnusable
	// makes this test fail because the sensitive marker remains in HopChain.
	const marker = "w4a-prepare-failure-marker-sk-never-persist"
	previousRedactor := ledgerRedactor
	ledgerRedactor = func() ledgerPayloadRedactor {
		return failingLedgerPayloadRedactor{marker: marker}
	}
	defer func() { ledgerRedactor = previousRedactor }()

	prepared, err := PrepareEntry(context.Background(), LedgerEntry{
		Timestamp:      "2026-05-22T10:00:00Z",
		RequestID:      "req_prepare_redaction_failure",
		TenantID:       7,
		TenantScopeRef: TenantScopeRef(7),
		HopChain: []proto.HopAttestation{{
			Hop:       proto.HopProvider,
			Timestamp: "2026-05-22T10:00:00Z",
			Detail:    json.RawMessage(`{"prompt":"` + marker + `"}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
		},
	})
	if err != nil {
		t.Fatalf("PrepareEntry returned error for sentinel fallback: %v", err)
	}
	if prepared.requestID != "req_prepare_redaction_failure" || prepared.tenantID != 7 || prepared.createdAt != "2026-05-22T10:00:00Z" {
		t.Fatalf("prepared scalar fields mismatch: %+v", prepared)
	}
	if len(prepared.hopChain) != 1 || prepared.hopChain[0].HopKind != redactionDroppedSentinel || prepared.hopChain[0].DecisionRef != redactionDroppedSentinel {
		t.Fatalf("hop chain must be redaction_dropped sentinel, got %+v", prepared.hopChain)
	}
	if prepared.modelChain != nil {
		t.Fatalf("model_chain must be dropped on redaction failure, got %+v", prepared.modelChain)
	}
	if prepared.tenantScopeRef != "" {
		t.Fatalf("tenant_scope_ref must be dropped on redaction failure, got %q", prepared.tenantScopeRef)
	}
}

func TestPreparedEntry_AsLedgerEntryReturnsDeepCopy(t *testing.T) {
	// Risk killed: mutating a read projection must not poison the sealed
	// PreparedEntry that Append trusts without re-sanitizing.
	const detailMarker = "w4a_projection_alias_marker"
	const featureMarker = "F-POLLUTED"
	const pollutedModel = "polluted-model"

	prepared, err := PrepareEntry(context.Background(), LedgerEntry{
		RequestID:      "req_projection_deep_copy",
		TenantID:       7,
		TenantScopeRef: TenantScopeRef(7),
		HopChain: []proto.HopAttestation{{
			Hop:         proto.HopProvider,
			HopKind:     "provider",
			FeatureRefs: []string{"F-ORIGINAL"},
			Timestamp:   "2026-05-22T10:00:00Z",
			Detail:      json.RawMessage(`{"safe_metric":"original","padding":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "match",
		},
	})
	if err != nil {
		t.Fatalf("PrepareEntry: %v", err)
	}

	le1 := prepared.AsLedgerEntry()
	if len(le1.HopChain) != 1 || len(le1.HopChain[0].Detail) < len(detailMarker) || len(le1.HopChain[0].FeatureRefs) != 1 {
		t.Fatalf("fixture did not produce mutable projection: %+v", le1.HopChain)
	}
	copy(le1.HopChain[0].Detail, []byte(detailMarker))
	le1.HopChain[0].FeatureRefs[0] = featureMarker
	le1.HopChain[0].HopKind = "polluted-hop"
	if le1.ModelChain == nil {
		t.Fatal("fixture did not produce model_chain projection")
	}
	le1.ModelChain.Requested = pollutedModel

	le2 := prepared.AsLedgerEntry()
	if len(le2.HopChain) != 1 {
		t.Fatalf("second projection hop_chain len=%d want 1", len(le2.HopChain))
	}
	if bytes.Contains(le2.HopChain[0].Detail, []byte(detailMarker)) {
		t.Fatalf("second projection detail was polluted through aliasing: %s", le2.HopChain[0].Detail)
	}
	if len(le2.HopChain[0].FeatureRefs) != 1 || le2.HopChain[0].FeatureRefs[0] == featureMarker {
		t.Fatalf("second projection feature_refs was polluted through aliasing: %+v", le2.HopChain[0].FeatureRefs)
	}
	if le2.HopChain[0].HopKind != "provider" {
		t.Fatalf("second projection hop struct was polluted through aliasing: %+v", le2.HopChain[0])
	}
	if le2.ModelChain == nil || le2.ModelChain.Requested == pollutedModel {
		t.Fatalf("second projection model_chain was polluted through aliasing: %+v", le2.ModelChain)
	}
}

func TestPrepareEntry_MissingRequestIDReturnsError(t *testing.T) {
	// Risk killed: callers must not get a PreparedEntry that Append cannot tie
	// to a stable request_id; without this guard future DLQ intents are unsafe.
	// Mutation self-check: deleting the RequestID precondition makes this test
	// fail because PrepareEntry returns nil error.
	_, err := PrepareEntry(context.Background(), LedgerEntry{
		TenantID: 7,
		HopChain: []proto.HopAttestation{{
			Hop:       proto.HopIngress,
			Timestamp: "2026-05-22T10:00:00Z",
		}},
	})
	if err == nil {
		t.Fatal("missing RequestID must return error")
	}
}

func TestAuditLedgerResultValidateEnforcesStateInvariants(t *testing.T) {
	// Risk killed: W4a-3 callers must not represent Deferred entries as signed
	// ledger rows or allow production Disabled results. Mutation self-check:
	// relaxing any per-state field check below makes the matching invalid case
	// pass and this table test fail.
	tests := []struct {
		name            string
		result          AuditLedgerResult
		production      bool
		wantErr         bool
		wantErrContains string
	}{
		{
			name: "persisted valid",
			result: AuditLedgerResult{
				State:       LedgerResultStatePersisted,
				LedgerID:    "ldg_t7_1",
				Fingerprint: "fp-live",
			},
		},
		{
			name: "persisted requires fingerprint",
			result: AuditLedgerResult{
				State:    LedgerResultStatePersisted,
				LedgerID: "ldg_t7_1",
			},
			wantErr: true,
		},
		{
			name: "deferred valid",
			result: AuditLedgerResult{
				State:  LedgerResultStateDeferred,
				DLQRef: "audit_ledger_dlq:42",
			},
		},
		{
			name: "deferred rejects fingerprint",
			result: AuditLedgerResult{
				State:       LedgerResultStateDeferred,
				DLQRef:      "audit_ledger_dlq:42",
				Fingerprint: "fp-must-not-exist",
			},
			wantErr: true,
		},
		{
			name: "deferred rejects upstream provider",
			result: AuditLedgerResult{
				State:            LedgerResultStateDeferred,
				DLQRef:           "audit_ledger_dlq:42",
				UpstreamProvider: "anthropic",
			},
			wantErr:         true,
			wantErrContains: "upstream metadata",
		},
		{
			name: "deferred rejects upstream model",
			result: AuditLedgerResult{
				State:         LedgerResultStateDeferred,
				DLQRef:        "audit_ledger_dlq:42",
				UpstreamModel: "claude-opus-4-20260514",
			},
			wantErr:         true,
			wantErrContains: "upstream metadata",
		},
		{
			name: "deferred rejects request id",
			result: AuditLedgerResult{
				State:     LedgerResultStateDeferred,
				DLQRef:    "audit_ledger_dlq:42",
				RequestID: "req_must_not_exist",
			},
			wantErr:         true,
			wantErrContains: "upstream metadata",
		},
		{
			name:   "disabled valid outside production",
			result: AuditLedgerResult{State: LedgerResultStateDisabled},
		},
		{
			name:       "disabled rejects production",
			result:     AuditLedgerResult{State: LedgerResultStateDisabled},
			production: true,
			wantErr:    true,
		},
		{
			name: "disabled rejects upstream provider",
			result: AuditLedgerResult{
				State:            LedgerResultStateDisabled,
				UpstreamProvider: "anthropic",
			},
			wantErr:         true,
			wantErrContains: "upstream metadata",
		},
		{
			name: "disabled rejects upstream model",
			result: AuditLedgerResult{
				State:         LedgerResultStateDisabled,
				UpstreamModel: "claude-opus-4-20260514",
			},
			wantErr:         true,
			wantErrContains: "upstream metadata",
		},
		{
			name: "disabled rejects request id",
			result: AuditLedgerResult{
				State:     LedgerResultStateDisabled,
				RequestID: "req_must_not_exist",
			},
			wantErr:         true,
			wantErrContains: "upstream metadata",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.Validate(tt.production)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
				t.Fatalf("Validate() error=%v want substring %q", err, tt.wantErrContains)
			}
		})
	}
}

func TestEnqueuePreparedEntryToDLQBuildsAuditLedgerEnvelope(t *testing.T) {
	// Risk killed: Append failure must enqueue the exact sanitized append intent
	// with the W4a-3 envelope. Mutation self-check: changing EventKind,
	// IdempotencyKey, ReplicaStatus, SourceTable, or DLQRef format makes this
	// test fail before any request can silently settle without a durable intent.
	ctx := context.Background()
	prepared := mustPrepareForAppend(t, ctx, LedgerEntry{
		RequestID: "req-dlq-producer",
		TenantID:  77,
		HopChain:  []proto.HopAttestation{{HopKind: "provider_response"}},
	})
	enqueuer := &recordingAuditLedgerDLQEnqueuer{id: 91}

	ref, err := EnqueuePreparedEntryToDLQ(ctx, enqueuer, prepared, errors.New("append broke"))
	if err != nil {
		t.Fatalf("EnqueuePreparedEntryToDLQ: %v", err)
	}
	if ref != "audit_ledger_dlq:91" {
		t.Fatalf("DLQRef=%q want audit_ledger_dlq:91", ref)
	}
	if len(enqueuer.events) != 1 {
		t.Fatalf("events=%d want 1", len(enqueuer.events))
	}
	event := enqueuer.events[0]
	if event.EventKind != dlq.EventKindAuditLedgerEntry {
		t.Fatalf("EventKind=%q want %q", event.EventKind, dlq.EventKindAuditLedgerEntry)
	}
	if event.TenantID != 77 {
		t.Fatalf("TenantID=%d want 77", event.TenantID)
	}
	if event.IdempotencyKey != "audit_ledger:req-dlq-producer" {
		t.Fatalf("IdempotencyKey=%q", event.IdempotencyKey)
	}
	if event.SourceTable != "audit_ledger" {
		t.Fatalf("SourceTable=%q want audit_ledger", event.SourceTable)
	}
	if event.ReplicaStatus != dlq.ReplicaStatusNone {
		t.Fatalf("ReplicaStatus=%q want %q", event.ReplicaStatus, dlq.ReplicaStatusNone)
	}
	if !json.Valid(event.Payload) {
		t.Fatalf("Payload is not valid JSON: %s", event.Payload)
	}
	decoded, err := decodeLedgerEntryFromDLQPayload(event.Payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded.RequestID != "req-dlq-producer" || decoded.TenantID != 77 || len(decoded.HopChain) != 1 {
		t.Fatalf("decoded payload mismatch: %+v", decoded)
	}
}

type recordingAuditLedgerDLQEnqueuer struct {
	id     int64
	events []dlq.Event
	err    error
}

func (e *recordingAuditLedgerDLQEnqueuer) Enqueue(_ context.Context, event dlq.Event) (int64, error) {
	e.events = append(e.events, event)
	if e.err != nil {
		return 0, e.err
	}
	return e.id, nil
}

func TestMemoryLedger_RedactionFailureWritesSignedSentinel(t *testing.T) {
	// Risk killed: if ledger redaction fails, append-only storage must not keep
	// the original privacy-sensitive hop chain forever.
	const marker = "w4b-sensitive-marker-sk-never-persist"
	previousRedactor := ledgerRedactor
	ledgerRedactor = func() ledgerPayloadRedactor {
		return failingLedgerPayloadRedactor{marker: marker}
	}
	defer func() { ledgerRedactor = previousRedactor }()

	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	prepared := mustPrepareForAppend(t, ctx, LedgerEntry{
		RequestID:      "req_redaction_failure",
		TenantID:       7,
		TenantScopeRef: TenantScopeRef(7),
		HopChain: []proto.HopAttestation{{
			Hop:       proto.HopProvider,
			Timestamp: "2026-05-22T10:00:00Z",
			Detail:    json.RawMessage(`{"prompt":"` + marker + `"}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
		},
	})
	out, err := l.Append(ctx, prepared)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	assertRedactionDroppedSignedEntry(t, signer, out, marker)
}

func TestAppendInTransaction_RedactionFailureWritesSignedSentinel(t *testing.T) {
	// Risk killed: the production Postgres append path must not persist the raw
	// hop_chain when ledger redaction fails before entry hashing/signing.
	const marker = "w4b-postgres-sensitive-marker-sk-never-persist"
	previousRedactor := ledgerRedactor
	ledgerRedactor = func() ledgerPayloadRedactor {
		return failingLedgerPayloadRedactor{marker: marker}
	}
	defer func() { ledgerRedactor = previousRedactor }()

	signer, _ := sign.GenerateKey()
	db := &appendTxTestDB{}
	ctx := context.Background()
	prepared := mustPrepareForAppend(t, ctx, LedgerEntry{
		RequestID:      "req_pg_redaction_failure",
		TenantID:       7,
		TenantScopeRef: TenantScopeRef(7),
		HopChain: []proto.HopAttestation{{
			Hop:       proto.HopProvider,
			Timestamp: "2026-05-22T10:00:00Z",
			Detail:    json.RawMessage(`{"prompt":"` + marker + `"}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
		},
	})
	out, err := AppendInTransaction(ctx, db, signer, prepared)
	if err != nil {
		t.Fatalf("AppendInTransaction: %v", err)
	}
	assertRedactionDroppedSignedEntry(t, signer, out, marker)
	if bytes.Contains(db.insertHopJSON, []byte(marker)) {
		t.Fatalf("insert hop_chain still contains sensitive marker: %s", db.insertHopJSON)
	}
	var insertedHops []proto.HopAttestation
	if err := json.Unmarshal(db.insertHopJSON, &insertedHops); err != nil {
		t.Fatalf("decode inserted hop_chain: %v", err)
	}
	if len(insertedHops) != 1 || insertedHops[0].HopKind != "redaction_dropped" {
		t.Fatalf("inserted hop_chain must be sentinel, got %+v", insertedHops)
	}
	if len(db.insertModelJSON) != 0 {
		t.Fatalf("model_chain insert payload must be nil/empty on redaction failure, got %s", db.insertModelJSON)
	}
}

func TestAppendInTransaction_FieldLevelRedactionPreservesSanitizedHopChain(t *testing.T) {
	// Risk killed: field-level redaction returns sanitized bytes plus
	// ErrUnsafePayload; that must not be misclassified as total redaction
	// failure and replaced with the redaction_dropped sentinel.
	const marker = "w4b-field-redaction-marker-sk-never-persist"
	previousRedactor := ledgerRedactor
	ledgerRedactor = func() ledgerPayloadRedactor {
		return fieldLevelLedgerPayloadRedactor{blockedField: "forbidden_marker"}
	}
	defer func() { ledgerRedactor = previousRedactor }()

	signer, _ := sign.GenerateKey()
	db := &appendTxTestDB{}
	ctx := context.Background()
	prepared := mustPrepareForAppend(t, ctx, LedgerEntry{
		RequestID:      "req_pg_field_level_redaction",
		TenantID:       7,
		TenantScopeRef: TenantScopeRef(7),
		HopChain: []proto.HopAttestation{{
			Hop:       proto.HopProvider,
			HopKind:   "provider",
			Provider:  "openai",
			Endpoint:  "https://api.openai.example/v1/chat/completions",
			Timestamp: "2026-05-22T10:00:00Z",
			Detail:    json.RawMessage(`{"safe_metric":"kept","forbidden_marker":"` + marker + `"}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
		},
	})
	out, err := AppendInTransaction(ctx, db, signer, prepared)
	if err != nil {
		t.Fatalf("AppendInTransaction: %v", err)
	}
	assertFieldLevelRedactedProviderHop(t, out.HopChain, marker)
	assertFieldLevelRedactedModelChain(t, out.ModelChain)

	var insertedHops []proto.HopAttestation
	if err := json.Unmarshal(db.insertHopJSON, &insertedHops); err != nil {
		t.Fatalf("decode inserted hop_chain: %v", err)
	}
	assertFieldLevelRedactedProviderHop(t, insertedHops, marker)
	if bytes.Contains(db.insertHopJSON, []byte(marker)) {
		t.Fatalf("insert hop_chain still contains stripped marker: %s", db.insertHopJSON)
	}
	if len(db.insertModelJSON) == 0 {
		t.Fatalf("model_chain insert payload must be retained after field-level redaction")
	}
}

func TestAppendInTransactionRequestIDUniqueViolationReturnsDuplicateRequestID(t *testing.T) {
	// Risk killed: DLQ replay treats ErrDuplicateRequestID as the successful
	// concurrent-worker race. Postgres must expose the same contract as
	// MemoryLedger when the request_id unique constraint fires.
	// Mutation self-check: remove the 23505 request_id constraint translation
	// and this test fails because errors.Is no longer matches
	// ErrDuplicateRequestID.
	signer, _ := sign.GenerateKey()
	db := &appendTxTestDB{
		insertErr: &pgconn.PgError{
			Code:           "23505",
			ConstraintName: "audit_ledger_entries_request_id_key",
		},
	}

	_, err := AppendInTransaction(context.Background(), db, signer, mustPrepareForAppend(t, context.Background(), LedgerEntry{
		RequestID: "req_pg_duplicate_contract",
		TenantID:  7,
	}))
	if !errors.Is(err, ErrDuplicateRequestID) {
		t.Fatalf("AppendInTransaction error=%v want ErrDuplicateRequestID", err)
	}
}

func assertFieldLevelRedactedProviderHop(t *testing.T, hops []proto.HopAttestation, marker string) {
	t.Helper()
	if len(hops) != 1 {
		t.Fatalf("expected one retained provider hop, got %+v", hops)
	}
	hop := hops[0]
	if hop.HopKind == redactionDroppedSentinel || hop.DecisionRef == redactionDroppedSentinel {
		t.Fatalf("field-level redaction must not write sentinel, got %+v", hop)
	}
	if hop.Hop != proto.HopProvider || hop.HopKind != "provider" || hop.Provider != "openai" {
		t.Fatalf("provider hop structure was not retained: %+v", hop)
	}
	if bytes.Contains(hop.Detail, []byte(marker)) || bytes.Contains(hop.Detail, []byte("forbidden_marker")) {
		t.Fatalf("provider hop detail still contains stripped marker: %s", hop.Detail)
	}
	if !bytes.Contains(hop.Detail, []byte(`"safe_metric":"kept"`)) {
		t.Fatalf("provider hop detail lost non-forbidden field: %s", hop.Detail)
	}
}

func assertFieldLevelRedactedModelChain(t *testing.T, model *proto.ModelChain) {
	t.Helper()
	if model == nil {
		t.Fatalf("model_chain must be retained after field-level redaction")
	}
	if model.Requested != "gpt-4o" || model.RouteDecided != "gpt-4o" || model.UpstreamReported != "gpt-4o" {
		t.Fatalf("model_chain fields were not retained: %+v", model)
	}
}

func assertRedactionDroppedSignedEntry(t *testing.T, signer *sign.Signer, out LedgerEntry, marker string) {
	t.Helper()
	if len(out.HopChain) != 1 || out.HopChain[0].HopKind != "redaction_dropped" || out.HopChain[0].DecisionRef != "redaction_dropped" {
		t.Fatalf("hop chain must be the redaction_dropped sentinel, got %+v", out.HopChain)
	}
	if bytes.Contains(CanonicalPayload(out), []byte(marker)) {
		t.Fatalf("canonical signed payload still contains sensitive marker: %s", CanonicalPayload(out))
	}
	if out.ModelChain != nil {
		t.Fatalf("model_chain must be dropped on redaction failure, got %+v", out.ModelChain)
	}
	if out.TenantScopeRef != "" {
		t.Fatalf("tenant_scope_ref must be dropped on redaction failure, got %q", out.TenantScopeRef)
	}

	sig, err := base64.StdEncoding.DecodeString(out.Signature)
	if err != nil {
		t.Fatalf("signature decode: %v", err)
	}
	entryHash, err := EntryHash(&out)
	if err != nil {
		t.Fatalf("entry hash: %v", err)
	}
	if err := sign.Verify(signer.PublicKey(), entryHash[:], sig); err != nil {
		t.Fatalf("sentinel entry signature did not verify: %v", err)
	}
	tampered := out
	tampered.HopChain[0].DecisionRef = "tampered_redaction_dropped"
	tamperedHash, err := EntryHash(&tampered)
	if err != nil {
		t.Fatalf("tampered entry hash: %v", err)
	}
	if err := sign.Verify(signer.PublicKey(), tamperedHash[:], sig); !errors.Is(err, sign.ErrSignatureMismatch) {
		t.Fatalf("signature must cover the sentinel hop; verify err=%v", err)
	}
}

func TestScanLedgerEntryCorruptBadHopChainJSON(t *testing.T) {
	// Risk killed: a corrupt hop_chain JSON row must not be silently converted
	// into a normal entry with an empty hop chain.
	tenantID := int64(7)
	row := validScanLedgerEntryTestRow()
	row.tenantID = &tenantID
	row.hopJSON = []byte(`{"hop":`)

	got, err := scanLedgerEntry(row)
	if !errors.Is(err, ErrLedgerEntryCorrupt) {
		t.Fatalf("scan error=%v want ErrLedgerEntryCorrupt", err)
	}
	assertCorruptScanCarriesOnlyReliableScalars(t, got, row, tenantID)
}

func TestScanLedgerEntryCorruptBadModelChainJSON(t *testing.T) {
	// Risk killed: a corrupt model_chain JSON row must not return a half-parsed
	// entry whose hop_chain can be mistaken for verified ledger content.
	tenantID := int64(7)
	row := validScanLedgerEntryTestRow()
	row.tenantID = &tenantID
	row.modelJSON = []byte(`{"requested":`)

	got, err := scanLedgerEntry(row)
	if !errors.Is(err, ErrLedgerEntryCorrupt) {
		t.Fatalf("scan error=%v want ErrLedgerEntryCorrupt", err)
	}
	assertCorruptScanCarriesOnlyReliableScalars(t, got, row, tenantID)
}

func TestScanLedgerEntryCorruptShortMerkleRoot(t *testing.T) {
	// Risk killed: a structurally invalid persisted Merkle root must not be
	// accepted as the zero root.
	tenantID := int64(7)
	row := validScanLedgerEntryTestRow()
	row.tenantID = &tenantID
	row.merkleRoot = bytes.Repeat([]byte{0x42}, 16)

	got, err := scanLedgerEntry(row)
	if !errors.Is(err, ErrLedgerEntryCorrupt) {
		t.Fatalf("scan error=%v want ErrLedgerEntryCorrupt", err)
	}
	assertCorruptScanCarriesOnlyReliableScalars(t, got, row, tenantID)
}

func TestPostgresScopedLookupHidesCorruptRowsFromOtherTenants(t *testing.T) {
	// Risk killed: a public verify lookup must not reveal that another tenant's
	// request_id exists just because the row is corrupt and would otherwise map
	// to ledger_corrupt/500 instead of not found/404.
	tenantA := int64(7)
	tenantB := int64(8)
	row := validScanLedgerEntryTestRow()
	row.requestID = "req_cross_tenant_corrupt"
	row.tenantID = &tenantA
	row.hopJSON = []byte(`{"hop":`)
	lookup := func(_ context.Context, requestID string) (LedgerEntry, error) {
		if requestID != row.requestID {
			return LedgerEntry{}, ErrLedgerEntryNotFound
		}
		return scanLedgerEntry(row)
	}

	_, err := getByRequestIDAndTenantScope(context.Background(), row.requestID, TenantScopeRef(tenantB), lookup)
	if errors.Is(err, ErrLedgerEntryCorrupt) {
		t.Fatalf("tenant B lookup leaked corrupt row existence: %v", err)
	}
	if !errors.Is(err, ErrLedgerEntryNotFound) {
		t.Fatalf("tenant B lookup error=%v want ErrLedgerEntryNotFound", err)
	}

	_, err = getByRequestIDAndTenantScope(context.Background(), row.requestID, TenantScopeRef(tenantA), lookup)
	if !errors.Is(err, ErrLedgerEntryCorrupt) {
		t.Fatalf("tenant A owner lookup error=%v want ErrLedgerEntryCorrupt", err)
	}
}

func TestScanLedgerEntryAcceptsValidThirtyTwoByteRoots(t *testing.T) {
	// Risk killed: the corrupt detector must distinguish actual malformed
	// roots from the normal 32-byte roots used by every persisted row.
	row := validScanLedgerEntryTestRow()

	got, err := scanLedgerEntry(row)
	if err != nil {
		t.Fatalf("scan valid row: %v", err)
	}
	if got.PrevMerkleRoot != bytesToRoot(row.prevRoot) {
		t.Fatalf("prev root mismatch")
	}
	if got.MerkleRoot != bytesToRoot(row.merkleRoot) {
		t.Fatalf("merkle root mismatch")
	}
	if len(got.HopChain) != 1 || got.HopChain[0].Hop != proto.HopIngress {
		t.Fatalf("hop chain mismatch: %+v", got.HopChain)
	}
}

func assertCorruptScanCarriesOnlyReliableScalars(t *testing.T, got LedgerEntry, row scanLedgerEntryTestRow, tenantID int64) {
	t.Helper()
	if got.TenantID != tenantID {
		t.Fatalf("corrupt scan TenantID=%d want %d", got.TenantID, tenantID)
	}
	if got.LedgerID != row.ledgerID || got.RequestID != row.requestID {
		t.Fatalf("corrupt scan scalar IDs not preserved: got ledger=%q request=%q", got.LedgerID, got.RequestID)
	}
	if got.Timestamp != row.occurredAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("corrupt scan timestamp=%q want %q", got.Timestamp, row.occurredAt.UTC().Format(time.RFC3339Nano))
	}
	if len(got.HopChain) != 0 || got.ModelChain != nil {
		t.Fatalf("corrupt scan must not expose half-parsed chains: hop=%+v model=%+v", got.HopChain, got.ModelChain)
	}
	if got.PrevMerkleRoot != ZeroRoot || got.MerkleRoot != ZeroRoot {
		t.Fatalf("corrupt scan must not expose merkle roots: prev=%x root=%x", got.PrevMerkleRoot, got.MerkleRoot)
	}
	if got.PubkeyFingerprint != "" || got.Signature != "" {
		t.Fatalf("corrupt scan must not expose trailing scalars: fp=%q sig=%q", got.PubkeyFingerprint, got.Signature)
	}
}

type failingLedgerPayloadRedactor struct {
	marker string
}

func (r failingLedgerPayloadRedactor) SanitizePayload(_ context.Context, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(raw, []byte(r.marker)) {
		return nil, fmt.Errorf("redaction failed for marker")
	}
	return raw, nil
}

type fieldLevelLedgerPayloadRedactor struct {
	blockedField string
}

func TestFieldLevelLedgerPayloadRedactorDoesNotMutateInput(t *testing.T) {
	// Risk killed: this fixture must prove PrepareEntry used the sanitized
	// return bytes. If the fixture mutates its input, a bad PrepareEntry that
	// ignores those bytes can still pass because rawEntry was already cleaned.
	const marker = "w4a-fixture-mutation-marker-sk-never-persist"
	payload := redactedLedgerPayload{
		TenantScopeRef: TenantScopeRef(7),
		HopChain: []proto.HopAttestation{{
			Hop:       proto.HopProvider,
			HopKind:   "provider",
			Provider:  "openai",
			Timestamp: "2026-05-22T10:00:00Z",
			Detail:    json.RawMessage(`{"safe_metric":"kept","forbidden_marker":"` + marker + `"}`),
		}},
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
		},
	}

	raw, err := (fieldLevelLedgerPayloadRedactor{blockedField: "forbidden_marker"}).SanitizePayload(context.Background(), payload)
	if !errors.Is(err, privacy.ErrUnsafePayload) {
		t.Fatalf("redactor error=%v want ErrUnsafePayload", err)
	}
	if !bytes.Contains(payload.HopChain[0].Detail, []byte(marker)) {
		t.Fatalf("fixture mutated input hop detail: %s", payload.HopChain[0].Detail)
	}
	if bytes.Contains(raw, []byte(marker)) || bytes.Contains(raw, []byte("forbidden_marker")) {
		t.Fatalf("sanitized payload still contains stripped marker: %s", raw)
	}
}

func (r fieldLevelLedgerPayloadRedactor) SanitizePayload(_ context.Context, payload any) ([]byte, error) {
	ledgerPayload, ok := payload.(redactedLedgerPayload)
	if !ok {
		return nil, fmt.Errorf("unexpected payload type %T", payload)
	}
	sanitizedPayload := cloneRedactedLedgerPayload(ledgerPayload)
	for i := range sanitizedPayload.HopChain {
		if len(sanitizedPayload.HopChain[i].Detail) == 0 {
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal(sanitizedPayload.HopChain[i].Detail, &detail); err != nil {
			return nil, err
		}
		delete(detail, r.blockedField)
		rawDetail, err := json.Marshal(detail)
		if err != nil {
			return nil, err
		}
		sanitizedPayload.HopChain[i].Detail = rawDetail
	}
	raw, err := json.Marshal(sanitizedPayload)
	if err != nil {
		return nil, err
	}
	return raw, privacy.ErrUnsafePayload
}

func cloneRedactedLedgerPayload(in redactedLedgerPayload) redactedLedgerPayload {
	out := redactedLedgerPayload{
		TenantScopeRef: in.TenantScopeRef,
	}
	if in.HopChain != nil {
		out.HopChain = make([]proto.HopAttestation, len(in.HopChain))
		copy(out.HopChain, in.HopChain)
		for i := range out.HopChain {
			out.HopChain[i].Detail = append(json.RawMessage(nil), in.HopChain[i].Detail...)
			out.HopChain[i].FeatureRefs = append([]string(nil), in.HopChain[i].FeatureRefs...)
		}
	}
	if in.ModelChain != nil {
		modelChain := *in.ModelChain
		out.ModelChain = &modelChain
	}
	return out
}

type scanLedgerEntryTestRow struct {
	ledgerID   string
	occurredAt time.Time
	requestID  string
	tenantID   *int64
	hopJSON    []byte
	modelJSON  []byte
	prevRoot   []byte
	merkleRoot []byte
	fp         string
	sig        string
	err        error
}

func validScanLedgerEntryTestRow() scanLedgerEntryTestRow {
	return scanLedgerEntryTestRow{
		ledgerID:   "ldg_t0_00000000000000000001",
		occurredAt: time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC),
		requestID:  "req_scan",
		hopJSON:    []byte(`[{"hop":"ingress","ts":"2026-05-22T10:00:00Z"}]`),
		prevRoot:   make([]byte, 32),
		merkleRoot: bytes.Repeat([]byte{0x7a}, 32),
		fp:         "0123456789abcdef",
		sig:        "signature",
	}
}

func (r scanLedgerEntryTestRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 10 {
		return fmt.Errorf("scan dest count=%d want 10", len(dest))
	}
	*dest[0].(*string) = r.ledgerID
	*dest[1].(*time.Time) = r.occurredAt
	*dest[2].(*string) = r.requestID
	if r.tenantID == nil {
		*dest[3].(**int64) = nil
	} else {
		tenantID := *r.tenantID
		*dest[3].(**int64) = &tenantID
	}
	*dest[4].(*[]byte) = append([]byte(nil), r.hopJSON...)
	*dest[5].(*[]byte) = append([]byte(nil), r.modelJSON...)
	*dest[6].(*[]byte) = append([]byte(nil), r.prevRoot...)
	*dest[7].(*[]byte) = append([]byte(nil), r.merkleRoot...)
	*dest[8].(*string) = r.fp
	*dest[9].(*string) = r.sig
	return nil
}

func bytesToRoot(raw []byte) [32]byte {
	var out [32]byte
	copy(out[:], raw)
	return out
}

type appendTxTestDB struct {
	insertHopJSON   []byte
	insertModelJSON []byte
	insertErr       error
}

func (db *appendTxTestDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "INSERT INTO audit_ledger_entries") {
		db.insertHopJSON = append([]byte(nil), args[4].([]byte)...)
		if modelJSON, ok := args[5].([]byte); ok {
			db.insertModelJSON = append([]byte(nil), modelJSON...)
		}
		if db.insertErr != nil {
			return pgconn.CommandTag{}, db.insertErr
		}
	}
	return pgconn.NewCommandTag("OK"), nil
}

func (db *appendTxTestDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "COUNT(*)"):
		return scanSingleInt64Row{n: 0}
	case strings.Contains(sql, "SELECT merkle_root"):
		return scanErrorRow{err: pgx.ErrNoRows}
	default:
		return scanErrorRow{err: fmt.Errorf("unexpected query: %s", sql)}
	}
}

type scanSingleInt64Row struct {
	n int64
}

func (r scanSingleInt64Row) Scan(dest ...any) error {
	if len(dest) != 1 {
		return fmt.Errorf("scan dest count=%d want 1", len(dest))
	}
	*dest[0].(*int64) = r.n
	return nil
}

type scanErrorRow struct {
	err error
}

func (r scanErrorRow) Scan(...any) error {
	return r.err
}
