// lib/api/huakai.ts (前端 API 工具库)
// 用于从 HUAKAI 后端网关获取数据的集中式 API 工具函数。

/**
 * HUAKAI 网关 API 的基础 URL。
 * 如果设置了 HUAKAI_GATEWAY_URL 环境变量，则优先使用该变量，
 * 否则回退到默认的本地开发服务器地址。
 */
const API_BASE_URL = process.env.HUAKAI_GATEWAY_URL || 'http://localhost:8080';

/**
 * 为给定的 API 端点路径构建一个完整的 URL。
 * @param path - API 端点的路径 (例如, '/admin/v1/usage')。
 * @returns 包含基础路径的完整 URL。
 */
export function getApiUrl(path: string): string {
  // 确保路径以斜杠开头以保持一致性。
  const formattedPath = path.startsWith('/') ? path : `/${path}`;
  return `${API_BASE_URL}${formattedPath}`;
}
