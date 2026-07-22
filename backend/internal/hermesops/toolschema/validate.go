// Package toolschema 校验 Hermes 工具使用的受限 JSON Schema 及调用参数。
package toolschema

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

var (
	// ErrSchema 表示工具参数合同不是受支持的 JSON Schema。
	ErrSchema = errors.New("hermes tool schema invalid")
	// ErrArguments 表示调用参数不符合已注册的工具合同。
	ErrArguments = errors.New("hermes tool arguments invalid")
)

// ValidateSchema 校验工具支持的 JSON Schema 子集。
func ValidateSchema(schema map[string]any) error {
	if schema == nil || schema["type"] != "object" {
		return fmt.Errorf("%w: 顶层 type 必须为 object", ErrSchema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: properties 必须为对象", ErrSchema)
	}
	if allow, ok := schema["additionalProperties"].(bool); !ok || allow {
		return fmt.Errorf("%w: additionalProperties 必须为 false", ErrSchema)
	}
	required, err := stringSlice(schema["required"])
	if err != nil {
		return err
	}
	for _, name := range required {
		if _, exists := properties[name]; !exists {
			return fmt.Errorf("%w: 必填字段 %s 未声明", ErrSchema, name)
		}
	}
	for name, raw := range properties {
		property, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: 字段 %s 的定义不是对象", ErrSchema, name)
		}
		typeName, _ := property["type"].(string)
		switch typeName {
		case "string", "integer", "object":
		default:
			return fmt.Errorf("%w: 字段 %s 的类型 %q 不受支持", ErrSchema, name, typeName)
		}
	}
	return nil
}

// ValidateArguments 按工具注册时的合同校验一次调用。
func ValidateArguments(schema map[string]any, args map[string]any) error {
	if err := ValidateSchema(schema); err != nil {
		return err
	}
	if args == nil {
		args = map[string]any{}
	}
	properties := schema["properties"].(map[string]any)
	for key := range args {
		if _, ok := properties[key]; !ok {
			return fmt.Errorf("%w: 未声明字段 %s", ErrArguments, key)
		}
	}
	required, _ := stringSlice(schema["required"])
	for _, key := range required {
		value, exists := args[key]
		if !exists || value == nil {
			return fmt.Errorf("%w: 缺少字段 %s", ErrArguments, key)
		}
	}
	for key, value := range args {
		property := properties[key].(map[string]any)
		if err := validateValue(key, value, property); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(name string, value any, schema map[string]any) error {
	switch schema["type"] {
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%w: 字段 %s 必须是字符串", ErrArguments, name)
		}
		if minimum, ok := schema["minLength"].(int); ok && len([]rune(text)) < minimum {
			return fmt.Errorf("%w: 字段 %s 不能为空", ErrArguments, name)
		}
		if enum, err := stringSlice(schema["enum"]); err == nil && len(enum) > 0 && !contains(enum, text) {
			return fmt.Errorf("%w: 字段 %s 的值不受支持", ErrArguments, name)
		}
	case "integer":
		number, ok := IntegerValue(value)
		if !ok {
			return fmt.Errorf("%w: 字段 %s 必须是整数", ErrArguments, name)
		}
		if minimum, ok := numeric(schema["minimum"]); ok && float64(number) < minimum {
			return fmt.Errorf("%w: 字段 %s 小于最小值", ErrArguments, name)
		}
		if maximum, ok := numeric(schema["maximum"]); ok && float64(number) > maximum {
			return fmt.Errorf("%w: 字段 %s 大于最大值", ErrArguments, name)
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("%w: 字段 %s 必须是对象", ErrArguments, name)
		}
	default:
		return fmt.Errorf("%w: 字段 %s 的类型无效", ErrSchema, name)
	}
	return nil
}

func stringSlice(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...), nil
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			item, ok := value.(string)
			if !ok || strings.TrimSpace(item) == "" {
				return nil, fmt.Errorf("%w: required 必须是非空字符串数组", ErrSchema)
			}
			out = append(out, item)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: required 必须是字符串数组", ErrSchema)
	}
}

// IntegerValue 把 JSON 解码后的整数值规范化为 int64。
func IntegerValue(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int64:
		return number, true
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) || number > math.MaxInt64 || number < math.MinInt64 {
			return 0, false
		}
		return int64(number), true
	default:
		return 0, false
	}
}

func numeric(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == candidate {
			return true
		}
	}
	return false
}
