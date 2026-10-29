import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  del: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    get,
    post,
    delete: del
  }
}))

import { create, inspect, list, remove } from '@/api/admin/ipBans'

describe('admin ipBans API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    del.mockReset()
  })

  it('lists ban rules with pagination and omits an empty search', async () => {
    const data = { items: [], total: 0, page: 2, page_size: 10, pages: 1 }
    get.mockResolvedValue({ data })

    await expect(list(2, 10, { search: '' })).resolves.toBe(data)
    expect(get).toHaveBeenCalledWith('/admin/ip-bans', {
      params: { page: 2, page_size: 10, search: undefined },
      signal: undefined
    })

    await list(1, 20, { search: '203.0' })
    expect(get).toHaveBeenLastCalledWith('/admin/ip-bans', {
      params: { page: 1, page_size: 20, search: '203.0' },
      signal: undefined
    })
  })

  it('creates a ban with the target and optional reason', async () => {
    const data = { ban: { id: 1, ip: '203.0.113.7', reason: '', created_by: 1, created_at: '' }, created: true }
    post.mockResolvedValue({ data })

    await expect(create({ ip: '203.0.113.7' })).resolves.toBe(data)
    expect(post).toHaveBeenCalledWith('/admin/ip-bans', { ip: '203.0.113.7' })
  })

  it('removes a ban by id', async () => {
    del.mockResolvedValue({ data: { message: 'IP ban removed' } })

    await remove(7)
    expect(del).toHaveBeenCalledWith('/admin/ip-bans/7')
  })

  it('inspects an IP via the query string', async () => {
    const data = {
      ip: '2001:db8::1',
      network_key: '2001:db8::/64',
      banned: false,
      ban: null,
      registered_total: 0,
      registered_today: 0,
      bannable: true
    }
    get.mockResolvedValue({ data })

    await expect(inspect('2001:db8::1')).resolves.toBe(data)
    expect(get).toHaveBeenCalledWith('/admin/ip-bans/inspect', { params: { ip: '2001:db8::1' } })
  })
})
