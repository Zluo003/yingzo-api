import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { updateAccountMock, checkMixedChannelRiskMock } = vi.hoisted(() => ({
  updateAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: true }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      update: updateAccountMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock,
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

import EditAccountModal from '../EditAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

function buildVideoAccount(extra: Record<string, unknown> = {}) {
  return {
    id: 11,
    name: 'Seedance Account',
    notes: '',
    platform: 'video',
    type: 'apikey',
    credentials: {
      api_key: 'sk-video',
      model_mapping: {
        'seedance-2.0': 'seedance-2.0',
        'seedance-2.0-fast': 'seedance-2.0-fast',
        'seedance-2.5': 'seedance-2.5',
      },
    },
    extra: {
      video_provider: 'newtoken',
      base_url: 'https://newtoken.club',
      api_path: '/v1/videos',
      poll_interval_ms: 5000,
      poll_timeout_ms: 900000,
      request_timeout_ms: 300000,
      connect_timeout_ms: 15000,
      ...extra,
    },
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false,
  } as any
}

function mountModal(account: ReturnType<typeof buildVideoAccount>) {
  return mount(EditAccountModal, {
    props: { show: true, account, proxies: [], groups: [] },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: true,
        Icon: true,
        ProxySelector: true,
        ProxyAdBanner: true,
        GroupSelector: true,
        ModelWhitelistSelector: true,
        GrokBaseUrlPresets: true,
        CnBaseUrlPresets: true,
        HeaderOverrideEditor: true,
        HelpTooltip: true,
        QuotaLimitCard: true,
        OllamaCloudUsageSettings: true,
        Toggle: true,
      },
    },
  })
}

const resolutionChip = (wrapper: ReturnType<typeof mount>, model: string, resolution: string) =>
  wrapper.get(`[data-testid="video-resolution-${model}-${resolution}"]`)

async function submitEdit(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('form#edit-account-form').trigger('submit.prevent')
  await flushPromises()
  return updateAccountMock.mock.calls.at(-1)?.[1] as Record<string, any>
}

