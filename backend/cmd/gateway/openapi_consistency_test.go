// openapi_consistency_test 覆盖 P2.3：docs/openapi/openapi.yaml 与
// cmd/gateway 实际 chi 路由的一致性检查。
//
// 验证目标（Owner deep-review 2026-05-17）：
//   - spec 中声明的 45 条 path 都能在 main.go mountRoutes 后真实命中
//   - main.go 注册但 spec 未声明的 path 必须列出（用于触发文档补救）
//
// 与 main.go 同 package main：直接调用 mountRoutes(nil-deps)，依赖：
//   - mountRoutes 自身只用 chi 注册，不读 deps 字段
//   - handler 内部对 nil-deps 的处理留给运行期；本测试只验 path 集合
package main

import (
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/openapicheck"
)

// 构造与 main.go run() 路径等价的 chi 路由树（仅 path 维度，handler
// body 不会被执行）。这要求 mountRoutes 在 nil-deps 下也能完成注册。
func buildTestRouter(t *testing.T) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	logger := zap.NewNop()
	// mountRoutes 在注册期会 deref d.cfg.BillingPolicyVersion / RequestClass，
	// 因此 deps.cfg 必须非 nil；其它字段保持零值（handler 本身不会 invoke）。
	d := &deps{
		cfg: &config.Config{
			BillingPolicyVersion: "test-1.0",
			RequestClass:         "standard",
		},
	}
	mountRoutes(r, d, logger)
	return r
}

// 主一致性测试：用 openapicheck.Compare 算 spec ↔ impl 漂移。
// 期望：spec_only 子集为空（或仅有已知占位）；impl_only 列出额外暴露
// 的 path 但不 fail（main.go 有 /debug/vars、/v1/audit/* 等系统级
// endpoint 不在 OpenAPI 范围内）。
func TestOpenAPI_ImplementationConsistency(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("解析 spec path: %v", err)
	}
	specPaths, err := openapicheck.ParseSpecPaths(specAbs)
	if err != nil {
		t.Fatalf("解析 OpenAPI spec %s: %v", specAbs, err)
	}
	if len(specPaths) == 0 {
		t.Fatalf("spec 解析出 0 path — parser 漏了 paths: 块？")
	}

	r := buildTestRouter(t)
	implPaths := openapicheck.WalkChiPaths(r)
	if len(implPaths) == 0 {
		t.Fatalf("impl 走出 0 path — mountRoutes 未注册任何 chi 路由")
	}

	rep := openapicheck.Compare(specPaths, implPaths)
	t.Logf("openapi-check report:\n%s", openapicheck.FormatReport(rep))

	// 主断言：spec 声明的每条 path 都必须有 impl 兜底。
	// 这一条 fail 意味着前端 / 第三方按 OpenAPI 调用会撞 404。
	// 软白名单（KnownSpecOnly）允许列出"spec 已声明但 impl 暂未落地"
	// 的占位条目；目前为空。Owner / PM 添加时必须附 PR ref + 闭环 ETA。
	knownSpecOnly := map[string]struct{}{}
	residualSpecOnly := make([]string, 0, len(rep.SpecOnly))
	for _, p := range rep.SpecOnly {
		if _, ok := knownSpecOnly[p]; !ok {
			residualSpecOnly = append(residualSpecOnly, p)
		}
	}
	if len(residualSpecOnly) > 0 {
		t.Errorf("OpenAPI 声明但 main.go 未注册的 %d 条 path（白名单后剩余）：%v",
			len(residualSpecOnly), residualSpecOnly)
	}

	// 软断言：impl 注册但 spec 未声明的 path 用 Log 报，不 fail。
	// 已知系统级 endpoint（/v1/audit/verify、/v1/audit/merkle-tree.json、
	// /v1/auth/social/identity-changed 内部 webhook）不进 OpenAPI；
	// /admin/v1/cache/l2/* / /admin/v1/provider-accounts/.../credentials/*
	// 等业务路由若长期不进 spec 则需要补文档 — 这里 Log 出来让
	// PM 在下次 OpenAPI 校准 wave 闭环。
	if len(rep.ImplOnly) > 0 {
		t.Logf("[INFO] impl 注册但 OpenAPI 未声明的 %d 条 path（需文档跟进）：%v",
			len(rep.ImplOnly), rep.ImplOnly)
	}
}

// 单独覆盖 parser，避免 spec 文件结构变动时一致性测试整个报错却
// 看不清原因。
func TestOpenAPI_ParserSmoke(t *testing.T) {
	specAbs, err := filepath.Abs("../../../docs/openapi/openapi.yaml")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	paths, err := openapicheck.ParseSpecPaths(specAbs)
	if err != nil {
		t.Fatalf("ParseSpecPaths: %v", err)
	}
	if len(paths) < 30 {
		t.Errorf("OpenAPI 解析出 %d 条 path — 当前 spec 应 ~45 条。"+
			"parser 退化或 spec 大幅缩减？", len(paths))
	}
	// 抽样断言几个 anchor path 必须能解析出。
	mustHave := []string{
		"/v1/chat/completions",
		"/v1/messages",
		"/admin/v1/api-keys",
		"/admin/v1/usage",
	}
	got := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		got[p] = struct{}{}
	}
	for _, p := range mustHave {
		if _, ok := got[p]; !ok {
			t.Errorf("parser 漏 anchor path %q", p)
		}
	}
}
