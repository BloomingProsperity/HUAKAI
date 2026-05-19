package gatewayhttp

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
)

type AuditPubkeyDeps struct {
	Signer   CostReceiptSigner
	Registry auditledger.PubkeyRegistry
}

type AuditPubkeyResponse struct {
	Algorithm         string `json:"algorithm"`
	Fingerprint       string `json:"fingerprint"`
	PubkeyFingerprint string `json:"pubkey_fingerprint"`
	PublicKeyBase64   string `json:"public_key_base64"`
	KeyStatus         string `json:"key_status,omitempty"`
	EffectiveFrom     string `json:"effective_from,omitempty"`
	EffectiveTo       string `json:"effective_to,omitempty"`
}

type AuditPubkeysResponse struct {
	Keys []AuditPubkeyResponse `json:"keys"`
}

func MountAuditPubkeyRoutes(r interface {
	Get(pattern string, h http.HandlerFunc)
}, d AuditPubkeyDeps) {
	r.Get("/v1/audit/pubkey", NewAuditPubkeyHandler(d))
	r.Get("/v1/audit/pubkey/{fingerprint_hex}", NewAuditPubkeyByFingerprintHandler(d))
	r.Get("/v1/audit/pubkeys", NewAuditPubkeysHandler(d))
}

func NewAuditPubkeyHandler(d AuditPubkeyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Signer == nil {
			writeAuditJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "audit signer dependency unset")
			return
		}
		fp := d.Signer.Fingerprint()
		if d.Registry != nil {
			key, err := auditledger.LookupPubkey(r.Context(), d.Registry, []byte(fp))
			if err == nil {
				writeAuditJSON(w, http.StatusOK, auditPubkeyResponseFromRegistry(key))
				return
			}
			if !errors.Is(err, auditledger.ErrPubkeyNotFound) && !errors.Is(err, auditledger.ErrLedgerPubkeyNotFound) {
				writeAuditJSONError(w, http.StatusServiceUnavailable, "audit_pubkey_registry_error", err.Error())
				return
			}
		}
		writeAuditJSON(w, http.StatusOK, activeAuditPubkeyResponse(d.Signer))
	}
}

func NewAuditPubkeyByFingerprintHandler(d AuditPubkeyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(chi.URLParam(r, "fingerprint_hex"))
		fp, err := auditledger.NormalizePubkeyFingerprint([]byte(raw))
		if err != nil {
			writeAuditJSONError(w, http.StatusBadRequest, "audit_pubkey_fingerprint_invalid", "fingerprint_hex must be 16 lowercase hex characters")
			return
		}
		if d.Registry != nil {
			key, err := auditledger.LookupPubkey(r.Context(), d.Registry, fp)
			if errors.Is(err, auditledger.ErrPubkeyNotFound) || errors.Is(err, auditledger.ErrLedgerPubkeyNotFound) {
				writeAuditJSONError(w, http.StatusNotFound, "audit_pubkey_not_found", "audit signer public key not found")
				return
			}
			if err != nil {
				writeAuditJSONError(w, http.StatusServiceUnavailable, "audit_pubkey_registry_error", err.Error())
				return
			}
			writeAuditJSON(w, http.StatusOK, auditPubkeyResponseFromRegistry(key))
			return
		}
		if d.Signer != nil && string(fp) == d.Signer.Fingerprint() {
			writeAuditJSON(w, http.StatusOK, activeAuditPubkeyResponse(d.Signer))
			return
		}
		writeAuditJSONError(w, http.StatusNotFound, "audit_pubkey_not_found", "audit signer public key not found")
	}
}

func NewAuditPubkeysHandler(d AuditPubkeyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var out []AuditPubkeyResponse
		if d.Registry != nil {
			keys, err := auditledger.ListPubkeys(r.Context(), d.Registry)
			if err != nil {
				writeAuditJSONError(w, http.StatusServiceUnavailable, "audit_pubkey_registry_error", err.Error())
				return
			}
			for _, key := range keys {
				out = append(out, auditPubkeyResponseFromRegistry(key))
			}
		}
		if len(out) == 0 && d.Signer != nil {
			out = append(out, activeAuditPubkeyResponse(d.Signer))
		}
		if len(out) == 0 {
			writeAuditJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "audit signer registry dependency unset")
			return
		}
		writeAuditJSON(w, http.StatusOK, AuditPubkeysResponse{Keys: out})
	}
}

func auditPubkeyResponseFromRegistry(key *auditledger.Pubkey) AuditPubkeyResponse {
	if key == nil {
		return AuditPubkeyResponse{}
	}
	resp := AuditPubkeyResponse{
		Algorithm:         key.Algorithm,
		Fingerprint:       string(key.Fingerprint),
		PubkeyFingerprint: string(key.Fingerprint),
		PublicKeyBase64:   base64.StdEncoding.EncodeToString(key.PublicKey),
		KeyStatus:         key.Status(),
	}
	if !key.EffectiveFrom.IsZero() {
		resp.EffectiveFrom = key.EffectiveFrom.UTC().Format(time.RFC3339)
	}
	if key.EffectiveTo != nil {
		resp.EffectiveTo = key.EffectiveTo.UTC().Format(time.RFC3339)
	}
	return resp
}

func activeAuditPubkeyResponse(signer CostReceiptSigner) AuditPubkeyResponse {
	fp := signer.Fingerprint()
	return AuditPubkeyResponse{
		Algorithm:         auditledger.AuditSignerAlgorithmEd25519,
		Fingerprint:       fp,
		PubkeyFingerprint: fp,
		PublicKeyBase64:   base64.StdEncoding.EncodeToString(signer.PublicKey()),
		KeyStatus:         "active",
	}
}
