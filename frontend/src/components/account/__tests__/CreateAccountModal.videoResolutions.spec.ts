import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { createAccountMock } = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: true }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      probeUpstreamBilling: vi.fn(),
      syncUpstreamModels: vi.fn(),
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }),
      importCodexSession: vi.fn(),
      createOpenAICodexPAT: vi.fn(),
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({}),
    },
    tlsFingerprintProfiles: { list: vi.fn().mockResolvedValue([]) },
  },
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([]),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

const passthroughStub = (name: string) =>
  defineComponent({ name, template: '<div><slot /></div>' })

async function mountVideoModal() {
  const wrapper = mount(CreateAccountModal, {
    props: { show: false, proxies: [], groups: [], mode: 'video' as const },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        OAuthAuthorizationFlow: passthroughStub('OAuthAuthorizationFlow'),
        ConfirmDialog: true,
        Select: true,
        Icon: true,
        PlatformIcon: true,
        ProxySelector: true,
        ProxyAdBanner: true,
        GroupSelector: passthroughStub('GroupSelector'),
        ModelWhitelistSelector: true,
        QuotaLimitCard: true,
      },
    },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

const resolutionChip = (wrapper: ReturnType<typeof mount>, model: string, resolution: string) =>
  wrapper.get(`[data-testid="video-resolution-${model}-${resolution}"]`)

const durationChip = (wrapper: ReturnType<typeof mount>, model: string, seconds: number) =>
  wrapper.get(`[data-testid="video-duration-${model}-${seconds}"]`)

async function submitVideoAccount(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('form#create-account-form input[type="text"]').setValue('video account')
  await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-video')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
  return createAccountMock.mock.calls.at(-1)?.[0]
}

// 模型与上游解耦后的默认勾选：全部模型的全部官方档位。
const DEFAULT_RESOLUTIONS: Record<string, string[]> = {
  'seedance-2.0': ['480p', '720p', '1080p', '4K'],
  'seedance-2.0-fast': ['480p', '720p'],
  'seedance-2.5': ['480p', '720p', '1080p'],
  'grok-imagine-video-1.5': ['480p', '720p', '1080p'],
  'kling-v3-omni': ['720p', '1080p', '4K'],
}
const DEFAULT_DURATIONS: Record<string, number[]> = {
  'seedance-2.0': [4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15],
  'seedance-2.0-fast': [4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15],
  'seedance-2.5': [4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30],
  'grok-imagine-video-1.5': [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15],
  'kling-v3-omni': [3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15],
}

describe('CreateAccountModal video resolutions', () => {
  beforeEach(() => {
    createAccountMock.mockReset().mockResolvedValue({})
  })

  it('renders every official resolution of the selected models', async () => {
    const wrapper = await mountVideoModal()

    for (const [model, resolutions] of Object.entries(DEFAULT_RESOLUTIONS)) {
      for (const resolution of resolutions) {
        expect(resolutionChip(wrapper, model, resolution).exists()).toBe(true)
      }
    }
    // 2.0 Fast 没有 1080p/4K 这种官方档位，连展示都不该有。
    expect(wrapper.find('[data-testid="video-resolution-seedance-2.0-fast-1080p"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="video-resolution-seedance-2.5-4K"]').exists()).toBe(false)
  })

  it('never disables resolution chips and default-checks every official tier', async () => {
    const wrapper = await mountVideoModal()

    for (const [model, resolutions] of Object.entries(DEFAULT_RESOLUTIONS)) {
      for (const resolution of resolutions) {
        const chip = resolutionChip(wrapper, model, resolution)
        expect(chip.attributes('disabled')).toBeUndefined()
        expect(chip.attributes('aria-pressed')).toBe('true')
      }
    }

    // 切换上游不再影响模型与档位：路由由账号配置 + 适配器闸门决定。
    // 平台选项随模型联动：先取消 grok/kling，newtoken 才会出现在可选列表里。
    await wrapper.get('[data-testid="video-model-dropdown"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="video-model-grok-imagine-video-1.5"]').trigger('click')
    await wrapper.get('[data-testid="video-model-kling-v3-omni"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="video-provider-select"]').setValue('newtoken')
    await flushPromises()

    // 取消勾选的模型不再渲染档位区，剩余模型的档位不受切上游影响。
    expect(wrapper.find('[data-testid="video-resolution-grok-imagine-video-1.5-480p"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="video-resolution-kling-v3-omni-720p"]').exists()).toBe(false)
    for (const model of ['seedance-2.0', 'seedance-2.0-fast', 'seedance-2.5']) {
      for (const resolution of DEFAULT_RESOLUTIONS[model]) {
        const chip = resolutionChip(wrapper, model, resolution)
        expect(chip.attributes('disabled')).toBeUndefined()
        expect(chip.attributes('aria-pressed')).toBe('true')
      }
    }
    expect(wrapper.find('[data-testid^="video-resolution-hint-"]').exists()).toBe(false)
  })

  it('submits the full resolution and duration maps by default', async () => {
    const wrapper = await mountVideoModal()

    const payload = await submitVideoAccount(wrapper)
    expect(payload.extra.video_model_resolutions).toEqual(DEFAULT_RESOLUTIONS)
    expect(payload.extra.video_model_durations).toEqual(DEFAULT_DURATIONS)
  })

  it('renders and submits per-model duration chips', async () => {
    const wrapper = await mountVideoModal()

    // grok 从 1 秒起、可灵从 3 秒起：模型规格差异直接体现在 chips 上。
    expect(durationChip(wrapper, 'grok-imagine-video-1.5', 1).exists()).toBe(true)
    expect(wrapper.find('[data-testid="video-duration-kling-v3-omni-1"]').exists()).toBe(false)
    expect(durationChip(wrapper, 'kling-v3-omni', 3).exists()).toBe(true)
    expect(durationChip(wrapper, 'seedance-2.5', 30).exists()).toBe(true)
    expect(wrapper.find('[data-testid="video-duration-seedance-2.0-fast-16"]').exists()).toBe(false)
  })

  it('omits unchecked entries and unchecked models, and never unchecks a model to empty', async () => {
    const wrapper = await mountVideoModal()

    await resolutionChip(wrapper, 'seedance-2.0', '480p').trigger('click')
    await durationChip(wrapper, 'seedance-2.0-fast', 4).trigger('click')
    await durationChip(wrapper, 'seedance-2.0-fast', 5).trigger('click')
    // 取消勾选模型白名单后，该模型的档位不应再写入。
    await wrapper.get('[data-testid="video-model-dropdown"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="video-model-seedance-2.5"]').trigger('click')
    await flushPromises()

    const payload = await submitVideoAccount(wrapper)
    expect(payload.extra.video_model_resolutions['seedance-2.0']).toEqual(['720p', '1080p', '4K'])
    expect(payload.extra.video_model_resolutions).not.toHaveProperty('seedance-2.5')
    expect(payload.extra.video_model_durations['seedance-2.0-fast']).toEqual([6, 7, 8, 9, 10, 11, 12, 13, 14, 15])
    expect(payload.extra.video_model_durations).not.toHaveProperty('seedance-2.5')

    // 逐个取消到只剩一档：最后一次取消应被拒绝（不能把模型勾空）。
    for (const seconds of [6, 7, 8, 9, 10, 11, 12, 13, 14]) {
      await durationChip(wrapper, 'seedance-2.0-fast', seconds).trigger('click')
    }
    await flushPromises()
    await durationChip(wrapper, 'seedance-2.0-fast', 15).trigger('click')
    await flushPromises()
    expect(durationChip(wrapper, 'seedance-2.0-fast', 15).attributes('aria-pressed')).toBe('true')

    const narrowed = await submitVideoAccount(wrapper)
    expect(narrowed.extra.video_model_durations['seedance-2.0-fast']).toEqual([15])
    expect(Object.keys(narrowed.extra)).toContain('video_provider')
  })

  it('never emits an empty entry for a selected model', async () => {
    // serialize 层的兜底：即便拿到全空选择，也不产生空条目（空条目读作"不限制"）。
    const wrapper = await mountVideoModal()
    const payload = await submitVideoAccount(wrapper)
    for (const resolutions of Object.values(
      payload.extra.video_model_resolutions as Record<string, string[]>
    )) {
      expect(resolutions.length).toBeGreaterThan(0)
    }
    for (const durations of Object.values(
      payload.extra.video_model_durations as Record<string, number[]>
    )) {
      expect(durations.length).toBeGreaterThan(0)
    }
  })
})
