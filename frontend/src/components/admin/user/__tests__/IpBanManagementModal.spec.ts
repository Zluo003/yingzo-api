import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import IpBanManagementModal from '../IpBanManagementModal.vue'
import type { IPBan } from '@/api/admin/ipBans'

const { list, create, remove, showSuccess, showError } = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  remove: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    ipBans: {
      list,
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
  'admin.users.ipBan.errors.IP_BAN_RANGE_TOO_BROAD': 'friendly: too broad'
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

const makeBan = (overrides: Partial<IPBan> = {}): IPBan => ({
  id: 1,
  ip: '203.0.113.7',
  reason: 'bulk sign-ups',
  created_by: 1,
  created_at: '2026-10-30T08:00:00Z',
  ...overrides
})

const page = (items: IPBan[], total = items.length) => ({
  items,
  total,
  page: 1,
  page_size: 10,
  pages: Math.max(1, Math.ceil(total / 10))
})

const mountModal = () =>
  mount(IpBanManagementModal, {
    props: { show: true },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: ConfirmDialogStub,
        Icon: true
      }
    }
  })

describe('IpBanManagementModal', () => {
  beforeEach(() => {
    list.mockReset()
    create.mockReset()
    remove.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('loads the first page of ban rules when opened', async () => {
    list.mockResolvedValue(page([makeBan(), makeBan({ id: 2, ip: '2001:db8:1:2::/64', reason: '' })]))

    const wrapper = mountModal()
    await flushPromises()

    expect(list).toHaveBeenCalledWith(1, 10, { search: undefined })
    expect(wrapper.get('[data-test="ip-ban-row-1"]').text()).toContain('203.0.113.7')
    expect(wrapper.get('[data-test="ip-ban-row-2"]').text()).toContain('2001:db8:1:2::/64')
  })

  it('debounces the search and reloads from the first page', async () => {
    vi.useFakeTimers()
    list.mockResolvedValue(page([]))

    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.find('[data-test="ip-ban-empty"]').exists()).toBe(true)

    await wrapper.get('[data-test="ip-ban-search"]').setValue('203.0')
    expect(list).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()

    expect(list).toHaveBeenLastCalledWith(1, 10, { search: '203.0' })
  })

  it('confirms before adding a ban, then reloads and notifies the parent', async () => {
    list.mockResolvedValueOnce(page([])).mockResolvedValueOnce(page([makeBan({ id: 3, ip: '198.51.100.0/24' })]))
    create.mockResolvedValue({ ban: makeBan({ id: 3, ip: '198.51.100.0/24' }), created: true })

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="ip-ban-target"]').setValue(' 198.51.100.0/24 ')
    await wrapper.get('[data-test="ip-ban-reason"]').setValue('abuse')
    await wrapper.get('[data-test="ip-ban-form"]').trigger('submit')

    expect(create).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="confirm-message"]').text()).toContain('198.51.100.0/24')

    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(create).toHaveBeenCalledWith({ ip: '198.51.100.0/24', reason: 'abuse' })
    expect(showSuccess).toHaveBeenCalledWith(expect.stringContaining('admin.users.ipBan.banSuccess'))
    expect(wrapper.emitted('success')).toHaveLength(1)
    expect(list).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="ip-ban-row-3"]').exists()).toBe(true)
    expect((wrapper.get('[data-test="ip-ban-target"]').element as HTMLInputElement).value).toBe('')
  })

  it('reports an already-banned target without failing', async () => {
    list.mockResolvedValue(page([makeBan()]))
    create.mockResolvedValue({ ban: makeBan(), created: false })

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="ip-ban-target"]').setValue('203.0.113.7')
    await wrapper.get('[data-test="ip-ban-form"]').trigger('submit')
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(showSuccess).toHaveBeenCalledWith(expect.stringContaining('admin.users.ipBan.banExisting'))
  })

  it('shows the localized reason when the target is rejected', async () => {
    list.mockResolvedValue(page([]))
    create.mockRejectedValue({ status: 400, code: 400, reason: 'IP_BAN_RANGE_TOO_BROAD', message: 'the CIDR range is too broad' })

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="ip-ban-target"]').setValue('10.0.0.0/4')
    await wrapper.get('[data-test="ip-ban-form"]').trigger('submit')
    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('friendly: too broad')
    expect(wrapper.emitted('success')).toBeUndefined()
    // 失败时保留输入，方便修改后重试
    expect((wrapper.get('[data-test="ip-ban-target"]').element as HTMLInputElement).value).toBe('10.0.0.0/4')
  })

  it('confirms before lifting a ban, then reloads and notifies the parent', async () => {
    list.mockResolvedValueOnce(page([makeBan()])).mockResolvedValueOnce(page([]))
    remove.mockResolvedValue({ message: 'IP ban removed' })

    const wrapper = mountModal()
    await flushPromises()

    await wrapper.get('[data-test="ip-ban-unban-1"]').trigger('click')
    expect(remove).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="confirm-message"]').text()).toContain('203.0.113.7')

    await wrapper.get('[data-test="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(remove).toHaveBeenCalledWith(1)
    expect(wrapper.emitted('success')).toHaveLength(1)
    expect(list).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="ip-ban-empty"]').exists()).toBe(true)
  })
})
