//go:build embed

package webui

import (
	"embed"
	"io/fs"
)

// embedded 持有构建管线产出的静态前端（vite build → dist/），
// 仅在 `embed` build tag 下才编译进二进制。
//
//go:embed all:dist
var embedded embed.FS

// Dist 返回以 dist/ 子树为根的内嵌静态前端。
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil
	}
	return sub
}
