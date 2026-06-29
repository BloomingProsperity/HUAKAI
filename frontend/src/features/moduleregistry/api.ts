import { apiGet } from '../../lib/api'
import type { ModulesResponse } from './types'

/*
 * 模块知识脊柱数据访问层(只读)。端点挂 /admin/v1/modules,经 tokenForPath 注入 admin token。
 * 端点真实性见 backend/cmd/gateway/routes_modules.go:31(GET /admin/v1/modules)
 * + handler backend/internal/modulehttp/handler.go:21;门控 platform-admin(routes_modules.go:21 mountModuleRegistryRoutes)。
 */

/**
 * 列出所有模块的合并视图(身份 + 能力 + 静态覆盖层 + 实时探针)。
 * GET /admin/v1/modules,可选 ?category= 过滤到单一类别(handler.go:27)。
 * category 为空串时省略该 query,后端返回全部。
 */
export async function listModules(
  category: string,
  signal?: AbortSignal,
): Promise<ModulesResponse> {
  const cat = category.trim()
  return apiGet<ModulesResponse>('/admin/v1/modules', {
    // 仅在非空时下发;空串省略,避免给后端发 category=""(会被当作精确匹配空类别 → 返回空)。
    query: cat ? { category: cat } : undefined,
    signal,
  })
}
