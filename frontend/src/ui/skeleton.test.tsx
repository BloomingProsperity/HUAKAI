import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { SkeletonRows } from './Skeleton'

describe('SkeletonRows 结构', () => {
  it('按指定行列数生成表格占位', () => {
    const html = renderToStaticMarkup(<SkeletonRows rows={4} cols={3} />)
    expect(html.match(/data-skeleton-row="true"/g)).toHaveLength(4)
    expect(html.match(/data-skeleton-cell="true"/g)).toHaveLength(12)
  })
})
