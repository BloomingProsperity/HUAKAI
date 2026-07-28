package main

import (
	"fmt"
	"sort"
	"strings"
)

func render(rows []row) []byte {
	var b strings.Builder
	b.WriteString("# HUAKAI 前后端双向追踪矩阵\n\n")
	b.WriteString("> 本文件由 `backend/cmd/frontend-traceability` 从当前 OpenAPI 与稳定页面归属规则生成。")
	b.WriteString("接口方法、路径、operationId、标签和安全声明不得手工修改；更新 OpenAPI 或页面归属后重新生成。\n\n")
	b.WriteString("## 1. 使用口径\n\n")
	b.WriteString("- 本矩阵回答“后端哪个模块，经哪个菜单和页面，由谁调用哪个精确接口”。\n")
	b.WriteString("- `后端已挂载` 只表示 OpenAPI 与生产路由 method+path 一致，不表示前端页面已经实现。\n")
	b.WriteString("- `SYS-INT` 是 Webhook、健康探针等服务端接口，不得为了追求页面覆盖率让浏览器直接调用。\n")
	b.WriteString("- 管理 operation 必须声明 `x-huakai-required-role`；非管理 operation 的角色由 OpenAPI security 推导，前端不得自行猜测权限。\n")
	b.WriteString("- 页面只使用工程设计手册定义的八个容器；角色可见性和写权限仍以后端认证上下文为准。\n\n")

	pageCounts := make(map[string]int)
	for _, item := range rows {
		pageCounts[item.Page]++
	}
	b.WriteString("## 2. 覆盖摘要\n\n")
	fmt.Fprintf(&b, "- OpenAPI operation：**%d** 个，全部进入本矩阵且每个只出现一次。\n", len(rows))
	b.WriteString("- 页面分布：")
	var pageKeys []string
	for page := range pageCounts {
		pageKeys = append(pageKeys, page)
	}
	sort.Strings(pageKeys)
	for i, page := range pageKeys {
		if i > 0 {
			b.WriteString("；")
		}
		fmt.Fprintf(&b, "`%s` %d", page, pageCounts[page])
	}
	b.WriteString("。\n")
	b.WriteString("- 机器门：OpenAPI 新增、删除或重命名 operation 后，生成结果不一致会使测试失败。\n\n")

	b.WriteString("## 3. 后端模块 → 前端页面 → 接口完整映射\n\n")
	b.WriteString("| 稳定追踪键 | 后端模块 | 主要源码包 | OpenAPI 标签 | 角色合同 | 页面 | 标签页/子场景 | 前端动作 | 方法 | 路径 | operationId | 当前状态 | 备注 |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, item := range rows {
		fmt.Fprintf(&b, "| `RTM:%s` | %s | `%s` | `%s` | %s | `%s` %s | %s | %s | `%s` | `%s` | `%s` | %s | %s |\n",
			escape(item.OperationID), escape(item.Module), escape(item.Package), escape(item.Tags), escape(item.Actor),
			item.Page, escape(item.Menu), escape(item.Scene), escape(item.Action), item.Method,
			escape(item.Path), escape(item.OperationID), escape(item.Status), escape(item.Remark))
	}

	b.WriteString("\n## 4. 前端需求 → 后端缺口\n\n")
	b.WriteString("下列需求当前没有可安全消费的完整 operation，前端不得用 mock、跨权限拼装或本地状态冒充完成。\n\n")
	b.WriteString("| 缺口编号 | 优先级 | 页面 | 后端模块 | 前端需要的结果 | 当前事实 |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, item := range gaps {
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s | %s |\n",
			item.ID, item.Priority, item.Page, escape(item.Module), escape(item.Need), escape(item.Current))
	}

	b.WriteString("\n## 5. 前端代码语言与工程边界\n\n")
	b.WriteString("| 层级 | 选择 | 强制边界 |\n")
	b.WriteString("| --- | --- | --- |\n")
	b.WriteString("| 业务代码语言 | TypeScript（严格模式） | 新业务代码不使用 JavaScript；类型来自 OpenAPI 生成结果，不手写第二套 DTO |\n")
	b.WriteString("| UI 框架 | React | 只负责视图组合和交互，不保存服务端权威身份、余额、权限或任务终态 |\n")
	b.WriteString("| 构建工具 | Vite | 只产出独立静态站点；Gateway 保持 API-only，不恢复 Go embed 或 SPA fallback |\n")
	b.WriteString("| 页面结构 | 八个业务容器按路由懒加载 | 三身份共享组件和数据模型，按真实权限显示页面与动作，不复制三套前端 |\n")
	b.WriteString("| API 接入 | OpenAPI 生成 TypeScript 客户端 | 页面不得自行拼接 Authorization、tenant scope、CSRF、幂等键或重试策略 |\n")
	b.WriteString("| HTML/CSS | 语义 HTML 与受设计系统约束的 CSS | 只承担结构、响应式和视觉；不得用隐藏按钮代替后端授权 |\n")
	b.WriteString("| 其他依赖 | 在前端初始化 PR 经维护状态、许可证、传递依赖和漏洞审计后锁定 | 本矩阵不提前指定路由、请求、表单、图表或测试库，防止未经审计形成第二套技术栈 |\n")

	b.WriteString("\n## 6. 更新与验收\n\n")
	b.WriteString("```bash\n")
	b.WriteString("cd backend\n")
	b.WriteString("go run ./cmd/frontend-traceability -write\n")
	b.WriteString("go run ./cmd/frontend-traceability -check\n")
	b.WriteString("go test ./cmd/frontend-traceability ./cmd/gateway ./internal/openapicheck\n")
	b.WriteString("```\n\n")
	b.WriteString("前端切片只有在矩阵中找到精确 operation、角色、页面和失败恢复合同后才满足 DoR。")
	b.WriteString("后端新增能力时必须同步 OpenAPI 与本矩阵；移除接口时必须先证明页面、客户端和运维入口不再依赖。\n")
	return []byte(b.String())
}

func escape(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return value
}
