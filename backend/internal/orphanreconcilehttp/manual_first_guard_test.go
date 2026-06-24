package orphanreconcilehttp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManualFirstNoAutomaticBackCharge 是 Manual-First 的架构守卫(命门 D):
// 追扣的唯一动钱入口 mediatask.ReconcileOrphan 绝不能被任何 worker / 定时器 / cron / 后台
// goroutine 引用——它只应由本 admin http 包的 reconcile handler 同步显式调用。
//
// 做法:扫描整个 backend 源码树(排除测试文件),找出所有引用 "ReconcileOrphan(" 的生产文件,
// 断言除 ReconcileOrphan 的定义文件(mediatask 包内)外,唯一的调用方文件就是本包的 reconcile.go。
//
// 判别(变异):若有人在 worker.go / 任何 *_worker.go / cron / loop 里加一行自动追扣调用
// ReconcileOrphan(...) → 本测试发现一个非白名单的引用文件 → RED。这就是"绝无自动扣费"的钉死。
func TestManualFirstNoAutomaticBackCharge(t *testing.T) {
	root := backendRoot(t)

	// 允许引用 ReconcileOrphan 的生产文件白名单(相对 backend 根):
	//   - 定义本身(mediatask 包)
	//   - 唯一调用方(本 admin http 包的 reconcile handler)
	//   - 路由接线(把 handler 挂到 admin 路由,本身不调用动钱,只构造 handler)
	allowed := map[string]bool{
		filepath.Join("internal", "mediatask", "store_orphan_backcharge.go"): true, // 定义本身
		filepath.Join("internal", "orphanreconcilehttp", "reconcile.go"):     true, // 唯一调用方(admin handler)
		filepath.Join("internal", "orphanreconcilehttp", "routes.go"):        true, // orphanStore 接口方法签名声明
	}

	var offenders []string
	walkGoProductionFiles(t, root, func(rel, content string) {
		// 只看代码行,剥掉注释(避免 wiring 等处的中文文档注释提及入口名造成误报)。
		code := stripLineComments(content)
		if !strings.Contains(code, "ReconcileOrphan(") && !strings.Contains(code, "captureOrphanHold(") {
			return
		}
		if allowed[rel] {
			return
		}
		offenders = append(offenders, rel)
	})

	if len(offenders) != 0 {
		t.Fatalf("发现非白名单文件引用孤儿追扣入口(Manual-First 违规,可能引入自动/定时扣费): %v\n"+
			"追扣只能由 admin reconcile handler 同步调用;如确需新增调用方,请评审是否破坏 Manual-First 再加入白名单。",
			offenders)
	}

	// 额外硬断言:worker.go 这种后台循环文件绝不能出现追扣入口。
	workerPath := filepath.Join(root, "internal", "mediatask", "worker.go")
	if b, err := os.ReadFile(workerPath); err == nil {
		if strings.Contains(string(b), "ReconcileOrphan(") || strings.Contains(string(b), "captureOrphanHold(") {
			t.Fatalf("mediatask/worker.go 不得引用孤儿追扣入口(后台 worker 自动扣费=Manual-First 红线)")
		}
	}
}

// stripLineComments 去掉每行 `//` 之后的内容,使文档注释中提及入口名不被误判为调用。
// 简化处理(不解析字符串字面量内的 //),对本守卫场景足够:入口名只出现在代码或注释里。
func stripLineComments(content string) string {
	var b strings.Builder
	for _, line := range strings.Split(content, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// backendRoot 定位 backend 模块根(本测试文件位于 internal/orphanreconcilehttp/ 下,上溯两级)。
func backendRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// wd = .../backend/internal/orphanreconcilehttp
	root := filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("未在预期路径找到 backend/go.mod(root=%s): %v", root, err)
	}
	return root
}

func walkGoProductionFiles(t *testing.T, root string, fn func(rel, content string)) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "vendor" || info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		fn(rel, string(b))
		return nil
	})
	if err != nil {
		t.Fatalf("walk backend tree: %v", err)
	}
}
