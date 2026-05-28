package trusthttp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseRevocationsJSONNormalizesOverlay(t *testing.T) {
	raw := []byte(`{"revoked":[{"fingerprint":"ABCDEF1234567890","revoked_at":"2026-05-27T12:00:00Z","reason_class":"test"}]}`)

	got, err := ParseRevocationsJSON(raw)
	if err != nil {
		t.Fatalf("ParseRevocationsJSON: %v", err)
	}
	rev, ok := got["abcdef1234567890"]
	if !ok {
		t.Fatalf("normalized fingerprint missing: %+v", got)
	}
	if rev.RevokedAt.Format(time.RFC3339) != "2026-05-27T12:00:00Z" || rev.ReasonClass != "test" {
		t.Fatalf("revocation mismatch: %+v", rev)
	}
}

func TestLoadRevocationsFromEnvPrefersFileWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "revoked.json")
	if err := os.WriteFile(path, []byte(`[{"fingerprint":"abcdef1234567890","revoked_at":"2026-05-27T12:00:00Z","reason_class":"key_compromise"}]`), 0o600); err != nil {
		t.Fatalf("write revocation file: %v", err)
	}
	t.Setenv(TrustRevocationsJSONEnv, `{"revoked":[{"fingerprint":"1111111111111111","reason_class":"other"}]}`)
	t.Setenv(TrustRevocationsFileEnv, path)

	got, err := LoadRevocationsFromEnv()
	if err != nil {
		t.Fatalf("LoadRevocationsFromEnv: %v", err)
	}
	if _, ok := got["1111111111111111"]; ok {
		t.Fatalf("file config must replace inline env config: %+v", got)
	}
	if got["abcdef1234567890"].ReasonClass != "key_compromise" {
		t.Fatalf("file revocation missing: %+v", got)
	}
}

func TestLoadRevocationsFromEnvRejectsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "revoked-too-large.json")
	raw := strings.Repeat(" ", 2*1024*1024) + "[]"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write oversized revocation file: %v", err)
	}
	t.Setenv(TrustRevocationsFileEnv, path)

	_, err := LoadRevocationsFromEnv()
	if err == nil {
		t.Fatal("LoadRevocationsFromEnv accepted oversized revocation file")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "size") && !strings.Contains(msg, "too large") {
		t.Fatalf("oversized file error=%q", err.Error())
	}
}

func TestRevocationsMarshalToStableArray(t *testing.T) {
	revocations := Revocations{
		"bbbbbbbbbbbbbbbb": {Fingerprint: "bbbbbbbbbbbbbbbb", RevokedAt: fixedTrustHTTPNow(), ReasonClass: "test"},
		"aaaaaaaaaaaaaaaa": {Fingerprint: "aaaaaaaaaaaaaaaa", RevokedAt: fixedTrustHTTPNow(), ReasonClass: "other"},
	}

	raw, err := json.Marshal(revocations.SortedList())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw[:len(`[{"fingerprint":"aaaaaaaaaaaaaaaa"`)]) != `[{"fingerprint":"aaaaaaaaaaaaaaaa"` {
		t.Fatalf("revocations not sorted by fingerprint: %s", raw)
	}
}
