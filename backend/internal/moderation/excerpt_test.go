package moderation

import (
	"strings"
	"testing"
)

// AT-09：摘录必须是用户消息本身，不是请求体 JSON 的开头。
// 变异：把 BuildExcerpt 改成直接截断整个 body 时本用例转红——断言里既要求出现
// 用户原话，又要求不出现协议字段名，两侧同时锁住。
func TestBuildExcerpt_取用户消息而非协议字段(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","stream":true,"temperature":0.7,` +
		`"messages":[{"role":"system","content":"你是一个乐于助人的助手"},` +
		`{"role":"user","content":"帮我写一段关于水质检测的说明"}]}`)

	got := BuildExcerpt(body, DefaultExcerptMaxRunes)

	if !strings.Contains(got, "水质检测") {
		t.Fatalf("摘录未包含用户消息内容: %q", got)
	}
	for _, protocolToken := range []string{"gpt-4o", "stream", "temperature", "role"} {
		if strings.Contains(got, protocolToken) {
			t.Fatalf("摘录混入协议字段 %q: %q", protocolToken, got)
		}
	}
}

// AT-09 补充：取最后一条用户消息。长对话里触发拦截的通常是新增那条，
// 取第一条会把最相关内容漏掉。变异：把遍历方向改成正序时转红。
func TestBuildExcerpt_取最后一条用户消息(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"第一轮无关内容"},` +
		`{"role":"assistant","content":"好的"},` +
		`{"role":"user","content":"第二轮才是本次请求"}]}`)

	got := BuildExcerpt(body, DefaultExcerptMaxRunes)

	if !strings.Contains(got, "第二轮才是本次请求") {
		t.Fatalf("未取到最后一条用户消息: %q", got)
	}
	if strings.Contains(got, "第一轮无关内容") {
		t.Fatalf("摘录包含了较早的用户消息: %q", got)
	}
}

// AT-04：脱敏必须发生在截断之前，否则长凭证会吃光字符预算，把它后面用户真正
// 说的话挤出摘录——运营就看不到这次请求在干什么，摘录也就失去意义。
//
// 本用例的判别性来自「凭证之后的内容是否幸存」：先脱敏时凭证塌缩成一个占位符，
// 后面的话能进摘录；先截断时凭证占满预算，后面的话被切掉。
// 变异：把 BuildExcerpt 里两步顺序对调时本用例转红。
func TestBuildExcerpt_先脱敏再截断使真实内容不被凭证挤掉(t *testing.T) {
	const secret = "sk-ant-api03-VERYLONGSECRETVALUE0123456789ABCDEFGH"
	const tail = "帮我查一下这个"
	limit := 20
	body := []byte(`{"messages":[{"role":"user","content":"` + secret + tail + `"}]}`)

	got := BuildExcerpt(body, limit)

	if !strings.Contains(got, tail) {
		t.Fatalf("凭证之后的真实内容被挤出摘录: %q", got)
	}
	if strings.Contains(got, "sk-") {
		t.Fatalf("摘录残留凭证明文残片: %q", got)
	}
	if strings.Contains(got, "[") && !strings.Contains(got, "[已脱敏]") {
		t.Fatalf("尾部残留不完整的脱敏占位符: %q", got)
	}
}

// 截断点落在占位符中间时，不得给运营留下 "[已脱敏" 这种断掉的标记。
// 变异：去掉 trimPartialPlaceholder 调用时转红。
func TestBuildExcerpt_不留半截脱敏占位符(t *testing.T) {
	const secret = "sk-ant-api03-SECRETVALUE"
	// 让截断点恰好落在占位符内部：前缀 + 占位符前 3 个字符。
	prefix := strings.Repeat("前", 10)
	limit := 10 + 3
	body := []byte(`{"messages":[{"role":"user","content":"` + prefix + secret + `"}]}`)

	got := BuildExcerpt(body, limit)

	if strings.Contains(got, "[") && !strings.Contains(got, "[已脱敏]") {
		t.Fatalf("尾部残留不完整的脱敏占位符: %q", got)
	}
	if got != prefix {
		t.Fatalf("摘录=%q want 仅保留完整前缀 %q", got, prefix)
	}
}

// AT-05：按 rune 截断。纯中文超长输入若按 byte 截断会切出半个字符（乱码）。
// 变异：把 truncateRunes 改成 s[:maxRunes] 时转红。
func TestBuildExcerpt_中文按字符截断不产生乱码(t *testing.T) {
	limit := 10
	body := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("检", 50) + `"}]}`)

	got := BuildExcerpt(body, limit)

	if gotRunes := []rune(got); len(gotRunes) != limit {
		t.Fatalf("截断后字符数=%d want %d: %q", len(gotRunes), limit, got)
	}
	if !strings.HasPrefix(got, "检") || strings.ContainsRune(got, '�') {
		t.Fatalf("截断产生乱码: %q", got)
	}
	if got != strings.Repeat("检", limit) {
		t.Fatalf("截断结果不是完整字符序列: %q", got)
	}
}

