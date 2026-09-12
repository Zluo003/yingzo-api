import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GroupSelector from '../GroupSelector.vue'

const authState = { isSimpleMode: false }

vi.mock('@/stores', () => ({ useAuthStore: () => authState }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const groups = [
  { id: 1, name: 'Basic', platform: 'anthropic', status: 'active' },
  { id: 2, name: 'Composite', platform: 'composite', status: 'active' }
] as any

const mountSelector = (modelValue: number[] = []) => mount(GroupSelector, {
  props: { modelValue, groups },
  global: { stubs: { GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' }, Icon: true } }
})

describe('GroupSelector platform binding policy', () => {
  beforeEach(() => { authState.isSimpleMode = false })

  const platformGroups = [
    { id: 1, name: 'OpenAI 分组', platform: 'openai', status: 'active' },
    { id: 2, name: 'Yingzo Agent', platform: 'openai', status: 'active', kind: 'agent', system_code: 'yingzo' },
    { id: 3, name: 'Deepseek 分组', platform: 'deepseek', status: 'active' }
  ] as any

  const mountFor = (platform: string) => mount(GroupSelector, {
    props: { modelValue: [], groups: platformGroups, platform },
    global: { stubs: { GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' }, Icon: true } }
  })

  it('offers the built-in aggregate group to every platform', () => {
    // 聚合分组的 platform 只是占位：任何平台的账号都应该能绑进来，
    // 否则不同 provider 的账号根本进不了这个分组。
    for (const platform of ['openai', 'anthropic', 'gemini', 'video', 'deepseek']) {
      expect(mountFor(platform).text()).toContain('Yingzo Agent')
    }
  })

  it('still hides unrelated platform groups', () => {
    const wrapper = mountFor('deepseek')
    expect(wrapper.text()).toContain('Deepseek 分组')
    expect(wrapper.text()).not.toContain('OpenAI 分组')
  })
})

describe('GroupSelector simple-mode binding policy', () => {
  beforeEach(() => { authState.isSimpleMode = false })

  it('hides composite groups in simple mode and preserves basic groups', () => {
    authState.isSimpleMode = true
    const wrapper = mountSelector()
    expect(wrapper.text()).toContain('Basic')
    expect(wrapper.text()).not.toContain('Composite')
  })

  it('keeps composite groups available in advanced mode', () => {
    const wrapper = mountSelector()
    expect(wrapper.text()).toContain('Composite')
  })

  it('cleans hidden historical composite IDs while preserving visible selections', () => {
    authState.isSimpleMode = true
    const wrapper = mountSelector([1, 2])
    expect(wrapper.emitted('update:modelValue')).toEqual([[[1]]])
  })
})
