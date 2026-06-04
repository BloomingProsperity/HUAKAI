package proto

import (
	"errors"
	"testing"
)

// TestValidateEnvelopeVersionGuard_Nil 验证 env==nil 必拒。
func TestValidateEnvelopeVersionGuard_Nil(t *testing.T) {
	err := ValidateEnvelopeVersionGuard(nil)
	if err == nil {
		t.Fatalf("expected error for nil envelope, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if ve.Inv != "INV-0" {
		t.Fatalf("expected INV-0, got %q", ve.Inv)
	}
}

// TestValidateEnvelopeVersionGuard_BadVersion 验证 Version != HCSFVersion 必拒。
func TestValidateEnvelopeVersionGuard_BadVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string
	}{
		{"empty", ""},
		{"old03", "0.3"},
		{"future05", "0.5"},
		{"junk", "garbage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := &HCSFEnvelope{Version: tc.version}
			err := ValidateEnvelopeVersionGuard(env)
			if err == nil {
				t.Fatalf("expected error for version=%q, got nil", tc.version)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
			if ve.Inv != "INV-4" {
				t.Fatalf("expected INV-4, got %q", ve.Inv)
			}
		})
	}
}

// TestValidateEnvelopeVersionGuard_OK 验证版本正确时通过；
// 即使其它 INV 字段（RequestMeta / CapabilityGraph 等）都是零值也接受——
// 这是与 ValidateEnvelope 的关键区分点。
func TestValidateEnvelopeVersionGuard_OK(t *testing.T) {
	env := &HCSFEnvelope{Version: HCSFVersion}
	if err := ValidateEnvelopeVersionGuard(env); err != nil {
		t.Fatalf("expected nil for HCSFVersion-only envelope, got %v", err)
	}
}

// TestValidateEnvelopeVersionGuard_NewEmptyEnvelope 验证 NewEmptyEnvelope 起点
// 也能通过 VersionGuard。
func TestValidateEnvelopeVersionGuard_NewEmptyEnvelope(t *testing.T) {
	if err := ValidateEnvelopeVersionGuard(NewEmptyEnvelope()); err != nil {
		t.Fatalf("NewEmptyEnvelope must pass version guard, got %v", err)
	}
}

// TestValidateEnvelopeDebug_Release 在 release build (默认) 下应该是 noop。
// Build tag 'debug' 不开时本测试有效；debug build 由 envelope_validate_debug_test.go 覆盖。
func TestValidateEnvelopeDebug_NoopOrFull(t *testing.T) {
	// 不论 release 还是 debug，nil envelope 行为不同：release 返回 nil，
	// debug 转发 ValidateEnvelope 会拿到校验错误；这里只断言不 panic 且
	// 函数可调用——具体 release vs debug 分支由各自 build tag 测试细化。
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ValidateEnvelopeDebug must not panic, got %v", r)
		}
	}()
	_ = ValidateEnvelopeDebug(nil)
	_ = ValidateEnvelopeDebug(NewEmptyEnvelope())
}
