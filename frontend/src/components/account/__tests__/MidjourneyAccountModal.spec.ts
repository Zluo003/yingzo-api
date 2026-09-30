import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import MidjourneyAccountModal from '../MidjourneyAccountModal.vue'
import type { Account, AdminGroup } from '@/types'
const { create, update } = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }))
vi.mock('@/api', () => ({ adminAPI: { accounts: { create, update } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (s: string) => s }) }))
const groups = [{ id: 7, name: 'Agent', platform: 'openai', kind: 'agent' }] as AdminGroup[]
function panel(account?: Account) {
  return mount(MidjourneyAccountModal, { props: { show: true, groups, proxies: [], account }, global: { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' } } } })
}
describe('Midjourney account panel', () => {
  beforeEach(() => { vi.clearAllMocks(); create.mockResolvedValue({ id: 1 }); update.mockResolvedValue({ id: 1 }) })
  it('creates a restricted image account from the dedicated panel', async () => {
    const w = panel()
    await w.get('[data-testid="mj-name"]').setValue('MJ account')
    await w.get('[data-testid="mj-api-key"]').setValue('new-secret')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ platform: 'openai', type: 'apikey', group_ids: [7], credentials: { api_key: 'new-secret', base_url: 'https://api.apimart.ai', model_mapping: { 'midjourney-v8.2': 'midjourney-v8.2' } }, extra: { image_account: true, image_provider: 'apimart_midjourney' } }))
    expect(w.emitted('created')?.[0]).toEqual([{ imageModels: ['midjourney-v8.2'] }])
  })
  it('keeps the stored secret when editing without a replacement', async () => {
    const w = panel({ id: 3, name: 'Existing MJ', group_ids: [7], credentials: { api_key: '***', base_url: 'https://api.apimart.ai' }, extra: { image_provider: 'apimart_midjourney', custom_flag: true } } as unknown as Account)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(update).toHaveBeenCalledWith(3, expect.objectContaining({ name: 'Existing MJ', extra: expect.objectContaining({ custom_flag: true, image_provider: 'apimart_midjourney' }) }))
    expect(update.mock.calls[0][1].credentials).not.toHaveProperty('api_key')
  })
  it('rejects endpoint paths before saving', async () => {
    const w = panel()
    await w.get('[data-testid="mj-name"]').setValue('MJ')
    await w.get('[data-testid="mj-api-key"]').setValue('secret')
    await w.get('[data-testid="mj-base-url"]').setValue('https://api.apimart.ai/v1/images')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(create).not.toHaveBeenCalled(); expect(w.get('[role="alert"]').text()).toBe('midjourney.invalidUrl')
  })
})