describe('EditAccountModal video resolutions', () => {
  beforeEach(() => {
    updateAccountMock.mockReset().mockImplementation((_id: number, payload: unknown) => payload)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
  })

  it('restores the saved resolution whitelist from extra', async () => {
    const wrapper = mountModal(
      buildVideoAccount({ video_model_resolutions: { 'seedance-2.0': ['720p', '1080p'] } })
    )
    await flushPromises()

    expect(resolutionChip(wrapper, 'seedance-2.0', '720p').attributes('aria-pressed')).toBe('true')
    expect(resolutionChip(wrapper, 'seedance-2.0', '1080p').attributes('aria-pressed')).toBe('true')
    // newtoken 不提供 480p/4K：禁用且不勾选。
    expect(resolutionChip(wrapper, 'seedance-2.0', '480p').attributes('disabled')).toBeDefined()
    expect(resolutionChip(wrapper, 'seedance-2.0', '480p').attributes('aria-pressed')).toBe('false')
    expect(resolutionChip(wrapper, 'seedance-2.0', '4K').attributes('disabled')).toBeDefined()
    // 未在 extra 中配置的模型 => 后端语义是"不限制"，界面应显示该上游可服务的全部档位。
    // 若显示为空，运营会误以为这个账号不支持该模型，与后端行为相反。
    expect(resolutionChip(wrapper, 'seedance-2.5', '720p').attributes('aria-pressed')).toBe('true')
  })

  it('saves the edited whitelist back into extra and keeps the other extras', async () => {
    const wrapper = mountModal(
      buildVideoAccount({ video_model_resolutions: { 'seedance-2.0': ['720p', '1080p'] } })
    )
    await flushPromises()

    await resolutionChip(wrapper, 'seedance-2.0', '1080p').trigger('click')
    await flushPromises()

    const payload = await submitEdit(wrapper)
    expect(payload.extra.video_model_resolutions).toEqual({
      'seedance-2.0': ['720p'],
      // 未配置过的模型按默认全档位提交（等价于不限制，但把当前意图显式固化）。
      'seedance-2.0-fast': ['720p'],
      'seedance-2.5': ['720p', '1080p'],
    })
    expect(payload.extra.video_provider).toBe('newtoken')
    expect(payload.extra.poll_interval_ms).toBe(5000)
  })

  it('defaults an unconfigured account to every servable resolution and can narrow it', async () => {
    const account = buildVideoAccount({ video_provider: 'aigod' })
    delete (account.extra as Record<string, unknown>).video_model_resolutions
    const wrapper = mountModal(account)
    await flushPromises()

    // aigod 未配置过 => 官方档位全勾（含 4K：目录里有 seedance-2.0-4k）。
    expect(resolutionChip(wrapper, 'seedance-2.0', '480p').attributes('aria-pressed')).toBe('true')
    expect(resolutionChip(wrapper, 'seedance-2.0', '4K').attributes('aria-pressed')).toBe('true')
    expect(resolutionChip(wrapper, 'seedance-2.0', '4K').attributes('disabled')).toBeUndefined()
    // 2.0-fast 官方档位不含 4K，该 chip 不存在。
    expect(wrapper.find('[data-testid="video-resolution-seedance-2.0-fast-4K"]').exists()).toBe(false)

    // 取消 480p
    await resolutionChip(wrapper, 'seedance-2.0', '480p').trigger('click')
    await flushPromises()

    const payload = await submitEdit(wrapper)
    expect(payload.extra.video_model_resolutions).toEqual({
      'seedance-2.0': ['720p', '1080p', '4K'],
      'seedance-2.0-fast': ['480p', '720p'],
      'seedance-2.5': ['480p', '720p', '1080p'],
    })
  })

  it('refuses to uncheck the last resolution of a model', async () => {
    // 勾空在下游等价于"不限制该模型的分辨率"。允许取消最后一档，会让运营以为
    // 禁用了全部分辨率，实际却把限制放开了；要禁用模型应走模型白名单。
    const wrapper = mountModal(
      buildVideoAccount({ video_model_resolutions: { 'seedance-2.0': ['720p'] } })
    )
    await flushPromises()

    await resolutionChip(wrapper, 'seedance-2.0', '720p').trigger('click')
    await flushPromises()
    expect(resolutionChip(wrapper, 'seedance-2.0', '720p').attributes('aria-pressed')).toBe('true')

    const payload = await submitEdit(wrapper)
    expect(payload.extra.video_model_resolutions).toEqual({
      'seedance-2.0': ['720p'],
      'seedance-2.0-fast': ['720p'],
      'seedance-2.5': ['720p', '1080p'],
    })
    expect(payload.extra.video_provider).toBe('newtoken')
  })

  it('re-derives availability when the upstream is switched', async () => {
    const wrapper = mountModal(
      buildVideoAccount({ video_model_resolutions: { 'seedance-2.0': ['1080p'] } })
    )
    await flushPromises()

    // newtoken -> aigod：480p/1080p 都可服务，已保存的 1080p 保留。
    await wrapper.get('select').setValue('aigod')
    await flushPromises()
    expect(resolutionChip(wrapper, 'seedance-2.0', '480p').attributes('disabled')).toBeUndefined()
    expect(resolutionChip(wrapper, 'seedance-2.0', '1080p').attributes('aria-pressed')).toBe('true')

    await resolutionChip(wrapper, 'seedance-2.0', '480p').trigger('click')
    await flushPromises()

    // aigod -> newtoken：480p 不再可服务，勾选被丢弃。
    await wrapper.get('select').setValue('newtoken')
    await flushPromises()
    expect(resolutionChip(wrapper, 'seedance-2.0', '480p').attributes('disabled')).toBeDefined()
    expect(resolutionChip(wrapper, 'seedance-2.0', '480p').attributes('aria-pressed')).toBe('false')

    const payload = await submitEdit(wrapper)
    expect(payload.extra.video_provider).toBe('newtoken')
    expect(payload.extra.video_model_resolutions).toEqual({
      'seedance-2.0': ['1080p'],
      'seedance-2.0-fast': ['720p'],
      'seedance-2.5': ['720p', '1080p'],
    })
  })
})
