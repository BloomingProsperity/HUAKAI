//go:build !embed

package webui

import "io/fs"

// Dist 在默认构建中返回 nil:没有内嵌前端，所以 gateway 对未匹配的路径保持
// 朴素的 404。用 `-tags embed`(在生成静态 dist 之后)构建即可内嵌 SPA。
func Dist() fs.FS { return nil }
