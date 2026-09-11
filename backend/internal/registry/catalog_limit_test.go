package registry

import "testing"

func TestPositiveInt32CopiesCatalogLimit(t *testing.T) {
	v := int32(8192)
	got := positiveInt32(&v)
	if got == nil || *got != 8192 {
		t.Fatalf("正目录上限=%v want 8192", got)
	}
	if got == nil {
		t.Fatal("unexpected nil")
	}
	v = 1
	if *got != 8192 {
		t.Fatal("返回值必须是拷贝，不能与入参共用存储")
	}
}

func TestPositiveInt32RejectsMissingOrNonPositive(t *testing.T) {
	if got := positiveInt32(nil); got != nil {
		t.Fatalf("nil 必须视为未登记: %v", got)
	}
	zero := int32(0)
	if got := positiveInt32(&zero); got != nil {
		t.Fatalf("0 必须视为未登记: %v", got)
	}
	neg := int32(-8)
	if got := positiveInt32(&neg); got != nil {
		t.Fatalf("负数必须视为未登记: %v", got)
	}
}
