import type { PlatformSetting, SettingUpdateRequest } from './types'

/*
 * 设置中心纯逻辑(可单测)。两块职责:
 *  1) 把后端 platform-settings 的全部 key 按 9 个分签(sub2 风格设置中心)归组——
 *     TAB_GROUPS 是唯一归组来源,SettingsCenterPage 据此渲染横向 tab + 每 tab 设置卡。
 *  2) 复用 system 模块已验证的 secret-mask / env 只读 / buildSettingUpdate 守卫逻辑:
 *     密钥类 key 后端不回吐明文(以 value_configured 出现、value 为空),前端不显明文、
 *     编辑空输入=不修改(绝不下发空串覆盖已配置的密钥);env 来源只读。
 *
 * clean-room:分签思路功能性借鉴 sub2(通用/安全/网关/功能/用户/支付/邮件/协议/备份),
 * 但分签 key、中文文案、控件判定、配色全部 HUAKAI 自有,未复制其任何标识符/代码。
 */

/** 设置中心的 9 个分签标识。 */
export type SettingsTabKey =
  | 'general'
  | 'users'
  | 'security'
  | 'gateway'
  | 'features'
  | 'payment'
  | 'email'
  | 'agreement'
  | 'backup'

/** 控件渲染类型:由 key 判定,决定 SettingsCenterPage 用哪种输入控件。 */
export type SettingControl = 'bool' | 'number' | 'string' | 'json' | 'secret' | 'multiline'

/** 单个设置项的元数据(归到某 tab 内)。 */
export interface SettingItemMeta {
  /** 后端 platform-settings key(真实常量值)。 */
  key: string
  /** 中文标签。 */
  label: string
  /** 中文说明(可选)。 */
  hint?: string
  /** 控件类型。 */
  control: SettingControl
}

/** 一个分签:标识 + 中文名 + 图标符号 + 其下设置项。 */
export interface SettingsTabGroup {
  key: SettingsTabKey
  label: string
  /** 纯文本图标符号(不引图标库,玉青风格用 emoji 占位)。 */
  icon: string
  items: SettingItemMeta[]
}

/*
 * 控件类型判定。先列出确定为某类型的 key 集合,其余 key 按后端校验语义归类。
 * 这些集合直接对应 platformsettings/types.go 的 ValidateValue 分支:
 *  - bool 集 = validateBoolValue 分支;
 *  - number 集 = validatePositiveIntValue / NonNegativeInt / BoundedNonNegativeInt 分支;
 *  - json 集 = JSON 对象/数组类校验(model_fallback_chains/budget_limits/支付/媒体估价/审核阈值/passkey_rp_origins);
 *  - secret 集 = IsSecretKey(moderation_external_api_keys);
 *  - multiline 集 = 站点长文本(首页内容/页脚);
 *  - 其余 = 普通字符串。
 */
const BOOL_KEYS = new Set<string>([
  'registration_enabled',
  'invitation_required',
  'password_register_enabled',
  'password_login_enabled',
  'email_domain_allowlist_enabled',
  'email_alias_restriction_enabled',
  'captcha_enabled',
  'two_factor_enabled',
  'promo_enabled',
  'checkin_enabled',
  'referral_reward_enabled',
  'passkey_enabled',
  'passkey_registration_enabled',
  'mediatask_enabled',
  'moderation_external_enabled',
  'moderation_external_image_enabled',
  'warmup_intercept_enabled',
])

const NUMBER_KEYS = new Set<string>([
  'stream_timeout_seconds',
  'cooldown_429_seconds',
  'cooldown_529_seconds',
  'checkin_min_cents',
  'checkin_max_cents',
  'referral_reward_cents',
  'mediatask_poll_interval_seconds',
  'mediatask_task_timeout_seconds',
  'moderation_external_timeout_ms',
  'moderation_external_retry_count',
])

const JSON_KEYS = new Set<string>([
  'model_fallback_chains',
  'budget_limits',
  'payment_provider_config',
  'mediatask_default_estimated_cents',
  'moderation_external_thresholds',
  'passkey_rp_origins',
  'oauth_providers_enabled',
  'oauth_providers_config',
])

const MULTILINE_KEYS = new Set<string>(['site_home_content', 'site_footer'])

