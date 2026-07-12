import { afterEach, describe, expect, it, vi } from 'vitest'

function memoryStorage(): Storage {
  const data = new Map<string, string>()
  return {
    get length() {
      return data.size
    },
    clear: () => data.clear(),
    getItem: (key: string) => data.get(key) ?? null,
    key: (index: number) => [...data.keys()][index] ?? null,
    removeItem: (key: string) => data.delete(key),
    setItem: (key: string, value: string) => data.set(key, value),
  } as Storage
}

class FakeBroadcastChannel {
  static channels: FakeBroadcastChannel[] = []

  readonly name: string
  onmessage: ((ev: { data: unknown }) => void) | null = null

  constructor(name: string) {
    this.name = name
    FakeBroadcastChannel.channels.push(this)
  }

  postMessage(data: unknown) {
    for (const ch of FakeBroadcastChannel.channels) {
      if (ch !== this && ch.name === this.name) ch.onmessage?.({ data })
    }
  }

  close() {
    FakeBroadcastChannel.channels = FakeBroadcastChannel.channels.filter((ch) => ch !== this)
  }
}

afterEach(() => {
  FakeBroadcastChannel.channels = []
  vi.unstubAllGlobals()
  vi.resetModules()
})

describe('auth store 跨标签 token 同步', () => {
  it('setSessionTokens 把轮换后的 token 广播到其它 store 实例', async () => {
    const storage = memoryStorage()
    vi.stubGlobal('localStorage', storage)
    vi.stubGlobal('window', {
      BroadcastChannel: FakeBroadcastChannel,
      localStorage: storage,
      addEventListener: vi.fn(),
    })

    vi.resetModules()
    const storeA = await import('./store')
    storeA.setSessionTokens({
      sessionToken: 'hus_tab_a_old',
      refreshToken: 'husr_tab_a_old',
      sessionExpiresAt: '2026-07-05T10:00:00Z',
    })

    vi.resetModules()
    const storeB = await import('./store')
    storeB.setSessionTokens({
      sessionToken: 'hus_tab_b_old',
      refreshToken: 'husr_tab_b_old',
      sessionExpiresAt: '2026-07-05T10:00:00Z',
    })
    expect(storeB.getTokens().sessionToken).toBe('hus_tab_b_old')

    storeA.setSessionTokens({
      sessionToken: 'hus_new',
      refreshToken: 'husr_new',
      sessionExpiresAt: '2026-07-05T10:15:00Z',
    })

    // 判别核心:其它标签内存快照必须拿到新 refresh token。变异(不 broadcastSessionTokens)→ 仍是旧值,RED。
    expect(storeB.getTokens().sessionToken).toBe('hus_new')
    expect(storeB.getRefreshToken()).toBe('husr_new')
    expect(storeB.getSessionExpiry()).toBe('2026-07-05T10:15:00Z')
  })

  it('无 window 环境写 token 不抛错', async () => {
    vi.resetModules()
    const store = await import('./store')
    expect(() =>
      store.setSessionTokens({ sessionToken: 'hus_ssr', refreshToken: 'husr_ssr', sessionExpiresAt: null }),
    ).not.toThrow()
  })
})
