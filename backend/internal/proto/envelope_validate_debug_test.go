//go:build debug

package proto

import (
	"errors"
	"testing"
)

// TestValidateEnvelopeDebug_DebugBuild 验证 -tags debug 编译时
// ValidateEnvelopeDebug 转发到完整 ValidateEnvelope，能拿到校验错误/等
// 完整诊断信息。
//
// 用法：go test -tags debug ./internal/proto/
func TestValidateEnvelopeDebug_DebugBuild(t *testing.T) {
	if err := ValidateEnvelopeDebug(nil); err == nil {
		t.Fatalf("debug build: expected error for nil envelope")
	} else {
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *ValidationError, got %T", err)
		}
		if ve.Inv != "INV-0" {
			t.Fatalf("expected INV-0, got %q", ve.Inv)
		}
	}

	// 仅 Version 字段对的 envelope 在 debug 下应被 ValidateEnvelope 拒绝
	// （RequestMeta 必填字段缺失），证明转发到了完整校验。
	env := &HCSFEnvelope{Version: HCSFVersion}
	if err := ValidateEnvelopeDebug(env); err == nil {
		t.Fatalf("debug build: expected ValidateEnvelope to reject empty RequestMeta")
	}

	// NewEmptyEnvelope 的 RequestMeta 仍是零值（RequestID / Model 等空），
	// 完整 ValidateEnvelope 也应拒绝；这正是为什么我们要 build-tag 隔离。
	if err := ValidateEnvelopeDebug(NewEmptyEnvelope()); err == nil {
		t.Fatalf("debug build: NewEmptyEnvelope has empty RequestMeta, must fail INV-5")
	}
}