/**
 * 判定某 key 的控件类型。secret 优先(脱敏第一),其后 bool/number/json/multiline,
 * 兜底为普通字符串。secret 判定与后端 IsSecretKey 一致(目前仅审核 provider 密钥数组)。
 */
export function controlFor(key: string): SettingControl {
  if (SECRET_KEYS.has(key)) return 'secret'
  if (BOOL_KEYS.has(key)) return 'bool'
  if (NUMBER_KEYS.has(key)) return 'number'
  if (JSON_KEYS.has(key)) return 'json'
  if (MULTILINE_KEYS.has(key)) return 'multiline'
  return 'string'
}

/**
 * 密钥/凭据类 key 集合,镜像后端 platformsettings/secret_keys.go 的 secretSettingKeys。
 * 唯一来源:后端读路径对这些 key 脱敏(清空明文 value、改给 value_configured)。
 * 前端据此判定控件为 secret 且不回显明文。
 */
const SECRET_KEYS = new Set<string>([
  'moderation_external_api_keys',
  'telegram_bot_token',
  'captcha_secret',
  'oauth_providers_secrets',
])

/*
 * 9 个分签的归组表。规则:后端每一个 platform-settings key 必须且只能出现在一个 tab。
 * 分签划分功能性借鉴 sub2 设置中心(通用/用户/安全/网关/功能/支付/邮件/协议/备份),
 * 中文名与归组结果 HUAKAI 自有。注意:HUAKAI 后端目前没有 SMTP/备份相关 key,
 * email/agreement/backup 三个 tab 的归组为空(tab 仍展示但提示"暂无可配置项"),
 * 这是真实事实而非遗漏——见 settings.test.ts 的全覆盖断言。
 */
