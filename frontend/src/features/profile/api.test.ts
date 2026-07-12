import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))

vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import {
  deletePasskey,
  listPasskeys,
  registerPasskeyBegin,
  registerPasskeyFinish,
} from './api'

describe('Passkey API 接线', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({ passkeys: [] })
    client.send.mockResolvedValue({})
  })

  it('列表锁定带尾斜杠的真实挂载路径并透传 signal', async () => {
    const controller = new AbortController()
    await listPasskeys(controller.signal)
    expect(client.get).toHaveBeenCalledWith('/v1/me/passkeys/', { signal: controller.signal })
  })

  it('注册 begin/finish 锁定路径、session_id、name、step_up 与 credential', async () => {
    const proof = { password: 'current-secret' }
    const credential = { id: 'cred-1', response: { attestationObject: 'AQID' } }
    await registerPasskeyBegin('MacBook', proof)
    await registerPasskeyFinish('ceremony-1', 'MacBook', proof, credential)

    expect(client.send.mock.calls).toEqual([
      [
        'POST',
        '/v1/me/passkeys/register/begin',
        { name: 'MacBook', step_up: proof },
      ],
      [
        'POST',
        '/v1/me/passkeys/register/finish',
        { session_id: 'ceremony-1', name: 'MacBook', step_up: proof, credential },
      ],
    ])
  })

  it('删除必须携带 step_up，不能退化为空 body', async () => {
    await deletePasskey(17, { two_factor_code: '654321' })
    expect(client.send).toHaveBeenCalledWith('DELETE', '/v1/me/passkeys/17', {
      step_up: { two_factor_code: '654321' },
    })
  })
})
