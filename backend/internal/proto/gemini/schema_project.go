package gemini

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ErrUnsupportedToolSchema 表示工具参数合同含 Gemini 无法接受的构造，必须 fail-closed。
var ErrUnsupportedToolSchema = fmt.Errorf("gemini tool schema uses constructs the upstream subset rejects")

// ProjectToolSchema 把工具参数 JSON Schema 收成 Gemini 可接受的子集。
// 能就地展开的局部引用会被展开；无法展开的引用或 patternProperties 直接拒绝，
// 避免把残缺 schema 打到上游再吃不透明 400。
func ProjectToolSchema(raw json.RawMessage) (json.RawMessage, error) {
	normalized := normalizeRawObject(raw)
	var root any
	if err := json.Unmarshal(normalized, &root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedToolSchema, err)
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: schema must be an object", ErrUnsupportedToolSchema)
	}
	projected, err := projectSchemaNode(obj, obj, nil)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(projected)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

func projectSchemaNode(node any, root map[string]any, stack []string) (any, error) {
	switch typed := node.(type) {
	case map[string]any:
		if _, ok := typed["patternProperties"]; ok {
			return nil, fmt.Errorf("%w: patternProperties", ErrUnsupportedToolSchema)
		}
		if ref, ok := typed["$ref"].(string); ok {
			target, err := resolveLocalSchemaRef(ref, root)
			if err != nil {
				return nil, err
			}
			if containsString(stack, ref) {
				return nil, fmt.Errorf("%w: cyclic $ref %q", ErrUnsupportedToolSchema, ref)
			}
			return projectSchemaNode(target, root, append(stack, ref))
		}
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			if key == "$defs" || key == "definitions" {
				continue
			}
			next, err := projectSchemaNode(value, root, stack)
			if err != nil {
				return nil, err
			}
			out[key] = next
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			next, err := projectSchemaNode(item, root, stack)
			if err != nil {
				return nil, err
			}
			out = append(out, next)
		}
		return out, nil
	default:
		return typed, nil
	}
}

func resolveLocalSchemaRef(ref string, root map[string]any) (map[string]any, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("%w: $ref %q is not a local pointer", ErrUnsupportedToolSchema, ref)
	}
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var current any = root
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("%w: empty $ref segment in %q", ErrUnsupportedToolSchema, ref)
		}
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: $ref %q leaves object scope", ErrUnsupportedToolSchema, ref)
		}
		next, ok := object[part]
		if !ok {
			return nil, fmt.Errorf("%w: $ref %q is unresolved", ErrUnsupportedToolSchema, ref)
		}
		current = next
	}
	object, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: $ref %q does not point to an object", ErrUnsupportedToolSchema, ref)
	}
	return object, nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
