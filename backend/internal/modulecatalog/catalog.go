// Package modulecatalog 是模块知识主干的静态(方案 A)覆盖层。它持有关于每个
// HUAKAI 模块的、已签入的生成知识 —— 它属于哪个 section、它的 feature id、
// parity/stage 状态,以及实现它的 Go 包 —— 这些都派生自项目 feature tree
//(docs/process/feature-tree/feature-tree.json)。
//
// 这是「模块是什么」的那一半。实时的 moduleregistry 是「模块此刻在做什么」的
// 那一半。两者在 admin 端点处合并。
//
// catalog JSON 由 backend/cmd/modulecatalog-gen 生成,并以 module-catalog.json
// 签入本包,再通过 go:embed 嵌入。我们采用嵌入(而非在启动时读取某个路径),
// 这样二进制在运行时对 docs/ 没有任何文件系统依赖 —— 网关可从任意工作目录
// 运行 —— 同时陈旧性守卫测试可以在不触碰嵌入副本的情况下,将已签入的产物与
// 一次全新的重新生成做 diff。
package modulecatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// CatalogModule 是一个模块的静态知识条目。
type CatalogModule struct {
	// Pkg 是 Go 包短名(例如 "billing"),在 Modules 内唯一。
	Pkg string `json:"pkg"`
	// Section 是该模块所属的 feature-tree section(例如 "§5 计费")。
	Section string `json:"section"`
	// FeatureID 是 feature-tree 叶子标识符(例如 "F-BILL-001")。
	FeatureID string `json:"feature_id"`
	// Title 是 feature-tree 叶子名称(人类可读)。
	Title string `json:"title"`
	// Status 是来自 feature tree 的实现阶段(例如 "tested"、"wired"、
	// "planned")。
	Status string `json:"status"`
	// Parity 是来自 feature tree 的简短「与参考的 parity」备注。
	Parity string `json:"parity,omitempty"`
	// Pkgs 是所属 feature 叶子列出的完整包集合(一个叶子可横跨数个包);
	// Pkg 是该集合中的一个成员。
	Pkgs []string `json:"pkgs,omitempty"`
}

// Catalog 是顶层的生成产物。
type Catalog struct {
	// Source 记录 feature-tree 的生成标记,以便漂移可被审计。
	Source string `json:"source"`
	// Generated 是被原样复制过来的 feature-tree "generated" 日期。
	Generated string `json:"generated"`
	// Modules 按 Pkg 排序,以保证确定性输出。
	Modules []CatalogModule `json:"modules"`
}

// Lookup 返回包短名对应的 catalog 条目。
func (c Catalog) Lookup(pkg string) (CatalogModule, bool) {
	for _, m := range c.Modules {
		if m.Pkg == pkg {
			return m, true
		}
	}
	return CatalogModule{}, false
}

// MarshalDeterministic 以稳定的 key/字段顺序和一个末尾换行符来编码 catalog,
// 这样生成器输出与陈旧性守卫的重新生成能逐字节匹配。编码前 Modules 会按 Pkg
// 排序。
func (c Catalog) MarshalDeterministic() ([]byte, error) {
	sort.Slice(c.Modules, func(i, j int) bool { return c.Modules[i].Pkg < c.Modules[j].Pkg })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // 在已签入的产物中保持 §/CJK 可读
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("modulecatalog: marshal: %w", err)
	}
	return buf.Bytes(), nil
}
