import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

import AccountTableActions from '../AccountTableActions.vue'

const mountActions = () =>
  mount(AccountTableActions, {
    props: { loading: false },
    global: { stubs: { Icon: true } },
  })

describe('AccountTableActions', () => {
  it('renders the video-account button next to the create-account button', async () => {
    const wrapper = mountActions()
    const labels = wrapper.findAll('button').map((button) => button.text())

    expect(labels).toContain('admin.accounts.createAccount')
    expect(labels).toContain('admin.accounts.createVideoAccount')
    // 「添加视频账号」紧跟在「添加账号」之后
    expect(labels.indexOf('admin.accounts.createVideoAccount')).toBe(
      labels.indexOf('admin.accounts.createAccount') + 1
    )
  })

  it('emits create-video without emitting create', async () => {
    const wrapper = mountActions()

    await wrapper.get('[data-testid="create-video-account"]').trigger('click')

    expect(wrapper.emitted('create-video')).toHaveLength(1)
    expect(wrapper.emitted('create')).toBeUndefined()
  })

  it('still emits create for the standard button', async () => {
    const wrapper = mountActions()
    const createButton = wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.accounts.createAccount')

    await createButton?.trigger('click')

    expect(wrapper.emitted('create')).toHaveLength(1)
    expect(wrapper.emitted('create-video')).toBeUndefined()
  })
})
