import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UserRegistrationIpModal from '../UserRegistrationIpModal.vue'
import type { IPInspection } from '@/api/admin/ipBans'

const { inspect, create, remove, showSuccess, showError } = vi.hoisted(() => ({
  inspect: vi.fn(),
  create: vi.fn(),
  remove: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    ipBans: {
      inspect,
      create,
      remove
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess,
    showError
  })
}))

// 错误原因码需要「已翻译」的文案：extractI18nErrorMessage 以 t(key) !== key 判断 key 是否存在
const translations = vi.hoisted<Record<string, string>>(() => ({
  'admin.users.ipBan.errors.IP_BAN_SELF': 'friendly: self',
  'admin.users.ipBan.errors.IP_BAN_RESERVED_TARGET': 'friendly: reserved'
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        const text = translations[key] ?? key
        return params && Object.keys(params).length > 0 ? `${text}:${JSON.stringify(params)}` : text
      }
    })
  }
})

const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: '<div v-if="show" data-test="dialog"><slot /><slot name="footer" /></div>'
}

// 确认弹窗直接渲染确认/取消按钮，便于驱动二次确认流程
const ConfirmDialogStub = {
  props: ['show', 'title', 'message', 'confirmText', 'danger'],
  emits: ['confirm', 'cancel'],
  template: `
    <div v-if="show" data-test="confirm-dialog">
      <p data-test="confirm-message">{{ message }}</p>
      <slot />
      <button data-test="confirm-ok" @click="$emit('confirm')">ok</button>
      <button data-test="confirm-cancel" @click="$emit('cancel')">cancel</button>
    </div>
  `
}

const IPV6 = '2001:db8:1:2:aaaa:bbbb:cccc:dddd'
const IPV6_NETWORK = '2001:db8:1:2::/64'

const makeInspection = (overrides: Partial<IPInspection> = {}): IPInspection => ({
  ip: IPV6,
  network_key: IPV6_NETWORK,
  banned: false,
  ban: null,
  registered_total: 5,
  registered_today: 2,
  bannable: true,
  ...overrides
})

const bannedInspection = (): IPInspection =>
  makeInspection({
    banned: true,
    ban: {
      id: 7,
      ip: IPV6_NETWORK,
      reason: 'bulk sign-ups',
      created_by: 1,
      created_at: '2026-10-30T08:00:00Z'
    }
  })

const mountModal = (ip: string = IPV6) =>
  mount(UserRegistrationIpModal, {
    props: { show: true, ip },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: ConfirmDialogStub,
        Icon: true
      }
    }
  })

