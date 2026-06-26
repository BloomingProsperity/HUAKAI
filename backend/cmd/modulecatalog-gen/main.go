// Package main 是 `modulecatalog-gen` 工具：它读取项目功能树
// (docs/process/feature-tree/feature-tree.json)，生成签入仓库的
// 静态模块目录，供 internal/modulecatalog 通过 go:embed 消费。
//
// 用法（在 backend/ 目录下）：
//
//	go run ./cmd/modulecatalog-gen
//	go run ./cmd/modulecatalog-gen \
//	    --feature-tree ../docs/process/feature-tree/feature-tree.json \
//	    --out internal/modulecatalog/module-catalog.json
//
// 退出码：
//
//	0 = 已写出目录（或本已是最新）
//	1 = 生成 / IO 错误
//
// 陈旧性守卫（internal/modulecatalog 的 staleness 测试）会在内存中重新生成，
// 并与签入的 module-catalog.json 做 diff，因此改了功能树却没重跑本工具，
// 会让单元测试门失败。
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
