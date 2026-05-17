// Package main 是 `openapi-check` 二进制：比对 docs/openapi/openapi.yaml
// 与 cmd/gateway 实际注册的 chi 路由，报漂移。
//
// 用 stdlib 行解析提取 OpenAPI `paths:` 块下 2-space 缩进的 `/xxx:` 行
// 作为 spec path 集合（不引 yaml dep — 约束：本仓库 go.mod 仅允许已有
// direct deps）。
//
// 实现路由侧则需调用 cmd/gateway 的 mountRoutes，但 mountRoutes 依赖
// 完整 deps（pgxpool / handlers）。为避免 import cycle 与重型初始化，
// 路由集合由 RouteRegistry 提供：测试 / build 时由调用方注入 chi
// router 后调用 walkChiPaths。
//
// 退出码：
//   0 = 一致或仅有可接受漂移（动态前缀、向后兼容 alias）
//   1 = 有 unresolved spec-only 或 impl-only 漂移
//   2 = 工具内部错误（读 spec 失败等）
//
// 用法：
//
//	openapi-check --spec docs/openapi/openapi.yaml
//
// 默认 spec 路径相对当前工作目录。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/openapicheck"
)

func main() {
	specPath := flag.String("spec", "docs/openapi/openapi.yaml",
		"path to OpenAPI 3.x YAML; relative paths resolve against CWD")
	implRoutes := flag.String("impl-routes", "",
		"newline-separated text file of impl routes (METHOD<TAB>PATH); "+
			"defaults to scanning a pre-built chi.Router (test mode only)")
	failOnMismatch := flag.Bool("fail-on-mismatch", true,
		"exit 1 if any spec-only or impl-only routes detected")
	flag.Parse()

	specAbs, err := filepath.Abs(*specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi-check: 解析 spec path 失败: %v\n", err)
		os.Exit(2)
	}
	specPaths, err := openapicheck.ParseSpecPaths(specAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi-check: 解析 spec 失败 %s: %v\n", specAbs, err)
		os.Exit(2)
	}

	var implPaths []string
	if *implRoutes != "" {
		implPaths, err = openapicheck.ReadImplRoutesFile(*implRoutes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "openapi-check: 读 impl-routes 失败 %s: %v\n", *implRoutes, err)
			os.Exit(2)
		}
	} else {
		// 命令行直跑场景：用空 chi router 走一次防 import 报错；
		// 真正的 wiring 校验由 cmd/gateway/openapi_consistency_test.go
		// 注入真正 router 跑。
		implPaths = openapicheck.WalkChiPaths(chi.NewRouter())
	}

	rep := openapicheck.Compare(specPaths, implPaths)

	fmt.Printf("openapi-check: spec=%d impl=%d common=%d spec_only=%d impl_only=%d\n",
		len(specPaths), len(implPaths), len(rep.Common), len(rep.SpecOnly), len(rep.ImplOnly))

	if len(rep.SpecOnly) > 0 {
		fmt.Println("--- spec_only (documented but not implemented) ---")
		sort.Strings(rep.SpecOnly)
		for _, p := range rep.SpecOnly {
			fmt.Printf("  - %s\n", p)
		}
	}
	if len(rep.ImplOnly) > 0 {
		fmt.Println("--- impl_only (implemented but undocumented) ---")
		sort.Strings(rep.ImplOnly)
		for _, p := range rep.ImplOnly {
			fmt.Printf("  - %s\n", p)
		}
	}

	if *failOnMismatch && (len(rep.SpecOnly) > 0 || len(rep.ImplOnly) > 0) {
		os.Exit(1)
	}
}
