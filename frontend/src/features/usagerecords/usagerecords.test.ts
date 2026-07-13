import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearAll, getRefreshToken, setSessionTokens } from '../../auth/store'
import { exportUsageCSV } from './api'
import {
  MAX_DISPUTE_REASON_LEN,
  buildExportQuery,
  dayEndRFC3339,
  dayStartRFC3339,
  defaultExportRange,
  disputeStatusLabel,
  disputeStatusTone,
  encodeReceiptRequestID,
  formatCost,
  formatMicroUSD,
  hasMore,
  isSuccess,
  mapDisputeRows,
  mapUsagePageStats,
  modelDisplay,
  statusLabel,
  statusTone,
  tokensSummary,
  totalTokens,
  validateDisputeReason,
  validateExportRange,
  verifyLabel,
  verifyStatusLabel,
  verifyTone,
} from './usagerecords'

describe('statusTone / statusLabel(镜像后端 usageStatus)', () => {
  it('成功类 end_class → ok / 成功', () => {
    expect(statusTone('stream_end_graceful')).toBe('ok')
    expect(statusTone('non_streaming')).toBe('ok')
    expect(statusLabel('stream_end_graceful')).toBe('成功')
    expect(statusLabel('non_streaming')).toBe('成功')
  })
  it('待对账 → warn', () => {
    expect(statusTone('pending_reconciliation')).toBe('warn')
    expect(statusLabel('pending_reconciliation')).toBe('待对账')
  })
  it('错误类 → danger,标签原样保留诊断信息', () => {
    expect(statusTone('upstream_error_5xx')).toBe('danger')
    expect(statusLabel('upstream_error_5xx')).toBe('upstream_error_5xx')
    expect(statusTone('client_abort')).toBe('danger')
  })
  it('空 → muted / 占位', () => {
    expect(statusTone('')).toBe('muted')
    expect(statusLabel('   ')).toBe('—')
  })
  it('成功类不被错误分支误伤(精确集合判定)', () => {
    // 必须靠白名单集合,而非"非 pending 即 danger"
    expect(statusTone('non_streaming')).not.toBe('danger')
  })
})

describe('isSuccess', () => {
  it('仅两个成功 end_class 为真', () => {
    expect(isSuccess('stream_end_graceful')).toBe(true)
    expect(isSuccess('non_streaming')).toBe(true)
    expect(isSuccess('pending_reconciliation')).toBe(false)
    expect(isSuccess('upstream_error_5xx')).toBe(false)
  })
})

describe('formatCost', () => {
  it('定点小数串去尾零,保留 ≥2 位小数观感', () => {
    expect(formatCost('0.01000000')).toBe('$0.01')
    expect(formatCost('0.10000000')).toBe('$0.10')
    expect(formatCost('1.50000000')).toBe('$1.50')
    expect(formatCost('2.00000000')).toBe('$2')
  })
  it('极小额放宽小数位,不塌成 $0.00', () => {
    const out = formatCost('0.00005000')
    expect(out).not.toBe('$0.00')
    expect(out.startsWith('$0.0000')).toBe(true)
  })
  it('非零但低于最小精度 → 阈值形态,不塌成 $0(计费诚实性)', () => {
    // 真实微小扣费(亚微分)不能显示成 $0 误导用户
    expect(formatCost('0.00000001')).toBe('<$0.000001')
    expect(formatCost('0')).toBe('$0') // 真零仍是 $0
  })
  it('0 → $0;非数字原样;空 → 占位', () => {
    expect(formatCost('0')).toBe('$0')
    expect(formatCost('abc')).toBe('abc')
    expect(formatCost('   ')).toBe('—')
  })
})

describe('token 汇总', () => {
  it('totalTokens = 入+出(不含缓存)', () => {
    expect(totalTokens({ input: 10, output: 20 })).toBe(30)
    expect(totalTokens({ input: 0, output: 0, cache_read: 99 })).toBe(0)
  })
  it('tokensSummary 含缓存(若有)', () => {
    expect(tokensSummary({ input: 10, output: 20 })).toBe('入 10 / 出 20')
    expect(tokensSummary({ input: 10, output: 20, cache_creation: 5, cache_read: 3 })).toBe('入 10 / 出 20 / 缓存写 5 / 缓存读 3')
  })
})

