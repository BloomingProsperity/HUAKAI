/*
 * 模块知识脊柱(module-knowledge)只读总览的 DTO 类型,逐字段镜像后端:
 *   - 响应外壳 ModulesResponse:backend/internal/modulehttp/handler.go:10(ModulesResponse.Modules)
 *   - 单模块视图 ModuleView:backend/internal/modulehttp/view.go:23
 *   - 静态覆盖层 CatalogOverlay:backend/internal/modulehttp/view.go:35
 *   - 实时探针 ProbeResult / ProbeStatus:backend/internal/moduleregistry/descriptor.go:21,38
 * 该面只携带模块身份、枚举状态与简短诊断 detail —— 后端约定绝不含密钥或用户数据。
 */

/** 探针状态封闭枚举(descriptor.go:23-34)。后端只产出这四个值。 */
export type ProbeStatus = 'ok' | 'degraded' | 'unknown' | 'error'

/** 实时探针结果(descriptor.go:38)。Detail 为面向运维的诊断串,可能为空。 */
export interface ProbeResult {
  status: ProbeStatus
  /** json:"detail,omitempty" —— 缺省时后端省略,前端按可选处理。 */
  detail?: string
}

/** 静态知识覆盖层,来自 feature-tree catalog(view.go:35)。无 catalog 匹配时整体缺省。 */
export interface CatalogOverlay {
  /** 所属 section(如 "§5 计费")。json omitempty。 */
  section?: string
  feature_id?: string
  /** parity/实现状态(如 tested / partial)。 */
  status?: string
  /** clean-room parity 强度(如 strong / partial)。 */
  parity?: string
  /** 关联的 Go 包短名列表。 */
  pkgs?: string[]
}

/** 单个模块的合并视图:实时身份 + 能力 + 静态覆盖层 + 实时探针(view.go:23)。 */
export interface ModuleView {
  /** 稳定的点分小写 ID(如 "billing.service")。 */
  id: string
  /** 运维过滤用的分组(如 "money-path" / "routing" / "credentials" / "observability")。 */
  category: string
  /** 人类可读名称。 */
  title: string
  /** 能力清单,json omitempty —— 可能缺省。 */
  capabilities?: string[]
  /** 静态覆盖层,无匹配时缺省(纯实时模块)。 */
  catalog?: CatalogOverlay
  /** 实时探针结果(始终存在;无探针时 status=unknown)。 */
  live_probe: ProbeResult
}

/** GET /admin/v1/modules 响应外壳(handler.go:10)。 */
export interface ModulesResponse {
  modules: ModuleView[]
}
