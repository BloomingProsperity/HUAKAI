// Package openapicheck 比对 docs/openapi/openapi.yaml 与 cmd/gateway
// 注册的 chi 路由，报漂移。
//
// 设计取舍：
//   - spec 的 paths 和 operation 由轻量行解析器提取，权威文件另有严格
//     YAML 解析测试守住语法和重复键。若以后改变缩进或引入 anchor，需同步
//     升级提取器及其判别测试。
//   - method 维度与 path 一起比较。否则 spec 写 GET、runtime 挂 POST
//     会被 path-only 检查误判为一致。
//   - 路径归一：把 `{param}` 与 chi 的 `:param` 都归一为 `:_`。这样
//     param 名字漂移（id ↔ flow_id）不会误报；只在结构差异上报警。
//   - 后向兼容 alias 列在 KnownAliases，比对前展开到 canonical form。
package openapicheck

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Report 是一次对比的结果。Common = 两边都有；SpecOnly = 只 spec 有；
// ImplOnly = 只 impl 有。比对前已套用 normalization + alias 展开。
// method-aware 对比会把条目格式化为 "METHOD /path"。
type Report struct {
	Common   []string
	SpecOnly []string
	ImplOnly []string
}

// Operation 是 OpenAPI / chi 的 method + path 合同单元。
type Operation struct {
	Method string
	Path   string
}

// KnownAliases 列出后向兼容的 path alias：spec 用 canonical，impl 可能
// 额外注册了 alias 形态。比对时把 alias 全部映射到 canonical 再 dedup。
//
// 维护：commit 这个 map 时要附 PR ref 说明为什么这条 alias 必要、
var KnownAliases = map[string]string{
	// 临时兼容：/v1/admin/pool-accounts/* → 规范路径
	// /admin/v1/provider-accounts/*。
	"/v1/admin/pool-accounts": "/admin/v1/provider-accounts",
	// 临时兼容：/v1/admin/provider-accounts 是 main.go 路径上的双注册
	// （line 895-896）；spec 只暴露规范路径 /admin/v1/provider-accounts。
	"/v1/admin/provider-accounts": "/admin/v1/provider-accounts",
	// 兼容带一个 slash 的 request_id：实现用两段 chi param 限界，
	// OpenAPI 仍用规范的 /v1/receipts/{request_id} 表达同一资源。
	"/v1/receipts/{request_id_host}/{request_id_tail}":        "/v1/receipts/{request_id}",
	"/v1/receipts/{request_id_host}/{request_id_tail}/verify": "/v1/receipts/{request_id}/verify",
}

// ParseSpecPaths 用行解析从 OpenAPI YAML 抽 paths 集合。
func ParseSpecPaths(path string) ([]string, error) {
	paths, _, err := parseSpec(path)
	return paths, err
}

// ParseSpecOperations 用行解析从 OpenAPI YAML 抽 method + path 集合。
func ParseSpecOperations(path string) ([]Operation, error) {
	_, ops, err := parseSpec(path)
	return ops, err
}