describe('modelDisplay', () => {
  it('上游同则只显请求模型,不同则附注箭头', () => {
    expect(modelDisplay({ requested_model: 'claude-x', upstream_model: 'claude-x' })).toBe('claude-x')
    expect(modelDisplay({ requested_model: 'claude-x', upstream_model: 'claude-upstream' })).toBe('claude-x → claude-upstream')
  })
  it('请求模型空时回退上游 / 占位', () => {
    expect(modelDisplay({ requested_model: '', upstream_model: 'up' })).toBe('up')
    expect(modelDisplay({ requested_model: '', upstream_model: '' })).toBe('—')
  })
})

describe('hasMore(游标分页)', () => {
  it('next_cursor 非空 → 还有下一页', () => {
    expect(hasMore('abc123')).toBe(true)
    expect(hasMore('')).toBe(false)
    expect(hasMore('   ')).toBe(false)
  })
})

describe('页面展示纯映射', () => {
  it('当页记录数与合计花费都明确标注当前页', () => {
    const base = {
      requested_model: 'model-a', upstream_model: 'model-a', tokens: { input: 1, output: 2 },
      provider: 'provider-a', ledger_id: 'ledger-1', created_at: '2026-07-13T10:00:00Z',
      status: 'non_streaming', stream: false,
    }
    expect(mapUsagePageStats([
      { ...base, actual_cost: '0.10' },
      { ...base, ledger_id: 'ledger-2', actual_cost: '0.25' },
    ])).toEqual([
      { label: '记录数', value: '2', hint: '当前页' },
      { label: '合计花费', value: '$0.35', hint: '当前页' },
    ])
    // 变异证红点:删掉 hint 或漏加任一费用 → 上述完整对象断言 RED。
  })

  it('争议表行保留状态 tone、追踪 ID、原因与运营结果', () => {
    const createdAt = '2026-07-13T10:00:00Z'
    const resolvedAt = '2026-07-13T11:00:00Z'
    expect(mapDisputeRows([{
      id: 7,
      dispute_id: 'dispute-7',
      tenant_id: 2,
      user_id: 3,
      request_id: 'request-9',
      reason: '重复计费',
      status: 'rejected',
      operator_note: '核验无误',
      created_at: createdAt,
      resolved_at: resolvedAt,
    }])).toEqual([{
      id: 'dispute-7',
      requestID: 'request-9',
      reason: '重复计费',
      statusLabel: '已驳回',
      statusTone: 'danger',
      createdAt: new Date(createdAt).toLocaleString('zh-CN', { hour12: false }),
      resolvedAt: new Date(resolvedAt).toLocaleString('zh-CN', { hour12: false }),
      operatorNote: '核验无误',
    }])
    // 变异证红点:删 statusTone 映射或把 rejected 改成 warn → 完整对象断言 RED。
  })
})

describe('dayStartRFC3339 / dayEndRFC3339(导出日期转 RFC3339)', () => {
  it('起点=当天 UTC 零点,终点=次日 UTC 零点(右界半开)', () => {
    // 判别核心:to 必须是次日零点(变异成同当天零点 → 零跨度漏当日数据 → RED)。
    expect(dayStartRFC3339('2026-06-25')).toBe('2026-06-25T00:00:00.000Z')
    expect(dayEndRFC3339('2026-06-25')).toBe('2026-06-26T00:00:00.000Z')
  })
  it('非法日期 → 空串', () => {
    expect(dayStartRFC3339('2026/06/25')).toBe('')
    expect(dayEndRFC3339('')).toBe('')
  })
})

describe('validateExportRange', () => {
  it('合法范围 → null', () => {
    expect(validateExportRange('2026-06-01', '2026-06-30')).toBeNull()
  })
  it('开始晚于结束 → 拦下', () => {
    // 判别核心:from>to 必须拦(变异成放行 → 后端 400 invalid_date_range → RED)。
    expect(validateExportRange('2026-06-30', '2026-06-01')).toContain('不能晚于')
  })
  it('跨度 > 366 天 → 拦下', () => {
    // 判别核心:超 366 天必须拦(变异成放行 → 后端 400 date_range_too_large → RED)。
    expect(validateExportRange('2025-01-01', '2026-06-30')).toContain('366')
  })
  it('恰好 366 天(含右界整日)→ 放行', () => {
    // 2026-01-01 到 2026-12-31 含两端共 365 天;再加一天到 366 边界仍放行。
    expect(validateExportRange('2026-01-01', '2026-12-31')).toBeNull()
  })
})