// AT-06：无法解析出用户消息时返回空串，绝不回退成原始请求体。
// 变异：给 extractUserText 加一条 return string(body) 的兜底时转红。
func TestBuildExcerpt_无法提取时为空而非回退原始请求体(t *testing.T) {
	cases := map[string][]byte{
		"非 JSON":      []byte(`这不是 JSON，里面还带 sk-ant-api03-LEAKME`),
		"空请求体":        nil,
		"无用户消息":       []byte(`{"messages":[{"role":"system","content":"仅系统提示"}]}`),
		"未知形态但含敏感内容": []byte(`{"unknown_shape":"sk-ant-api03-LEAKME"}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got := BuildExcerpt(body, DefaultExcerptMaxRunes)
			if got != "" {
				t.Fatalf("期望空摘录，实际 %q", got)
			}
		})
	}
}

// 多协议形态覆盖：审核入口被多个协议共用，任一形态解析失效都会让该协议的
// 摘录静默变空。变异：删掉任一 extractFrom* 分支时对应子用例转红。
func TestBuildExcerpt_覆盖多种请求体形态(t *testing.T) {
	cases := map[string]struct {
		body []byte
		want string
	}{
		"messages 数组": {
			body: []byte(`{"messages":[{"role":"user","content":"消息形态内容"}]}`),
			want: "消息形态内容",
		},
		"messages 多模态分片": {
			body: []byte(`{"messages":[{"role":"user","content":[` +
				`{"type":"text","text":"分片文本内容"},` +
				`{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}]}`),
			want: "分片文本内容",
		},
		"contents 数组": {
			body: []byte(`{"contents":[{"role":"user","parts":[{"text":"内容形态文本"}]}]}`),
			want: "内容形态文本",
		},
		"contents 省略 role": {
			body: []byte(`{"contents":[{"parts":[{"text":"省略角色的文本"}]}]}`),
			want: "省略角色的文本",
		},
		"input 裸字符串": {
			body: []byte(`{"input":"裸字符串输入"}`),
			want: "裸字符串输入",
		},
		"input 条目数组": {
			body: []byte(`{"input":[{"role":"user","content":"条目数组输入"}]}`),
			want: "条目数组输入",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := BuildExcerpt(tc.body, DefaultExcerptMaxRunes)
			if got != tc.want {
				t.Fatalf("摘录=%q want %q", got, tc.want)
			}
		})
	}
}

// 图片等非文本来源不应进摘录：分片里只取文本，避免把 data URL 整段落库。
func TestBuildExcerpt_多模态只取文本不取图片来源(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAABBBBCCCC"}},` +
		`{"type":"text","text":"这张图是什么"}]}]}`)

	got := BuildExcerpt(body, DefaultExcerptMaxRunes)

	if got != "这张图是什么" {
		t.Fatalf("摘录=%q want 仅文本分片", got)
	}
	if strings.Contains(got, "base64") || strings.Contains(got, "data:image") {
		t.Fatalf("摘录混入图片来源: %q", got)
	}
}
