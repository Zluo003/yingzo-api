import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { createAccountMock, showErrorMock, syncUpstreamPreviewMock } = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  showErrorMock: vi.fn(),
  syncUpstreamPreviewMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
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
      syncUpstreamModelsPreview: syncUpstreamPreviewMock,
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
 * 图片入口在真实界面里是 show: false → true 打开的，初始化逻辑挂在 show 的
 * watcher 上，因此测试必须复现这个翻转。
 */
async function mountImageModal(groups: unknown[] = []) {
  const wrapper = mount(CreateAccountModal, {
    props: { show: false, proxies: [], groups: groups as never, mode: 'image' as const },
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

const agentGroup = { id: 2, name: 'Yingzo Agent', kind: 'agent', system_code: 'yingzo' }

describe('CreateAccountModal image mode', () => {
  beforeEach(() => {
    createAccountMock.mockReset().mockResolvedValue({ id: 77 })
    showErrorMock.mockReset()
    syncUpstreamPreviewMock.mockReset().mockResolvedValue({
      models: ['gemini-3-pro-image', 'gemini-3.1-flash-image-preview', 'gemini-3-pro'],
    })
  })

  it('only offers the two platforms that have standard image interfaces', async () => {
    const wrapper = await mountImageModal()

    expect(wrapper.find('[data-testid="image-platform-openai"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="image-platform-gemini"]').exists()).toBe(true)
    // 没有图片接口的平台不能出现在图片账号入口里。
    for (const unsupported of ['anthropic', 'antigravity', 'video', 'kimi']) {
      expect(wrapper.find(`[data-testid="image-platform-${unsupported}"]`).exists()).toBe(false)
    }
    // 默认 Gemini（标准图片接口最常用），并且凭证走 API Key。
    expect(wrapper.find('[data-testid="image-account-platform"]').text()).toContain(
      'admin.accounts.image.platformGemini'
    )
  })

  it('fetches upstream models and writes the checked ones into the model mapping', async () => {
    const wrapper = await mountImageModal([agentGroup])

    await wrapper.get('form#create-account-form input[type="text"]').setValue('gemini image')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-image')
    await wrapper.get('[data-testid="image-model-fetch"]').trigger('click')
    await flushPromises()

    // 拉取用的是表单里填的连接信息，不必先建账号。
    expect(syncUpstreamPreviewMock).toHaveBeenCalledWith(
      expect.objectContaining({ platform: 'gemini', type: 'apikey', api_key: 'sk-image' })
    )

    await wrapper.get('[data-testid="image-model-option-gemini-3-pro-image"] input').setValue(true)
    await wrapper
      .get('[data-testid="image-model-option-gemini-3.1-flash-image-preview"] input')
      .setValue(true)
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload.platform).toBe('gemini')
    expect(payload.type).toBe('apikey')
    // 勾选即同名映射（下游名 = 上游名），图片账号不靠模型名猜类型。
    expect(payload.credentials.model_mapping).toEqual({
      'gemini-3-pro-image': 'gemini-3-pro-image',
      'gemini-3.1-flash-image-preview': 'gemini-3.1-flash-image-preview',
    })
    expect(payload.extra.image_account).toBe(true)
    // 默认绑定系统内置聚合分组：模型要进 Yingzo Agent 目录才能定价与分发。
    expect(payload.group_ids).toEqual([2])
  })

  it('allows selecting every fetched model at once', async () => {
    const wrapper = await mountImageModal([agentGroup])

    await wrapper.get('form#create-account-form input[type="text"]').setValue('gemini image')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-image')
    await wrapper.get('[data-testid="image-model-fetch"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="image-model-select-all"]').trigger('click')
    await flushPromises()
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(Object.keys(createAccountMock.mock.calls[0]?.[0].credentials.model_mapping ?? {})).toEqual([
      'gemini-3-pro-image',
      'gemini-3.1-flash-image-preview',
      'gemini-3-pro',
    ])
  })

  it('falls back to a manually typed model when the upstream has no model list', async () => {
    syncUpstreamPreviewMock.mockRejectedValueOnce(new Error('upstream failed'))
    const wrapper = await mountImageModal([agentGroup])

    await wrapper.get('form#create-account-form input[type="text"]').setValue('relay image')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-relay')
    await wrapper.get('[data-testid="image-model-fetch"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="image-model-fetch-error"]').text()).toBe('upstream failed')

    await wrapper.get('[data-testid="image-model-manual"]').setValue('nano-banana-pro')
    await wrapper.get('[data-testid="image-model-manual-add"]').trigger('click')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock.mock.calls[0]?.[0].credentials.model_mapping).toEqual({
      'nano-banana-pro': 'nano-banana-pro',
    })
  })

  it('reports the declared models back to the caller for catalog sync', async () => {
    const wrapper = await mountImageModal([agentGroup])

    await wrapper.get('form#create-account-form input[type="text"]').setValue('openai image')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-image')
    await wrapper.get('[data-testid="image-platform-openai"]').trigger('click')
    await wrapper.get('[data-testid="image-model-manual"]').setValue('gpt-image-2')
    await wrapper.get('[data-testid="image-model-manual-add"]').trigger('click')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = createAccountMock.mock.calls[0]?.[0]
    // 切到 OpenAI 平台时 base_url 跟着换成对应默认地址。
    expect(payload.platform).toBe('openai')
    expect(payload.credentials.base_url).toBe('https://api.openai.com')
    expect(wrapper.emitted('created')?.[0]?.[0]).toEqual({
      accountId: 77,
      imageModels: ['gpt-image-2'],
    })
  })

  it('refuses to create an image account without any model', async () => {
    const wrapper = await mountImageModal([agentGroup])

    await wrapper.get('form#create-account-form input[type="text"]').setValue('empty image')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-image')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).not.toHaveBeenCalled()
    expect(showErrorMock).toHaveBeenCalledWith('admin.accounts.image.modelsRequired')
  })
})
