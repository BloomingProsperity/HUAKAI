package bodymodel

import (
	"encoding/json"
	"testing"
)

// TestModelMatches 判别:相等返回 true、不等/无法解析返回 false。
// 变异:把 ModelMatches 恒返回 true → "不等"用例红。
func TestModelMatches(t *testing.T) {
	cases := []struct {
		body  string
		model string
		want  bool
	}{
		{`{"model":"claude-sonnet-4-5"}`, "claude-sonnet-4-5", true},
		{`{"model":"alias"}`, "claude-sonnet-4-5", false},
		{`{"messages":[]}`, "claude-sonnet-4-5", false}, // 缺 model
		{`not json`, "x", false},
	}
	for _, c := range cases {
		if got := ModelMatches([]byte(c.body), c.model); got != c.want {
			t.Fatalf("ModelMatches(%s,%q)=%v want %v", c.body, c.model, got, c.want)
		}
	}
}

// TestRewriteModel 判别:改写后顶层 model 变为目标值、其余字段保留;坏 JSON ok=false。
// 变异:去掉 obj["model"]=modelRaw 赋值 → model 未改写,断言红。
func TestRewriteModel(t *testing.T) {
	out, ok := RewriteModel([]byte(`{"model":"alias","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`), "claude-sonnet-4-5")
	if !ok {
		t.Fatal("RewriteModel 应成功")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("改写产物非 JSON: %v", err)
	}
	var model string
	_ = json.Unmarshal(obj["model"], &model)
	if model != "claude-sonnet-4-5" {
		t.Fatalf("model 未改写: %q", model)
	}
	if _, has := obj["max_tokens"]; !has {
		t.Fatal("其余字段应保留")
	}
	if _, ok := RewriteModel([]byte(`not json`), "x"); ok {
		t.Fatal("坏 JSON 应 ok=false")
	}
	// 合法 JSON null/非对象:解出 nil map,不得 panic,ok=false。
	// 变异:去掉 nil map 判 → 对 `null` nil-map 赋值 panic,本用例红。
	for _, b := range []string{`null`, `[]`, `"str"`, `123`} {
		if _, ok := RewriteModel([]byte(b), "x"); ok {
			t.Fatalf("非对象 body %q 应 ok=false", b)
		}
	}
}