export const TAB_GROUPS: SettingsTabGroup[] = [
  {
    key: 'general',
    label: '通用',
    icon: '🏠',
    items: [
      { key: 'site_name', label: '站点名称', control: 'string' },
      { key: 'site_subtitle', label: '站点副标题', hint: '站名下方的简短标语(公开)', control: 'string' },
      { key: 'site_logo', label: '站点 Logo', hint: 'Logo 图片地址', control: 'string' },
      { key: 'site_footer', label: '页脚内容', hint: '页脚 HTML/文本', control: 'multiline' },
      { key: 'site_home_content', label: '首页内容', hint: '首页正文(HTML/Markdown)', control: 'multiline' },
      { key: 'site_contact_info', label: '联系方式', hint: '公开的运营联系方式(邮箱/IM/自由文本)', control: 'string' },
      { key: 'site_doc_url', label: '文档链接', hint: '公开文档地址(http/https)', control: 'string' },
      { key: 'site_api_base_url', label: 'API 基础地址', hint: '公开网关地址,供用户配置客户端', control: 'string' },
      { key: 'site_frontend_base_url', label: '前端站点地址', hint: '本站前端 URL(如 https://你的域名),配置后验证/重置/设备确认邮件会发完整可点链接(用户直接点);留空则发裸 token 由粘贴框兜底', control: 'string' },
    ],
  },
  {
    key: 'users',
    label: '注册登录',
    icon: '👤',
    items: [
      { key: 'registration_enabled', label: '开放注册', control: 'bool' },
      { key: 'invitation_required', label: '需邀请码', control: 'bool' },
      { key: 'password_register_enabled', label: '允许密码注册', control: 'bool' },
      { key: 'password_login_enabled', label: '允许密码登录', control: 'bool' },
      { key: 'email_domain_allowlist_enabled', label: '邮箱域名白名单', control: 'bool' },
      { key: 'email_domain_allowlist', label: '允许的邮箱域名', hint: '逗号分隔', control: 'string' },
      { key: 'email_alias_restriction_enabled', label: '限制邮箱别名', hint: '禁止 + 别名等', control: 'bool' },
      { key: 'reserved_email_localparts', label: '保留邮箱前缀', hint: '逗号分隔', control: 'string' },
      { key: 'oauth_providers_enabled', label: '第三方登录开关', hint: '启用的 provider 列表(逗号分隔或 JSON 数组),决定登录页渲染哪些按钮', control: 'json' },
      { key: 'oauth_providers_config', label: '第三方登录配置', hint: '各 provider 的非密钥配置(JSON):{"github":{"client_id":"...","redirect_uri":"...","auth_url":"..."}};配置后即生效、无需重部署,留空则回退环境变量。client_secret 请填下方「密钥」项', control: 'json' },
      { key: 'oauth_providers_secrets', label: '第三方登录密钥', hint: '各 provider 的 client_secret(JSON):{"github":"...","google":"..."}(脱敏存储、不回显);留空则回退环境变量', control: 'secret' },
      { key: 'telegram_bot_username', label: 'Telegram Bot 用户名', hint: '公开 bot 名(t.me/<name>),配合 oauth_providers_enabled 含 telegram 启用 Telegram 登录', control: 'string' },
      { key: 'telegram_bot_token', label: 'Telegram Bot Token', hint: 'BotFather 颁发的 bot token(HMAC 校验密钥,脱敏存储、不回显);配置后即生效、无需重部署,留空则回退环境变量', control: 'secret' },
    ],
  },
  {
    key: 'security',
    label: '安全',
    icon: '🛡️',
    items: [
      { key: 'two_factor_enabled', label: '双因子认证(2FA)', control: 'bool' },
      { key: 'captcha_enabled', label: '启用人机验证', hint: '需先配好 Turnstile 密钥', control: 'bool' },
      { key: 'captcha_provider', label: '验证码提供方', hint: 'turnstile / recaptcha / hcaptcha', control: 'string' },
      { key: 'captcha_site_key', label: '验证码 Site Key', hint: '前端公开 site key', control: 'string' },
      { key: 'captcha_secret', label: '验证码 Secret', hint: '提供方服务端校验密钥(脱敏存储、不回显);配置后即生效、无需重部署,留空则回退环境变量。配好本项才能开启人机验证', control: 'secret' },
      { key: 'passkey_enabled', label: '启用 Passkey 登录', control: 'bool' },
      { key: 'passkey_registration_enabled', label: '允许注册 Passkey', control: 'bool' },
      { key: 'passkey_rp_id', label: 'Passkey RP ID', hint: '依赖方域名标识', control: 'string' },
      { key: 'passkey_rp_display_name', label: 'Passkey RP 显示名', control: 'string' },
      { key: 'passkey_rp_origins', label: 'Passkey 来源列表', hint: 'JSON 字符串数组', control: 'json' },
    ],
  },
  {
    key: 'gateway',
    label: '网关行为',
    icon: '🛰️',
    items: [
      { key: 'stream_timeout_seconds', label: '流式超时(秒)', control: 'number' },
      { key: 'cooldown_429_seconds', label: '429 冷却(秒)', hint: '上游限流后冷却时长', control: 'number' },
      { key: 'cooldown_529_seconds', label: '529 冷却(秒)', hint: '上游过载后冷却时长', control: 'number' },
      { key: 'response_header_deny_extra', label: '响应头额外拒绝', hint: '逗号分隔的 header 名', control: 'string' },
      { key: 'response_header_allow_override', label: '响应头允许覆盖', hint: '逗号分隔的 header 名', control: 'string' },
      { key: 'model_fallback_chains', label: '模型回退链', hint: 'JSON 回退配置', control: 'json' },
      { key: 'budget_limits', label: '预算上限', hint: 'JSON 预算配置', control: 'json' },
      { key: 'warmup_intercept_enabled', label: '预热拦截', control: 'bool' },
    ],
  },
  {
    key: 'features',
    label: '功能',
    icon: '⚡',
    items: [
      { key: 'promo_enabled', label: '启用促销', control: 'bool' },
      { key: 'checkin_enabled', label: '启用签到', control: 'bool' },
      { key: 'checkin_min_cents', label: '签到最小奖励(分)', control: 'number' },
      { key: 'checkin_max_cents', label: '签到最大奖励(分)', control: 'number' },
      { key: 'referral_reward_enabled', label: '启用邀请奖励', control: 'bool' },
      { key: 'referral_reward_cents', label: '邀请奖励(分)', control: 'number' },
      { key: 'mediatask_enabled', label: '启用媒体任务', hint: '图片/视频生成', control: 'bool' },
      { key: 'mediatask_provider_base_url', label: '媒体任务上游地址', hint: 'http/https', control: 'string' },
      { key: 'mediatask_poll_interval_seconds', label: '媒体任务轮询间隔(秒)', control: 'number' },
      { key: 'mediatask_task_timeout_seconds', label: '媒体任务超时(秒)', control: 'number' },
      { key: 'mediatask_default_estimated_cents', label: '媒体任务默认估价(分)', hint: 'JSON:按任务类型', control: 'json' },
      { key: 'moderation_external_enabled', label: '启用外部审核', control: 'bool' },
      { key: 'moderation_external_base_url', label: '外部审核上游地址', hint: 'http/https', control: 'string' },
      { key: 'moderation_external_api_keys', label: '外部审核密钥', hint: '上游 Bearer 密钥数组(脱敏)', control: 'secret' },
      { key: 'moderation_external_model', label: '外部审核模型', control: 'string' },
      { key: 'moderation_external_thresholds', label: '外部审核阈值', hint: 'JSON:分类阈值 [0,1]', control: 'json' },
      { key: 'moderation_external_timeout_ms', label: '外部审核超时(毫秒)', control: 'number' },
      { key: 'moderation_external_retry_count', label: '外部审核重试次数', control: 'number' },
      { key: 'moderation_external_image_enabled', label: '外部审核含图片', control: 'bool' },
    ],
  },
  {
    key: 'payment',
    label: '支付',
    icon: '💳',
    items: [
      { key: 'payment_provider_config', label: '支付方式配置', hint: 'JSON:manual/taobao 开关与收银台地址', control: 'json' },
    ],
  },
  {
    key: 'email',
    label: '邮件',
    icon: '✉️',
    items: [
      { key: 'admin_notification_email', label: '运维通知邮箱', hint: '每日巡检报告收件地址;留空则巡检关闭', control: 'string' },
    ],
  },
  {
    key: 'agreement',
    label: '登录协议',
    icon: '📜',
    // HUAKAI 后端暂无独立的"登录协议正文" platform-settings key(站点页脚/首页可承载),
    // tab 保留占位以对齐 sub2 设置中心形态,后端补 key 后再归入。
    items: [],
  },
  {
    key: 'backup',
    label: '备份',
    icon: '💾',
    // HUAKAI 后端暂无备份相关 platform-settings key(备份属运维侧能力,非平台设置),
    // tab 保留占位以对齐形态。
    items: [],
  },
]

