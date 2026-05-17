package gatewayhttp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

type auditVerifyLedger interface {
	GetByRequestID(context.Context, string) (auditledger.LedgerEntry, error)
	LatestMerkleRoot(context.Context) ([32]byte, error)
	Size(context.Context) int
}

// AuditVerifyDeps is the narrow dependency interface for user-facing audit
// verification endpoints. Tests and future Postgres wiring can inject any
// implementation with the same read surface.
type AuditVerifyDeps interface {
	AuditLedger() auditVerifyLedger
}

type AuditVerifyStaticDeps struct {
	Ledger auditVerifyLedger
}

func (d AuditVerifyStaticDeps) AuditLedger() auditVerifyLedger { return d.Ledger }

type AuditVerifyRouter interface {
	Get(pattern string, h http.HandlerFunc)
}

func MountAuditVerifyRoutes(r AuditVerifyRouter, d AuditVerifyDeps) {
	r.Get("/v1/audit/verify", NewAuditVerifyHandler(d))
	r.Get("/v1/audit/merkle-tree.json", NewAuditMerkleTreeHandler(d))
}

type AuditVerifyResponse struct {
	LedgerEntry AuditLedgerEntryJSON `json:"ledger_entry"`
	ChainProof  AuditChainProofJSON  `json:"chain_proof"`
}

type AuditLedgerEntryJSON struct {
	LedgerID       string                 `json:"ledger_id"`
	Timestamp      string                 `json:"timestamp"`
	RequestID      string                 `json:"request_id"`
	TenantID       int64                  `json:"-"`
	TenantScopeRef string                 `json:"tenant_scope_ref,omitempty"`
	HopChain       []proto.HopAttestation `json:"hop_chain"`
	ModelChain     *proto.ModelChain      `json:"model_chain,omitempty"`
}

type AuditChainProofJSON struct {
	PrevMerkleRoot    string `json:"prev_merkle_root"`
	MerkleRoot        string `json:"merkle_root"`
	Signature         string `json:"signature"`
	PubkeyFingerprint string `json:"pubkey_fingerprint"`
}

type AuditMerkleTreeResponse struct {
	LatestMerkleRoot string `json:"latest_merkle_root"`
	Size             int    `json:"size"`
}

func NewAuditVerifyHandler(d AuditVerifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAuditJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
			return
		}
		ledger, ok := auditLedgerFromDeps(d)
		if !ok {
			writeAuditJSONError(w, http.StatusServiceUnavailable, "audit_ledger_not_configured", "audit ledger dependency unset")
			return
		}
		requestID := r.URL.Query().Get("request_id")
		if requestID == "" {
			writeAuditJSONError(w, http.StatusBadRequest, "missing_request_id", "request_id query parameter required")
			return
		}
		entry, err := ledger.GetByRequestID(r.Context(), requestID)
		if errors.Is(err, auditledger.ErrLedgerEntryNotFound) {
			writeAuditJSONError(w, http.StatusNotFound, "audit_entry_not_found", "request_id not found")
			return
		}
		if err != nil {
			writeAuditJSONError(w, http.StatusInternalServerError, "audit_ledger_error", err.Error())
			return
		}
		if scope := r.URL.Query().Get("tenant_scope_ref"); scope != "" && scope != auditledger.TenantScopeRef(entry.TenantID) {
			writeAuditJSONError(w, http.StatusNotFound, "audit_entry_not_found", "request_id not found")
			return
		}
		writeAuditJSON(w, http.StatusOK, auditVerifyResponse(entry))
	}
}

func NewAuditMerkleTreeHandler(d AuditVerifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAuditJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
			return
		}
		ledger, ok := auditLedgerFromDeps(d)
		if !ok {
			writeAuditJSONError(w, http.StatusServiceUnavailable, "audit_ledger_not_configured", "audit ledger dependency unset")
			return
		}
		root, err := ledger.LatestMerkleRoot(r.Context())
		if err != nil {
			writeAuditJSONError(w, http.StatusInternalServerError, "audit_ledger_error", err.Error())
			return
		}
		writeAuditJSON(w, http.StatusOK, AuditMerkleTreeResponse{
			LatestMerkleRoot: rootHex(root),
			Size:             ledger.Size(r.Context()),
		})
	}
}

func auditLedgerFromDeps(d AuditVerifyDeps) (auditVerifyLedger, bool) {
	if d == nil {
		return nil, false
	}
	ledger := d.AuditLedger()
	return ledger, ledger != nil
}

func auditVerifyResponse(entry auditledger.LedgerEntry) AuditVerifyResponse {
	scopeRef := entry.TenantScopeRef
	if scopeRef == "" {
		scopeRef = auditledger.TenantScopeRef(entry.TenantID)
	}
	return AuditVerifyResponse{
		LedgerEntry: AuditLedgerEntryJSON{
			LedgerID:       entry.LedgerID,
			Timestamp:      entry.Timestamp,
			RequestID:      entry.RequestID,
			TenantID:       entry.TenantID,
			TenantScopeRef: scopeRef,
			HopChain:       entry.HopChain,
			ModelChain:     entry.ModelChain,
		},
		ChainProof: AuditChainProofJSON{
			PrevMerkleRoot:    rootHex(entry.PrevMerkleRoot),
			MerkleRoot:        rootHex(entry.MerkleRoot),
			Signature:         entry.Signature,
			PubkeyFingerprint: entry.PubkeyFingerprint,
		},
	}
}

func rootHex(root [32]byte) string {
	return hex.EncodeToString(root[:])
}

func writeAuditJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAuditJSONError(w http.ResponseWriter, status int, code, message string) {
	writeAuditJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func ParseAuditRootHex(s string) ([32]byte, error) {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return [32]byte{}, err
	}
	if len(raw) != 32 {
		return [32]byte{}, fmt.Errorf("audit root must be 32 bytes, got %d", len(raw))
	}
	var out [32]byte
	copy(out[:], raw)
	return out, nil
}
