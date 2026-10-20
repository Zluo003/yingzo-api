import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import MediaInputAuditCard from '../MediaInputAuditCard.vue'

const api = vi.hoisted(() => ({
  getConfig: vi.fn(),
  saveConfig: vi.fn(),
  probe: vi.fn(),
  listEvents: vi.fn(),
  getEvent: vi.fn(),
  getAsset: vi.fn()
}))
vi.mock('@/api/admin/mediaInputAudit', () => ({ mediaInputAuditAPI: api }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }) }))
const baseConfig = () => ({
  primary: {
    protocol: 'openai',
    domain: 'https://first.example.com',
    model: 'vision-a',
    api_key: '',
    key_configured: true,
    timeout_seconds: 30
  },
  fallback: {
    protocol: 'gemini',
    domain: 'https://second.example.com',
    model: 'vision-b',
    api_key: '',
    key_configured: true,
    timeout_seconds: 60
  },
  admin_emails: ['admin@example.com'],
  retention_days: 30,
  political_names: true,
  public_figure_names: true,
  nudity: true
})
async function setup() {
  const wrapper = mount(MediaInputAuditCard, {
    global: {
      plugins: [createI18n({ legacy: false, locale: 'zh', messages: {} })],
      stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' } }
    }
  })
  await flushPromises()
  return wrapper
}
describe('account media input audit configuration', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.getConfig.mockResolvedValue(baseConfig())
    api.listEvents.mockResolvedValue([])
    api.saveConfig.mockResolvedValue(baseConfig())
    api.probe.mockResolvedValue({ decision: 'allow' })
  })
  it('saves independent protocols and domains without copying the masked key', async () => {
    const w = await setup()
    const protocols = w.findAll('select')
    await protocols[0].setValue('gemini')
    await protocols[1].setValue('openai')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith(
      expect.objectContaining({
        primary: expect.objectContaining({
          protocol: 'gemini',
          domain: 'https://first.example.com',
          api_key: ''
        }),
        fallback: expect.objectContaining({
          protocol: 'openai',
          domain: 'https://second.example.com',
          api_key: ''
        })
      })
    )
    expect(w.text()).toContain('审核配置已保存')
  })
  it('tests only the selected reviewer with every supplied image', async () => {
    const w = await setup()
    const areas = w.findAll('textarea')
    await areas[1].setValue('test prompt')
    await areas[2].setValue('https://a.example/1.png\nhttps://b.example/2.png')
    const button = w.findAll('button').find((b) => b.text().includes('测试 · 备用'))!
    await button.trigger('click')
    await flushPromises()
    expect(api.probe).toHaveBeenCalledWith('fallback', 'test prompt', [
      'https://a.example/1.png',
      'https://b.example/2.png'
    ])
    expect(api.saveConfig).not.toHaveBeenCalled()
  })
  it('shows stored prompt and evidence metadata through the authenticated case API', async () => {
    api.listEvents.mockResolvedValue([
      {
        id: 'case-1',
        user_id: 7,
        user_email: 'user@example.com',
        account_name: 'NSFW',
        model: 'seedance-2.5',
        decision: 'block',
        category: 'nudity',
        created_at: '2026-10-20T00:00:00Z'
      }
    ])
    api.getEvent.mockResolvedValue({
      id: 'case-1',
      user_id: 7,
      account_id: 3,
      api_key_id: 9,
      user_email: 'user@example.com',
      model: 'seedance-2.5',
      account_name: 'NSFW',
      created_at: '2026-10-20T00:00:00Z',
      expires_at: '2026-11-20T00:00:00Z',
      notify_attempts: 0,
      notify_pending: true,
      evidence: {
        input: { prompt: '<script>untrusted</script>', video_channel_id: 'nsfw-channel' },
        normalized_prompt: 'normalized',
        result: { policy_version: 'media-input-v1' },
        assets: [
          { original_url: 'https://ref.example/image', sha256: 'saved-hash', mime: 'image/png' }
        ]
      }
    })
    const w = await setup()
    await w
      .findAll('button')
      .find((b) => b.text().includes('查看证据'))!
      .trigger('click')
    await flushPromises()
    expect(api.getEvent).toHaveBeenCalledWith('case-1')
    expect(w.text()).toContain('saved-hash')
    expect(w.text()).toContain('nsfw-channel')
    expect(w.find('script').exists()).toBe(false)
    expect(w.find('a[href="https://ref.example/image"]').exists()).toBe(false)
  })
})
