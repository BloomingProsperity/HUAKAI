/*
 * Key「一键接入」深链/配置生成(纯逻辑,可单测)。
 *
 * 目的:把"刚拿到的一次性明文 key + relay 端点"直接拼成各客户端可用的接入配置,
 * 极大降低从"拿到 key"到"在客户端跑起来"的摩擦(卖额度 SaaS 的转化关键体验)。
 *
 * clean-room:接入交互思路借鉴自第三方客户端切换器,但深链 scheme 用 HUAKAI 自有
 * `huakai://connect`,深链构造逻辑全部功能性自写,不复制任何第三方标识符/格式。
 *
 * 核源码确认的两条硬约束(整段设计的正确性锚点):
 *  1. relay 入站鉴权只读 `Authorization: Bearer`(api_key_resolver.go parseBearer),
 *     不读 x-api-key/x-goog-api-key → Claude Code 必须用 ANTHROPIC_AUTH_TOKEN
 *     (它走 Authorization: Bearer),用 ANTHROPIC_API_KEY(走 x-api-key)会 401。
 *  2. relay 端点挂在根:/v1/messages(anthropic)、/v1/chat/completions、/v1/responses
 *     → Claude Code 的 ANTHROPIC_BASE_URL 用根地址(它自己追加 /v1/messages);
 *        OpenAI 兼容客户端的 Base URL 用 <根>/v1。
 */

/** 去掉末尾斜杠,得到 relay 根地址。 */
export function normalizeOrigin(origin: string): string {
  return origin.replace(/\/+$/, '')
}

export type ClientKind = 'claude-code' | 'openai'

/**
 * 据客户端类型给出 relay base:
 *  - claude-code:根地址(客户端自己追加 /v1/messages)
 *  - openai:根地址 + /v1(OpenAI 兼容客户端在此之上追加 /chat/completions 等)
 */
export function relayBaseFor(origin: string, client: ClientKind): string {
  const root = normalizeOrigin(origin)
  return client === 'openai' ? `${root}/v1` : root
}

export interface ConnectConfig {
  /** 该客户端应填的 base 地址(已按客户端类型处理 /v1)。 */
  endpoint: string
  /** 明文 key。 */
  token: string
  /** key 名,作为深链里的标识。 */
  name: string
  client: ClientKind
}

/**
 * 生成 HUAKAI 自有「一键接入」深链(clean-room 自有 scheme)。
 * 形如 huakai://connect?endpoint=..&token=..&name=..&client=..
 * 用 URLSearchParams 做百分号编码,天然处理中文 key 名与特殊字符。
 */
export function buildConnectLink(cfg: ConnectConfig): string {
  const q = new URLSearchParams({
    endpoint: cfg.endpoint,
    token: cfg.token,
    name: cfg.name,
    client: cfg.client,
  })
  return `huakai://connect?${q.toString()}`
}

export interface IntegrationField {
  label: string
  value: string
  /** 含明文 key 的字段,UI 用更醒目的密文盒展示并提供复制。 */
  secret?: boolean
}

export interface Integration {
  id: ClientKind
  label: string
  /** 一句话接入提示(含关键避坑,如 AUTH_TOKEN vs API_KEY)。 */
  hint: string
  fields: IntegrationField[]
  /** 可一键复制的接入脚本/配置片段。 */
  snippet: string
  /** HUAKAI 自有一键接入深链。 */
  deepLink: string
}

/**
 * 据 relay origin + 一次性明文 + key 名,生成各客户端的接入配置。
 * origin 由调用方注入(组件层传 window.location.origin),便于单测。
 */
export function buildIntegrations(origin: string, plaintext: string, keyName: string): Integration[] {
  const ccBase = relayBaseFor(origin, 'claude-code')
  const oaBase = relayBaseFor(origin, 'openai')
  const safeName = keyName.trim() || 'HUAKAI'

  const claudeCode: Integration = {
    id: 'claude-code',
    label: 'Claude Code',
    hint: '设置环境变量后直接运行 claude。务必用 ANTHROPIC_AUTH_TOKEN(走 Authorization: Bearer),不要用 ANTHROPIC_API_KEY(走 x-api-key,本服务不接受会 401)。',
    fields: [
      { label: 'ANTHROPIC_BASE_URL', value: ccBase },
      { label: 'ANTHROPIC_AUTH_TOKEN', value: plaintext, secret: true },
    ],
    snippet: [
      `export ANTHROPIC_BASE_URL="${ccBase}"`,
      `export ANTHROPIC_AUTH_TOKEN="${plaintext}"`,
      'claude',
    ].join('\n'),
    deepLink: buildConnectLink({ endpoint: ccBase, token: plaintext, name: safeName, client: 'claude-code' }),
  }

  const openai: Integration = {
    id: 'openai',
    label: 'OpenAI 兼容客户端(Cherry Studio / NextChat 等)',
    hint: 'Base URL 填到 /v1 一级;API Key 填明文 key(客户端会以 Authorization: Bearer 发送)。',
    fields: [
      { label: 'Base URL', value: oaBase },
      { label: 'API Key', value: plaintext, secret: true },
    ],
    snippet: [
      `curl ${oaBase}/chat/completions \\`,
      `  -H "Authorization: Bearer ${plaintext}" \\`,
      '  -H "Content-Type: application/json" \\',
      '  -d \'{"model":"<模型名>","messages":[{"role":"user","content":"hi"}]}\'',
    ].join('\n'),
    deepLink: buildConnectLink({ endpoint: oaBase, token: plaintext, name: safeName, client: 'openai' }),
  }

  return [claudeCode, openai]
}
