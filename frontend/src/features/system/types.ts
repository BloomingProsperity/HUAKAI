/*
 * 系统设置(运维台)前端类型 —— 镜像 platformsettings_handler 的 JSON。
 * 端点:/v1/admin/platform-settings(admin token 鉴权)。
 * 安全:密钥/凭据类 key 后端不回吐明文,改给 value_configured 布尔;前端尊重该约定不显明文。
 */
export interface PlatformSetting {
  key: string
  value: string | null
  /** 仅密钥/凭据类 key 出现:表示其是否已配置(明文不回吐)。 */
  value_configured?: boolean
  source: string
  updated_at?: string | null
  updated_by?: string | null
  health?: { status: string; issue?: string } | null
}

export interface SettingsListResponse {
  items: PlatformSetting[]
  captcha_secret_configured?: boolean
}

export interface SettingUpdateRequest {
  value: string
  reason?: string
}
