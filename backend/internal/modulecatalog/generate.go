package modulecatalog

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// featureTree 只镜像 feature-tree.json 中生成器需要的字段。它是一个读模型 ——
// 未知字段会被忽略。
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

// GenerateFromFile 读取一个 feature-tree.json 文件并构建 Catalog。
func GenerateFromFile(path string) (Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("modulecatalog: read feature tree: %w", err)
	}
	return GenerateFromBytes(raw)
}

// GenerateFromBytes 从 feature-tree.json 字节构建 Catalog。
//
// 每个 (leaf, pkg) 对产出一个 module 条目:一个叶子可能列出数个包,每个包都
// 成为一个可寻址的模块,反向指回同一个 section/feature/status。像 "(rust)"
// 和 "(frontend)" 这样的合成非 Go 包标记会被跳过 —— catalog 按短名索引 Go
// 包。当两个叶子都声明同一个包时,(排序后)首次出现的胜出,作为主要的
// Section/FeatureID,但每个所属包集合都会被保留在 Pkgs 中;平局通过先对叶子
// 排序而被确定性地解决,因此输出在多次运行间保持稳定。
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
	// 收集一个稳定、有序的叶子引用列表,使下面的去重不受 map 迭代 / JSON
	// 排序影响,保持确定性。
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
				continue // (排序后)首次出现的叶子拥有主要映射
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

// isSyntheticPkg 报告一个 feature-tree 的 pkg 标记是否为不应被索引的非 Go 包
// 占位符(例如 "(rust)"、"(frontend)")。
func isSyntheticPkg(pkg string) bool {
	return strings.HasPrefix(pkg, "(") && strings.HasSuffix(pkg, ")")
}
