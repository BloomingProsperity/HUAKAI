//go:build !embed

package webui

import "io/fs"

// Dist returns nil in the default build: no frontend is embedded, so the gateway
// keeps its plain 404 for unmatched paths. Build with `-tags embed` (after
// producing the static dist) to include the SPA.
func Dist() fs.FS { return nil }
