package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func TestSafePayloadLogSummaryRedactsJSONValuesAndKeepsCorrelation(t *testing.T) {
	const marker = "SENSITIVE_CONTENT_MARKER"
	payload := []byte(`{"message":"` + marker + ` user prompt","content":"` + marker + `","status":429,"retryable":true}`)

	got := SafePayloadLogSummary(payload)

	if strings.Contains(got, marker) || strings.Contains(got, "user prompt") {
		t.Fatalf("summary leaked raw JSON value: %s", got)
	}
	wantSnippet := `{"field_redacted_1":"[REDACTED]","message":"[REDACTED]","retryable":"[REDACTED_BOOL]","status":"[REDACTED_NUMBER]"}`
	for _, want := range []string{
		fmt.Sprintf("payload_bytes=%d", len(payload)),
		"payload_summary_sha256_prefix=" + hashPrefix([]byte(wantSnippet)),
		"payload_snippet=",
		"[REDACTED]",
		"[REDACTED_NUMBER]",
		"[REDACTED_BOOL]",
		"message",
		"field_redacted_1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "payload_sha256_prefix="+hashPrefix(payload)) || strings.Contains(got, "content") {
		t.Fatalf("summary logged raw payload hash or unsafe key: %s", got)
	}
}

func TestSafePayloadLogSummaryRedactsNonJSONPayload(t *testing.T) {
	const marker = "SENSITIVE_CONTENT_MARKER"
	payload := []byte("upstream error body with " + marker)

	got := SafePayloadLogSummary(payload)

	if strings.Contains(got, marker) || strings.Contains(got, "upstream error body") {
		t.Fatalf("summary leaked raw non-JSON payload: %s", got)
	}
	for _, want := range []string{
		fmt.Sprintf("payload_bytes=%d", len(payload)),
		"payload_summary_sha256_prefix=" + hashPrefix([]byte("[non_json_payload_redacted]")),
		`payload_snippet="[non_json_payload_redacted]"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q: %s", want, got)
		}
	}
}

func TestSafePayloadLogSummaryRedactsJSONKeys(t *testing.T) {
	const marker = "SENSITIVE_KEY_MARKER"
	payload := []byte(`{"` + marker + `":"hidden","message":"safe structural key"}`)

	got := SafePayloadLogSummary(payload)

	if strings.Contains(got, marker) || strings.Contains(got, "hidden") || strings.Contains(got, "safe structural key") {
		t.Fatalf("summary leaked raw JSON key or value: %s", got)
	}
	for _, want := range []string{
		"field_redacted_1",
		"message",
		"[REDACTED]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, hashPrefix([]byte(marker))) {
		t.Fatalf("summary logged hash of unsafe JSON key: %s", got)
	}
}

func TestSafePayloadLogSummaryBoundsLargeJSONInspection(t *testing.T) {
	const marker = "SENSITIVE_CONTENT_MARKER"
	payload := []byte(`{"message":"` + marker + strings.Repeat("x", payloadLogInspectMaxBytes) + `"}`)

	got := SafePayloadLogSummary(payload)

	if strings.Contains(got, marker) {
		t.Fatalf("summary leaked marker from oversized payload: %s", got)
	}
	for _, want := range []string{
		fmt.Sprintf("payload_bytes=%d", len(payload)),
		"payload_summary_sha256_prefix=" + hashPrefix([]byte("[payload_too_large_redacted]")),
		`payload_snippet="[payload_too_large_redacted]"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q: %s", want, got)
		}
	}
}

func TestSafePayloadLogAttrsUsesBoundedFields(t *testing.T) {
	const marker = "RAWPROMPT_SECRET_MARKER"
	payload := []byte(`{"message":"` + marker + `","detail":"` + marker + `","details":"` + marker + `","error":"` + marker + `","reason":"` + marker + `","status":429,"type":"rate_limit","retryable":true,"unknown1":"` + marker + `","unknown2":"` + marker + `","api_key":"sk-` + marker + `"}`)

	attrs := SafePayloadLogAttrs(payload)

	if attrs["payload_bytes"] != len(payload) {
		t.Fatalf("payload_bytes=%v want %d", attrs["payload_bytes"], len(payload))
	}
	for _, key := range []string{"payload_summary_sha256_prefix", "payload_snippet"} {
		if _, ok := attrs[key]; !ok {
			t.Fatalf("attrs missing %q: %+v", key, attrs)
		}
	}
	for key, value := range attrs {
		text := fmt.Sprint(value)
		if len(text) > 256 {
			t.Fatalf("%s length=%d exceeds privacy string cap: %q", key, len(text), text)
		}
		if strings.Contains(text, marker) || strings.Contains(text, "sk-") {
			t.Fatalf("%s leaked sensitive text: %q", key, text)
		}
	}
}

func hashPrefix(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:8])
}
