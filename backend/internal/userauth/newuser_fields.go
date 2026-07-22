package userauth

import (
	"net/mail"
	"strings"
	"unicode/utf8"
)

// newuser_fields.go — 四个建号入口(公开密码注册 / 社交补邮箱 / 管理端建用户 / 首装)共用的
// 单一字段校验真相源,消除各入口口径漂移(弱口令、不可投递邮箱、超长/控制字符名进库)。
//
// 注:口令长度校验刻意放在建号入口(本文件),不放进 HashPassword——后者也被 ResetPassword
// 等复用,在那里加长度上限会改动既有语义。散列成本参数见 PasswordPolicy,与本文件无关。

const (
	// MinPasswordLength / MaxPasswordLength 与首装入口(setuphttp)历史口径一致(8-128,按 rune 计)。
	MinPasswordLength = 8
	MaxPasswordLength = 128
	// MaxEmailLength 取 RFC 5321 地址上限,与首装入口一致。
	MaxEmailLength = 254
)

// ValidateNewUserEmail 规范化并校验邮箱:小写+trim,只接受裸地址(拒 "Name <a@b.c>" 显示名形态,
// 否则整串入库会导致该账号无法用裸邮箱登录),限长。返回可直接入库的规范化邮箱。
func ValidateNewUserEmail(raw string) (string, error) {
	email := NormalizeEmail(raw)
	if email == "" || len(email) > MaxEmailLength {
		return "", ErrInvalidInput
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", ErrInvalidInput
	}
	return email, nil
}

// ValidateNewUserPassword 校验口令长度上下限(按 rune 计,与前端 maxLength 同口径,避免多字节
// 输入前端放行后端按字节拒的漂移)。不校验强度组成(交由 argon2 成本与限流承担)。
func ValidateNewUserPassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLength || n > MaxPasswordLength {
		return ErrInvalidInput
	}
	return nil
}

// ValidateOptionalDisplayName 允许空名(可选语义:注册不收名、管理端可留空);非空则复用
// NormalizeDisplayName 校验 UTF-8/控制字符/长度并返回规范化值。
func ValidateOptionalDisplayName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", nil
	}
	return NormalizeDisplayName(name)
}
