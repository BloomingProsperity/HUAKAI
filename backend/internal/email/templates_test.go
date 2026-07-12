package email

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// 可注入读错误的设置存储,专测 resolveAuthEmail 的 fail-safe 回退。
type templateStoreStub struct {
	values  StoredSettings
	loadErr error
}

func (s *templateStoreStub) Load(context.Context, int64) (StoredSettings, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.values, nil
}

func (s *templateStoreStub) List(context.Context, int64) ([]StoredSetting, error) { return nil, nil }
func (s *templateStoreStub) Save(context.Context, int64, map[string]string, string) error {
	return nil
}
func (s *templateStoreStub) ListActiveTenantIDs(context.Context) ([]int64, error) {
	return []int64{1}, nil
}

func TestValidateTemplateOverride(t *testing.T) {
	// 变异:放行未知占位符 / 砍掉必含凭证校验 → 本测红。
	if err := ValidateTemplateOverride(TemplateKindVerification, "", "点这里 {{link}} 或 {{token}}"); err != nil {
		t.Fatalf("合法覆盖被拒: %v", err)
	}
	if err := ValidateTemplateOverride(TemplateKindVerification, "", "你好 {{nope}} {{token}}"); err == nil {
		t.Fatal("未知占位符应被拒")
	}
	if err := ValidateTemplateOverride(TemplateKindVerification, "", "没有凭证的正文"); err == nil {
		t.Fatal("正文缺 {{token}} 应被拒")
	}
	if err := ValidateTemplateOverride(TemplateKindOAuthCode, "", "验证码 {{code}}"); err != nil {
		t.Fatalf("oauth_code 合法覆盖被拒: %v", err)
	}
	if err := ValidateTemplateOverride(TemplateKindOAuthCode, "", "用 {{token}}"); err == nil {
		t.Fatal("oauth_code 不允许 {{token}}")
	}
	// 空正文 = 清除覆盖,合法。
	if err := ValidateTemplateOverride(TemplateKindVerification, "", ""); err != nil {
		t.Fatalf("清除覆盖应放行: %v", err)
	}
	if err := ValidateTemplateOverride("bogus", "", ""); err == nil {
		t.Fatal("未知 kind 应被拒")
	}
}

func TestRenderTemplate(t *testing.T) {
	out, err := RenderTemplate("A {{ token }} B {{link}}", map[string]string{"token": "t<1>", "link": `https://x/?a="1"`})
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	// 值必须 HTML 转义(变异:去掉转义 → 红);link 走属性安全转义保持可点。
	if !strings.Contains(out, "t&lt;1&gt;") {
		t.Fatalf("token 未转义: %q", out)
	}
	if !strings.Contains(out, "https://x/?a=%221%22") {
		t.Fatalf("link 未做属性转义: %q", out)
	}
	if _, err := RenderTemplate("{{unknown}}", map[string]string{"token": "x"}); err == nil {
		t.Fatal("未知占位符渲染必须报错(触发回退)")
	}
}

// 主题是纯文本头字段:不做 HTML 转义(变异:主题也走 html.EscapeString → 红)。
func TestRenderSubjectTemplateNoHTMLEscape(t *testing.T) {
	out, err := RenderSubjectTemplate("重置 {{email}}", map[string]string{"email": "sales&ops@example.com"})
	if err != nil {
		t.Fatalf("主题渲染失败: %v", err)
	}
	if out != "重置 sales&ops@example.com" {
		t.Fatalf("主题不应 HTML 转义: %q", out)
	}
	if _, err := RenderSubjectTemplate("{{ghost}}", nil); err == nil {
		t.Fatal("主题未知占位符必须报错")
	}
}

// 模板覆盖生效 + 三层 fail-safe 回退:store 读错 / 无覆盖 / 渲染失败,都必须回到内置默认。
func TestResolveAuthEmailFailSafe(t *testing.T) {
	subjectKey, bodyKey := TemplateSettingKeys(TemplateKindVerification)
	vars := map[string]string{"link": "", "token": "tok123"}

	store := &templateStoreStub{values: StoredSettings{
		subjectKey: "自定义主题 {{token}}",
		bodyKey:    "<p>自定义正文 {{token}}</p>",
	}}
	sender := &AuthSender{store: store}

	subject, body := sender.resolveAuthEmail(context.Background(), 1, TemplateKindVerification, vars, "默认主题", "默认正文")
	if subject != "自定义主题 tok123" || !strings.Contains(body, "自定义正文 tok123") {
		t.Fatalf("覆盖未生效: %q / %q", subject, body)
	}

	// store 读错 → 双回退(变异:回退分支删掉 → 红)。
	store.loadErr = errors.New("db down")
	subject, body = sender.resolveAuthEmail(context.Background(), 1, TemplateKindVerification, vars, "默认主题", "默认正文")
	if subject != "默认主题" || body != "默认正文" {
		t.Fatalf("store 读错未回退: %q / %q", subject, body)
	}
	store.loadErr = nil

	// 正文渲染失败(存量坏模板绕过保存校验)→ 只有正文回退,主题覆盖仍生效。
	store.values[bodyKey] = "坏 {{ghost}}"
	subject, body = sender.resolveAuthEmail(context.Background(), 1, TemplateKindVerification, vars, "默认主题", "默认正文")
	if subject != "自定义主题 tok123" {
		t.Fatalf("主题应独立生效: %q", subject)
	}
	if body != "默认正文" {
		t.Fatalf("坏正文未回退默认: %q", body)
	}

	// 存量正文缺凭证占位符(渲染本身能成功)→ 也必须回退默认,否则收件人拿不到凭证
	// (变异:渲染前的 ValidateTemplateOverride 重验删掉 → 红)。
	store.values[bodyKey] = "<p>好看但没有凭证</p>"
	_, body = sender.resolveAuthEmail(context.Background(), 1, TemplateKindVerification, vars, "默认主题", "默认正文")
	if body != "默认正文" {
		t.Fatalf("缺凭证正文未回退默认: %q", body)
	}

	// 无任何覆盖 → 默认。
	store.values = StoredSettings{}
	subject, body = sender.resolveAuthEmail(context.Background(), 1, TemplateKindVerification, vars, "默认主题", "默认正文")
	if subject != "默认主题" || body != "默认正文" {
		t.Fatalf("无覆盖未用默认: %q / %q", subject, body)
	}
}

// 端到端:SendVerification 走覆盖模板出信;无主题覆盖时主题保持内置默认。
func TestSendVerificationUsesTemplateOverride(t *testing.T) {
	keys := testEmailKeys(t)
	raw := completeRawSettings(t, keys, 1)
	_, bodyKey := TemplateSettingKeys(TemplateKindVerification)
	raw[bodyKey] = "<p>专属正文 {{token}}</p>"
	store := &fakeSettingsStore{settings: map[int64]StoredSettings{1: raw}}

	var sent Message
	sender, err := BuildEmailSender(context.Background(), store, keys,
		WithSMTPDispatch(func(_ context.Context, _ SMTPSettings, msg Message) error {
			sent = msg
			return nil
		}))
	if err != nil {
		t.Fatalf("构建 sender 失败: %v", err)
	}
	user := userauth.User{TenantID: 1, Email: "a@b.c"}
	if err := sender.SendVerification(context.Background(), user, "tok9"); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	if !strings.Contains(sent.HTMLBody, "专属正文 tok9") {
		t.Fatalf("未用覆盖正文: %q", sent.HTMLBody)
	}
	if sent.Subject != "HUAKAI email verification" {
		t.Fatalf("无主题覆盖时应用默认主题: %q", sent.Subject)
	}
}
