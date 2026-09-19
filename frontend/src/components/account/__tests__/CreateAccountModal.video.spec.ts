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

/**
 * 视频入口在真实界面里是 show: false → true 打开的，弹窗的初始化逻辑挂在
 * show 的 watcher 上，因此测试必须复现这个翻转，而不是直接以 show: true 挂载。
 */
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

async function submitVideoAccount(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('form#create-account-form input[type="text"]').setValue('video account')
  await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-video')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
  return createAccountMock.mock.calls.at(-1)?.[0]
}

describe('CreateAccountModal video mode', () => {
  beforeEach(() => {
    createAccountMock.mockReset().mockResolvedValue({})
  })

  it('hides the platform selector and locks the form to the video platform', async () => {
    const wrapper = await mountVideoModal()

    // The segmented platform control is not rendered in video mode.
    expect(wrapper.text()).not.toContain('admin.accounts.platform')

    await wrapper.get('form#create-account-form input[type="text"]').setValue('video account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-video')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload.platform).toBe('video')
    expect(payload.type).toBe('apikey')
  })

  it('links the platform options to the selected models', async () => {
    const wrapper = await mountVideoModal()

    // 默认全选模型：只有 mikuapi 能服务全部五个模型。
    const select = wrapper.get('[data-testid="video-provider-select"]')
    const optionValues = () =>
      select.findAll('option').map((option) => option.element.value)
    expect(optionValues()).toEqual(['mikuapi'])

    // 取消 grok / kling 后，四个平台都能服务剩余的 Seedance 系列。
    await wrapper.get('[data-testid="video-model-dropdown"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="video-model-grok-imagine-video-1.5"]').trigger('click')
    await wrapper.get('[data-testid="video-model-kling-v3-omni"]').trigger('click')
    await flushPromises()

    // jingyu 不服务 seedance-2.0-fast，因此不在可服务平台里。
    expect(optionValues()).toEqual(['aigod', 'newtoken', 'mikuapi'])
    // 早期清理掉的上游不能悄悄回来。
    expect(optionValues()).not.toContain('ycyapi')
  })

  it('auto-switches the platform to the only upstream serving the selected model', async () => {
    const wrapper = await mountVideoModal()

    // 只勾 kling：平台应自动切到 mikuapi（唯一可服务它的上游）。
    await wrapper.get('[data-testid="video-model-dropdown"]').trigger('click')
    await flushPromises()
    for (const model of [
      'seedance-2.0',
      'seedance-2.0-fast',
      'seedance-2.5',
      'grok-imagine-video-1.5'
    ]) {
      await wrapper.get(`[data-testid="video-model-${model}"]`).trigger('click')
    }
    await flushPromises()

    const payload = await submitVideoAccount(wrapper)
    expect(payload.extra.video_provider).toBe('mikuapi')
  })

  it('switches base URL + timeouts when mikuapi is picked', async () => {
    const wrapper = await mountVideoModal()

    await wrapper.get('[data-testid="video-provider-select"]').setValue('mikuapi')
    await flushPromises()

    await wrapper.get('form#create-account-form input[type="text"]').setValue('mikuapi account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-mikuapi')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload.extra.video_provider).toBe('mikuapi')
    expect(payload.extra.base_url).toBe('https://mikuapi.org')
    expect(payload.extra.api_path).toBe('/v1/videos')
    expect(payload.extra.poll_interval_ms).toBe(5000)
    expect(payload.extra.poll_timeout_ms).toBe(900000)
  })

  it('switches base URL + timeouts when newtoken is picked after narrowing models', async () => {
    const wrapper = await mountVideoModal()

    // 默认全选模型时平台自动落在 mikuapi；先取消 grok/kling 让 newtoken 可选。
    await wrapper.get('[data-testid="video-model-dropdown"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="video-model-grok-imagine-video-1.5"]').trigger('click')
    await wrapper.get('[data-testid="video-model-kling-v3-omni"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="video-provider-select"]').setValue('newtoken')
    await flushPromises()

    await wrapper.get('form#create-account-form input[type="text"]').setValue('newtoken account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-newtoken')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload.extra.video_provider).toBe('newtoken')
    expect(payload.extra.base_url).toBe('https://newtoken.club')
    expect(payload.extra.api_path).toBe('/v1/videos')
    expect(payload.extra.poll_interval_ms).toBe(5000)
    // aigod / newtoken 统一 15 分钟
    expect(payload.extra.poll_timeout_ms).toBe(900000)
  })

  it('whitelists exactly the shipped video models by default', async () => {
    const wrapper = await mountVideoModal()

    await wrapper.get('[data-testid="video-model-dropdown"]').trigger('click')
    await flushPromises()
    for (const model of [
      'seedance-2.0',
      'seedance-2.0-fast',
      'seedance-2.5',
      'grok-imagine-video-1.5',
      'kling-v3-omni'
    ]) {
      expect(wrapper.find(`[data-testid="video-model-${model}"]`).exists()).toBe(true)
    }

    await wrapper.get('form#create-account-form input[type="text"]').setValue('video account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-video')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock.mock.calls[0]?.[0].credentials.model_mapping).toEqual({
      'seedance-2.0': 'seedance-2.0',
      'seedance-2.0-fast': 'seedance-2.0-fast',
      'seedance-2.5': 'seedance-2.5',
      'grok-imagine-video-1.5': 'grok-imagine-video-1.5',
      'kling-v3-omni': 'kling-v3-omni',
    })
  })

  it('narrows the whitelist when a model is unchecked', async () => {
    const wrapper = await mountVideoModal()

    await wrapper.get('[data-testid="video-model-dropdown"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="video-model-seedance-2.0-fast"]').trigger('click')
    await flushPromises()

    await wrapper.get('form#create-account-form input[type="text"]').setValue('video account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-video')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock.mock.calls[0]?.[0].credentials.model_mapping).toEqual({
      'seedance-2.0': 'seedance-2.0',
      'seedance-2.5': 'seedance-2.5',
      'grok-imagine-video-1.5': 'grok-imagine-video-1.5',
      'kling-v3-omni': 'kling-v3-omni',
    })
  })

  it('never sends the upstream billing probe flag for video accounts', async () => {
    const wrapper = await mountVideoModal()

    await wrapper.get('form#create-account-form input[type="text"]').setValue('video account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-video')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock.mock.calls[0]?.[0].upstream_billing_probe_enabled).toBeUndefined()
  })
})
