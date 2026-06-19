//go:build embed

package webui

import (
	"embed"
	"io/fs"
)

// embedded holds the static frontend produced by the build pipeline (vite build
// → dist/), compiled into the binary only under the `embed` tag.
//
//go:embed all:dist
var embedded embed.FS

// Dist returns the embedded static frontend rooted at the dist/ subtree.
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil
	}
	return sub
}
