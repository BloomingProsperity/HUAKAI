// R7.4 测试：覆盖工具名两个出现位置（顶层 tools[] / messages 中 tool_use
// 块）× 5 种 Reason 的核心路径，包含未知字段保留、tool_result 不被触碰、
// 幂等等场景。
package gateway

import (
	"encoding/json"
	"testing"
)

func TestRewriteToolNames_Table(t *testing.T) {
	mapMcpToAnalyze := ToolNameMapping{"mcp_search": "analyze_sea00"}
	mapMcpAndWeb := ToolNameMapping{
		"mcp_search": "analyze_sea00",
		"web_search": "fetch_web01",
	}

	tests := []struct {
		name        string
		input       string
		mapping     ToolNameMapping
		wantReason  string
		wantApplied bool
		wantRenames int
		wantErr     bool
		assertBody  func(t *testing.T, res ToolNameRewriteResult)
	}{
		{
			name:       "空 body 触发 invalid_body",
			input:      "",
			mapping:    mapMcpToAnalyze,
			wantReason: reasonToolInvalidBody,
			wantErr:    true,
		},
		{
			name:       "完全无工具字段返回 no_tools",
			input:      `{"model":"claude-opus","messages":[{"role":"user","content":"hi"}]}`,
			mapping:    mapMcpToAnalyze,
			wantReason: reasonToolNoTools,
		},
		{
			name:       "tools 存在但无名称命中映射",
			input:      `{"tools":[{"name":"bash","description":"run shell"}],"messages":[]}`,
			mapping:    mapMcpToAnalyze,
			wantReason: reasonToolNoMatch,
		},
		{
			name:        "顶层 tools 单个命中并产生审计行",
			input:       `{"tools":[{"name":"mcp_search","description":"search"}],"messages":[]}`,
			mapping:     mapMcpToAnalyze,
			wantReason:  reasonToolRenamed,
			wantApplied: true,
			wantRenames: 1,
			assertBody: func(t *testing.T, res ToolNameRewriteResult) {
				t.Helper()
				if res.Renames[0].Path != "tools[0].name" {
					t.Errorf("Path = %q", res.Renames[0].Path)
				}
				if res.Renames[0].From != "mcp_search" || res.Renames[0].To != "analyze_sea00" {
					t.Errorf("rename = %+v", res.Renames[0])
				}
				assertToolNameEq(t, res.Body, 0, "analyze_sea00")
			},
		},
		{
			name: "顶层 tools 多个命中产生多条审计行",
			input: `{"tools":[
				{"name":"mcp_search"},
				{"name":"web_search"},
				{"name":"bash"}
			],"messages":[]}`,
			mapping:     mapMcpAndWeb,
			wantReason:  reasonToolRenamed,
			wantApplied: true,
			wantRenames: 2,
			assertBody: func(t *testing.T, res ToolNameRewriteResult) {
				t.Helper()
				assertToolNameEq(t, res.Body, 2, "bash")
			},
		},
		{
			name: "messages 中 tool_use 块命中",
			input: `{"messages":[{"role":"assistant","content":[
				{"type":"tool_use","id":"tu_1","name":"mcp_search","input":{}}
			]}]}`,
			mapping:     mapMcpToAnalyze,
			wantReason:  reasonToolRenamed,
			wantApplied: true,
			wantRenames: 1,
			assertBody: func(t *testing.T, res ToolNameRewriteResult) {
				t.Helper()
				if res.Renames[0].Path != "messages[0].content[0].name" {
					t.Errorf("Path = %q", res.Renames[0].Path)
				}
			},
		},
		{
			name: "tool_use 已是目标名走 no_match（幂等）",
			input: `{"messages":[{"role":"assistant","content":[
				{"type":"tool_use","id":"tu_1","name":"analyze_sea00","input":{}}
			]}]}`,
			mapping:    mapMcpToAnalyze,
			wantReason: reasonToolNoMatch,
		},
		{
			name: "顶层 tools 与 tool_use 块同时命中",
			input: `{
				"tools":[{"name":"mcp_search"}],
				"messages":[{"role":"assistant","content":[
					{"type":"tool_use","id":"tu_1","name":"mcp_search","input":{}}
				]}]
			}`,
			mapping:     mapMcpToAnalyze,
			wantReason:  reasonToolRenamed,
			wantApplied: true,
			wantRenames: 2,
			assertBody: func(t *testing.T, res ToolNameRewriteResult) {
				t.Helper()
				paths := map[string]bool{}
				for _, r := range res.Renames {
					paths[r.Path] = true
				}
				if !paths["tools[0].name"] || !paths["messages[0].content[0].name"] {
					t.Errorf("缺少预期路径: %v", paths)
				}
			},
		},
		{
			name: "tool 对象缺少 name 字段时静默跳过",
			input: `{"tools":[
				{"description":"no name"},
				{"name":"mcp_search"}
			],"messages":[]}`,
			mapping:     mapMcpToAnalyze,
			wantReason:  reasonToolRenamed,
			wantApplied: true,
			wantRenames: 1,
		},
		{
			name:       "映射中 original==target 不产生审计",
			input:      `{"tools":[{"name":"bash"}],"messages":[]}`,
			mapping:    ToolNameMapping{"bash": "bash"},
			wantReason: reasonToolNoMatch,
		},
		{
			name: "改写后 tool 对象未知字段完整保留",
			input: `{"tools":[{
				"name":"mcp_search",
				"description":"search tool",
				"cache_control":{"type":"ephemeral"},
				"custom_field":"keep_me"
			}],"messages":[]}`,
			mapping:     mapMcpToAnalyze,
			wantReason:  reasonToolRenamed,
			wantApplied: true,
			wantRenames: 1,
			assertBody: func(t *testing.T, res ToolNameRewriteResult) {
				t.Helper()
				var root map[string]json.RawMessage
				if err := json.Unmarshal(res.Body, &root); err != nil {
					t.Fatal(err)
				}
				var tools []map[string]json.RawMessage
				if err := json.Unmarshal(root["tools"], &tools); err != nil {
					t.Fatal(err)
				}
				for _, k := range []string{"cache_control", "custom_field", "description"} {
					if _, ok := tools[0][k]; !ok {
						t.Errorf("字段 %q 在改写后丢失", k)
					}
				}
			},
		},
		{
			name: "tool_result 块无 name 字段不被触碰",
			input: `{"messages":[{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"tu_1","content":"r"}
			]}]}`,
			mapping:    mapMcpToAnalyze,
			wantReason: reasonToolNoTools,
		},
		{
			name:       "空 mapping 直接返回 empty_mapping",
			input:      `{"tools":[{"name":"mcp_search"}]}`,
			mapping:    ToolNameMapping{},
			wantReason: reasonToolEmptyMapping,
		},
		{
			name:        "tool_choice 强制工具命中改写",
			input:       `{"tools":[{"name":"mcp_search"}],"tool_choice":{"type":"tool","name":"mcp_search"}}`,
			mapping:     mapMcpToAnalyze,
			wantReason:  reasonToolRenamed,
			wantApplied: true,
			wantRenames: 2, // tools[0].name + tool_choice.name
			assertBody: func(t *testing.T, res ToolNameRewriteResult) {
				t.Helper()
				paths := map[string]bool{}
				for _, r := range res.Renames {
					paths[r.Path] = true
				}
				if !paths["tool_choice.name"] {
					t.Errorf("缺少 tool_choice.name 审计行：%+v", res.Renames)
				}
				// 序列化后 tool_choice.name 必须是混淆后的名
				var root map[string]json.RawMessage
				if err := json.Unmarshal(res.Body, &root); err != nil {
					t.Fatal(err)
				}
				var tc map[string]json.RawMessage
				if err := json.Unmarshal(root["tool_choice"], &tc); err != nil {
					t.Fatal(err)
				}
				var nm string
				if err := json.Unmarshal(tc["name"], &nm); err != nil {
					t.Fatal(err)
				}
				if nm != "analyze_sea00" {
					t.Errorf("tool_choice.name = %q want analyze_sea00", nm)
				}
			},
		},
		{
			name: "tool_choice type=auto 不改写但视为涉及工具",
			input: `{"tool_choice":{"type":"auto"},"messages":[
				{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"unknown","input":{}}]}
			]}`,
			mapping:    mapMcpToAnalyze,
			wantReason: reasonToolNoMatch,
		},
		{
			name:       "tool_choice 字符串 none 视为不涉及工具",
			input:      `{"tool_choice":"none"}`,
			mapping:    mapMcpToAnalyze,
			wantReason: reasonToolNoTools,
		},
		{
			name:       "tool_choice 字符串 auto 不改写但视为涉及工具",
			input:      `{"tool_choice":"auto"}`,
			mapping:    mapMcpToAnalyze,
			wantReason: reasonToolNoMatch,
		},
		{
			name: "混合场景：多消息+tool_use+tool_result",
			input: `{
				"tools":[{"name":"web_search"},{"name":"bash"}],
				"messages":[
					{"role":"user","content":"hi"},
					{"role":"assistant","content":[
						{"type":"text","text":"thinking"},
						{"type":"tool_use","id":"tu_1","name":"web_search","input":{}},
						{"type":"tool_use","id":"tu_2","name":"bash","input":{}}
					]},
					{"role":"user","content":[
						{"type":"tool_result","tool_use_id":"tu_1","content":"res1"},
						{"type":"tool_result","tool_use_id":"tu_2","content":"ok"}
					]}
				]
			}`,
			mapping:     ToolNameMapping{"web_search": "fetch_web00"},
			wantReason:  reasonToolRenamed,
			wantApplied: true,
			wantRenames: 2, // tools[0].name + messages[1].content[1].name
			assertBody: func(t *testing.T, res ToolNameRewriteResult) {
				t.Helper()
				assertToolNameEq(t, res.Body, 1, "bash") // bash 未命中映射
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			res, err := RewriteToolNames([]byte(tt.input), ToolNameRewritePlan{Mapping: tt.mapping})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if res.Reason != tt.wantReason {
				t.Errorf("Reason = %q want %q", res.Reason, tt.wantReason)
			}
			if res.Applied != tt.wantApplied {
				t.Errorf("Applied = %v want %v", res.Applied, tt.wantApplied)
			}
			if len(res.Renames) != tt.wantRenames {
				t.Errorf("Renames len = %d want %d (%+v)", len(res.Renames), tt.wantRenames, res.Renames)
			}
			if tt.assertBody != nil {
				tt.assertBody(t, res)
			}
		})
	}
}

