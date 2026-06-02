package releasecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const (
	patchedNextSecurityFloor    = "15.5.18"
	patchedNestedPostCSSFloor   = "8.5.10"
	sharpStubPath               = "local-packages/sharp-disabled"
	sharpStubDependency         = "file:./" + sharpStubPath
	nextNestedPostCSSPackageKey = "node_modules/next/node_modules/postcss"
)

type frontendPackageJSON struct {
	Dependencies map[string]string `json:"dependencies"`
}

type frontendPackageLock struct {
	Packages map[string]lockPackage `json:"packages"`
}

type lockPackage struct {
	Version              string            `json:"version"`
	Name                 string            `json:"name"`
	Resolved             string            `json:"resolved"`
	License              string            `json:"license"`
	Deprecated           string            `json:"deprecated"`
	Link                 bool              `json:"link"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

func TestFrontendNextDependencyStaysPastPatchedSecurityFloor(t *testing.T) {
	root := repoRoot(t)

	var manifest frontendPackageJSON
	readJSON(t, filepath.Join(root, "frontend", "package.json"), &manifest)
	nextManifestVersion := manifest.Dependencies["next"]
	if !semverAtLeast(nextManifestVersion, patchedNextSecurityFloor) {
		t.Fatalf("frontend package.json pins next=%q below patched security floor %s", nextManifestVersion, patchedNextSecurityFloor)
	}
	if got := manifest.Dependencies["sharp"]; got != sharpStubDependency {
		t.Fatalf("frontend package.json must pin sharp to the local MIT stub, got %q", got)
	}
	assertSharpStubPackage(t, root)
	assertNextConfigDisablesImageOptimizer(t, root)

	var lock frontendPackageLock
	readJSON(t, filepath.Join(root, "frontend", "package-lock.json"), &lock)
	assertNoCopyleftLicense(t, lock)

	nextLock, ok := lock.Packages["node_modules/next"]
	if !ok {
		t.Fatalf("frontend package-lock.json missing node_modules/next package entry")
	}
	if !semverAtLeast(nextLock.Version, patchedNextSecurityFloor) {
		t.Fatalf("frontend package-lock.json pins next=%q below patched security floor %s", nextLock.Version, patchedNextSecurityFloor)
	}
	if strings.TrimSpace(nextLock.Deprecated) != "" {
		t.Fatalf("frontend package-lock.json still marks next as deprecated: %s", nextLock.Deprecated)
	}

	nextEnvVersion, ok := nextLock.Dependencies["@next/env"]
	if !ok {
		t.Fatalf("next lock dependency @next/env missing")
	}
	if !semverAtLeast(nextEnvVersion, patchedNextSecurityFloor) {
		t.Fatalf("next lock dependency @next/env=%q below patched security floor %s", nextEnvVersion, patchedNextSecurityFloor)
	}
	for dep, version := range nextLock.OptionalDependencies {
		if strings.HasPrefix(dep, "@next/swc-") && !semverAtLeast(version, patchedNextSecurityFloor) {
			t.Fatalf("next lock optional dependency %s=%q below patched security floor %s", dep, version, patchedNextSecurityFloor)
		}
	}

	nextPostCSS, ok := lock.Packages[nextNestedPostCSSPackageKey]
	if !ok {
		t.Fatalf("frontend package-lock.json missing %s package entry", nextNestedPostCSSPackageKey)
	}
	if !semverAtLeast(nextPostCSS.Version, patchedNestedPostCSSFloor) {
		t.Fatalf("next nested postcss=%q below patched security floor %s", nextPostCSS.Version, patchedNestedPostCSSFloor)
	}

	sharpLink, ok := lock.Packages["node_modules/sharp"]
	if !ok {
		t.Fatalf("frontend package-lock.json missing node_modules/sharp link")
	}
	if !sharpLink.Link || sharpLink.Resolved != sharpStubPath {
		t.Fatalf("sharp must resolve to local MIT stub, got link=%v resolved=%q", sharpLink.Link, sharpLink.Resolved)
	}
	sharpStub, ok := lock.Packages[sharpStubPath]
	if !ok {
		t.Fatalf("frontend package-lock.json missing %s package", sharpStubPath)
	}
	if sharpStub.Name != "sharp" || !semverAtLeast(sharpStub.Version, "0.34.5") || sharpStub.License != "MIT" {
		t.Fatalf("sharp stub metadata mismatch: name=%q version=%q license=%q", sharpStub.Name, sharpStub.Version, sharpStub.License)
	}
}

func assertSharpStubPackage(t *testing.T, root string) {
	t.Helper()
	var stub lockPackage
	readJSON(t, filepath.Join(root, "frontend", sharpStubPath, "package.json"), &stub)
	if stub.Name != "sharp" || !semverAtLeast(stub.Version, "0.34.5") || stub.License != "MIT" {
		t.Fatalf("tracked sharp stub metadata mismatch: name=%q version=%q license=%q", stub.Name, stub.Version, stub.License)
	}
}

func assertNextConfigDisablesImageOptimizer(t *testing.T, root string) {
	t.Helper()
	config, err := os.ReadFile(filepath.Join(root, "frontend", "next.config.mjs"))
	if err != nil {
		t.Fatalf("read frontend next.config.mjs: %v", err)
	}
	if !strings.Contains(string(config), "unoptimized: true") {
		t.Fatalf("frontend next.config.mjs must keep Next image optimization disabled while sharp is a local stub")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func readJSON(t *testing.T, path string, dst any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func semverAtLeast(got, floor string) bool {
	g := parseSemver(got)
	f := parseSemver(floor)
	for i := range f {
		if g[i] != f[i] {
			return g[i] > f[i]
		}
	}
	return true
}

func parseSemver(version string) [3]int {
	version = strings.TrimSpace(version)
	version = strings.TrimLeft(version, "^~<>= v")
	if idx := strings.IndexAny(version, "-+"); idx >= 0 {
		version = version[:idx]
	}

	parts := strings.Split(version, ".")
	var parsed [3]int
	for i := 0; i < len(parsed) && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}
		}
		parsed[i] = n
	}
	return parsed
}

func assertNoCopyleftLicense(t *testing.T, lock frontendPackageLock) {
	t.Helper()
	for path, pkg := range lock.Packages {
		license := strings.TrimSpace(pkg.License)
		if path != "" && !pkg.Link && license == "" {
			t.Fatalf("frontend package-lock.json missing license at %s", path)
		}
		license = strings.ToUpper(license)
		if strings.Contains(license, "GPL") || strings.Contains(license, "AGPL") || strings.Contains(license, "LGPL") {
			t.Fatalf("frontend package-lock.json contains copyleft license at %s: %s", path, pkg.License)
		}
	}
}
