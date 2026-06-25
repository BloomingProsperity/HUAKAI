import { describe, expect, it } from 'vitest'
import {
  buildFooterMeta,
  FALLBACK_SITE_NAME,
  interpolate,
  LEGAL_DOCS,
  selectDoc,
} from './legal'
import type { SiteConfig } from './types'

function cfg(over: Partial<SiteConfig> = {}): SiteConfig {
  return { site_name: '', site_footer: '', site_contact_info: '', site_doc_url: '', ...over }
}

describe('interpolate', () => {
  it('有站点名 → 全局替换 {{name}} 占位', () => {
    // 判别核心:占位必须被真实站点名替换,且同段多处都替换。
    // 变异(改成只 replace 首个)→ 第二处占位残留 → RED。
    expect(interpolate('{{name}} 欢迎你,{{name}} 提供服务', '玉青云')).toBe('玉青云 欢迎你,玉青云 提供服务')
  })

  it('站点名空白 → 回退兜底主体名,不留空主体', () => {
    // 判别核心:空名必须回退到 FALLBACK_SITE_NAME 而非留下空串。
    // 变异(去掉 || 兜底)→ 结果以空白开头 → RED。
    expect(interpolate('{{name}} 服务条款', '   ')).toBe(`${FALLBACK_SITE_NAME} 服务条款`)
  })

  it('无占位文本原样返回', () => {
    expect(interpolate('普通段落', '玉青云')).toBe('普通段落')
  })
})

describe('selectDoc', () => {
  it('已知 key 返回对应文档', () => {
    expect(selectDoc('privacy').key).toBe('privacy')
    expect(selectDoc('terms').key).toBe('terms')
  })

  it('未知 key 回退默认文档(用户协议)', () => {
    // 判别核心:未知 key 必须回退 terms,不能返回 undefined。
    // 变异(find 失败时返回 undefined)→ .key 抛错 → RED。
    expect(selectDoc('unknown').key).toBe('terms')
    expect(selectDoc('').key).toBe('terms')
  })

  it('两份文档都至少有一节正文', () => {
    for (const d of LEGAL_DOCS) {
      expect(d.sections.length).toBeGreaterThan(0)
      expect(d.sections.every((s) => s.paragraphs.length > 0)).toBe(true)
    }
  })
})

describe('buildFooterMeta', () => {
  it('去除首尾空白并保留真实值', () => {
    const m = buildFooterMeta(cfg({ site_footer: '  © 2026 运营方  ', site_contact_info: ' ops@example.com ' }))
    expect(m.footer).toBe('© 2026 运营方')
    expect(m.contact).toBe('ops@example.com')
  })

  it('仅空白字段归一为空串(用于隐藏空块)', () => {
    // 判别核心:纯空白 contact 必须变空串,渲染层据此不显示空联系块。
    // 变异(去掉 .trim())→ contact 仍为 '   ' 非空 → RED。
    const m = buildFooterMeta(cfg({ site_contact_info: '   ', site_doc_url: '' }))
    expect(m.contact).toBe('')
    expect(m.docUrl).toBe('')
  })
})
