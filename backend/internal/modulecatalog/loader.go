package modulecatalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// embeddedCatalog is the checked-in, generated artifact. It is embedded (not
// read from disk at boot) so the gateway binary has zero runtime dependency on
// the docs/ tree and runs from any working directory. Regenerate it with
// `go run ./cmd/modulecatalog-gen` after editing the feature tree; the staleness
// guard test fails if this file drifts from the feature tree.
//
//go:embed module-catalog.json
var embeddedCatalog []byte

var (
	loadOnce sync.Once
	loaded   Catalog
	loadErr  error
)

// Load returns the embedded static catalog, parsed once and cached.
func Load() (Catalog, error) {
	loadOnce.Do(func() {
		loadErr = json.Unmarshal(embeddedCatalog, &loaded)
		if loadErr != nil {
			loadErr = fmt.Errorf("modulecatalog: parse embedded catalog: %w", loadErr)
		}
	})
	return loaded, loadErr
}

// MustLoad returns the embedded catalog or an empty catalog if it fails to
// parse. Used by wiring where a catalog-parse failure must not abort startup —
// the live registry still works without the static overlay.
func MustLoad() Catalog {
	c, err := Load()
	if err != nil {
		return Catalog{}
	}
	return c
}
