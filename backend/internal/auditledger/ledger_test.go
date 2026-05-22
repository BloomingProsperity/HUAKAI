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
	out, err := l.Append(ctx, in)
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
	out, err := l.Append(ctx, entry)
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

	e1, _ := l.Append(ctx, LedgerEntry{LedgerID: "1", RequestID: "r1"})
	e2, _ := l.Append(ctx, LedgerEntry{LedgerID: "2", RequestID: "r2"})

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

	_, _ = l.Append(ctx, LedgerEntry{LedgerID: "1", RequestID: "rA"})
	_, _ = l.Append(ctx, LedgerEntry{LedgerID: "2", RequestID: "rB"})

	got, err := l.GetByRequestID(ctx, "rB")
	if err != nil {
		t.Fatalf("Get rB: %v", err)
	}
	if got.LedgerID != "2" {
		t.Errorf("got LedgerID=%q want 2", got.LedgerID)
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

	entry, err := l.Append(ctx, LedgerEntry{RequestID: "req_scope_a", TenantID: 7})
	if err != nil {
		t.Fatalf("append A: %v", err)
	}
	if _, err := l.Append(ctx, LedgerEntry{RequestID: "req_scope_b", TenantID: 8}); err != nil {
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

func TestMemoryLedger_SnapshotIsDeepCopy(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	_, _ = l.Append(ctx, LedgerEntry{LedgerID: "1", RequestID: "r1"})
	snap1 := l.Snapshot()
	_, _ = l.Append(ctx, LedgerEntry{LedgerID: "2", RequestID: "r2"})
	snap2 := l.Snapshot()

	if len(snap1) != 1 || len(snap2) != 2 {
		t.Errorf("snapshots don't reflect appends: %d / %d", len(snap1), len(snap2))
	}
	// 篡改 snap1 不应影响内部 chain。
	snap1[0].LedgerID = "polluted"
	got, _ := l.GetByRequestID(ctx, "r1")
	if got.LedgerID != "1" {
		t.Error("snapshot mutation polluted internal chain")
	}
}

func TestMemoryLedger_VerifyChainHappyPath(t *testing.T) {
	signer, _ := sign.GenerateKey()
	l, _ := NewMemoryLedger(signer)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, _ = l.Append(ctx, LedgerEntry{LedgerID: itoa(i), RequestID: "r" + itoa(i)})
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

	entry, _ := l.Append(ctx, LedgerEntry{LedgerID: "1", RequestID: "r1"})

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

	out, err := l.Append(ctx, LedgerEntry{
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
	out, err := AppendInTransaction(context.Background(), db, signer, LedgerEntry{
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
	out, err := AppendInTransaction(context.Background(), db, signer, LedgerEntry{
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

func (r fieldLevelLedgerPayloadRedactor) SanitizePayload(_ context.Context, payload any) ([]byte, error) {
	ledgerPayload, ok := payload.(redactedLedgerPayload)
	if !ok {
		return nil, fmt.Errorf("unexpected payload type %T", payload)
	}
	for i := range ledgerPayload.HopChain {
		if len(ledgerPayload.HopChain[i].Detail) == 0 {
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal(ledgerPayload.HopChain[i].Detail, &detail); err != nil {
			return nil, err
		}
		delete(detail, r.blockedField)
		rawDetail, err := json.Marshal(detail)
		if err != nil {
			return nil, err
		}
		ledgerPayload.HopChain[i].Detail = rawDetail
	}
	raw, err := json.Marshal(ledgerPayload)
	if err != nil {
		return nil, err
	}
	return raw, privacy.ErrUnsafePayload
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
}

func (db *appendTxTestDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "INSERT INTO audit_ledger_entries") {
		db.insertHopJSON = append([]byte(nil), args[4].([]byte)...)
		if modelJSON, ok := args[5].([]byte); ok {
			db.insertModelJSON = append([]byte(nil), modelJSON...)
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