describe('buildExportQuery', () => {
  it('format=csv + from 起点 + to 次日零点', () => {
    // 判别核心:format 必须 csv,且 to 用 dayEndRFC3339(变异成漏 format 或错边界 → RED)。
    expect(buildExportQuery('2026-06-01', '2026-06-01')).toEqual({
      format: 'csv',
      from: '2026-06-01T00:00:00.000Z',
      to: '2026-06-02T00:00:00.000Z',
    })
  })
})

describe('defaultExportRange', () => {
  it('最近 N 天(含今天):toDay=今天,fromDay=今天往前 N-1 天', () => {
    // 固定 now 验证边界:最近 30 天 → from = now-29 天。
    const now = new Date('2026-06-30T12:00:00.000Z')
    const r = defaultExportRange(30, now)
    expect(r.toDay).toBe('2026-06-30')
    expect(r.fromDay).toBe('2026-06-01')
  })
})

describe('encodeReceiptRequestID(收据路由路径段编码)', () => {
  it('无斜杠 → 整体编码', () => {
    expect(encodeReceiptRequestID('req_abc123')).toBe('req_abc123')
    // 段内特殊字符必须编码(防路径注入)。
    expect(encodeReceiptRequestID('req a?b')).toBe('req%20a%3Fb')
  })
  it('含一个斜杠(host/tail)→ 斜杠保留为分隔符,两段分别编码', () => {
    // 判别核心:斜杠必须是字面 '/'(命中后端双段路由),不能被编成 %2F。
    expect(encodeReceiptRequestID('host.example/tail abc')).toBe('host.example/tail%20abc')
    const out = encodeReceiptRequestID('a/b')
    expect(out).toBe('a/b')
    expect(out).not.toContain('%2F')
  })
  it('host 段内的特殊字符编码,但分隔斜杠不动', () => {
    expect(encodeReceiptRequestID('h?x/t')).toBe('h%3Fx/t')
  })
})

describe('formatMicroUSD(微美分→USD)', () => {
  it('1_000_000 微美分 = $1', () => {
    // 判别核心:必须除以 1e6(变异成不除 → $1000000 → RED)。
    expect(formatMicroUSD(1_000_000)).toBe('$1')
    expect(formatMicroUSD(10_000)).toBe('$0.01')
    expect(formatMicroUSD(0)).toBe('$0')
  })
  it('最小整数微美分(1)= 恰 $0.000001,诚实展示不塌成 $0', () => {
    // 1 micro-USD = 0.000001 USD,正好是可展示最小精度;不能被塌成 $0 误导用户。
    expect(formatMicroUSD(1)).toBe('$0.000001')
    expect(formatMicroUSD(1)).not.toBe('$0')
  })
  it('非有限值 → 占位', () => {
    expect(formatMicroUSD(Number.NaN)).toBe('—')
  })
})

describe('verifyTone / verifyLabel(验签整体结论)', () => {
  it('valid=true → 可信(ok)', () => {
    expect(verifyTone({ valid: true, signature_valid: true })).toBe('ok')
    expect(verifyLabel({ valid: true, signature_valid: true })).toBe('已验签 · 可信')
  })
  it('签名有效但 valid=false(撤销/窗口外/哈希不符)→ warn,不误标可信', () => {
    // 判别核心:必须先看 valid(变异成只看 signature_valid → key_revoked 误判可信 → RED)。
    expect(verifyTone({ valid: false, signature_valid: true })).toBe('warn')
    expect(verifyLabel({ valid: false, signature_valid: true })).toBe('签名有效但不被采信')
  })
  it('签名无效 → 验签失败(danger)', () => {
    expect(verifyTone({ valid: false, signature_valid: false })).toBe('danger')
    expect(verifyLabel({ valid: false, signature_valid: false })).toBe('验签失败')
  })
})

describe('verifyStatusLabel', () => {
  it('已知机器码 → 中文,未知码原样保留诊断', () => {
    expect(verifyStatusLabel('signed-only')).toBe('已签名')
    expect(verifyStatusLabel('unverified')).toBe('未采信')
    expect(verifyStatusLabel('some_new_state')).toBe('some_new_state')
    expect(verifyStatusLabel(undefined)).toBe('—')
  })
})

