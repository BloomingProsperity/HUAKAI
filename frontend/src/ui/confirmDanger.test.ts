import { describe, it, expect } from 'vitest'
import { buildIrreversibleMessage, IRREVERSIBLE_PREFIX } from './confirmDanger'

/*
 * 不可逆确认文案构造测试。核心不变量:恒以「无法撤销」前缀开头(用户必被明确告知不可逆),
 * detail 有则成独立段、无则不留空段,恒以「确认继续?」收尾。
 */
describe('buildIrreversibleMessage', () => {
  it('恒以「无法撤销」前缀开头', () => {
    const m = buildIrreversibleMessage('吊销券 #12')
    expect(m.startsWith(IRREVERSIBLE_PREFIX)).toBe(true)
    expect(m).toContain('吊销券 #12')
    expect(m.endsWith('确认继续?')).toBe(true)
  })

  it('带 detail → 追加为独立段(前缀仍在最前)', () => {
    const m = buildIrreversibleMessage('裁决争议 D-9 为「支持退款」', '将退回该笔已计费请求的费用。')
    expect(m.startsWith(IRREVERSIBLE_PREFIX)).toBe(true)
    expect(m).toContain('将退回该笔已计费请求的费用。')
    // detail 与动作句之间应有空行分段。
    expect(m).toContain('\n\n将退回')
  })

  it('detail 为空串 → 不产生多余空段', () => {
    const withEmpty = buildIrreversibleMessage('删除模板 #3', '   ')
    const without = buildIrreversibleMessage('删除模板 #3')
    expect(withEmpty).toBe(without)
    // 不应出现三连换行(空 detail 段)。
    expect(withEmpty).not.toContain('\n\n\n')
  })
})