func parseSpec(path string) ([]string, []Operation, error) {
	f, err := os.Open(path) // #nosec G304 — caller-supplied spec path
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)
	// 顶层缩进 0 的 `paths:` 行进入 in-paths 模式；遇到下一个顶层 key 退出。
	pathLine := regexp.MustCompile(`^\s\s(/[A-Za-z0-9_\-{}./]+):\s*$`)
	methodLine := regexp.MustCompile(`^\s{4}(get|put|post|delete|options|head|patch|trace):\s*(?:\{\})?\s*$`)
	topKey := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*:\s*$`)
	inPaths := false
	var paths []string
	var ops []Operation
	currentPath := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !inPaths {
			if strings.TrimRight(line, " \t") == "paths:" {
				inPaths = true
			}
			continue
		}
		// 检测下一个顶层 key（缩进 0），退出 paths 块。
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			if topKey.MatchString(strings.TrimRight(line, " \t")) {
				break
			}
		}
		if m := pathLine.FindStringSubmatch(line); m != nil {
			currentPath = m[1]
			paths = append(paths, currentPath)
			continue
		}
		if currentPath != "" {
			if m := methodLine.FindStringSubmatch(line); m != nil {
				ops = append(ops, Operation{
					Method: strings.ToUpper(m[1]),
					Path:   currentPath,
				})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return paths, ops, nil
}

// WalkChiPaths 用 chi.Walk 把 router 注册的全部路径抽出（method 维度
// 合并为 path）。对 chi 的 `{param}` 与 `*` glob 不做特殊归一 — 留给
// normalize 阶段做。
func WalkChiPaths(r chi.Router) []string {
	seen := make(map[string]struct{})
	_ = chi.Walk(r, func(_ string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		seen[route] = struct{}{}
		return nil
	})
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// WalkChiOperations 用 chi.Walk 把 router 注册的 method + path 合同抽出。
// 此处保留规范路径和兼容别名的真实形态；别名只在最终合同比较时归一。
// 若在抽取阶段提前合并，规范入口被删而别名仍在时会产生假绿。
func WalkChiOperations(r chi.Router) []Operation {
	seen := make(map[string]Operation)
	_ = chi.Walk(r, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		op := Operation{Method: strings.ToUpper(method), Path: route}
		seen[rawOperationKey(op)] = op
		return nil
	})
	out := make([]Operation, 0, len(seen))
	for _, op := range seen {
		out = append(out, op)
	}
	sortOperations(out)
	return out
}

// ReadImplRoutesFile 读一行一路径的文本文件（# 开头是注释，空行忽略）。
// 给 CI 用：当 wiring 太重启动不下时，把路由 dump 到文件喂 openapi-check。
func ReadImplRoutesFile(path string) ([]string, error) {
	f, err := os.Open(path) // #nosec G304 — caller-supplied list path
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)
	seen := make(map[string]struct{})
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		seen[line] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// 用于把 `{anyName}` 归一为 `{}`，让 param 名漂移（id ↔ flow_id ↔ flowID）
// 不再误报 spec ↔ impl 不一致。
var paramNameRe = regexp.MustCompile(`\{[^}]+\}`)

// 用于把 chi mount path 中的 `/*` glob 段（chi 在 r.Mount("/") 注册时
// 会插入）去掉；spec 不会有这种段。
var chiMountGlobRe = regexp.MustCompile(`/\*(/|$)`)

// normalize 把 path 模板归一：
//   - 去掉末尾 `/`
//   - 应用 KnownAliases 把后向兼容 alias 展开到 canonical
//   - chi 的 `/*` mount-glob 段视为可选（spec 不写）
//   - 所有 `{name}` 归一为 `{}` — param 名漂移不算结构性差异
func normalize(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		p = "/"
	}
	for alias, canonical := range KnownAliases {
		if p == alias || strings.HasPrefix(p, alias+"/") {
			p = canonical + strings.TrimPrefix(p, alias)
			break
		}
	}
	return normalizeStructure(p)
}

// normalizeCanonical 只归一模板结构，不把兼容别名映射成规范路径。
// 它用于证明 OpenAPI 声明的规范入口本身真实注册，而不是由别名冒充。
func normalizeCanonical(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		p = "/"
	}
	return normalizeStructure(p)
}

func normalizeStructure(p string) string {
	// chi mount 引入的 `/*` 段：`/admin/v1/pools/*` → `/admin/v1/pools`,
	// `/admin/v1/pools/*/{id}` → `/admin/v1/pools/{id}`。
	for chiMountGlobRe.MatchString(p) {
		p = chiMountGlobRe.ReplaceAllString(p, "$1")
		p = strings.TrimRight(p, "/")
		if p == "" {
			p = "/"
		}
	}
	// param 名归一：`{id}` → `{}`、`{flow_id}` / `{flowID}` → `{}`。
	p = paramNameRe.ReplaceAllString(p, "{}")
	return p
}

func operationKey(op Operation) string {
	return strings.ToUpper(strings.TrimSpace(op.Method)) + " " + normalize(op.Path)
}

func canonicalOperationKey(op Operation) string {
	return strings.ToUpper(strings.TrimSpace(op.Method)) + " " + normalizeCanonical(op.Path)
}

func rawOperationKey(op Operation) string {
	return strings.ToUpper(strings.TrimSpace(op.Method)) + " " + strings.TrimSpace(op.Path)
}

func sortOperations(ops []Operation) {
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
}

// Compare 计算 spec ↔ impl 的 path 级漂移。
//
// 边界 case：
//   - 路径仅在 method 维度不一致（spec 有 POST 但 impl 注册了 PUT）
//     不在本检查范围；method 一致性用 CompareOperations。
//   - chi 的 r.Route("/x", ...) 注册时也会暴露 /x 这个父节点；
//     如果父节点没有真实 handler 也会被 Walk 列出。本工具不区分
//     真假 handler；多出一条 /admin/v1/provider-accounts 父节点
//     会和 spec 的同名 path 自然 match。
func Compare(specPaths, implPaths []string) Report {
	specSet := make(map[string]struct{}, len(specPaths))
	for _, p := range specPaths {
		specSet[normalize(p)] = struct{}{}
	}
	implSet := make(map[string]struct{}, len(implPaths))
	for _, p := range implPaths {
		implSet[normalize(p)] = struct{}{}
	}

	rep := Report{}
	for p := range specSet {
		if _, ok := implSet[p]; ok {
			rep.Common = append(rep.Common, p)
		} else {
			rep.SpecOnly = append(rep.SpecOnly, p)
		}
	}
	for p := range implSet {
		if _, ok := specSet[p]; !ok {
			rep.ImplOnly = append(rep.ImplOnly, p)
		}
	}
	sort.Strings(rep.Common)
	sort.Strings(rep.SpecOnly)
	sort.Strings(rep.ImplOnly)
	return rep
}

// CompareOperations 计算 method + path 合同漂移。
func CompareOperations(specOps, implOps []Operation) Report {
	return compareOperationSets(specOps, implOps, operationKey)
}

// CompareCanonicalOperations 不应用兼容别名映射，证明 OpenAPI 中每个规范
// method + path 都有同形态的生产入口。调用方通常只需阻断 SpecOnly；
// ImplOnly 还会包含已明确保留的兼容别名。
func CompareCanonicalOperations(specOps, implOps []Operation) Report {
	return compareOperationSets(specOps, implOps, canonicalOperationKey)
}

func compareOperationSets(specOps, implOps []Operation, key func(Operation) string) Report {
	specSet := make(map[string]struct{}, len(specOps))
	for _, op := range specOps {
		specSet[key(op)] = struct{}{}
	}
	implSet := make(map[string]struct{}, len(implOps))
	for _, op := range implOps {
		implSet[key(op)] = struct{}{}
	}

	rep := Report{}
	for op := range specSet {
		if _, ok := implSet[op]; ok {
			rep.Common = append(rep.Common, op)
		} else {
			rep.SpecOnly = append(rep.SpecOnly, op)
		}
	}
	for op := range implSet {
		if _, ok := specSet[op]; !ok {
			rep.ImplOnly = append(rep.ImplOnly, op)
		}
	}
	sort.Strings(rep.Common)
	sort.Strings(rep.SpecOnly)
	sort.Strings(rep.ImplOnly)
	return rep
}

// FormatReport 是文本格式化便捷函数，供 test 输出消息使用。
func FormatReport(rep Report) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "common=%d spec_only=%d impl_only=%d\n",
		len(rep.Common), len(rep.SpecOnly), len(rep.ImplOnly))
	if len(rep.SpecOnly) > 0 {
		sb.WriteString("spec_only:\n")
		for _, p := range rep.SpecOnly {
			fmt.Fprintf(&sb, "  - %s\n", p)
		}
	}
	if len(rep.ImplOnly) > 0 {
		sb.WriteString("impl_only:\n")
		for _, p := range rep.ImplOnly {
			fmt.Fprintf(&sb, "  - %s\n", p)
		}
	}
	return sb.String()
}

// ParseSpecPublicOperations 从 OpenAPI YAML 抽出**显式声明 `security: []`（即公开、不要求认证)**
// 的 method+path 操作。用途：把"契约声明的公开面"与实现实际挂的认证中间件做一致性校验，
// 防止"spec 标公开但实现要求认证（前端/第三方按 spec 当公开调用却撞 401）"这类契约漂移逃过 CI。
//
// 解析取舍（沿用本文件 §设计取舍的行解析风格，不引 yaml dep）：只识别**操作属性缩进（6-space）**
// 上的单行 `security: []`；多行 `security:` 列表（声明了具体 scheme）视为"非公开"，不在本检查范围。
// 遇下一个 method 行或 path 行即切换当前操作归属。
func ParseSpecPublicOperations(path string) ([]Operation, error) {
	f, err := os.Open(path) // #nosec G304 — caller-supplied spec path
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)
	pathLine := regexp.MustCompile(`^\s\s(/[A-Za-z0-9_\-{}./]+):\s*$`)
	methodLine := regexp.MustCompile(`^\s{4}(get|put|post|delete|options|head|patch|trace):\s*(?:\{\})?\s*$`)
	// 操作级 `security: []`（空数组 = OpenAPI 标准的"覆盖顶层安全、声明本操作公开"）。
	securityEmptyLine := regexp.MustCompile(`^\s{6}security:\s*\[\s*\]\s*$`)
	topKey := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*:\s*$`)
	inPaths := false
	currentPath := ""
	currentMethod := ""
	var ops []Operation
	seen := make(map[string]struct{})
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !inPaths {
			if strings.TrimRight(line, " \t") == "paths:" {
				inPaths = true
			}
			continue
		}
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && topKey.MatchString(strings.TrimRight(line, " \t")) {
			break
		}
		if m := pathLine.FindStringSubmatch(line); m != nil {
			currentPath = m[1]
			currentMethod = ""
			continue
		}
		if m := methodLine.FindStringSubmatch(line); m != nil {
			currentMethod = strings.ToUpper(m[1])
			continue
		}
		if currentPath != "" && currentMethod != "" && securityEmptyLine.MatchString(line) {
			op := Operation{Method: currentMethod, Path: currentPath}
			k := operationKey(op)
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				ops = append(ops, op)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sortOperations(ops)
	return ops, nil
}

