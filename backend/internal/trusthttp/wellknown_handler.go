package trusthttp

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
)

const (
	pubkeySchemaVersion = "huakai.pubkey.v1"
	trustSchemaVersion  = "trust.receipt.v1"
)

type publicKeySigner interface {
	Fingerprint() string
	PublicKey() ed25519.PublicKey
}

type WellKnownDeps struct {
	Signer      publicKeySigner
	Registry    auditledger.PubkeyRegistry
	Revocations Revocations
	Now         func() time.Time
}

type wellKnownPubkeyResponse struct {
	SchemaVersion     string             `json:"schema_version"`
	GeneratedAt       string             `json:"generated_at"`
	NextRotationAfter string             `json:"next_rotation_after"`
	Keys              []wellKnownJWK     `json:"keys"`
	Current           string             `json:"current"`
	Revoked           []wellKnownRevoked `json:"revoked"`
}

type wellKnownJWK struct {
	KTY           string `json:"kty"`
	CRV           string `json:"crv"`
	KID           string `json:"kid"`
	X             string `json:"x"`
	Algorithm     string `json:"alg"`
	Use           string `json:"use"`
	Status        string `json:"status"`
	EffectiveFrom string `json:"effective_from,omitempty"`
	EffectiveTo   string `json:"effective_to,omitempty"`
	RevokedAt     string `json:"revoked_at,omitempty"`
	ReasonClass   string `json:"reason_class,omitempty"`
}

type wellKnownRevoked struct {
	Fingerprint string `json:"fingerprint"`
	RevokedAt   string `json:"revoked_at,omitempty"`
	ReasonClass string `json:"reason_class,omitempty"`
}

func NewWellKnownHandler(d WellKnownDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeTrustJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
			return
		}
		revocations, err := revocationsFromDeps(d.Revocations)
		if err != nil {
			writeTrustJSONError(w, http.StatusServiceUnavailable, "revocation_config_invalid", err.Error())
			return
		}
		resp, err := wellKnownResponse(r, d, revocations)
		if err != nil {
			writeTrustJSONError(w, http.StatusServiceUnavailable, "pubkey_registry_unavailable", err.Error())
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=300, stale-while-revalidate=86400")
		writeTrustJSON(w, http.StatusOK, resp)
	}
}

func wellKnownResponse(r *http.Request, d WellKnownDeps, revocations Revocations) (wellKnownPubkeyResponse, error) {
	now := trustNow(d.Now)
	keys, err := pubkeysFromDeps(r, d)
	if err != nil {
		return wellKnownPubkeyResponse{}, err
	}
	out := wellKnownPubkeyResponse{
		SchemaVersion: pubkeySchemaVersion,
		GeneratedAt:   now.Format(time.RFC3339),
		Keys:          make([]wellKnownJWK, 0, len(keys)),
		Revoked:       revokedResponseList(revocations),
	}
	var currentEffective time.Time
	for _, key := range keys {
		jwk := jwkFromPubkey(key, revocations)
		out.Keys = append(out.Keys, jwk)
		if jwk.Status == "active" && (out.Current == "" || key.EffectiveFrom.After(currentEffective)) {
			out.Current = jwk.KID
			currentEffective = key.EffectiveFrom
		}
	}
	sort.SliceStable(out.Keys, func(i, j int) bool {
		if out.Keys[i].EffectiveFrom != out.Keys[j].EffectiveFrom {
			return out.Keys[i].EffectiveFrom < out.Keys[j].EffectiveFrom
		}
		return out.Keys[i].KID < out.Keys[j].KID
	})
	if !currentEffective.IsZero() {
		out.NextRotationAfter = currentEffective.Add(90 * 24 * time.Hour).UTC().Format(time.RFC3339)
	} else {
		out.NextRotationAfter = now.Add(90 * 24 * time.Hour).UTC().Format(time.RFC3339)
	}
	return out, nil
}

func pubkeysFromDeps(r *http.Request, d WellKnownDeps) ([]*auditledger.Pubkey, error) {
	var keys []*auditledger.Pubkey
	if d.Registry != nil {
		listed, err := auditledger.ListPubkeys(r.Context(), d.Registry)
		if err != nil {
			return nil, err
		}
		keys = append(keys, listed...)
	}
	if len(keys) == 0 && d.Signer != nil {
		key, err := auditledger.PubkeyFromSigner(d.Signer, trustNow(d.Now))
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("no signer public keys configured")
	}
	return keys, nil
}

func jwkFromPubkey(key *auditledger.Pubkey, revocations Revocations) wellKnownJWK {
	if key == nil {
		return wellKnownJWK{}
	}
	kid := string(key.Fingerprint)
	status := key.Status()
	var revokedAt, reason string
	if rev, ok := revocations.Lookup(kid); ok {
		status = "revoked"
		revokedAt = formatOptionalTime(rev.RevokedAt)
		reason = rev.ReasonClass
	}
	out := wellKnownJWK{
		KTY:           "OKP",
		CRV:           "Ed25519",
		KID:           kid,
		X:             base64.RawURLEncoding.EncodeToString(key.PublicKey),
		Algorithm:     "EdDSA",
		Use:           "sig",
		Status:        status,
		EffectiveFrom: formatOptionalTime(key.EffectiveFrom),
		RevokedAt:     revokedAt,
		ReasonClass:   reason,
	}
	if key.EffectiveTo != nil {
		out.EffectiveTo = formatOptionalTime(*key.EffectiveTo)
	}
	return out
}

func revokedResponseList(revocations Revocations) []wellKnownRevoked {
	revoked := revocations.SortedList()
	out := make([]wellKnownRevoked, 0, len(revoked))
	for _, rev := range revoked {
		out = append(out, wellKnownRevoked{
			Fingerprint: rev.Fingerprint,
			RevokedAt:   formatOptionalTime(rev.RevokedAt),
			ReasonClass: rev.ReasonClass,
		})
	}
	return out
}

func formatOptionalTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339)
}

func trustNow(now func() time.Time) time.Time {
	if now != nil {
		return now().UTC()
	}
	return time.Now().UTC()
}

func writeTrustJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeTrustJSONError(w http.ResponseWriter, status int, code, message string) {
	writeTrustJSON(w, status, map[string]string{"error": code, "message": message})
}
