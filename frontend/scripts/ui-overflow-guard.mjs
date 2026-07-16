/**
 * 全站视觉体检门。
 *
 * 用法：
 * 1. 先启动可访问后端接口的前端：`npm run dev -- --host 127.0.0.1 --port 5176`，
 *    或先执行 `npm run build`，再执行 `npm run preview -- --host 127.0.0.1 --port 5176`。
 * 2. 另开终端执行 `npm run ui:guard`。
 * 3. 可用 GUARD_BASE、GUARD_USER、GUARD_PASS、GUARD_ROUTES、GUARD_SHOT_DIR 覆盖默认值。
 * 4. 退出码 0 表示没有 error（warning 仅提示）；退出码 1 表示发现 error 或守卫无法完成。
 */

import { mkdir, readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from '@playwright/test'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const frontendDir = resolve(scriptDir, '..')
const routerFile = resolve(frontendDir, 'src/app/router.tsx')

const base = (process.env.GUARD_BASE || 'http://127.0.0.1:5176').replace(/\/$/, '')
const username = process.env.GUARD_USER || 'huakai'
const password = process.env.GUARD_PASS || 'huakai'
const shotDir = process.env.GUARD_SHOT_DIR
  ? resolve(process.cwd(), process.env.GUARD_SHOT_DIR)
  : null

function parseRoutes(source) {
  const routes = new Set()
  const routeRegions = [
    ...source.matchAll(/(?:const\s+BUILT_PAGES[^=]*=\s*\{)([\s\S]*?)(?:\n\})/g),
  ]

  for (const region of routeRegions) {
    for (const match of region[1].matchAll(/^\s*(['"])(\/[^'"]*)\1\s*:/gm)) {
      routes.add(match[2])
    }
  }

  for (const match of source.matchAll(/\bpath\s*:\s*(['"])(.*?)\1/g)) {
    routes.add(match[2])
  }

  return [...routes]
    .filter((route) => route.startsWith('/'))
    .filter((route) => !route.includes(':'))
    .filter((route) => route !== '/welcome' && route !== '/rankings')
    .sort((left, right) => left.localeCompare(right))
}

async function loadRoutes() {
  if (process.env.GUARD_ROUTES !== undefined) {
    const overridden = process.env.GUARD_ROUTES
      .split(',')
      .map((route) => route.trim())
      .filter(Boolean)

    if (overridden.length === 0) {
      throw new Error('GUARD_ROUTES 已设置，但没有可用路由')
    }
    return [...new Set(overridden.map((route) => (route.startsWith('/') ? route : `/${route}`)))]
  }

  return parseRoutes(await readFile(routerFile, 'utf8'))
}

async function waitUntilStable(page) {
  try {
    await page.waitForLoadState('networkidle', { timeout: 5_000 })
  } catch {
    await page.waitForTimeout(2_000)
  }
}

async function login(page) {
  await page.goto(`${base}/login`, { waitUntil: 'domcontentloaded' })
  await page.locator('input[type=text]').fill(username)
  await page.locator('input[type=password]').fill(password)

  await Promise.all([
    page.waitForURL((url) => url.pathname !== '/login', { timeout: 15_000 }),
    page.locator('button[type=submit]').click(),
  ])
  await waitUntilStable(page)
}

async function inspectPage(page) {
  return page.evaluate(() => {
    const errors = []
    const warnings = []
    const errorKeys = new Set()
    const warningKeys = new Set()

    const pushError = (type, detail) => {
      const key = `${type}\u0000${detail}`
      if (!errorKeys.has(key)) {
        errorKeys.add(key)
        errors.push({ type, detail })
      }
    }

    const pushWarning = (type, detail) => {
      const key = `${type}\u0000${detail}`
      if (!warningKeys.has(key)) {
        warningKeys.add(key)
        warnings.push({ type, detail })
      }
    }

    const visiblyRendered = (element) => {
      if (!(element instanceof HTMLElement)) return false
      const rect = element.getBoundingClientRect()
      if (rect.width <= 0 || rect.height <= 0) return false
      const style = getComputedStyle(element)
      return style.display !== 'none' && style.visibility !== 'hidden'
    }

    const visibleOverflowCandidate = (element) =>
      element instanceof HTMLElement && element.offsetParent !== null && visiblyRendered(element)

    const selectorPath = (element) => {
      const parts = []
      let current = element

      while (current instanceof HTMLElement && current !== document.body) {
        let part = current.tagName.toLowerCase()
        if (current.id) {
          parts.unshift(`${part}#${CSS.escape(current.id)}`)
          break
        }

        const testId = current.getAttribute('data-testid')
        if (testId) {
          part += `[data-testid="${CSS.escape(testId)}"]`
        } else {
          const stableClasses = [...current.classList].filter((name) => name.length <= 48).slice(0, 2)
          if (stableClasses.length > 0) {
            part += `.${stableClasses.map((name) => CSS.escape(name)).join('.')}`
          }
        }

        const parent = current.parentElement
        if (parent) {
          const peers = [...parent.children].filter((child) => child.tagName === current.tagName)
          if (peers.length > 1) part += `:nth-of-type(${peers.indexOf(current) + 1})`
        }
        parts.unshift(part)
        current = parent
      }

      return parts.slice(-6).join(' > ')
    }

    const documentWidth = document.documentElement.scrollWidth
    const viewportWidth = window.innerWidth
    if (documentWidth > viewportWidth + 4) {
      pushError('页面横向溢出', `scrollWidth=${documentWidth} vw=${viewportWidth}`)
    }

    const overflowCandidates = document.querySelectorAll(
      '.hk-card [data-stat-value], .hk-card .hk-stat-value, .hk-card .stat-value, [data-stat-value], table td, *',
    )
    for (const element of overflowCandidates) {
      if (!(element instanceof HTMLElement)) continue
      if (element.children.length > 0 || !visibleOverflowCandidate(element)) continue

      const text = (element.textContent || '').trim().replace(/\s+/g, ' ')
      if (text.length === 0 || element.scrollWidth <= element.clientWidth + 2) continue

      const style = getComputedStyle(element)
      const hasEllipsisFallback =
        style.whiteSpace === 'nowrap' &&
        style.textOverflow === 'ellipsis' &&
        (style.overflowX === 'hidden' || style.overflowX === 'clip')
      if (hasEllipsisFallback) continue

      pushError(
        '元素撑破容器',
        `${selectorPath(element)} scrollWidth=${element.scrollWidth} clientWidth=${element.clientWidth} 文本="${text.slice(0, 40)}"`,
      )
    }

    const decimalPattern = /\$?\d+\.\d{6,}/g
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    let textNode = walker.nextNode()
    while (textNode) {
      const parent = textNode.parentElement
      if (parent && visiblyRendered(parent)) {
        for (const match of textNode.nodeValue?.match(decimalPattern) || []) {
          pushError('裸长小数', match)
        }
      }
      textNode = walker.nextNode()
    }

    const suspiciousHeader = (text) => ['模型', '名称', '渠道', '用户'].some((word) => text.includes(word))
    const inspectTable = (table) => {
      const headerRow =
        table.querySelector('thead tr') ||
        table.querySelector('[role=row]:has([role=columnheader])') ||
        table.querySelector('[data-list-header]') ||
        table.querySelector('tr')
      if (!headerRow) return
      const headers = [
        ...headerRow.querySelectorAll('th, [role=columnheader], [data-column-header]'),
      ]
      if (headers.length === 0) return

      const rows = [
        ...table.querySelectorAll('tbody tr, [role=row], [role=listitem], [data-list-row]'),
      ].filter((row) => row !== headerRow)
      headers.forEach((header, index) => {
        const title = (header.textContent || '').trim().replace(/\s+/g, ' ')
        if (!suspiciousHeader(title)) return

        for (const row of rows) {
          const cells = row.matches('tr')
            ? [...row.querySelectorAll(':scope > td, :scope > th')]
            : [
                ...row.querySelectorAll(
                  ':scope > [role=cell], :scope > [role=gridcell], :scope > [data-column-cell]',
                ),
              ]
          const cell = cells[index]
          const value = (cell?.textContent || '').trim()
          if (cell && visiblyRendered(cell) && /^\d+$/.test(value)) {
            pushWarning('疑似显数字ID未显名', `列名="${title}" 样例值="${value}"`)
            break
          }
        }
      })
    }

    document
      .querySelectorAll('table, [role=table], [role=grid], [role=list], [data-list]')
      .forEach(inspectTable)

    return { errors, warnings }
  })
}

function safeShotName(route) {
  if (route === '/') return 'root.png'
  return `${route.slice(1).replace(/[^a-zA-Z0-9_-]+/g, '_') || 'route'}.png`
}

async function main() {
  const routes = await loadRoutes()
  if (routes.length === 0) throw new Error('没有解析到可巡检路由')
  if (shotDir) await mkdir(shotDir, { recursive: true })

  console.log(`视觉体检开始：base=${base}，路由数=${routes.length}`)
  const browser = await chromium.launch({ headless: true })
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  let errorCount = 0
  let warningCount = 0
  let errorRouteCount = 0

  try {
    await login(page)

    for (const route of routes) {
      try {
        await page.goto(new URL(route, `${base}/`).href, { waitUntil: 'domcontentloaded' })
        await waitUntilStable(page)
        const result = await inspectPage(page)

        for (const violation of result.errors) {
          console.error(`[ERROR] ${route} | ${violation.type} | ${violation.detail}`)
        }
        for (const violation of result.warnings) {
          console.warn(`[WARNING] ${route} | ${violation.type} | ${violation.detail}`)
        }

        errorCount += result.errors.length
        warningCount += result.warnings.length
        if (result.errors.length > 0) {
          errorRouteCount += 1
          if (shotDir) {
            await page.screenshot({ path: resolve(shotDir, safeShotName(route)), fullPage: true })
          }
        }
      } catch (error) {
        errorCount += 1
        errorRouteCount += 1
        console.error(`[ERROR] ${route} | 路由巡检失败 | ${error instanceof Error ? error.message : String(error)}`)
        if (shotDir) {
          await page.screenshot({ path: resolve(shotDir, safeShotName(route)), fullPage: true }).catch(() => {})
        }
      }
    }
  } finally {
    await browser.close()
  }

  console.log(
    `视觉体检汇总：路由=${routes.length}，error=${errorCount}（涉及路由=${errorRouteCount}），warning=${warningCount}`,
  )
  process.exitCode = errorCount > 0 ? 1 : 0
}

main().catch((error) => {
  console.error(`[ERROR] 守卫启动失败 | ${error instanceof Error ? error.stack || error.message : String(error)}`)
  console.log('视觉体检汇总：error=1，warning=0')
  process.exitCode = 1
})
