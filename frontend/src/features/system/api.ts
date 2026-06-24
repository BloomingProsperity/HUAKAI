import { apiGet, apiSend } from '../../lib/api'
import type { PlatformSetting, SettingsListResponse, SettingUpdateRequest } from './types'

/*
 * 系统设置数据访问层。端点 /v1/admin/platform-settings(admin token 鉴权)。
 */
const PATH = '/v1/admin/platform-settings'

export async function listSettings(signal?: AbortSignal): Promise<SettingsListResponse> {
  return apiGet<SettingsListResponse>(PATH, { signal })
}

/** 更新单项设置:PUT /{key} {value, reason?}。 */
export async function updateSetting(key: string, body: SettingUpdateRequest): Promise<PlatformSetting> {
  return apiSend<PlatformSetting>('PUT', `${PATH}/${encodeURIComponent(key)}`, body)
}