describe('disputeStatusLabel / disputeStatusTone', () => {
  it('解决类 → ok,驳回 → danger,进行中 → warn', () => {
    expect(disputeStatusTone('resolved')).toBe('ok')
    expect(disputeStatusTone('rejected')).toBe('danger')
    expect(disputeStatusTone('open')).toBe('warn')
    // 判别核心:rejected 不能落进「其它进行中 → warn」分支(变异成默认 warn → RED)。
    expect(disputeStatusTone('rejected')).not.toBe('warn')
  })
  it('标签中文,大小写无关,未知码原样', () => {
    expect(disputeStatusLabel('OPEN')).toBe('待处理')
    expect(disputeStatusLabel('resolved')).toBe('已解决')
    expect(disputeStatusLabel('weird_state')).toBe('weird_state')
    expect(disputeStatusLabel('  ')).toBe('—')
  })
})

describe('validateDisputeReason(发起争议原因校验,镜像 dispute_store.go:197-200)', () => {
  it('正常原因 → null', () => {
    expect(validateDisputeReason('该请求未成功返回但被计费')).toBeNull()
  })
  it('空 / 纯空白 → 拦下(必填)', () => {
    // 判别核心:去空白后为空必须拦(变异成放行 → 后端 400 reason required → RED)。
    expect(validateDisputeReason('')).toContain('请填写')
    expect(validateDisputeReason('   ')).toContain('请填写')
    expect(validateDisputeReason('\n\t ')).toContain('请填写')
  })
  it('去空白后超 4000 字 → 拦下;恰 4000 → 放行', () => {
    // 判别核心:长度按「去空白后」算(变异成按原始长度判 → 与后端不一致 → RED)。
    // 恰好 4000 个有效字 + 首尾空白:去空白后正好 4000,应放行(边界含)。
    const exactly4000 = '  ' + 'x'.repeat(MAX_DISPUTE_REASON_LEN) + '  '
    expect(exactly4000.trim().length).toBe(MAX_DISPUTE_REASON_LEN)
    expect(validateDisputeReason(exactly4000)).toBeNull()
    // 4001 个有效字 → 必须拦(变异成放行 → 后端 400 reason too long → RED)。
    expect(validateDisputeReason('y'.repeat(MAX_DISPUTE_REASON_LEN + 1))).toContain('4000')
  })
  it('仅靠首尾空白凑长度不能绕过(去空白后短才算短)', () => {
    // 5 个有效字 + 一堆空白,去空白后只有 5 字,合法 → null(若按原始长度判会误拦或误放,RED)。
    const padded = ' '.repeat(5000) + 'abcde' + ' '.repeat(5000)
    expect(padded.length).toBeGreaterThan(MAX_DISPUTE_REASON_LEN)
    expect(validateDisputeReason(padded)).toBeNull()
  })
})

describe('用量 CSV 下载主动刷新接线', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-05T10:00:00.000Z'))
    clearAll()
    setSessionTokens({
      sessionToken: 'hus_old',
      refreshToken: 'husr_old',
      sessionExpiresAt: '2026-07-05T10:01:00.000Z',
    })
  })

  afterEach(() => {
    clearAll()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('session 临近到期时先刷新,再用新 token 下载 CSV', async () => {
    const f = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input)
      if (url === '/v1/sessions/refresh') {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            session: {
              session_token: 'hus_new',
              refresh_token: 'husr_new',
              session_expires_at: '2026-07-05T10:15:00Z',
            },
          }),
        } as Response
      }
      return {
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        text: async () => JSON.stringify({ error: { code: 'session_expired', message: '会话已过期' } }),
      } as Response
    })
    vi.stubGlobal('fetch', f)

    await expect(exportUsageCSV('2026-07-01', '2026-07-01')).rejects.toMatchObject({
      status: 401,
      code: 'session_expired',
    })

    // 判别核心:裸下载必须复用统一主动刷新前置。变异(去掉 ensureFreshSessionForPath)→ 不会刷新,RED。
    expect(f).toHaveBeenCalledTimes(2)
    expect(f.mock.calls[0][0]).toBe('/v1/sessions/refresh')
    expect(JSON.parse((f.mock.calls[0][1] as RequestInit).body as string)).toEqual({ refresh_token: 'husr_old' })
    expect(String(f.mock.calls[1][0])).toContain('/v1/me/usage/export.csv?format=csv')
    expect((f.mock.calls[1][1] as RequestInit).headers).toEqual({ Authorization: 'Bearer hus_new' })
    expect(getRefreshToken()).toBe('husr_new')
  })
})
