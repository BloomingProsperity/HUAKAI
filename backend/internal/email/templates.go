package email

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
)

// 鉴权邮件模板覆盖:租户可在邮件设置存储里为四类鉴权邮件自定义主题/正文。
// 键形态 email_template.<kind>.subject / email_template.<kind>.body,复用现有 k/v 存储(零 schema)。
// 渲染是 fail-safe 的:覆盖缺失、读取失败、出现未知占位符时一律回退内置默认正文,发送永不因模板中断。

const (
	TemplateKindVerification       = "verification"
	TemplateKindPasswordReset      = "password_reset"
	TemplateKindDeviceConfirmation = "device_confirmation"
	TemplateKindOAuthCode          = "oauth_code"

	templateSettingPrefix = "email_template."
)

// templatePlaceholderPattern 匹配 {{name}} 占位符(允许两侧空白)。
var templatePlaceholderPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z][a-zA-Z0-9_]*)\s*\}\}`)

// templateKindSpec 描述一类邮件允许的占位符与必含凭证占位符。
// credential 是"始终有值"的占位符(link 可能因未配前端 base URL 为空),
// 正文必须包含它,否则收件人拿不到任何可操作凭证。
type templateKindSpec struct {
	allowed    map[string]bool
	credential string
}

var templateKinds = map[string]templateKindSpec{
	TemplateKindVerification:       {allowed: map[string]bool{"link": true, "token": true}, credential: "token"},
	TemplateKindPasswordReset:      {allowed: map[string]bool{"link": true, "token": true, "email": true}, credential: "token"},
	TemplateKindDeviceConfirmation: {allowed: map[string]bool{"link": true, "token": true}, credential: "token"},
	TemplateKindOAuthCode:          {allowed: map[string]bool{"code": true}, credential: "code"},
}

// TemplateKinds 返回全部模板 kind(稳定排序),供管理面枚举。
func TemplateKinds() []string {
	kinds := make([]string, 0, len(templateKinds))
	for k := range templateKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// TemplateAllowedPlaceholders 返回某 kind 允许的占位符(稳定排序);未知 kind 返回 nil。
func TemplateAllowedPlaceholders(kind string) []string {
	spec, ok := templateKinds[kind]
	if !ok {
		return nil
	}
	names := make([]string, 0, len(spec.allowed))
	for n := range spec.allowed {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// TemplateSettingKeys 返回某 kind 的 (subjectKey, bodyKey);未知 kind 返回空串。
func TemplateSettingKeys(kind string) (string, string) {
	if _, ok := templateKinds[kind]; !ok {
		return "", ""
	}
	return templateSettingPrefix + kind + ".subject", templateSettingPrefix + kind + ".body"
}

// ValidateTemplateOverride 校验保存前的模板覆盖。空串合法(表示清除覆盖)。
// 正文非空时必须包含该 kind 的凭证占位符;主题/正文都拒绝未知占位符。
func ValidateTemplateOverride(kind, subject, body string) error {
	spec, ok := templateKinds[kind]
	if !ok {
		return fmt.Errorf("%w: unknown template kind %q", ErrEmailSettingsInvalid, kind)
	}
	if err := checkTemplatePlaceholders(spec, subject); err != nil {
		return fmt.Errorf("%w: subject %v", ErrEmailSettingsInvalid, err)
	}
	if err := checkTemplatePlaceholders(spec, body); err != nil {
		return fmt.Errorf("%w: body %v", ErrEmailSettingsInvalid, err)
	}
	if strings.TrimSpace(body) != "" && !templateContainsPlaceholder(body, spec.credential) {
		return fmt.Errorf("%w: body must contain {{%s}}", ErrEmailSettingsInvalid, spec.credential)
	}
	return nil
}

func checkTemplatePlaceholders(spec templateKindSpec, text string) error {
	for _, m := range templatePlaceholderPattern.FindAllStringSubmatch(text, -1) {
		if !spec.allowed[m[1]] {
			return fmt.Errorf("unknown placeholder {{%s}}", m[1])
		}
	}
	return nil
}

func templateContainsPlaceholder(text, name string) bool {
	for _, m := range templatePlaceholderPattern.FindAllStringSubmatch(text, -1) {
		if m[1] == name {
			return true
		}
	}
	return false
}

// RenderTemplate 用 vars 渲染 HTML 正文模板。出现 vars 之外的占位符返回错误(调用方回退默认)。
// 值统一 SanitizeHeaderValue + HTML 转义;link 例外——保持 URL 原样仅做属性安全转义,
// 与内置正文的 href 处理一致,自定义模板可把 {{link}} 放进 href 或纯文本两用。
func RenderTemplate(tpl string, vars map[string]string) (string, error) {
	return renderTemplateWith(tpl, vars, func(name, safe string) string {
		if name == "link" {
			return strings.NewReplacer(`"`, "%22", `<`, "%3C", `>`, "%3E").Replace(safe)
		}
		return html.EscapeString(safe)
	})
}

// RenderSubjectTemplate 渲染邮件主题:主题是纯文本头字段,不做 HTML 转义
// (否则 sales&ops@example.com 会显示成 sales&amp;ops@...),只做头字段消毒。
func RenderSubjectTemplate(tpl string, vars map[string]string) (string, error) {
	return renderTemplateWith(tpl, vars, func(_, safe string) string { return safe })
}

func renderTemplateWith(tpl string, vars map[string]string, encode func(name, safe string) string) (string, error) {
	var renderErr error
	out := templatePlaceholderPattern.ReplaceAllStringFunc(tpl, func(m string) string {
		name := templatePlaceholderPattern.FindStringSubmatch(m)[1]
		val, ok := vars[name]
		if !ok {
			if renderErr == nil {
				renderErr = fmt.Errorf("unknown placeholder {{%s}}", name)
			}
			return m
		}
		return encode(name, SanitizeHeaderValue(val))
	})
	if renderErr != nil {
		return "", renderErr
	}
	return out, nil
}

// resolveAuthEmail 决定一封鉴权邮件的最终主题与正文:
// 覆盖存在且渲染成功 → 用覆盖;任何异常(store 读错/覆盖为空/渲染失败)→ 回退内置默认。
// 主题与正文独立回退,永不返回错误——模板问题绝不能阻断 auth 邮件送达。
// 额外一次 store.Load 相对冷却闸限频的发送频率可忽略。
func (s *AuthSender) resolveAuthEmail(ctx context.Context, tenantID int64, kind string, vars map[string]string, defaultSubject, defaultBody string) (string, string) {
	subject, body := defaultSubject, defaultBody
	if s == nil || s.store == nil {
		return subject, body
	}
	raw, err := s.store.Load(ctx, tenantID)
	if err != nil {
		return subject, body
	}
	subjectKey, bodyKey := TemplateSettingKeys(kind)
	if subjectKey == "" {
		return subject, body
	}
	if tpl := strings.TrimSpace(raw[subjectKey]); tpl != "" {
		if rendered, err := RenderSubjectTemplate(tpl, vars); err == nil {
			subject = rendered
		}
	}
	// 正文渲染前重跑完整校验:存量坏模板(旧版本/人工写库绕过保存校验)若缺凭证
	// 占位符,渲染虽能成功但收件人拿不到任何凭证——此时同样回退内置默认。
	if tpl := strings.TrimSpace(raw[bodyKey]); tpl != "" && ValidateTemplateOverride(kind, "", tpl) == nil {
		if rendered, err := RenderTemplate(tpl, vars); err == nil {
			body = rendered
		}
	}
	return subject, body
}
