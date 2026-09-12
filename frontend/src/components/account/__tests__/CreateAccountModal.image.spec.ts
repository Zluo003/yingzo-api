import { defineComponent, ref, watch } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { createAccountMock, showErrorMock } = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  showErrorMock: vi.fn(),
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
      probeUpstreamBilling: vi.fn(),
      syncUpstreamModels: vi.fn(),
      syncUpstreamModelsPreview: vi.fn(),
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
 * 原版模型选择器的替身：把绑定的 modelValue 暴露成一个输入框，测试里往里写模型名
 * 就等于在界面上勾选/同步了模型（图片账号复用原版选择器，不再自制拉取 UI）。
 */
const ModelSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: { modelValue: { type: Array, default: () => [] } },
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    const text = ref((props.modelValue as string[]).join(','))
    watch(
      () => props.modelValue,
      (value) => {
        text.value = (value as string[]).join(',')
      }
    )
    watch(text, (value) => {
      emit(
        'update:modelValue',
        value
          .split(',')
          .map((item) => item.trim())
          .filter(Boolean)
      )
    })
    return { text }
  },
  template: '<input data-testid="model-selector" v-model="text" />',
})

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
        ModelWhitelistSelector: ModelSelectorStub,
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
  })

  it('only offers the two platforms that have standard image interfaces', async () => {
    const wrapper = await mountImageModal()

    expect(wrapper.find('[data-testid="image-platform-openai"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="image-platform-gemini"]').exists()).toBe(true)
    for (const unsupported of ['anthropic', 'antigravity', 'video', 'kimi']) {
      expect(wrapper.find(`[data-testid="image-platform-${unsupported}"]`).exists()).toBe(false)
    }
    expect(wrapper.find('[data-testid="image-account-platform"]').text()).toContain(
      'admin.accounts.image.platformGemini'
    )
    // 图片账号凭证固定 API Key，不提供 OAuth 选择。
    expect(wrapper.text()).not.toContain('admin.accounts.accountType')
  })

  it('reuses the stock model selector instead of a bespoke fetch UI', async () => {
    const wrapper = await mountImageModal([agentGroup])

    expect(wrapper.find('[data-testid="model-selector"]').exists()).toBe(true)
    for (const removed of ['image-model-fetch', 'image-model-select-all', 'image-model-manual']) {
      expect(wrapper.find(`[data-testid="${removed}"]`).exists()).toBe(false)
    }
  })

  it('writes the picked models into the model mapping and marks the account as an image account', async () => {
    const wrapper = await mountImageModal([agentGroup])

    await wrapper.get('form#create-account-form input[type="text"]').setValue('gemini image')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-image')
    await wrapper.get('[data-testid="model-selector"]').setValue('gemini-3-pro-image,nano-banana-pro')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload.platform).toBe('gemini')
    expect(payload.type).toBe('apikey')
    expect(payload.credentials.model_mapping).toEqual({
      'gemini-3-pro-image': 'gemini-3-pro-image',
      'nano-banana-pro': 'nano-banana-pro',
    })
    // 目录按这个标记把模型登记成图片类型，不靠模型名猜。
    expect(payload.extra.image_account).toBe(true)
    // 默认绑定系统内置聚合分组：模型要进 Yingzo Agent 目录才能定价与分发。
    expect(payload.group_ids).toEqual([2])
  })

  it('reports the declared models back to the caller for catalog sync', async () => {
    const wrapper = await mountImageModal([agentGroup])

    await wrapper.get('form#create-account-form input[type="text"]').setValue('openai image')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-image')
    await wrapper.get('[data-testid="image-platform-openai"]').trigger('click')
    await wrapper.get('[data-testid="model-selector"]').setValue('gpt-image-2')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = createAccountMock.mock.calls[0]?.[0]
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
