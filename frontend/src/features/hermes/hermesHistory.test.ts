import { describe, expect, it } from 'vitest'
import type { HermesMessage, HermesModuleView } from './hermesClient'
import {
  conversationTitle,
  groupModulesByCategory,
  messageText,
  probeTone,
  roleLabel,
  sortMessagesByCreatedAt,
} from './hermesHistory'

/*
 * Hermes 历史回看 / 模块上下文纯逻辑单测。
 * 每条用例都按 §14 留了变异余量:故意把守卫删掉 / 翻转分支时,断言必须转红。
 */

describe('conversationTitle —— 标题回退', () => {
  it('有 title 用 title(变异:若忽略 title 直接回退会显示 #id 而非真实标题)', () => {
    expect(conversationTitle({ id: 7, title: '排查 429' })).toBe('排查 429')
  })

  it('title 为空/全空白回退「会话 #id」(变异:若不 trim 会把 "   " 当有效标题渲染空白)', () => {
    expect(conversationTitle({ id: 7, title: '   ' })).toBe('会话 #7')
    expect(conversationTitle({ id: 9, title: null })).toBe('会话 #9')
    expect(conversationTitle({ id: 3 })).toBe('会话 #3')
  })
})

describe('messageText —— content 多形态归一化', () => {
  it('纯字符串原样返回(变异:若先 JSON.stringify 会得到带引号的 "\\"你好\\"")', () => {
    expect(messageText('你好')).toBe('你好')
  })

  it('内容块数组拼接各块 text(变异:若只取首块会丢掉后续块)', () => {
    const content = [
      { type: 'text', text: '第一段' },
      { type: 'text', text: '第二段' },
    ]
    expect(messageText(content)).toBe('第一段\n第二段')
  })

  it('对象取 text 字段(变异:若不识别 text 字段会落到 JSON 兜底)', () => {
    expect(messageText({ text: '对象文本' })).toBe('对象文本')
    expect(messageText({ content: '另一字段' })).toBe('另一字段')
  })

  it('null/undefined 返回空串(变异:若不判空会抛或串化成 "null")', () => {
    expect(messageText(null)).toBe('')
    expect(messageText(undefined)).toBe('')
  })

  it('未知形态兜底串化、绝不丢内容(变异:若兜底返回空串,有内容的消息会渲染成空白)', () => {
    // 数字这种非字符串/数组/对象形态走兜底 JSON.stringify。
    expect(messageText(42)).toBe('42')
    // 对象但无 text/content 字段也兜底串化,保证可见。
    expect(messageText({ foo: 'bar' })).toBe('{"foo":"bar"}')
  })
})

describe('roleLabel —— 角色中文标签', () => {
  it('已知角色映射中文(变异:若 user/assistant 映射对调,断言转红)', () => {
    expect(roleLabel('user')).toBe('用户')
    expect(roleLabel('assistant')).toBe('Hermes')
    expect(roleLabel('system')).toBe('系统')
  })

  it('未知角色原样返回不吞数据(变异:若兜底成固定串会丢失真实 role)', () => {
    expect(roleLabel('weird-role')).toBe('weird-role')
  })
})

function msg(id: number, createdAt?: string): HermesMessage {
  return { id, conversation_id: 1, role: 'user', content: '', created_at: createdAt }
}

describe('sortMessagesByCreatedAt —— 时间正序稳定排序', () => {
  it('按 created_at 升序(变异:若用降序,首个不会是最早的)', () => {
    const out = sortMessagesByCreatedAt([
      msg(3, '2026-06-29T03:00:00Z'),
      msg(1, '2026-06-29T01:00:00Z'),
      msg(2, '2026-06-29T02:00:00Z'),
    ])
    expect(out.map((m) => m.id)).toEqual([1, 2, 3])
  })

  it('时间相同保持原相对顺序(稳定;变异:若非稳定排序顺序可能乱)', () => {
    const out = sortMessagesByCreatedAt([
      msg(10, '2026-06-29T01:00:00Z'),
      msg(20, '2026-06-29T01:00:00Z'),
    ])
    expect(out.map((m) => m.id)).toEqual([10, 20])
  })

  it('缺时间的排在最后(变异:若当成 0/最早会插到最前打乱回看顺序)', () => {
    const out = sortMessagesByCreatedAt([
      msg(99), // 无 created_at
      msg(1, '2026-06-29T01:00:00Z'),
    ])
    expect(out.map((m) => m.id)).toEqual([1, 99])
  })
})

function mv(id: string, category: string): HermesModuleView {
  return { id, category, title: id, live_probe: { status: 'healthy' } }
}

describe('groupModulesByCategory —— 按类聚合保序', () => {
  it('同类聚到一组、组按首次出现序(变异:若按字母排序会打乱后端给定序)', () => {
    const groups = groupModulesByCategory([
      mv('a', 'routing'),
      mv('b', 'account'),
      mv('c', 'routing'),
    ])
    expect(groups.map((g) => g.category)).toEqual(['routing', 'account'])
    expect(groups[0].modules.map((m) => m.id)).toEqual(['a', 'c'])
    expect(groups[1].modules.map((m) => m.id)).toEqual(['b'])
  })

  it('空 category 归入「未分类」(变异:若用空串当 key 会出现无标签的组)', () => {
    const groups = groupModulesByCategory([mv('x', '')])
    expect(groups[0].category).toBe('未分类')
  })
})

describe('probeTone —— 探针状态色调', () => {
  it('健康类→ok(变异:若不识别 healthy 会落到 muted,健康显示成灰)', () => {
    expect(probeTone('healthy')).toBe('ok')
    expect(probeTone('UP')).toBe('ok')
  })

  it('故障类→danger、降级→warn(变异:若 danger/warn 混淆,告警色失真)', () => {
    expect(probeTone('down')).toBe('danger')
    expect(probeTone('error')).toBe('danger')
    expect(probeTone('degraded')).toBe('warn')
  })

  it('未知状态→muted(变异:若兜底成 ok 会把未知状态误显为健康)', () => {
    expect(probeTone('something-new')).toBe('muted')
  })
})
