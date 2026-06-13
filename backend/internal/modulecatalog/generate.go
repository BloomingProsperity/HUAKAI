package modulecatalog

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// featureTree mirrors only the fields of feature-tree.json the generator needs.
// It is a read model — unknown fields are ignored.
type featureTree struct {
	Meta struct {
		Generated string `json:"generated"`
	} `json:"meta"`
	Sections []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Leaves []struct {
			Name   string   `json:"name"`
			FID    string   `json:"fid"`
			Pkgs   []string `json:"pkgs"`
			Stage  string   `json:"stage"`
			Parity string   `json:"parity"`
		} `json:"leaves"`
	} `json:"sections"`
}

// GenerateFromFile reads a feature-tree.json file and builds the Catalog.
func GenerateFromFile(path string) (Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("modulecatalog: read feature tree: %w", err)
	}
	return GenerateFromBytes(raw)
}

// GenerateFromBytes builds the Catalog from feature-tree.json bytes.
//
// One module entry is emitted per (leaf, pkg) pair: a leaf may list several
// packages and each becomes an addressable module pointing back at the same
// section/feature/status. Synthetic non-Go pkg markers like "(rust)" and
// "(frontend)" are skipped — the catalog indexes Go packages by short name.
// When two leaves both claim the same package, the first-seen (after sort) wins
// for the primary Section/FeatureID but every owning package set is preserved in
// Pkgs; ties are resolved deterministically by sorting leaves first so the
// output is stable across runs.
func GenerateFromBytes(raw []byte) (Catalog, error) {
	var ft featureTree
	if err := json.Unmarshal(raw, &ft); err != nil {
		return Catalog{}, fmt.Errorf("modulecatalog: parse feature tree: %w", err)
	}

	type leafRef struct {
		section   string
		featureID string
		title     string
		status    string
		parity    string
		pkgs      []string
	}
	// Collect a stable, sorted list of leaf references so the de-dup below is
	// deterministic regardless of map iteration / JSON ordering.
	var leaves []leafRef
	for _, sec := range ft.Sections {
		section := strings.TrimSpace(sec.ID + " " + sec.Name)
		for _, lf := range sec.Leaves {
			leaves = append(leaves, leafRef{
				section:   section,
				featureID: lf.FID,
				title:     lf.Name,
				status:    lf.Stage,
				parity:    lf.Parity,
				pkgs:      append([]string(nil), lf.Pkgs...),
			})
		}
	}
	sort.Slice(leaves, func(i, j int) bool {
		if leaves[i].featureID != leaves[j].featureID {
			return leaves[i].featureID < leaves[j].featureID
		}
		return leaves[i].title < leaves[j].title
	})

	seen := map[string]bool{}
	var modules []CatalogModule
	for _, lf := range leaves {
		for _, pkg := range lf.pkgs {
			pkg = strings.TrimSpace(pkg)
			if pkg == "" || isSyntheticPkg(pkg) {
				continue
			}
			if seen[pkg] {
				continue // first-seen (sorted) leaf owns the primary mapping
			}
			seen[pkg] = true
			modules = append(modules, CatalogModule{
				Pkg:       pkg,
				Section:   lf.section,
				FeatureID: lf.featureID,
				Title:     lf.title,
				Status:    lf.status,
				Parity:    lf.parity,
				Pkgs:      append([]string(nil), lf.pkgs...),
			})
		}
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Pkg < modules[j].Pkg })

	return Catalog{
		Source:    "docs/process/feature-tree/feature-tree.json",
		Generated: ft.Meta.Generated,
		Modules:   modules,
	}, nil
}

// isSyntheticPkg reports whether a feature-tree pkg marker is a non-Go-package
// placeholder (e.g. "(rust)", "(frontend)") that should not be indexed.
func isSyntheticPkg(pkg string) bool {
	return strings.HasPrefix(pkg, "(") && strings.HasSuffix(pkg, ")")
}
