package auditexporthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func TestAuditProofDownload_Attachment(t *testing.T) {
	// Mutation: remove Content-Disposition from the proof download writer; this
	// test fails because the response is no longer a browser download.
	ctx := context.Background()
	ledger, registry := newAuditExportTestLedger(t)
	entry := appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{
		RequestID: "req_proof_download",
		TenantID:  7,
		Timestamp: "2026-06-02T10:00:00Z",
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-06-02T10:00:00Z"}},
	})

	rec := invokeAuditProof(t, Deps{Ledger: ledger, Registry: registry}, "/v1/audit/proof/"+entry.RequestID+".json?tenant_scope_ref="+auditledger.TenantScopeRef(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="audit-proof-req_proof_download.json"` {
		t.Fatalf("Content-Disposition=%q want audit proof attachment", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q want application/json", got)
	}

	verifyRec := httptest.NewRecorder()
	verifyReq := httptest.NewRequest(http.MethodGet, "/v1/audit/verify?request_id="+entry.RequestID+"&tenant_scope_ref="+auditledger.TenantScopeRef(7), nil)
	gatewayhttp.NewAuditVerifyHandler(gatewayhttp.AuditVerifyStaticDeps{Ledger: ledger, Registry: registry})(verifyRec, verifyReq.WithContext(ctx))
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", verifyRec.Code, verifyRec.Body.String())
	}
	got := decodeVerifyResponse(t, rec.Body.Bytes())
	want := decodeVerifyResponse(t, verifyRec.Body.Bytes())
	if got.LedgerEntry.RequestID != want.LedgerEntry.RequestID ||
		got.LedgerEntry.TenantScopeRef != want.LedgerEntry.TenantScopeRef ||
		got.ChainProof.MerkleRoot != want.ChainProof.MerkleRoot ||
		got.ChainProof.Signature != want.ChainProof.Signature ||
		got.ChainProof.PubkeyFingerprint != want.ChainProof.PubkeyFingerprint {
		t.Fatalf("proof download body differs from verify body\ngot=%+v\nwant=%+v", got, want)
	}
}

func TestAuditExport_RangeBundleSelfAttests(t *testing.T) {
	// Mutation: skip auditledger.VerifyChain before writing the bundle, or include
	// a tampered ledger entry; this test fails because the tampered export must
	// return 500 instead of a self-attesting attachment.
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_export_1", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_export_2", TenantID: 7, Timestamp: "2026-06-02T10:05:00Z"})

	rec := invokeAuditExport(t, Deps{Ledger: ledger, Registry: registry}, "/v1/audit/export?tenant_scope_ref="+auditledger.TenantScopeRef(7)+"&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="audit-export.json"` {
		t.Fatalf("Content-Disposition=%q want audit export attachment", got)
	}
	var bundle ExportBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if got := verifyRequestIDs(bundle.Entries); !equalStrings(got, []string{"req_export_1", "req_export_2"}) {
		t.Fatalf("entries=%v want exported range entries", got)
	}
	if len(bundle.Pubkeys) == 0 {
		t.Fatalf("bundle missing pubkey metadata: %+v", bundle)
	}
	if !bundle.SelfAttestation.ChainValid {
		t.Fatalf("self attestation not marked valid: %+v", bundle.SelfAttestation)
	}
	if err := auditledger.VerifyChain(bundleLedgerEntries(t, bundle.ChainEntries)); err != nil {
		t.Fatalf("bundle chain entries do not self-attest: %v", err)
	}

	tampered := &tamperingRangeLedger{Ledger: ledger}
	badRec := invokeAuditExport(t, Deps{Ledger: tampered, Registry: registry}, "/v1/audit/export?tenant_scope_ref="+auditledger.TenantScopeRef(7)+"&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z")
	if badRec.Code != http.StatusInternalServerError {
		t.Fatalf("tampered status=%d want 500 body=%s", badRec.Code, badRec.Body.String())
	}
}

func TestAuditExport_TenantScoped(t *testing.T) {
	// Mutation: drop tenant_scope_ref filtering in the range reader; this test
	// fails because tenant B's request appears in tenant A's export bundle.
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_tenant_a_1", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_tenant_a_2", TenantID: 7, Timestamp: "2026-06-02T10:05:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_tenant_b", TenantID: 8, Timestamp: "2026-06-02T10:10:00Z"})

	rec := invokeAuditExport(t, Deps{Ledger: ledger, Registry: registry}, "/v1/audit/export?tenant_scope_ref="+auditledger.TenantScopeRef(7)+"&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var bundle ExportBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if got := verifyRequestIDs(bundle.Entries); !equalStrings(got, []string{"req_tenant_a_1", "req_tenant_a_2"}) {
		t.Fatalf("tenant scoped entries=%v want only tenant A", got)
	}
	for _, entry := range bundle.ChainEntries {
		if entry.LedgerEntry.TenantScopeRef != auditledger.TenantScopeRef(7) {
			t.Fatalf("chain proof leaked non-tenant-A entry: %+v", entry.LedgerEntry)
		}
	}
}

