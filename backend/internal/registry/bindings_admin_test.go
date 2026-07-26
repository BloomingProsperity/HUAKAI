package registry

import (
	"errors"
	"testing"
	"time"
)

func TestApplyBindingPatchPreservesAndClearsFields(t *testing.T) {
	override := "provider-model"
	rpm := int32(120)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	b := AdminBinding{
		Priority:                12,
		Weight:                  3,
		SelectionMode:           "priority_weighted",
		ProviderModelIDOverride: &override,
		RPMLimit:                &rpm,
		FallbackClass:           "quota",
		Enabled:                 true,
		EffectiveFrom:           &from,
		EffectiveUntil:          &until,
		Reason:                  "保留",
	}

	if err := applyBindingPatch(&b, UpdateBindingInput{
		Enabled:                 BindingField[bool]{Set: true, Value: false},
		ProviderModelIDOverride: BindingField[*string]{Set: true, Value: nil},
	}); err != nil {
		t.Fatalf("applyBindingPatch: %v", err)
	}
	if b.Enabled {
		t.Fatal("enabled=true want false")
	}
	if b.ProviderModelIDOverride != nil {
		t.Fatalf("provider override=%v want nil", b.ProviderModelIDOverride)
	}
	if b.Priority != 12 || b.Weight != 3 || b.SelectionMode != "priority_weighted" ||
		b.RPMLimit == nil || *b.RPMLimit != 120 || b.FallbackClass != "quota" ||
		b.EffectiveFrom == nil || !b.EffectiveFrom.Equal(from) ||
		b.EffectiveUntil == nil || !b.EffectiveUntil.Equal(until) || b.Reason != "保留" {
		t.Fatalf("未触及字段被改写: %+v", b)
	}
}

// 只修改生效窗一侧也必须与数据库当前另一侧联合校验。删掉合并后的窗口校验，
// 本测试会从 ErrBindingWindow 变为 nil。
func TestApplyBindingPatchRejectsWindowAgainstPreservedSide(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	b := AdminBinding{
		Weight:         1,
		SelectionMode:  "strict_priority",
		FallbackClass:  "normal",
		EffectiveFrom:  &from,
		EffectiveUntil: &until,
	}
	tooLate := until.Add(time.Hour)

	err := applyBindingPatch(&b, UpdateBindingInput{
		EffectiveFrom: BindingField[*time.Time]{Set: true, Value: &tooLate},
	})
	if !errors.Is(err, ErrBindingWindow) {
		t.Fatalf("err=%v want ErrBindingWindow", err)
	}
}

func TestApplyBindingPatchRejectsInvalidDomainValues(t *testing.T) {
	valid := AdminBinding{
		TenantID:      1,
		ModelID:       2,
		PoolGroupID:   3,
		Priority:      100,
		Weight:        1,
		SelectionMode: "strict_priority",
		FallbackClass: "normal",
	}
	negative := int32(-1)

	tests := []UpdateBindingInput{
		{Priority: BindingField[int32]{Set: true, Value: -1}},
		{Weight: BindingField[int32]{Set: true, Value: 0}},
		{SelectionMode: BindingField[string]{Set: true, Value: "unknown"}},
		{RPMLimit: BindingField[*int32]{Set: true, Value: &negative}},
		{TPMLimit: BindingField[*int32]{Set: true, Value: &negative}},
		{MaxParallelRequests: BindingField[*int32]{Set: true, Value: &negative}},
		{FallbackClass: BindingField[string]{Set: true, Value: "unknown"}},
	}
	for i, in := range tests {
		got := valid
		if err := applyBindingPatch(&got, in); !errors.Is(err, ErrBindingInvalid) {
			t.Errorf("case %d err=%v want ErrBindingInvalid", i, err)
		}
	}
}