describe('UserRegistrationIpModal', () => {
  beforeEach(() => {
    inspect.mockReset()
    create.mockReset()
    remove.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
  })

  it('inspects the IP on open and shows its /64 network and registration counts', async () => {
    inspect.mockResolvedValue(makeInspection())

    const wrapper = mountModal()
    await flushPromises()

    expect(inspect).toHaveBeenCalledWith(IPV6)
    expect(wrapper.get('[data-test="inspect-ip"]').text()).toBe(IPV6)
    expect(wrapper.get('[data-test="inspect-network"]').text()).toContain(IPV6_NETWORK)
    expect(wrapper.get('[data-test="registered-total"]').text()).toBe('5')
    expect(wrapper.get('[data-test="registered-today"]').text()).toBe('2')
    expect(wrapper.get('[data-test="inspect-status"]').text()).toBe('admin.users.registrationIp.notBanned')
    expect(wrapper.get('[data-test="ban"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-test="unban"]').exists()).toBe(false)
  })

  it('hides the network key when it equals the IPv4 address', async () => {
    inspect.mockResolvedValue(makeInspection({ ip: '203.0.113.7', network_key: '203.0.113.7' }))

    const wrapper = mountModal('203.0.113.7')
    await flushPromises()

    expect(wrapper.find('[data-test="inspect-network"]').exists()).toBe(false)
  })

  it('bans the network key after confirmation, then refreshes the dialog and notifies the list', async () => {
    inspect.mockResolvedValueOnce(makeInspection()).mockResolvedValueOnce(bannedInspection())
    create.mockResolvedValue({ ban: bannedInspection().ban, created: true })

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="ban-reason"]').setValue('  bulk sign-ups  ')
    await wrapper.get('[data-test="ban"]').trigger('click')

    // 二次确认：说明会封禁整个网络，且确认前不发请求
    const message = wrapper.get('[data-test="confirm-message"]').text()
    expect(message).toContain('admin.users.ipBan.banConfirmMessage')
    expect(message).toContain(IPV6_NETWORK)
    expect(create).not.toHaveBeenCalled()

    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    // IPv6 封禁目标是 /64 网段而不是单个地址
    expect(create).toHaveBeenCalledWith({ ip: IPV6_NETWORK, reason: 'bulk sign-ups' })
    expect(showSuccess).toHaveBeenCalledWith(expect.stringContaining('admin.users.ipBan.banSuccess'))
    expect(wrapper.emitted('success')).toHaveLength(1)
    expect(inspect).toHaveBeenCalledTimes(2)

    expect(wrapper.get('[data-test="inspect-status"]').text()).toBe('admin.users.registrationIp.bannedBadge')
    const detail = wrapper.get('[data-test="ban-detail"]').text()
    expect(detail).toContain(IPV6_NETWORK)
    expect(detail).toContain('bulk sign-ups')
    expect(wrapper.find('[data-test="ban"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="unban"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="confirm-dialog"]').exists()).toBe(false)
  })

  it('does not ban when the confirmation is cancelled', async () => {
    inspect.mockResolvedValue(makeInspection())

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="ban"]').trigger('click')
    await wrapper.get('[data-test="confirm-cancel"]').trigger('click')
    await flushPromises()

    expect(create).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="confirm-dialog"]').exists()).toBe(false)
    expect(wrapper.emitted('success')).toBeUndefined()
  })

  it('lifts a ban after confirmation and refreshes', async () => {
    inspect.mockResolvedValueOnce(bannedInspection()).mockResolvedValueOnce(makeInspection())
    remove.mockResolvedValue({ message: 'IP ban removed' })

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="unban"]').trigger('click')
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(remove).toHaveBeenCalledWith(7)
    expect(wrapper.emitted('success')).toHaveLength(1)
    expect(inspect).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="inspect-status"]').text()).toBe('admin.users.registrationIp.notBanned')
  })

  it.each([
    ['IP_BAN_SELF', 'friendly: self'],
    ['IP_BAN_RESERVED_TARGET', 'friendly: reserved']
  ])('disables the ban action and explains %s', async (reason, message) => {
    inspect.mockResolvedValue(
      makeInspection({ ip: '10.0.0.8', network_key: '10.0.0.8', bannable: false, not_bannable_reason: reason })
    )

    const wrapper = mountModal('10.0.0.8')
    await flushPromises()

    const banButton = wrapper.get('[data-test="ban"]')
    expect(banButton.attributes('disabled')).toBeDefined()
    expect(banButton.attributes('title')).toBe(message)
    expect(wrapper.get('[data-test="not-bannable"]').text()).toBe(message)
    expect(wrapper.find('[data-test="ban-reason"]').exists()).toBe(false)

    await banButton.trigger('click')
    expect(wrapper.find('[data-test="confirm-dialog"]').exists()).toBe(false)
  })

  it('shows the localized reason when the ban request is rejected', async () => {
    inspect.mockResolvedValue(makeInspection())
    create.mockRejectedValue({ status: 400, code: 400, reason: 'IP_BAN_SELF', message: 'you cannot ban the network you are currently using' })

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="ban"]').trigger('click')
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('friendly: self')
    expect(wrapper.emitted('success')).toBeUndefined()
    expect(inspect).toHaveBeenCalledTimes(1)
  })

  it('emits search with the IP for "view users registered from this IP"', async () => {
    inspect.mockResolvedValue(makeInspection())

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="view-users"]').trigger('click')

    expect(wrapper.emitted('search')).toEqual([[IPV6]])
  })

  it('shows an error with a retry button when inspection fails', async () => {
    inspect.mockRejectedValueOnce({ status: 500, message: 'boom' }).mockResolvedValueOnce(makeInspection())

    const wrapper = mountModal()
    await flushPromises()

    expect(wrapper.get('[data-test="inspect-error"]').text()).toContain('boom')
    expect(wrapper.find('[data-test="ban"]').exists()).toBe(false)

    await wrapper.get('[data-test="inspect-error"] button').trigger('click')
    await flushPromises()

    expect(inspect).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="inspect-ip"]').text()).toBe(IPV6)
  })
})
