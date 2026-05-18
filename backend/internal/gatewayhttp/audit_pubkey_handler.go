package gatewayhttp

import (
	"encoding/base64"
	"net/http"
)

type AuditPubkeyDeps struct {
	Signer CostReceiptSigner
}

type AuditPubkeyResponse struct {
	Algorithm         string `json:"algorithm"`
	Fingerprint       string `json:"fingerprint"`
	PubkeyFingerprint string `json:"pubkey_fingerprint"`
	PublicKeyBase64   string `json:"public_key_base64"`
}

func NewAuditPubkeyHandler(d AuditPubkeyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Signer == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "audit signer dependency unset")
			return
		}
		fp := d.Signer.Fingerprint()
		writeAuditJSON(w, http.StatusOK, AuditPubkeyResponse{
			Algorithm:         "ed25519",
			Fingerprint:       fp,
			PubkeyFingerprint: fp,
			PublicKeyBase64:   base64.StdEncoding.EncodeToString(d.Signer.PublicKey()),
		})
	}
}
