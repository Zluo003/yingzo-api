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

async function submitVideoAccount(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('form#create-account-form input[type="text"]').setValue('video account')
  await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-video')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
  return createAccountMock.mock.calls.at(-1)?.[0]
}

describe('CreateAccountModal video resolutions', () => {
  beforeEach(() => {
    createAccountMock.mockReset().mockResolvedValue({})
  })

  it('renders every official resolution of the selected models', async () => {
    const wrapper = await mountVideoModal()

    for (const resolution of ['480p', '720p', '1080p', '4K']) {
      expect(resolutionChip(wrapper, 'seedance-2.0', resolution).exists()).toBe(true)
    }
    for (const resolution of ['480p', '720p']) {
      expect(resolutionChip(wrapper, 'seedance-2.0-fast', resolution).exists()).toBe(true)
    }
    for (const resolution of ['480p', '720p', '1080p']) {
      expect(resolutionChip(wrapper, 'seedance-2.5', resolution).exists()).toBe(true)
    }
    // 2.0 Fast 没有 1080p/4K 这种官方档位，连展示都不该有。
    expect(wrapper.find('[data-testid="video-resolution-seedance-2.0-fast-1080p"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="video-resolution-seedance-2.5-4K"]').exists()).toBe(false)
  })

  it('disables and explains the chips the selected upstream cannot serve', async () => {
    const wrapper = await mountVideoModal()

    // aigod 目录含 seedance-2.0-4k，所以 4K 可选、也不应有"不支持"提示。
    expect(resolutionChip(wrapper, 'seedance-2.0', '4K').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="video-resolution-hint-seedance-2.0"]').exists()).toBe(false)

    // 切到 newtoken：它没有 4K 变体，4K 与 480p 都应禁用并给出提示。
    await wrapper.get('[data-testid="video-provider-newtoken"]').trigger('click')
    await flushPromises()

    const chip = resolutionChip(wrapper, 'seedance-2.0', '4K')
    expect(chip.attributes('disabled')).toBeDefined()
    expect(chip.attributes('aria-pressed')).toBe('false')
    expect(chip.classes()).toContain('disabled:opacity-40')
    expect(wrapper.get('[data-testid="video-resolution-hint-seedance-2.0"]').text())
      .toContain('admin.accounts.video.resolutionUnsupported')
    // 2.0-fast 官方档位不含 4K，因此没有该 chip；
    // 但 newtoken 只提供它的 720p，480p 不被支持，故该模型仍有提示。
    expect(wrapper.find('[data-testid="video-resolution-seedance-2.0-fast-4K"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="video-resolution-hint-seedance-2.0-fast"]').text())
      .toContain('admin.accounts.video.resolutionUnsupported')
  })

  it('checks every resolution the selected upstream can serve and submits the map', async () => {
    const wrapper = await mountVideoModal()

    // 默认勾选 = aigod 能服务的全部档位（含 4K：目录里有 seedance-2.0-4k）。
    for (const [model, resolutions] of Object.entries({
      'seedance-2.0': ['480p', '720p', '1080p', '4K'],
      'seedance-2.0-fast': ['480p', '720p'],
      'seedance-2.5': ['480p', '720p', '1080p'],
    })) {
      for (const resolution of resolutions) {
        expect(resolutionChip(wrapper, model, resolution).attributes('aria-pressed')).toBe('true')
      }
    }

    const payload = await submitVideoAccount(wrapper)
    expect(payload.extra.video_model_resolutions).toEqual({
      'seedance-2.0': ['480p', '720p', '1080p', '4K'],
      'seedance-2.0-fast': ['480p', '720p'],
      'seedance-2.5': ['480p', '720p', '1080p'],
    })
  })

  it('re-derives availability when the upstream changes and drops stale checks', async () => {
    const wrapper = await mountVideoModal()

    await wrapper.get('[data-testid="video-provider-newtoken"]').trigger('click')
    await flushPromises()

    expect(resolutionChip(wrapper, 'seedance-2.0', '480p').attributes('disabled')).toBeDefined()
    expect(resolutionChip(wrapper, 'seedance-2.0', '480p').attributes('aria-pressed')).toBe('false')
    // 4K 被 newtoken 裁掉（其目录无 4K 变体）。
    expect(resolutionChip(wrapper, 'seedance-2.0', '4K').attributes('disabled')).toBeDefined()
    // 2.5 的 1080p 对 newtoken 可服务（sd2.5-1080p-official），不再禁用。
    expect(resolutionChip(wrapper, 'seedance-2.5', '1080p').attributes('disabled')).toBeUndefined()
    expect(resolutionChip(wrapper, 'seedance-2.5', '720p').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-testid="video-resolution-hint-seedance-2.0"]').text())
      .toContain('admin.accounts.video.resolutionUnsupported')

    const payload = await submitVideoAccount(wrapper)
    expect(payload.extra.video_model_resolutions).toEqual({
      'seedance-2.0': ['720p', '1080p'],
      'seedance-2.0-fast': ['720p'],
      'seedance-2.5': ['720p', '1080p'],
    })
  })

  it('omits unchecked resolutions and unchecked models, and never unchecks a model to empty', async () => {
    const wrapper = await mountVideoModal()

    await resolutionChip(wrapper, 'seedance-2.0', '480p').trigger('click')
    await resolutionChip(wrapper, 'seedance-2.0-fast', '480p').trigger('click')
    // 2.0-fast 只剩 720p，这一下取消应被拒绝（不能把模型勾空）。
    await resolutionChip(wrapper, 'seedance-2.0-fast', '720p').trigger('click')
    await flushPromises()

    const narrowed = await submitVideoAccount(wrapper)
    expect(narrowed.extra.video_model_resolutions).toEqual({
      // 取消 480p 后其余保持默认勾选（aigod 的 2.0 含 4K）
      'seedance-2.0': ['720p', '1080p', '4K'],
      'seedance-2.0-fast': ['720p'],
      'seedance-2.5': ['480p', '720p', '1080p'],
    })

    // 取消勾选模型白名单后，该模型的分辨率不应再写入。
    await wrapper.get('[data-testid="video-model-seedance-2.5"]').trigger('click')
    await flushPromises()
    const withoutModel = await submitVideoAccount(wrapper)
    expect(withoutModel.extra.video_model_resolutions).toEqual({
      'seedance-2.0': ['720p', '1080p', '4K'],
      'seedance-2.0-fast': ['720p'],
    })

    // 逐个取消，直到只剩最后一档：此时 720p 仍可正常取消（还剩 4K）。
    await resolutionChip(wrapper, 'seedance-2.0', '1080p').trigger('click')
    await flushPromises()
    expect(resolutionChip(wrapper, 'seedance-2.0', '720p').attributes('aria-pressed')).toBe('true')
    expect(resolutionChip(wrapper, 'seedance-2.0', '4K').attributes('aria-pressed')).toBe('true')

    await resolutionChip(wrapper, 'seedance-2.0', '4K').trigger('click')
    await flushPromises()

    // 只剩最后一档时再点应被拒绝：勾空在下游等价于"不限制该模型的分辨率"，
    // 会让运营以为禁用了全部档位、实际却放开了限制。要禁用模型请用上方模型白名单。
    await resolutionChip(wrapper, 'seedance-2.0', '720p').trigger('click')
    await flushPromises()
    expect(resolutionChip(wrapper, 'seedance-2.0', '720p').attributes('aria-pressed')).toBe('true')

    const lastOneStanding = await submitVideoAccount(wrapper)
    expect(lastOneStanding.extra.video_model_resolutions).toEqual({
      'seedance-2.0': ['720p'],
      'seedance-2.0-fast': ['720p'],
    })
    expect(Object.keys(lastOneStanding.extra)).toContain('video_provider')
  })

  it('never emits an empty resolution entry for a selected model', async () => {
    // serialize 层的兜底：即便拿到全空选择，也不产生空条目（空条目读作"不限制"）。
    const wrapper = await mountVideoModal()
    const payload = await submitVideoAccount(wrapper)
    for (const resolutions of Object.values(
      payload.extra.video_model_resolutions as Record<string, string[]>
    )) {
      expect(resolutions.length).toBeGreaterThan(0)
    }
  })

  it('keeps a disabled resolution unchecked even if the click somehow fires', async () => {
    const wrapper = await mountVideoModal()

    await resolutionChip(wrapper, 'seedance-2.0', '4K').trigger('click')
    await flushPromises()

    expect(resolutionChip(wrapper, 'seedance-2.0', '4K').attributes('aria-pressed')).toBe('false')
    const payload = await submitVideoAccount(wrapper)
    expect(payload.extra.video_model_resolutions['seedance-2.0']).not.toContain('4K')
  })
})
