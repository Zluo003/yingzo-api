import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SetupWizardView from '../SetupWizardView.vue'
import { install } from '@/api/setup'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/api/setup', () => ({
  testDatabase: vi.fn().mockResolvedValue(undefined),
  testRedis: vi.fn().mockResolvedValue(undefined),
  install: vi.fn().mockResolvedValue({ restart: true }),
}))
vi.mock('@/api/client', () => ({
  buildGatewayUrl: (path: string) => path,
}))

describe('setup completion', () => {
  const originalLocation = window.location

  afterEach(() => {
    Object.defineProperty(window, 'location', { value: originalLocation, writable: true })
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
    vi.clearAllMocks()
  })

  it.each(['/', '/admin/'])('returns to the %s login after installation and restart', async (base) => {
    vi.useFakeTimers()
    vi.stubEnv('BASE_URL', base)
    Object.defineProperty(window, 'location', {
      value: { ...originalLocation, pathname: `${base}setup`, href: `${base}setup` },
      writable: true,
    })
    const fetchStatus = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ data: { needs_setup: false } }),
    })
    vi.stubGlobal('fetch', fetchStatus)
    const wrapper = mount(SetupWizardView, {
      global: { stubs: { Icon: true, Select: true, Toggle: true } },
    })
    async function clickButton(label: string) {
      const button = wrapper.findAll('button').find((button) => button.text() === label)
      expect(button, label).toBeDefined()
      await button!.trigger('click')
      await flushPromises()
    }

    try {
      await clickButton('setup.status.testConnection')
      await clickButton('common.next')
      await clickButton('setup.status.testConnection')
      await clickButton('common.next')
      await wrapper.get('input[type="email"]').setValue('admin@example.com')
      for (const input of wrapper.findAll('input[type="password"]')) {
        await input.setValue('setup-test-password')
      }
      await clickButton('common.next')
      await clickButton('setup.status.completeInstallation')
      expect(install).toHaveBeenCalledOnce()

      await vi.advanceTimersByTimeAsync(3000)
      expect(fetchStatus).toHaveBeenCalledWith('/setup/status', { method: 'GET', cache: 'no-store' })
      await vi.advanceTimersByTimeAsync(1500)
      expect(window.location.href).toBe(`${base}login`)
    } finally {
      wrapper.unmount()
    }
  })
})
