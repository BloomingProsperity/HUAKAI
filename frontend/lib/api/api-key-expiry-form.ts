// 用户自助 API-key expiry 更新的纯逻辑（零依赖 strip-types 单测）。
// 后端真码 internal/userkeyhttp/handlers.go PATCH /v1/api-keys/{id}：
//   expires_at 三态（CLAUDE.md #16 sub2api 风格）——
//     省略 / null  → 不改有效期
//     ""（空串）   → 清除有效期（key 变永不过期）
//     RFC3339 串   → 设为该时刻（后端解析失败 → 400 invalid_expires_at）
//   后端 delta：拒过去时刻（ErrInvalidExpiry，对齐 create 路径，关掉 sub2api/new-api 都有的静默废 key 脚枪）。

export type ExpiryAction = 'unchanged' | 'clear' | 'set';

// buildApiKeyExpiryPatch 把三态意图编码成 PATCH body 片段（仅 expires_at 维度）。
// 'unchanged' 绝不带 expires_at 键——否则后端会把它当 clear/set，静默改掉有效期。
export function buildApiKeyExpiryPatch(action: ExpiryAction, whenISO?: string): Record<string, unknown> {
  switch (action) {
    case 'unchanged':
      return {};
    case 'clear':
      return { expires_at: '' };
    case 'set':
      if (!whenISO || whenISO.trim() === '') {
        throw new Error('set 操作必须提供 expires_at 时刻');
      }
      return { expires_at: whenISO };
  }
}

// validateApiKeyExpiry 发请求前预校验「设新有效期」的输入：必须是合法且将来的时刻。
// 对齐后端 reject-past（ErrInvalidExpiry）。nowMs 可注入以便确定性测试。
export function validateApiKeyExpiry(whenISO: string, nowMs?: number): string | null {
  const t = Date.parse(whenISO);
  if (Number.isNaN(t)) return 'expires_at 必须是合法的 RFC3339 时刻';
  const now = nowMs ?? Date.now();
  if (t <= now) return 'expires_at 必须是将来的时刻';
  return null;
}

// API_KEY_PATCH_ERROR_MESSAGES：后端 writeUserKeyError + handler 错误码 → 中文文案。
export const API_KEY_PATCH_ERROR_MESSAGES: Record<string, string> = {
  invalid_expires_at: 'expires_at 非法（需将来的 RFC3339 时刻，或空串清除有效期）',
  invalid_name: 'name 非法（非空且不超长）',
  invalid_json: '请求体不是合法 JSON',
  api_key_not_found: 'API Key 不存在或不属于你',
  session_required: '需要登录',
  userkey_service_unavailable: 'API Key 服务暂不可用',
  userkey_backend_error: '后端暂时不可用，请稍后重试',
};

export function apiKeyPatchErrorMessage(code: string): string {
  return API_KEY_PATCH_ERROR_MESSAGES[code] ?? `API Key 更新失败（${code}）`;
}