// OperationsGatedByMiddleware 用 chi.Walk 抽出"中间件链里挂了名字匹配 marker 的中间件"的
// method+path 操作。chi 注册的中间件是闭包，无法按类型识别；改用 runtime 函数名判断——认证中间件
// 的闭包函数名形如 `...SessionMiddleware.1`，故 marker 传 "SessionMiddleware" 即可识别该路由是否
// 被会话认证守卫。供与 spec 的公开声明做一致性校验（见 SecurityContractDrift）。
func OperationsGatedByMiddleware(r chi.Router, marker string) []Operation {
	seen := make(map[string]Operation)
	_ = chi.Walk(r, func(method string, route string, _ http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		for _, mw := range middlewares {
			name := runtime.FuncForPC(reflect.ValueOf(mw).Pointer()).Name()
			if strings.Contains(name, marker) {
				op := Operation{Method: strings.ToUpper(method), Path: route}
				seen[operationKey(op)] = op
				break
			}
		}
		return nil
	})
	out := make([]Operation, 0, len(seen))
	for _, op := range seen {
		out = append(out, op)
	}
	sortOperations(out)
	return out
}

// SecurityContractDrift 返回"spec 声明 `security: []`（公开）但实现实际挂了认证中间件"的操作。
// 这类漂移会让按 spec 当公开调用的客户端撞 401，且是 IDOR 类回归（实现加了租户/会话校验却忘了
// 同步 spec，或反向）的征兆。比对在 method+path 维度做（operationKey 已套 normalize + alias 展开）。
func SecurityContractDrift(specPublic, implGated []Operation) []Operation {
	gatedSet := make(map[string]struct{}, len(implGated))
	for _, op := range implGated {
		gatedSet[operationKey(op)] = struct{}{}
	}
	var drift []Operation
	for _, op := range specPublic {
		if _, ok := gatedSet[operationKey(op)]; ok {
			drift = append(drift, op)
		}
	}
	sortOperations(drift)
	return drift
}