func TestAuditExport_RangeValidation(t *testing.T) {
	// Mutation: remove from>to or 366-day range checks; these requests return
	// 200/503 instead of the required public 400 validation errors.
	ledger, registry := newAuditExportTestLedger(t)
	cases := []string{
		"/v1/audit/export?tenant_scope_ref=" + auditledger.TenantScopeRef(7) + "&from=2026-06-03T00:00:00Z&to=2026-06-02T00:00:00Z",
		"/v1/audit/export?tenant_scope_ref=" + auditledger.TenantScopeRef(7) + "&from=2025-01-01T00:00:00Z&to=2026-06-02T00:00:00Z",
	}
	for _, target := range cases {
		rec := invokeAuditExport(t, Deps{Ledger: ledger, Registry: registry}, target)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d want 400 body=%s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestAuditExport_RequestIDsBundleFiltersAndAttests(t *testing.T) {
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_id_a_1", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_id_a_2", TenantID: 7, Timestamp: "2026-06-02T10:05:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_id_b", TenantID: 8, Timestamp: "2026-06-02T10:10:00Z"})

	rec := invokeAuditExport(t, Deps{Ledger: ledger, Registry: registry}, "/v1/audit/export?tenant_scope_ref="+auditledger.TenantScopeRef(7)+"&request_ids=req_id_b,req_id_a_2,req_id_a_1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var bundle ExportBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if got := verifyRequestIDs(bundle.Entries); !equalStrings(got, []string{"req_id_a_1", "req_id_a_2"}) {
		t.Fatalf("request id entries=%v want tenant A matches in chain order", got)
	}
	if err := auditledger.VerifyChain(bundleLedgerEntries(t, bundle.ChainEntries)); err != nil {
		t.Fatalf("request id chain entries do not self-attest: %v", err)
	}
}

func newAuditExportTestLedger(t testing.TB) (*auditledger.MemoryLedger, auditledger.PubkeyRegistry) {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	key, err := auditledger.PubkeyFromSigner(signer, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("pubkey from signer: %v", err)
	}
	return ledger, auditledger.NewMemoryPubkeyRegistry(key)
}

func appendAuditExportEntry(t testing.TB, ledger *auditledger.MemoryLedger, entry auditledger.LedgerEntry) auditledger.LedgerEntry {
	t.Helper()
	prepared, err := auditledger.PrepareEntry(context.Background(), entry)
	if err != nil {
		t.Fatalf("PrepareEntry: %v", err)
	}
	out, err := ledger.Append(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	return out
}

func invokeAuditProof(t testing.TB, deps Deps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/v1/audit", func(r chi.Router) {
		MountRoutes(r, deps)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	r.ServeHTTP(rec, req)
	return rec
}

func invokeAuditExport(t testing.TB, deps Deps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/v1/audit", func(r chi.Router) {
		MountRoutes(r, deps)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	r.ServeHTTP(rec, req)
	return rec
}

func decodeVerifyResponse(t testing.TB, body []byte) gatewayhttp.AuditVerifyResponse {
	t.Helper()
	var out gatewayhttp.AuditVerifyResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode verify response: %v", err)
	}
	return out
}

func verifyRequestIDs(entries []gatewayhttp.AuditVerifyResponse) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.LedgerEntry.RequestID)
	}
	return out
}

func bundleLedgerEntries(t testing.TB, entries []gatewayhttp.AuditVerifyResponse) []auditledger.LedgerEntry {
	t.Helper()
	out := make([]auditledger.LedgerEntry, 0, len(entries))
	for _, entry := range entries {
		prev, err := gatewayhttp.ParseAuditRootHex(entry.ChainProof.PrevMerkleRoot)
		if err != nil {
			t.Fatalf("parse prev root: %v", err)
		}
		root, err := gatewayhttp.ParseAuditRootHex(entry.ChainProof.MerkleRoot)
		if err != nil {
			t.Fatalf("parse root: %v", err)
		}
		out = append(out, auditledger.LedgerEntry{
			LedgerID:          entry.LedgerEntry.LedgerID,
			Timestamp:         entry.LedgerEntry.Timestamp,
			RequestID:         entry.LedgerEntry.RequestID,
			TenantScopeRef:    entry.LedgerEntry.TenantScopeRef,
			HopChain:          entry.LedgerEntry.HopChain,
			ModelChain:        entry.LedgerEntry.ModelChain,
			PrevMerkleRoot:    prev,
			MerkleRoot:        root,
			Signature:         entry.ChainProof.Signature,
			PubkeyFingerprint: entry.ChainProof.PubkeyFingerprint,
		})
	}
	return out
}

type tamperingRangeLedger struct {
	Ledger
}

func (l *tamperingRangeLedger) ListByRange(ctx context.Context, tenantScopeRef string, from, to time.Time, limit int) ([]auditledger.LedgerEntry, error) {
	rows, err := l.Ledger.ListByRange(ctx, tenantScopeRef, from, to, limit)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	rows[0].LedgerID = rows[0].LedgerID + "-tampered"
	return rows, nil
}

func (l *tamperingRangeLedger) ListByRequestIDs(ctx context.Context, tenantScopeRef string, requestIDs []string, limit int) ([]auditledger.LedgerEntry, error) {
	rows, err := l.Ledger.ListByRequestIDs(ctx, tenantScopeRef, requestIDs, limit)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	rows[0].LedgerID = rows[0].LedgerID + "-tampered"
	return rows, nil
}

func ledgerRequestIDs(entries []auditledger.LedgerEntry) []string {
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
