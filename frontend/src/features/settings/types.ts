/*
 * 设置中心前端类型 —— 镜像 controlhttp/platformsettings_handler 的 JSON。
 * 端点:/v1/admin/platform-settings(admin token 鉴权,platform_admin 角色)。
 * 安全:密钥/凭据类 key 后端不回吐明文,改给 value_configured 布尔;前端尊重该约定不显明文。
 */
export interface PlatformSetting {
  key: string
  value: string | null
  /** 仅密钥/凭据类 key 出现:表示其是否已配置(明文不回吐)。 */
  value_configured?: boolean
  /** 来源:env(环境变量,只读)/ db(数据库覆盖)/ default(默认值)。 */
  source: string
  updated_at?: string | null
  updated_by?: string | null
  /** 仅 captcha_enabled 出现:展示健康态(如缺 Turnstile 密钥时 degraded)。 */
  health?: { status: string; issue?: string } | null
}

export interface SettingsListResponse {
  items: PlatformSetting[]
}

export interface SettingUpdateRequest {
  value: string
  reason?: string
}
