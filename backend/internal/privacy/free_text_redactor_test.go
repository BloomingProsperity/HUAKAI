package privacy

import (
	"strings"
	"testing"
)

// AT-07：各类凭证形态逐一覆盖。变异：从共享前缀表里删掉任一形态时，
// 对应子用例转红。
func TestRedactCredentialTokens_覆盖各类凭证形态(t *testing.T) {
	cases := map[string]string{
		"Anthropic key":  "sk-ant-api03-ABCDEFGHIJKLMNOP",
		"OpenAI 风格 key":  "sk-proj-ABCDEFGHIJKLMNOPQRST",
		"工具调用 token":     "toolu_01ABCDEFGHIJKLMN",
		"GitHub OAuth":   "gho_ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"GitHub PAT":     "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"GitHub 细粒度 PAT": "github_pat_ABCDEFGHIJKLMNOP",
		"JWT":            "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxIn0.abc",
		"Google API key": "AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
	}
	for name, secret := range cases {
		t.Run(name, func(t *testing.T) {
			got := RedactCredentialTokens("我的凭证是 " + secret + " 请帮我检查")
			if strings.Contains(got, secret) {
				t.Fatalf("凭证未被脱敏: %q", got)
			}
			if !strings.Contains(got, CredentialPlaceholder) {
				t.Fatalf("未写入占位符: %q", got)
			}
			// 正向断言：周边正常文字必须原样保留，否则等于整段丢弃，摘录就没用了。
			if !strings.Contains(got, "我的凭证是") || !strings.Contains(got, "请帮我检查") {
				t.Fatalf("正常文字被误删: %q", got)
			}
		})
	}
}

// 只替换命中的 token，不整段丢弃——这是本函数与结构化脱敏的分工差异。
// 变异：把命中后改成返回整体占位符时转红。
func TestRedactCredentialTokens_只替换命中token保留其余文字(t *testing.T) {
	got := RedactCredentialTokens("第一段正常 sk-ant-api03-SECRET 第二段正常")

	want := "第一段正常 " + CredentialPlaceholder + " 第二段正常"
	if got != want {
		t.Fatalf("脱敏结果=%q want %q", got, want)
	}
}

// 误杀防线：正常词句里含有与凭证前缀相似的子串时不得被替换。
// 变异：把前缀锚定改成子串包含判定时转红。
func TestRedactCredentialTokens_不误杀正常文字(t *testing.T) {
	cases := []string{
		"这个 disk-usage-pct 指标偏高",
		"任务 task-list 已经完成",
		"人名 Faiza 和地名 Aizawl 都是正常词",
		"讨论 tenant-id 与 grant-type 的区别",
	}
	for _, text := range cases {
		t.Run(text, func(t *testing.T) {
			got := RedactCredentialTokens(text)
			if got != text {
				t.Fatalf("正常文字被误脱敏:\n输入 %q\n输出 %q", text, got)
			}
		})
	}
}

// 大小写敏感形态：真实 JWT / Google key 恒为该精确大小写，小写变体是普通文本，
// 不得误杀。变异：把大小写敏感判定改成忽略大小写时转红。
func TestRedactCredentialTokens_大小写敏感形态不误杀小写变体(t *testing.T) {
	if got := RedactCredentialTokens("单词 eyjafjallajokull 是冰岛火山"); strings.Contains(got, CredentialPlaceholder) {
		t.Fatalf("小写变体被误脱敏: %q", got)
	}
	if got := RedactCredentialTokens("token eyJhbGciOiJIUzI1NiJ9.eyJhIjoxfQ.sig"); !strings.Contains(got, CredentialPlaceholder) {
		t.Fatalf("真实 JWT 未被脱敏: %q", got)
	}
}

func TestRedactCredentialTokens_空输入(t *testing.T) {
	if got := RedactCredentialTokens(""); got != "" {
		t.Fatalf("空输入返回 %q want 空串", got)
	}
}
