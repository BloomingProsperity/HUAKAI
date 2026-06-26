package modulecatalog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// featureTreePath 是相对于本包目录的项目 feature tree
//(backend/internal/modulecatalog -> 仓库根 docs/...)。
const featureTreePath = "../../../docs/process/feature-tree/feature-tree.json"

// committedCatalogPath 是嵌入产物在磁盘上的位置。
const committedCatalogPath = "module-catalog.json"

// TestEmbeddedCatalogParses —— 已签入的产物必须能加载且非空。
// 回归:若 module-catalog.json 被手改成非法 JSON,或生成器产出 loader 无法
// 解码的形状,Load() 会报错 -> 转红。非空断言还能捕获意外被清空的产物。
func TestEmbeddedCatalogParses(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load embedded catalog: %v", err)
	}
	if len(c.Modules) == 0 {
		t.Fatalf("embedded catalog has 0 modules — generator regression or blanked artifact")
	}
	// 必须存在一个已知的 money-path 包,以便 Lookup 能针对真实数据被演练
	//(seed 约定依赖 billing 被编入 catalog)。
	if _, ok := c.Lookup("billing"); !ok {
		t.Fatalf("catalog missing 'billing' entry; Lookup over real data failed")
	}
}

// TestGeneratorOutputParsesAndMapsSection —— 生成器输出格式良好,且携带
// section/feature 映射(而不仅是裸的 pkg 名)。
// 回归:若 GenerateFromBytes 丢弃了 section/feature_id 的接线(例如停止复制
// lf.section),billing 条目的 Section 会为空 -> 转红。
func TestGeneratorOutputParsesAndMapsSection(t *testing.T) {
	cat, err := GenerateFromFile(featureTreePath)
	if err != nil {
		t.Fatalf("GenerateFromFile: %v", err)
	}
	if len(cat.Modules) == 0 {
		t.Fatalf("generated 0 modules")
	}
	m, ok := cat.Lookup("billing")
	if !ok {
		t.Fatalf("generated catalog missing 'billing'")
	}
	if m.Section == "" || m.FeatureID == "" {
		t.Fatalf("billing entry missing section/feature mapping: %+v", m)
	}
	// 合成的非 Go 标记绝不能被索引为模块。
	if _, ok := cat.Lookup("(rust)"); ok {
		t.Fatalf("synthetic pkg '(rust)' leaked into catalog as a module")
	}
}

// TestCatalogStalenessGuard —— 漂移守卫。它从实时 feature tree 在内存中重新
// 生成 catalog,并与已签入的 module-catalog.json 做逐字节比较。
//
// 一句话回归:若有人编辑了 docs/.../feature-tree.json(新增模块、改了
// status/parity)却忘记重跑 modulecatalog-gen,已签入的产物就不再匹配全新的
// 重新生成,此测试转红 —— 这样静态 catalog 永远不会与 feature tree 悄悄失同步。
func TestCatalogStalenessGuard(t *testing.T) {
	cat, err := GenerateFromFile(featureTreePath)
	if err != nil {
		t.Fatalf("regenerate from feature tree: %v", err)
	}
	fresh, err := cat.MarshalDeterministic()
	if err != nil {
		t.Fatalf("marshal fresh catalog: %v", err)
	}
	committed, err := os.ReadFile(committedCatalogPath)
	if err != nil {
		t.Fatalf("read committed catalog: %v", err)
	}
	if !bytes.Equal(bytes.TrimRight(fresh, "\n"), bytes.TrimRight(committed, "\n")) {
		t.Fatalf("module-catalog.json is STALE vs feature-tree.json.\n"+
			"Run: (cd %s && go run ./cmd/modulecatalog-gen)\n"+
			"then commit internal/modulecatalog/module-catalog.json.",
			backendDirHint())
	}
}

func backendDirHint() string {
	if abs, err := filepath.Abs("../.."); err == nil {
		return abs
	}
	return "backend"
}
