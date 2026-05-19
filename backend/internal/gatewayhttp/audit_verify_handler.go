package gatewayhttp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

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
	Ledger   auditVerifyLedger
	Registry auditledger.PubkeyRegistry
}

func (d AuditVerifyStaticDeps) AuditLedger() auditVerifyLedger { return d.Ledger }
func (d AuditVerifyStaticDeps) AuditPubkeyRegistry() auditledger.PubkeyRegistry {
	return d.Registry
}

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
	SignatureValid    *bool  `json:"signature_valid,omitempty"`
	KeyStatus         string `json:"key_status,omitempty"`
	Reason            string `json:"reason,omitempty"`
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
		writeAuditJSON(w, http.StatusOK, auditVerifyResponseWithRegistry(r.Context(), entry, auditPubkeyRegistryFromDeps(d)))
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

func auditPubkeyRegistryFromDeps(d AuditVerifyDeps) auditledger.PubkeyRegistry {
	if d == nil {
		return nil
	}
	provider, ok := d.(interface {
		AuditPubkeyRegistry() auditledger.PubkeyRegistry
	})
	if !ok {
		return nil
	}
	return provider.AuditPubkeyRegistry()
}

func auditVerifyResponseWithRegistry(ctx context.Context, entry auditledger.LedgerEntry, registry auditledger.PubkeyRegistry) AuditVerifyResponse {
	resp := auditVerifyResponse(entry)
	if registry == nil {
		return resp
	}
	verification, err := verifyAuditLedgerEntrySignature(ctx, registry, entry)
	if err != nil {
		valid := false
		resp.ChainProof.SignatureValid = &valid
		resp.ChainProof.KeyStatus = "unknown"
		resp.ChainProof.Reason = "signature_verify_error"
		return resp
	}
	resp.ChainProof.SignatureValid = &verification.Valid
	resp.ChainProof.KeyStatus = verification.KeyStatus
	resp.ChainProof.Reason = verification.Reason
	return resp
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

func verifyAuditLedgerEntrySignature(ctx context.Context, registry auditledger.PubkeyRegistry, entry auditledger.LedgerEntry) (auditledger.SignatureVerification, error) {
	entryHash, err := auditledger.EntryHash(&entry)
	if err != nil {
		return auditledger.SignatureVerification{}, err
	}
	sig, err := base64.StdEncoding.DecodeString(entry.Signature)
	if err != nil {
		return auditledger.SignatureVerification{Valid: false, KeyStatus: "unknown", Reason: "invalid_signature"}, nil
	}
	verification, err := auditledger.VerifySignatureWithRegistry(ctx, registry, entryHash[:], sig, []byte(entry.PubkeyFingerprint))
	if err != nil || !verification.Valid {
		return verification, err
	}
	key, err := auditledger.LookupPubkey(ctx, registry, []byte(entry.PubkeyFingerprint))
	if err != nil {
		return auditledger.SignatureVerification{}, err
	}
	entryTime, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		return auditledger.SignatureVerification{Valid: false, KeyStatus: verification.KeyStatus, Reason: "invalid_entry_timestamp"}, nil
	}
	if signatureOutsideKeyWindow(entryTime, key) {
		return auditledger.SignatureVerification{Valid: false, KeyStatus: key.Status(), Reason: "signature_outside_key_window"}, nil
	}
	return verification, nil
}

func signatureOutsideKeyWindow(ts time.Time, key *auditledger.Pubkey) bool {
	if key == nil {
		return true
	}
	ts = ts.UTC()
	if !key.EffectiveFrom.IsZero() && ts.Before(key.EffectiveFrom.UTC()) {
		return true
	}
	if key.EffectiveTo != nil && ts.After(key.EffectiveTo.UTC()) {
		return true
	}
	return false
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
