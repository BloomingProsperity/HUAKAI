package gemini

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProjectToolSchemaExpandsLocalRef(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"object",
		"properties":{"item":{"$ref":"#/$defs/Item"}},
		"$defs":{"Item":{"type":"object","properties":{"name":{"type":"string"}}}}
	}`)
	got, err := ProjectToolSchema(raw)
	if err != nil {
		t.Fatalf("可展开的局部引用必须成功: %v", err)
	}
	var projected map[string]any
	if err := json.Unmarshal(got, &projected); err != nil {
		t.Fatalf("投影 JSON: %v", err)
	}
	if _, ok := projected["$defs"]; ok {
		t.Fatalf("投影后不得残留 $defs: %s", got)
	}
	item := projected["properties"].(map[string]any)["item"].(map[string]any)
	if item["type"] != "object" {
		t.Fatalf("引用未展开: %s", got)
	}
	name := item["properties"].(map[string]any)["name"].(map[string]any)
	if name["type"] != "string" {
		t.Fatalf("展开后的字段丢失: %s", got)
	}
}

func TestProjectToolSchemaRejectsPatternProperties(t *testing.T) {
	_, err := ProjectToolSchema(json.RawMessage(`{"type":"object","patternProperties":{".*":{"type":"string"}}}`))
	if !errors.Is(err, ErrUnsupportedToolSchema) {
		t.Fatalf("patternProperties 必须 fail-closed, err=%v", err)
	}
	if !strings.Contains(err.Error(), "patternProperties") {
		t.Fatalf("错误应点明构造: %v", err)
	}
}

func TestProjectToolSchemaRejectsUnresolvedRef(t *testing.T) {
	_, err := ProjectToolSchema(json.RawMessage(`{"$ref":"#/$defs/Missing"}`))
	if !errors.Is(err, ErrUnsupportedToolSchema) {
		t.Fatalf("无法展开的 $ref 必须 fail-closed, err=%v", err)
	}
}

func TestProjectToolSchemaRejectsExternalRef(t *testing.T) {
	_, err := ProjectToolSchema(json.RawMessage(`{"$ref":"https://example.test/schema.json"}`))
	if !errors.Is(err, ErrUnsupportedToolSchema) {
		t.Fatalf("外部 $ref 必须 fail-closed, err=%v", err)
	}
}

func TestProjectToolSchemaRejectsCyclicRef(t *testing.T) {
	_, err := ProjectToolSchema(json.RawMessage(`{"$ref":"#/$defs/Loop","$defs":{"Loop":{"$ref":"#/$defs/Loop"}}}`))
	if !errors.Is(err, ErrUnsupportedToolSchema) {
		t.Fatalf("循环 $ref 必须 fail-closed, err=%v", err)
	}
}

func TestProjectToolSchemaEmptyObjectIsValidSubset(t *testing.T) {
	got, err := ProjectToolSchema(nil)
	if err != nil {
		t.Fatalf("空 schema 应收成对象子集: %v", err)
	}
	if string(got) != "{}" {
		t.Fatalf("空 schema 投影=%s want {}", got)
	}
}
