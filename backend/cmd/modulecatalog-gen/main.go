// Package main is the `modulecatalog-gen` tool: it reads the project feature
// tree (docs/process/feature-tree/feature-tree.json) and emits the checked-in
// static module catalog consumed by internal/modulecatalog via go:embed.
//
// Usage (from backend/):
//
//	go run ./cmd/modulecatalog-gen
//	go run ./cmd/modulecatalog-gen \
//	    --feature-tree ../docs/process/feature-tree/feature-tree.json \
//	    --out internal/modulecatalog/module-catalog.json
//
// Exit codes:
//
//	0 = catalog written (or already up to date)
//	1 = generation / IO error
//
// The staleness guard (internal/modulecatalog staleness test) regenerates in
// memory and diffs against the committed module-catalog.json, so editing the
// feature tree without re-running this tool fails the unit gate.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/BloomingProsperity/HUAKAI/internal/modulecatalog"
)

func main() {
	featureTree := flag.String("feature-tree", "../docs/process/feature-tree/feature-tree.json",
		"path to feature-tree.json (relative to backend/)")
	out := flag.String("out", "internal/modulecatalog/module-catalog.json",
		"output path for the generated catalog (relative to backend/)")
	flag.Parse()

	if err := run(*featureTree, *out); err != nil {
		fmt.Fprintln(os.Stderr, "modulecatalog-gen:", err)
		os.Exit(1)
	}
}

func run(featureTreePath, outPath string) error {
	cat, err := modulecatalog.GenerateFromFile(featureTreePath)
	if err != nil {
		return err
	}
	data, err := cat.MarshalDeterministic()
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("modulecatalog-gen: wrote %d modules to %s\n", len(cat.Modules), outPath)
	return nil
}
