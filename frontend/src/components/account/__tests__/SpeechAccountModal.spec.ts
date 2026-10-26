import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import SpeechAccountModal from '../SpeechAccountModal.vue'
import type { Account, AdminGroup } from '@/types'
const { create, update } = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn() }))
vi.mock('@/api', () => ({ adminAPI: { accounts: { create, update } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (s: string) => s }) }))
const groups = [{ id: 7, name: 'Agent', platform: 'openai', kind: 'agent' }] as AdminGroup[]
function panel(account?: Account) {
  return mount(SpeechAccountModal, { props: { show: true, groups, proxies: [], account }, global: { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' } } } })
}
describe('Speech account panel', () => {
  beforeEach(() => { vi.clearAllMocks(); create.mockResolvedValue({ id: 1 }); update.mockResolvedValue({ id: 1 }) })
  it('creates a restricted speech account from the dedicated panel', async () => {
    const w = panel()
    await w.get('[data-testid="speech-name"]').setValue('MiMo account')
    await w.get('[data-testid="speech-api-key"]').setValue('new-secret')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ platform: 'openai', type: 'apikey', group_ids: [7], credentials: { api_key: 'new-secret', base_url: 'https://api.xiaomimimo.com/v1', model_mapping: { 'mimo-v2.5-tts': 'mimo-v2.5-tts', 'mimo-v2.5-tts-voicedesign': 'mimo-v2.5-tts-voicedesign' } }, extra: { speech_account: true, speech_provider: 'mimo' } }))
    expect(w.emitted('created')).toHaveLength(1)
  })
  it('keeps the stored secret when editing without a replacement', async () => {
    const w = panel({ id: 3, name: 'Existing MiMo', group_ids: [7], credentials: { api_key: '***', base_url: 'https://api.xiaomimimo.com/v1' }, extra: { speech_provider: 'mimo', custom_flag: true } } as unknown as Account)
    await w.get('form').trigger('submit'); await flushPromises()
    expect(update).toHaveBeenCalledWith(3, expect.objectContaining({ name: 'Existing MiMo', extra: expect.objectContaining({ custom_flag: true, speech_provider: 'mimo' }) }))
    expect(update.mock.calls[0][1].credentials).not.toHaveProperty('api_key')
  })
  it('rejects endpoint paths before saving', async () => {
    const w = panel()
    await w.get('[data-testid="speech-name"]').setValue('MiMo')
    await w.get('[data-testid="speech-api-key"]').setValue('secret')
    await w.get('[data-testid="speech-base-url"]').setValue('https://api.xiaomimimo.com/v1/audio/speech')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(create).not.toHaveBeenCalled(); expect(w.get('[role="alert"]').text()).toBe('speech.invalidUrl')
  })
  it('clears the saved proxy when editing with no proxy selected', async () => {
    const w = panel({ id: 3, name: 'Existing MiMo', group_ids: [7], proxy_id: 8, credentials: {}, extra: { speech_provider: 'mimo' } } as unknown as Account)
    await w.get('[data-testid="speech-proxy"]').setValue('')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(update).toHaveBeenCalledWith(3, expect.objectContaining({ proxy_id: 0 }))
  })
})
