package trusthttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
)

const (
	TrustRevocationsJSONEnv = "HUAKAI_TRUST_REVOKED_KEYS_JSON"
	TrustRevocationsFileEnv = "HUAKAI_TRUST_REVOKED_KEYS_FILE"
	maxRevocationsFileBytes = 1 << 20
)

type Revocation struct {
	Fingerprint string    `json:"fingerprint"`
	RevokedAt   time.Time `json:"revoked_at,omitempty"`
	ReasonClass string    `json:"reason_class,omitempty"`
}

type Revocations map[string]Revocation

func LoadRevocationsFromEnv() (Revocations, error) {
	if path := strings.TrimSpace(os.Getenv(TrustRevocationsFileEnv)); path != "" {
		raw, err := readRevocationsFile(path)
		if err != nil {
			return nil, err
		}
		return ParseRevocationsJSON(raw)
	}
	raw := strings.TrimSpace(os.Getenv(TrustRevocationsJSONEnv))
	if raw == "" {
		return Revocations{}, nil
	}
	return ParseRevocationsJSON([]byte(raw))
}

func readRevocationsFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxRevocationsFileBytes {
		return nil, fmt.Errorf("trusthttp: revocation file size %d exceeds %d byte limit", info.Size(), maxRevocationsFileBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxRevocationsFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxRevocationsFileBytes {
		return nil, fmt.Errorf("trusthttp: revocation file too large: exceeds %d byte limit", maxRevocationsFileBytes)
	}
	return raw, nil
}

func ParseRevocationsJSON(raw []byte) (Revocations, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return Revocations{}, nil
	}
	var wrapped struct {
		Revoked []revocationJSON `json:"revoked"`
	}
	var rows []revocationJSON
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, err
		}
	} else {
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return nil, err
		}
		rows = wrapped.Revoked
	}
	out := Revocations{}
	for _, row := range rows {
		rev, err := row.revocation()
		if err != nil {
			return nil, err
		}
		out[rev.Fingerprint] = rev
	}
	return out, nil
}

type revocationJSON struct {
	Fingerprint string `json:"fingerprint"`
	RevokedAt   string `json:"revoked_at"`
	ReasonClass string `json:"reason_class"`
}

func (r revocationJSON) revocation() (Revocation, error) {
	fp, err := normalizeFingerprintString(r.Fingerprint)
	if err != nil {
		return Revocation{}, err
	}
	var revokedAt time.Time
	if strings.TrimSpace(r.RevokedAt) != "" {
		revokedAt, err = time.Parse(time.RFC3339, strings.TrimSpace(r.RevokedAt))
		if err != nil {
			return Revocation{}, err
		}
	}
	reason := strings.TrimSpace(r.ReasonClass)
	if reason == "" {
		reason = "other"
	}
	return Revocation{Fingerprint: fp, RevokedAt: revokedAt.UTC(), ReasonClass: reason}, nil
}

func (r Revocations) Lookup(fingerprint string) (Revocation, bool) {
	fp, err := normalizeFingerprintString(fingerprint)
	if err != nil {
		return Revocation{}, false
	}
	rev, ok := r[fp]
	return rev, ok
}

func (r Revocations) SortedList() []Revocation {
	out := make([]Revocation, 0, len(r))
	for fp, rev := range r {
		if strings.TrimSpace(rev.Fingerprint) == "" {
			rev.Fingerprint = fp
		}
		out = append(out, rev)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out
}

func revocationsFromDeps(revocations Revocations) (Revocations, error) {
	if revocations != nil {
		return revocations, nil
	}
	return LoadRevocationsFromEnv()
}

func normalizeFingerprintString(fingerprint string) (string, error) {
	fp, err := auditledger.NormalizePubkeyFingerprint([]byte(fingerprint))
	if err != nil {
		return "", err
	}
	if len(fp) == 0 {
		return "", errors.New("trusthttp: fingerprint required")
	}
	return string(fp), nil
}
