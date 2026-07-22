package hermesops

import (
	"errors"
	"testing"
)

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