/** 收集 TAB_GROUPS 里已归组的全部 key(用于全覆盖校验)。 */
export function groupedKeys(): string[] {
  const out: string[] = []
  for (const tab of TAB_GROUPS) {
    for (const item of tab.items) out.push(item.key)
  }
  return out
}

// ===== 以下为 system 模块迁移的 secret-mask / 只读 / 更新构造逻辑(行为保持一致) =====

/** 是否密钥/凭据类 key(后端以 value_configured 出现作信号)。 */
export function isSecretSetting(s: PlatformSetting): boolean {
  return s.value_configured !== undefined
}

/** 列表展示用的值文案:密钥类显示已配置/未配置(绝不显明文);普通键显原值。 */
export function displayValue(s: PlatformSetting): string {
  if (isSecretSetting(s)) return s.value_configured ? '已配置' : '未配置'
  return s.value ?? '(空)'
}

/** 来源标签:env=环境变量(只读)、db=数据库覆盖、default=默认。 */
export function sourceLabel(source: string): string {
  switch (source) {
    case 'env':
      return '环境变量'
    case 'db':
      return '数据库覆盖'
    case 'default':
      return '默认值'
    default:
      return source
  }
}

/** env 来源只读(进程环境变量,不可经 API 改),禁止编辑。 */
export function isReadOnly(s: PlatformSetting): boolean {
  return s.source === 'env'
}

export type UpdateResult = SettingUpdateRequest | { error: string } | { noop: true }

/**
 * 构造 PUT 请求体。密钥类:空输入视为"不修改"返回 noop(避免空串覆盖已配置密钥);
 * 普通键:允许设空串。reason 可选,空白省略。
 */
export function buildSettingUpdate(s: PlatformSetting, draftValue: string, reason: string): UpdateResult {
  if (isReadOnly(s)) return { error: '该项来自环境变量,只读不可改' }
  const secret = isSecretSetting(s)
  if (secret && draftValue.trim() === '') {
    return { noop: true }
  }
  const req: SettingUpdateRequest = { value: draftValue }
  const r = reason.trim()
  if (r) req.reason = r
  return req
}
