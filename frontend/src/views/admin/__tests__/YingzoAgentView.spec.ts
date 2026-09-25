import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import YingzoAgentView from '../YingzoAgentView.vue'
import type { AgentGroupModel } from '@/api/admin/agentModels'
import type { AdminGroup } from '@/types'

const { getAllGroups, getAgentModels, syncAgentModels, updateAgentModel, deleteAgentModel } =
  vi.hoisted(() => ({
    getAllGroups: vi.fn(),
    getAgentModels: vi.fn(),
    syncAgentModels: vi.fn(),
    updateAgentModel: vi.fn(),
    deleteAgentModel: vi.fn(),
  }))

vi.mock('@/api', () => ({
  adminAPI: {
    groups: { getAll: getAllGroups },
  },
}))

vi.mock('@/api/admin/agentModels', () => ({
  agentModelsAPI: { getAgentModels, syncAgentModels, updateAgentModel, deleteAgentModel },
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}|${JSON.stringify(params)}` : key,
  }),
}))

vi.mock('vue-router', () => ({
  RouterLink: { template: '<a><slot /></a>' },
}))

// AppLayout 会拉起完整的 i18n/stores 初始化，与上面的 vue-i18n mock 冲突；
// 布局壳不参与本页断言，直接透传 slot。
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<div><slot /></div>' },
}))

const AGENT_GROUP = {
  id: 7,
  name: 'Yingzo Agent',
  platform: 'openai',
  status: 'active',
  kind: 'agent',
  system_code: 'yingzo',
  account_count: 4,
  active_account_count: 3,
} as unknown as AdminGroup

const OTHER_GROUP = {
  id: 8,
  name: 'standard',
  platform: 'openai',
  status: 'active',
  kind: 'standard',
  system_code: '',
} as unknown as AdminGroup

function model(overrides: Partial<AgentGroupModel>): AgentGroupModel {
  return {
    id: 1,
    group_id: 7,
    platform: 'openai',
    model_code: 'gpt-5.4',
    media_type: 'text',
    enabled: true,
    available: true,
    excluded: false,
    discovered_at: '2026-01-01T00:00:00Z',
    last_seen_at: '2026-01-01T00:00:00Z',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    prices: [],
    rate_multiplier: null,
    ...overrides,
  }
}

const CATALOG = {
  models: [
    model({ id: 1, model_code: 'gpt-5.4', media_type: 'text', rate_multiplier: 1.2 }),
    model({
      id: 2,
      model_code: 'gpt-image-2',
      media_type: 'image',
      prices: [{ resolution: '1K', billing_unit: 'image', unit_price: 0.1 }],
    }),
    model({
      id: 3,
      model_code: 'seedance-2.5',
      platform: 'video',
      media_type: 'video',
      prices: [{ resolution: '720p', billing_unit: 'second', unit_price: 0.2 }],
    }),
  ],
}

function inputValue(wrapper: VueWrapper, testid: string): string {
  return (wrapper.get(`[data-testid="${testid}"]`).element as HTMLInputElement).value
}

async function mountView(): Promise<VueWrapper> {
  const wrapper = mount(YingzoAgentView, {
    global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } },
  })
  await flushPromises()
  return wrapper
}

describe('admin YingzoAgentView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getAllGroups.mockResolvedValue([OTHER_GROUP, AGENT_GROUP])
    getAgentModels.mockResolvedValue(CATALOG)
    syncAgentModels.mockResolvedValue(CATALOG)
    updateAgentModel.mockResolvedValue(CATALOG)
    deleteAgentModel.mockResolvedValue({ deleted: true })
  })

  it('locates the built-in agent group and loads its catalog', async () => {
    const wrapper = await mountView()

    expect(getAgentModels).toHaveBeenCalledWith(7)
    expect(wrapper.get('[data-testid="yingzo-agent-account-count"]').text()).toBe('3 / 4')
    expect(wrapper.get('[data-testid="yingzo-agent-model-count"]').text()).toBe('3 / 3')
    expect(wrapper.get('[data-testid="yingzo-agent-row-1"]').text()).toContain('gpt-5.4')
  })

  it('splits models across the text, image, and video tabs', async () => {
    const wrapper = await mountView()

    expect(wrapper.get('[data-testid="yingzo-agent-tab-text"]').text()).toContain('(1)')
    expect(wrapper.get('[data-testid="yingzo-agent-tab-image"]').text()).toContain('(1)')
    expect(wrapper.get('[data-testid="yingzo-agent-tab-video"]').text()).toContain('(1)')

    // 默认停在文本分区：倍率输入框回填已保存的值，且不出现媒体价格列。
    expect(inputValue(wrapper, 'yingzo-agent-rate-1')).toBe('1.2')
    expect(wrapper.find('[data-testid="yingzo-agent-price-1-1K"]').exists()).toBe(false)
  })

  it('only enables save when a draft actually differs and sends the text rate', async () => {
    const wrapper = await mountView()
    const save = wrapper.get('[data-testid="yingzo-agent-save"]')
    expect(save.attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="yingzo-agent-rate-1"]').setValue('2.5')
    expect(save.attributes('disabled')).toBeUndefined()

    await save.trigger('click')
    await flushPromises()

    expect(updateAgentModel).toHaveBeenCalledWith(7, 1, {
      media_type: 'text',
      enabled: true,
      rate_multiplier: 2.5,
    })
    expect(wrapper.get('[data-testid="yingzo-agent-success"]').exists()).toBe(true)
  })

  it('clears a text rate when the input is emptied', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="yingzo-agent-rate-1"]').setValue('')
    await wrapper.get('[data-testid="yingzo-agent-save"]').trigger('click')
    await flushPromises()

    expect(updateAgentModel).toHaveBeenCalledWith(7, 1, {
      media_type: 'text',
      enabled: true,
      rate_multiplier: null,
    })
  })

  it('sends per-resolution unit prices for image models', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="yingzo-agent-tab-image"]').trigger('click')
    expect(inputValue(wrapper, 'yingzo-agent-price-2-1K')).toBe('0.1')
    expect(inputValue(wrapper, 'yingzo-agent-price-2-4K')).toBe('')
    expect(
      (wrapper.get('[data-testid="yingzo-agent-resolution-enabled-2-1K"]').element as HTMLInputElement)
        .checked,
    ).toBe(true)
    expect(
      (wrapper.get('[data-testid="yingzo-agent-resolution-enabled-2-2K"]').element as HTMLInputElement)
        .checked,
    ).toBe(false)

    await wrapper.get('[data-testid="yingzo-agent-resolution-enabled-2-4K"]').setValue(true)
    await wrapper.get('[data-testid="yingzo-agent-price-2-4K"]').setValue('0.4')
    await wrapper.get('[data-testid="yingzo-agent-save"]').trigger('click')
    await flushPromises()

    expect(updateAgentModel).toHaveBeenCalledWith(7, 2, {
      media_type: 'image',
      enabled: true,
      prices: [
        { resolution: '1K', unit_price: 0.1 },
        { resolution: '4K', unit_price: 0.4 },
      ],
    })
  })

  it('persists a disabled resolution without exposing it downstream', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="yingzo-agent-tab-image"]').trigger('click')
    await wrapper.get('[data-testid="yingzo-agent-price-2-2K"]').setValue('0.2')
    await wrapper.get('[data-testid="yingzo-agent-resolution-enabled-2-2K"]').setValue(false)
    await wrapper.get('[data-testid="yingzo-agent-save"]').trigger('click')
    await flushPromises()

    expect(updateAgentModel).toHaveBeenCalledWith(7, 2, {
      media_type: 'image',
      enabled: true,
      prices: [
        { resolution: '1K', unit_price: 0.1 },
        { resolution: '2K', unit_price: 0.2, enabled: false },
      ],
    })
  })

  it('lets the admin correct a mis-detected media type', async () => {
    // 新模型（尤其带上游别名的 Gemini 图像模型）可能被识别成文本，页面必须能手工改。
    getAgentModels.mockResolvedValue({
      models: [
        model({ id: 1, model_code: 'gemini-3-pro-image-preview', media_type: 'text', rate_multiplier: 1 }),
      ],
    })
    const wrapper = await mountView()

    await wrapper.get('[data-testid="yingzo-agent-media-type-1"]').setValue('image')
    // 切成图片后立刻出现按张计价的档位输入，不必先保存再切页签。
    expect(wrapper.find('[data-testid="yingzo-agent-price-1-1K"]').exists()).toBe(true)

    await wrapper.get('[data-testid="yingzo-agent-resolution-enabled-1-1K"]').setValue(true)
    await wrapper.get('[data-testid="yingzo-agent-price-1-1K"]').setValue('0.1')
    await wrapper.get('[data-testid="yingzo-agent-save"]').trigger('click')
    await flushPromises()

    expect(updateAgentModel).toHaveBeenCalledWith(7, 1, {
      media_type: 'image',
      enabled: true,
      prices: [{ resolution: '1K', unit_price: 0.1 }],
    })
  })

  it('offers the official resolutions for a known video model', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="yingzo-agent-tab-video"]').trigger('click')
    expect(inputValue(wrapper, 'yingzo-agent-price-3-720p')).toBe('0.2')
    // seedance-2.5 官方档位是 480p/720p/1080p：不提供 4K 输入框。
    expect(wrapper.find('[data-testid="yingzo-agent-price-3-4K"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="yingzo-agent-price-3-480p"]').exists()).toBe(true)
  })

  it('excludes a model and reloads the catalog', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="yingzo-agent-delete-1"]').trigger('click')
    await flushPromises()

    expect(deleteAgentModel).toHaveBeenCalledWith(7, 1)
    expect(getAgentModels).toHaveBeenCalledTimes(2)
  })

  it('syncs the catalog from the accounts bound to the group', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="yingzo-agent-sync"]').trigger('click')
    await flushPromises()

    expect(syncAgentModels).toHaveBeenCalledWith(7)
    expect(wrapper.get('[data-testid="yingzo-agent-success"]').text()).toContain('count')
  })

  it('surfaces backend failures instead of silently keeping stale prices', async () => {
    const wrapper = await mountView()
    updateAgentModel.mockRejectedValue({ response: { data: { message: 'boom' } } })

    await wrapper.get('[data-testid="yingzo-agent-rate-1"]').setValue('3')
    await wrapper.get('[data-testid="yingzo-agent-save"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="yingzo-agent-error"]').text()).toBe('boom')
  })

  it('shows the flat error the api client rejects with', async () => {
    // apiClient 业务错误 reject 的是 {status, code, message}，没有 response.data；
    // 早先这里只读 response.data，导致页面永远显示兜底文案而看不到真实原因。
    getAgentModels.mockRejectedValue({ status: 400, code: 400, message: 'Agent model catalog is not configured' })

    const wrapper = await mountView()

    expect(wrapper.get('[data-testid="yingzo-agent-error"]').text()).toBe(
      'Agent model catalog is not configured',
    )
  })

  it('explains a missing system group instead of showing an empty catalog', async () => {
    getAllGroups.mockResolvedValue([OTHER_GROUP])

    const wrapper = await mountView()

    expect(getAgentModels).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="yingzo-agent-error"]').text()).toContain(
      'admin.yingzoAgent.groupMissing',
    )
  })
})