// TestRewriteToolNames_Idempotent 验证连跑两次的幂等性：第一次重命名后第二
// 次同样 mapping 不应再产生改名。
func TestRewriteToolNames_Idempotent(t *testing.T) {
	in := []byte(`{"tools":[{"name":"mcp_search"}]}`)
	plan := ToolNameRewritePlan{Mapping: ToolNameMapping{"mcp_search": "analyze_sea00"}}

	r1, err := RewriteToolNames(in, plan)
	if err != nil || !r1.Applied {
		t.Fatalf("第一次：err=%v applied=%v", err, r1.Applied)
	}
	r2, err := RewriteToolNames(r1.Body, plan)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Applied {
		t.Errorf("第二次应当 no-op，实际 reason=%s renames=%v", r2.Reason, r2.Renames)
	}
	if r2.Reason != reasonToolNoMatch {
		t.Errorf("第二次 reason=%q want=no_match", r2.Reason)
	}
}

// TestRewriteToolNames_PreservesUnknownFields 验证 tool_use 块上未知字段
// （例如 cache_control / extra）经改写后保持原样。
func TestRewriteToolNames_PreservesUnknownFields(t *testing.T) {
	in := []byte(`{"messages":[{"role":"assistant","content":[
		{"type":"tool_use","id":"tu_1","name":"mcp_search","input":{},"cache_control":{"type":"ephemeral"}}
	]}]}`)
	res, err := RewriteToolNames(in, ToolNameRewritePlan{Mapping: ToolNameMapping{"mcp_search": "analyze_sea00"}})
	if err != nil || !res.Applied {
		t.Fatalf("err=%v applied=%v", err, res.Applied)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(res.Body, &root); err != nil {
		t.Fatal(err)
	}
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(root["messages"], &msgs); err != nil {
		t.Fatal(err)
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(msgs[0]["content"], &blocks); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cache_control", "id", "input", "type"} {
		if _, ok := blocks[0][k]; !ok {
			t.Errorf("字段 %q 在改写后丢失", k)
		}
	}
}

// assertToolNameEq 解析 body 中 tools[idx].name 并断言等于 want。
func assertToolNameEq(t *testing.T, body []byte, idx int, want string) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("解析 body：%v", err)
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(root["tools"], &tools); err != nil {
		t.Fatalf("解析 tools：%v", err)
	}
	if idx >= len(tools) {
		t.Fatalf("索引 %d 超界（len=%d）", idx, len(tools))
	}
	var got string
	if err := json.Unmarshal(tools[idx]["name"], &got); err != nil {
		t.Fatalf("解析 tools[%d].name：%v", idx, err)
	}
	if got != want {
		t.Errorf("tools[%d].name = %q want %q", idx, got, want)
	}
}
