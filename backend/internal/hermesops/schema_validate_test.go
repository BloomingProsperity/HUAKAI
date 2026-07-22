package hermesops

import (
	"errors"
	"testing"
)

func TestObjectSchema无必填字段时输出空Required数组(t *testing.T) {
	schema := ObjectSchema(nil)
	required, ok := schema["required"].([]string)
	if !ok || required == nil || len(required) != 0 {
		t.Fatalf("无必填字段时 required 必须是非 nil 空数组：%+v", schema)
	}
	if err := ValidateInputSchema(schema); err != nil {
		t.Fatalf("空 required 数组的对象合同应保持有效：%v", err)
	}
}

func TestValidateToolArguments严格执行合同(t *testing.T) {
	schema := ObjectSchema(map[string]any{
		"account_id": PositiveIntegerSchema("账号 ID"),
		"state":      StringSchema("状态", "active", "disabled"),
	}, "account_id")

	if err := ValidateToolArguments(schema, map[string]any{"account_id": float64(7), "state": "active"}); err != nil {
		t.Fatalf("有效参数被拒绝：%v", err)
	}
	invalid := []map[string]any{
		{},
		{"account_id": float64(0)},
		{"account_id": 7.5},
		{"account_id": float64(7), "state": "unknown"},
		{"account_id": float64(7), "tenant_id": float64(99)},
	}
	for _, args := range invalid {
		if err := ValidateToolArguments(schema, args); !errors.Is(err, ErrInvalidArgs) {
			t.Fatalf("参数 %+v 的错误=%v，预期 ErrInvalidArgs", args, err)
		}
	}
}
