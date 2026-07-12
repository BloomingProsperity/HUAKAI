/*
 * 接入指引片段生成(纯逻辑,可单测)。给定网关基础地址 + API Key,拼出各客户端的接入配置。
 * Key 缺省时用占位符,避免逼用户先粘贴(创建后明文不可回读)。CC 车道仅官方 Claude Code。
 */

export interface Snippet {
  id: string
  title: string
  lang: string
  body: string
}

const KEY_PLACEHOLDER = '<你的_API_KEY>'

/** 去掉基础地址尾部斜杠;空则用占位。 */
function normBase(base: string): string {
  const b = (base || '').trim().replace(/\/+$/, '')
  return b || 'https://你的网关地址'
}

/**
 * 生成接入片段。openaiBase 用作 OpenAI 兼容 base_url(通常含 /v1);
 * anthropicBase 去掉尾部 /v1 供 Claude Code 的 ANTHROPIC_BASE_URL(它自带版本路径)。
 */
export function buildSnippets(apiBaseUrl: string, apiKey: string): Snippet[] {
  const base = normBase(apiBaseUrl)
  const key = apiKey.trim() || KEY_PLACEHOLDER
  const anthropicBase = base.replace(/\/v1$/, '')
  return [
    {
      id: 'claude-code',
      title: 'Claude Code(官方客户端)',
      lang: 'bash',
      body: `export ANTHROPIC_BASE_URL="${anthropicBase}"\nexport ANTHROPIC_AUTH_TOKEN="${key}"\nclaude`,
    },
    {
      id: 'openai-sdk',
      title: 'OpenAI 兼容 SDK(Python)',
      lang: 'python',
      body: `from openai import OpenAI\nclient = OpenAI(\n    base_url="${base}",\n    api_key="${key}",\n)\nresp = client.chat.completions.create(\n    model="gpt-4o",\n    messages=[{"role": "user", "content": "你好"}],\n)`,
    },
    {
      id: 'curl',
      title: 'curl(OpenAI 兼容)',
      lang: 'bash',
      body: `curl ${base}/chat/completions \\\n  -H "Authorization: Bearer ${key}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"你好"}]}'`,
    },
  ]
}

export const keyPlaceholder = KEY_PLACEHOLDER
