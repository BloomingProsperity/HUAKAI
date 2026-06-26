// Package registry:别名规范化(D14)。
//
// 公开模型别名是运营者标识符,而非区分大小写的密钥。规范化以用于唯一索引
// 查询;在 `public_alias_display` 中按种子录入时的大小写原样保留,供审计
// 与 `/models` 端点输出使用。

package registry

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// AliasNormalize 把公开别名字符串映射为其查询形式。
//
// 步骤:
//  1. 去掉首尾空白(运营者常从文档粘贴)。
//  2. 做 NFC 规范化,使重音组合的变体合并。
//  3. 转小写。
//
// 结果即 `model_aliases.public_alias_normalized` 所存储、且查询所匹配的值。
// 原始大小写由调用方保留并存于 `public_alias_display`。
func AliasNormalize(alias string) string {
	if alias == "" {
		return ""
	}
	trimmed := strings.TrimFunc(alias, unicode.IsSpace)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(norm.NFC.String(trimmed))
}
