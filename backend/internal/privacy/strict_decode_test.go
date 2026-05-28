package privacy

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestStrictDecodeRejectsTrailingJSONDocumentForBytePayload(t *testing.T) {
	raw := []byte(`{"request_id":"req"} {"prompt":"PROMPT_SENTINEL_leak"}`)

	got, err := SanitizePayload(context.Background(), raw)
	if !errors.Is(err, ErrUnsafePayload) {
		t.Fatalf("err=%v want ErrUnsafePayload", err)
	}
	if bytes.Contains(got, []byte("PROMPT_SENTINEL")) {
		t.Fatalf("sanitized payload leaked trailing document sentinel: %s", got)
	}
}

func TestStrictDecodeAllowsSingleJSONDocumentForBytePayload(t *testing.T) {
	raw := []byte(`{"request_id":"req"}`)

	got, err := SanitizePayload(context.Background(), raw)
	if err != nil {
		t.Fatalf("err=%v want nil", err)
	}
	if !bytes.Contains(got, []byte(`"request_id":"req"`)) {
		t.Fatalf("sanitized payload=%s want request_id", got)
	}
}

// 变异检查：删除尾部 EOF 守卫时，两段 JSON 的测试必须变红。
