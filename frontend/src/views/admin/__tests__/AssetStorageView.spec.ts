import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import AssetStorageView from '../AssetStorageView.vue'
import type { FileStorageSettings } from '@/api/admin/fileStorage'

const {
  getFileStorageSettings,
  updateFileStorageSettings,
  testFileStorageSettings,
  showError,
  showSuccess,
} = vi.hoisted(() => ({
  getFileStorageSettings: vi.fn(),
  updateFileStorageSettings: vi.fn(),
  testFileStorageSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    fileStorage: {
      getFileStorageSettings,
      updateFileStorageSettings,
      testFileStorageSettings,
    },
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/utils/format', () => ({
  formatBytes: (bytes: number, decimals = 2) => `bytes(${bytes},${decimals})`,
  formatNumberLocaleString: (num: number) => `count(${num})`,
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}|${JSON.stringify(params)}` : key,
  }),
}))

const GIB = 1024 * 1024 * 1024

function baseSettings(overrides: Partial<FileStorageSettings> = {}): FileStorageSettings {
  return {
    schema_version: 1,
    backend: 'local',
    // 空 = 使用默认目录，此时 local_path 就是那个默认目录
    local_dir: '',
    public_base_url: '',
    retention_hours: 24,
    daily_max_count: 100,
    daily_max_bytes: 2 * GIB,
    max_total_bytes: 0,
    result_retention_hours: 24,
    result_max_total_bytes: 0,
    // 生成产物自己的每日配额，默认 0 = 不限制
    result_daily_max_count: 0,
    result_daily_max_bytes: 0,
    capacity_reserve_percent: 10,
    s3: {
      endpoint: '',
      region: 'auto',
      bucket: '',
      access_key_id: '',
      secret_access_key: '',
      prefix: 'model-assets/',
      force_path_style: false,
    },
    source: 'database',
    local_path: '/app/data/agent-assets',
    secret_access_key_configured: true,
    usage: {
      active_files: 12,
      active_bytes: 3 * GIB,
      local_files: 4,
      s3_files: 8,
      expiring_within_1_hour: 3,
      reference_files: 8,
      reference_bytes: 2 * GIB,
      generated_files: 4,
      generated_bytes: GIB,
    },
    effective_public_base_url: 'https://api-key.cc',
    ...overrides,
  }
}

async function mountView() {
  const wrapper = mount(AssetStorageView)
  await flushPromises()
  return wrapper
}

function inputValue(wrapper: VueWrapper, testid: string): string {
  return (wrapper.get(`[data-testid="${testid}"]`).element as unknown as { value: string }).value
}

function text(wrapper: VueWrapper, testid: string): string {
  return wrapper.get(`[data-testid="${testid}"]`).text()
}

describe('admin AssetStorageView', () => {
  beforeEach(() => {
    getFileStorageSettings.mockReset()
    updateFileStorageSettings.mockReset()
    testFileStorageSettings.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    getFileStorageSettings.mockResolvedValue(baseSettings())
    updateFileStorageSettings.mockResolvedValue(baseSettings())
  })

  it('renders the usage summary and the effective public base URL', async () => {
    const wrapper = await mountView()

    expect(getFileStorageSettings).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="asset-storage-usage-active-files"]').text()).toBe('count(12)')
    expect(wrapper.get('[data-testid="asset-storage-usage-active-bytes"]').text()).toBe(
      `bytes(${3 * GIB},1)`,
    )
    expect(wrapper.get('[data-testid="asset-storage-usage-local-files"]').text()).toBe('count(4)')
    expect(wrapper.get('[data-testid="asset-storage-usage-s3-files"]').text()).toBe('count(8)')
    expect(wrapper.get('[data-testid="asset-storage-usage-expiring"]').text()).toBe('count(3)')
    // 两类素材分别展示文件数与占用
    expect(wrapper.get('[data-testid="asset-storage-usage-reference-files"]').text()).toBe(
      'count(8)',
    )
    expect(wrapper.get('[data-testid="asset-storage-usage-reference-bytes"]').text()).toBe(
      `bytes(${2 * GIB},1)`,
    )
    expect(wrapper.get('[data-testid="asset-storage-usage-generated-files"]').text()).toBe(
      'count(4)',
    )
    expect(wrapper.get('[data-testid="asset-storage-usage-generated-bytes"]').text()).toBe(
      `bytes(${GIB},1)`,
    )
    // 公网地址留空时用实际生效值做占位提示
    expect(wrapper.get('[data-testid="asset-storage-public-base-url"]').attributes('placeholder')).toBe(
      'https://api-key.cc',
    )
    expect(wrapper.text()).toContain('admin.assetStorage.sourceLabel')
    expect(wrapper.get('[data-testid="asset-storage-local-path"]').text()).toContain(
      '/app/data/agent-assets',
    )
  })

  it('renders the loaded local asset directory in an editable input and the effective path below it', async () => {
    getFileStorageSettings.mockResolvedValue(
      baseSettings({ local_dir: '/data/yingzo-assets', local_path: '/data/yingzo-assets' }),
    )

    const wrapper = await mountView()

    // 可编辑输入框里是配置的 local_dir
    const input = wrapper.get('[data-testid="asset-storage-local-dir"]')
    expect((input.element as unknown as { tagName: string }).tagName).toBe('INPUT')
    expect(inputValue(wrapper, 'asset-storage-local-dir')).toBe('/data/yingzo-assets')
    // 生效目录只在只读行里展示
    expect(text(wrapper, 'asset-storage-local-path')).toContain(
      'admin.assetStorage.backend.localDirEffective',
    )
    expect(text(wrapper, 'asset-storage-local-path')).toContain('/data/yingzo-assets')
  })

  it('uses the effective local path as the placeholder and keeps an empty value meaningful', async () => {
    const wrapper = await mountView()

    // local_dir 为空 = 使用默认目录，占位符告诉管理员当前实际写入哪里
    expect(inputValue(wrapper, 'asset-storage-local-dir')).toBe('')
    expect(
      wrapper.get('[data-testid="asset-storage-local-dir"]').attributes('placeholder'),
    ).toBe('/app/data/agent-assets')
    expect(text(wrapper, 'asset-storage-local-path')).toContain('/app/data/agent-assets')
  })

  it('saves the local asset directory and re-renders the effective path from the response', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="asset-storage-local-dir"]').setValue('/data/yingzo-assets')
    updateFileStorageSettings.mockResolvedValue(
      baseSettings({ local_dir: '/data/yingzo-assets', local_path: '/data/yingzo-assets' }),
    )
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({ local_dir: '/data/yingzo-assets' }),
    )
    // 生效目录取的是后端返回值，而不是本地输入
    expect(inputValue(wrapper, 'asset-storage-local-dir')).toBe('/data/yingzo-assets')
    expect(text(wrapper, 'asset-storage-local-path')).toContain('/data/yingzo-assets')
    expect(showSuccess).toHaveBeenCalledWith('admin.assetStorage.saved')
  })

  it('always sends local_dir, including the empty string that means the default directory', async () => {
    const wrapper = await mountView()

    // 默认目录：输入框留空也要把 local_dir 提交上去，而不是省略字段
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    const body = updateFileStorageSettings.mock.calls[0][0] as Record<string, unknown>
    expect(body).toHaveProperty('local_dir', '')
    expect(updateFileStorageSettings).toHaveBeenCalledWith(expect.objectContaining({ local_dir: '' }))
    expect(showError).not.toHaveBeenCalled()
  })

  it('rejects a relative, root or system local directory before calling the API', async () => {
    const wrapper = await mountView()
    const localDirInput = () => wrapper.get('[data-testid="asset-storage-local-dir"]')

    const invalidCases: [string, string][] = [
      ['data/agent-assets', 'admin.assetStorage.validation.localDirAbsolute'],
      ['./data/assets', 'admin.assetStorage.validation.localDirAbsolute'],
      ['/', 'admin.assetStorage.validation.localDirRoot'],
      ['//', 'admin.assetStorage.validation.localDirRoot'],
      ['/etc', 'admin.assetStorage.validation.localDirSystem'],
      // 写法不同但指向同一个系统目录，后端 Clean 后一样会被拒绝
      ['/etc/', 'admin.assetStorage.validation.localDirSystem'],
      ['/var/../etc', 'admin.assetStorage.validation.localDirSystem'],
    ]

    for (const [invalid, message] of invalidCases) {
      showError.mockReset()
      await localDirInput().setValue(invalid)
      await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
      await flushPromises()
      expect(showError).toHaveBeenCalledWith(message)
      expect(updateFileStorageSettings).not.toHaveBeenCalled()
    }

    // 宿主机上的真实目录是合法值
    showError.mockReset()
    await localDirInput().setValue('/data/yingzo-assets')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()
    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({ local_dir: '/data/yingzo-assets' }),
    )
    expect(showError).not.toHaveBeenCalled()
  })

  it('surfaces the backend write-probe failure verbatim instead of assuming the save worked', async () => {
    const wrapper = await mountView()

    // 后端真实验证目录可写：写不进去时保存失败并返回 FILE_STORAGE_LOCAL_DIR_UNAVAILABLE
    updateFileStorageSettings.mockRejectedValue({
      code: 'FILE_STORAGE_LOCAL_DIR_UNAVAILABLE',
      message: 'local_dir is not writable: permission denied',
    })
    await wrapper.get('[data-testid="asset-storage-local-dir"]').setValue('/data/yingzo-assets')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('local_dir is not writable: permission denied')
    expect(showSuccess).not.toHaveBeenCalled()
    // 生效目录仍保持保存前的值
    expect(text(wrapper, 'asset-storage-local-path')).toContain('/app/data/agent-assets')
  })

  it('shows each category against its own budget and falls back to unlimited when unset', async () => {
    getFileStorageSettings.mockResolvedValue(
      baseSettings({ max_total_bytes: 4 * GIB, result_max_total_bytes: 0 }),
    )

    const wrapper = await mountView()

    const referenceBudget = wrapper
      .get('[data-testid="asset-storage-usage-reference-budget"]')
      .text()
    expect(referenceBudget).toContain('admin.assetStorage.usage.budgetUsage')
    expect(referenceBudget).toContain(`bytes(${2 * GIB},1)`)
    expect(referenceBudget).toContain(`bytes(${4 * GIB},1)`)

    const generatedBudget = wrapper
      .get('[data-testid="asset-storage-usage-generated-budget"]')
      .text()
    expect(generatedBudget).toContain('admin.assetStorage.usage.budgetUnlimited')
    expect(generatedBudget).toContain(`bytes(${GIB},1)`)
  })

  it('renders the loaded generated result settings', async () => {
    getFileStorageSettings.mockResolvedValue(
      baseSettings({
        result_retention_hours: 720,
        result_max_total_bytes: 3 * GIB,
      }),
    )

    const wrapper = await mountView()

    expect(inputValue(wrapper, 'asset-storage-result-retention-hours')).toBe('720')
    expect(inputValue(wrapper, 'asset-storage-result-max-total-bytes')).toBe('3')
    expect(inputValue(wrapper, 'asset-storage-result-max-total-bytes-unit')).toBe('GiB')
  })

  it('renders the loaded generated result daily quota beside the retention and capacity inputs', async () => {
    getFileStorageSettings.mockResolvedValue(
      baseSettings({
        result_daily_max_count: 250,
        result_daily_max_bytes: 3 * GIB,
      }),
    )

    const wrapper = await mountView()

    // 配额输入与保留时长/容量同在生成产物卡片里
    const card = wrapper.get('[data-testid="asset-storage-generated-card"]')
    expect(card.find('[data-testid="asset-storage-result-daily-max-count"]').exists()).toBe(true)
    expect(card.find('[data-testid="asset-storage-result-daily-max-bytes"]').exists()).toBe(true)
    expect(inputValue(wrapper, 'asset-storage-result-daily-max-count')).toBe('250')
    expect(inputValue(wrapper, 'asset-storage-result-daily-max-bytes')).toBe('3')
    expect(inputValue(wrapper, 'asset-storage-result-daily-max-bytes-unit')).toBe('GiB')
    // 已填写配额时提示按当前字节数展示
    expect(text(wrapper, 'asset-storage-result-daily-max-bytes-hint')).toContain(
      'admin.assetStorage.generated.resultDailyMaxBytesHint',
    )
    expect(text(wrapper, 'asset-storage-result-daily-max-bytes-hint')).toContain(
      `count(${3 * GIB})`,
    )
  })

  it('shows the generated result quota default of 0 as unlimited', async () => {
    const wrapper = await mountView()

    expect(inputValue(wrapper, 'asset-storage-result-daily-max-count')).toBe('0')
    expect(inputValue(wrapper, 'asset-storage-result-daily-max-bytes')).toBe('0')
    // 0（不限制）用 MiB 展示，和参考素材的字节输入一致
    expect(inputValue(wrapper, 'asset-storage-result-daily-max-bytes-unit')).toBe('MiB')
    // 0 = 不限制的语义写在标签与帮助文本里
    expect(text(wrapper, 'asset-storage-result-daily-quota-hint')).toBe(
      'admin.assetStorage.generated.dailyQuotaDescription',
    )
    expect(text(wrapper, 'asset-storage-result-daily-max-count-hint')).toBe(
      'admin.assetStorage.generated.resultDailyMaxCountHint',
    )
    expect(text(wrapper, 'asset-storage-result-daily-max-bytes-hint')).toBe(
      'admin.assetStorage.generated.resultDailyMaxBytesUnlimited',
    )
  })

  it('keeps the generated quota inputs distinct from the reference upload quota inputs', async () => {
    const wrapper = await mountView()

    // 两组配额各自独立渲染，互不覆盖
    expect(inputValue(wrapper, 'asset-storage-daily-max-count')).toBe('100')
    expect(inputValue(wrapper, 'asset-storage-result-daily-max-count')).toBe('0')
    expect(wrapper.find('[data-testid="asset-storage-generated-card"]').find(
      '[data-testid="asset-storage-daily-max-count"]',
    ).exists()).toBe(false)
    expect(wrapper.find('[data-testid="asset-storage-generated-card"]').find(
      '[data-testid="asset-storage-daily-max-bytes"]',
    ).exists()).toBe(false)
  })

  it('no longer renders the removed result cleanup policy control', async () => {
    const wrapper = await mountView()

    expect(wrapper.find('[data-testid="asset-storage-result-cleanup-policy"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="asset-storage-result-cleanup-policy-hint"]').exists()).toBe(
      false,
    )
    // 容量冗余水位是共享配置，渲染在两类素材共用的卡片里
    expect(wrapper.find('[data-testid="asset-storage-capacity-reserve"]').exists()).toBe(true)
  })

  it('renders the loaded capacity reserve waterline and saves it back', async () => {
    getFileStorageSettings.mockResolvedValue(
      baseSettings({ capacity_reserve_percent: 25, max_total_bytes: 4 * GIB }),
    )

    const wrapper = await mountView()
    expect(inputValue(wrapper, 'asset-storage-capacity-reserve-percent')).toBe('25')

    updateFileStorageSettings.mockResolvedValue(
      baseSettings({ capacity_reserve_percent: 20, max_total_bytes: 4 * GIB }),
    )
    await wrapper.get('[data-testid="asset-storage-capacity-reserve-percent"]').setValue('30')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({ capacity_reserve_percent: 30 }),
    )
    // 保存后用后端返回值重新回填表单
    expect(inputValue(wrapper, 'asset-storage-capacity-reserve-percent')).toBe('20')
    expect(showSuccess).toHaveBeenCalledWith('admin.assetStorage.saved')
  })

  it('computes the cleanup waterline hint from the entered caps and reserve', async () => {
    getFileStorageSettings.mockResolvedValue(
      baseSettings({
        max_total_bytes: 100 * GIB,
        result_max_total_bytes: 40 * GIB,
        capacity_reserve_percent: 10,
      }),
    )

    const wrapper = await mountView()

    // 100 GiB 上限 + 10% 冗余 → 90 GiB；生成产物 40 GiB + 10% → 36 GiB
    expect(text(wrapper, 'asset-storage-capacity-reserve-hint')).toContain(
      'admin.assetStorage.capacityReserve.referenceHint',
    )
    expect(text(wrapper, 'asset-storage-capacity-reserve-hint')).toContain(
      '"threshold":"count(90) admin.assetStorage.byteUnit.gib"',
    )
    expect(text(wrapper, 'asset-storage-result-capacity-reserve-hint')).toContain(
      '"threshold":"count(36) admin.assetStorage.byteUnit.gib"',
    )

    // 水位随输入的冗余比例实时变化
    await wrapper.get('[data-testid="asset-storage-capacity-reserve-percent"]').setValue('50')
    expect(text(wrapper, 'asset-storage-capacity-reserve-hint')).toContain('"reserve":"count(50)"')
    expect(text(wrapper, 'asset-storage-capacity-reserve-hint')).toContain(
      '"threshold":"count(50) admin.assetStorage.byteUnit.gib"',
    )
    expect(text(wrapper, 'asset-storage-result-capacity-reserve-hint')).toContain(
      '"threshold":"count(20) admin.assetStorage.byteUnit.gib"',
    )

    // 也随当前输入（尚未保存）的上限实时变化
    await wrapper.get('[data-testid="asset-storage-max-total-bytes"]').setValue('10')
    expect(text(wrapper, 'asset-storage-capacity-reserve-hint')).toContain(
      '"threshold":"count(5) admin.assetStorage.byteUnit.gib"',
    )
  })

  it('keeps an explicit 0 reserve (no reserve) and treats a 0 cap as unlimited', async () => {
    getFileStorageSettings.mockResolvedValue(
      baseSettings({ capacity_reserve_percent: 0, max_total_bytes: 100 * GIB }),
    )

    const wrapper = await mountView()

    expect(inputValue(wrapper, 'asset-storage-capacity-reserve-percent')).toBe('0')
    // 0% 冗余 → 占用达到上限（100 GiB）才开始清理
    expect(text(wrapper, 'asset-storage-capacity-reserve-hint')).toContain(
      '"threshold":"count(100) admin.assetStorage.byteUnit.gib"',
    )
    // 生成产物未设置上限（0）→ 不触发清理
    expect(text(wrapper, 'asset-storage-result-capacity-reserve-hint')).toBe(
      'admin.assetStorage.capacityReserve.generatedUnlimited',
    )

    // 给生成产物设上限后提示立即改成水位线
    await wrapper.get('[data-testid="asset-storage-result-max-total-bytes-unit"]').setValue('GiB')
    await wrapper.get('[data-testid="asset-storage-result-max-total-bytes"]').setValue('5')
    expect(text(wrapper, 'asset-storage-result-capacity-reserve-hint')).toContain(
      '"threshold":"count(5) admin.assetStorage.byteUnit.gib"',
    )
  })

  it('says no cleanup is triggered when neither category has a cap', async () => {
    const wrapper = await mountView()

    expect(text(wrapper, 'asset-storage-capacity-reserve-hint')).toBe(
      'admin.assetStorage.capacityReserve.referenceUnlimited',
    )
    expect(text(wrapper, 'asset-storage-result-capacity-reserve-hint')).toBe(
      'admin.assetStorage.capacityReserve.generatedUnlimited',
    )
  })

  it('rejects an out-of-range capacity reserve before calling the API', async () => {
    const wrapper = await mountView()
    const reserveInput = () => wrapper.get('[data-testid="asset-storage-capacity-reserve-percent"]')

    for (const invalid of ['51', '-1', '10.5', '']) {
      showError.mockReset()
      await reserveInput().setValue(invalid)
      await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
      await flushPromises()
      expect(showError).toHaveBeenCalledWith('admin.assetStorage.validation.capacityReservePercent')
      expect(updateFileStorageSettings).not.toHaveBeenCalled()
    }

    // 边界值 0（不留冗余）与 50 都是合法的
    await reserveInput().setValue('0')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()
    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({ capacity_reserve_percent: 0 }),
    )

    showError.mockReset()
    await reserveInput().setValue('50')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()
    expect(updateFileStorageSettings).toHaveBeenLastCalledWith(
      expect.objectContaining({ capacity_reserve_percent: 50 }),
    )
    expect(showError).not.toHaveBeenCalled()
  })

  it('formats zero usage bytes as the localized zero value', async () => {
    getFileStorageSettings.mockResolvedValue(
      baseSettings({
        usage: {
          active_files: 0,
          active_bytes: 0,
          local_files: 0,
          s3_files: 0,
          expiring_within_1_hour: 0,
          reference_files: 0,
          reference_bytes: 0,
          generated_files: 0,
          generated_bytes: 0,
        },
      }),
    )

    const wrapper = await mountView()

    expect(wrapper.get('[data-testid="asset-storage-usage-active-bytes"]').text()).toBe(
      'admin.assetStorage.zeroBytes',
    )
    expect(wrapper.get('[data-testid="asset-storage-usage-generated-bytes"]').text()).toBe(
      'admin.assetStorage.zeroBytes',
    )
  })

  it('shows byte inputs in MiB/GiB and converts them back to bytes when saving', async () => {
    const wrapper = await mountView()

    // 2 GiB 以 GiB 展示，0（不限制）以 MiB 展示
    expect(inputValue(wrapper, 'asset-storage-daily-max-bytes')).toBe('2')
    expect(inputValue(wrapper, 'asset-storage-daily-max-bytes-unit')).toBe('GiB')
    expect(inputValue(wrapper, 'asset-storage-max-total-bytes')).toBe('0')
    expect(inputValue(wrapper, 'asset-storage-max-total-bytes-unit')).toBe('MiB')

    await wrapper.get('[data-testid="asset-storage-max-total-bytes-unit"]').setValue('GiB')
    await wrapper.get('[data-testid="asset-storage-max-total-bytes"]').setValue('5')
    await wrapper.get('[data-testid="asset-storage-daily-max-bytes"]').setValue('3')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        schema_version: 1,
        backend: 'local',
        max_total_bytes: 5 * GIB,
        daily_max_bytes: 3 * GIB,
      }),
    )
    expect(showSuccess).toHaveBeenCalledWith('admin.assetStorage.saved')
  })

  it('sends the generated result retention, capacity and the shared reserve when saving', async () => {
    const wrapper = await mountView()

    // 0（不限制）以 MiB 展示，参考素材与生成产物的单位互相独立
    expect(inputValue(wrapper, 'asset-storage-result-max-total-bytes')).toBe('0')
    expect(inputValue(wrapper, 'asset-storage-result-max-total-bytes-unit')).toBe('MiB')

    await wrapper.get('[data-testid="asset-storage-result-retention-hours"]').setValue('720')
    await wrapper.get('[data-testid="asset-storage-result-max-total-bytes-unit"]').setValue('GiB')
    await wrapper.get('[data-testid="asset-storage-result-max-total-bytes"]').setValue('4')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        retention_hours: 24,
        max_total_bytes: 0,
        result_retention_hours: 720,
        result_max_total_bytes: 4 * GIB,
        // 产物配额与参考素材配额一样始终提交，默认 0 = 不限制
        result_daily_max_count: 0,
        result_daily_max_bytes: 0,
        capacity_reserve_percent: 10,
      }),
    )
    expect(showSuccess).toHaveBeenCalledWith('admin.assetStorage.saved')
  })

  it('saves the generated result daily quota without touching the reference upload quota', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="asset-storage-result-daily-max-count"]').setValue('250')
    await wrapper.get('[data-testid="asset-storage-result-daily-max-bytes-unit"]').setValue('GiB')
    await wrapper.get('[data-testid="asset-storage-result-daily-max-bytes"]').setValue('4')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        // 产物配额按 MiB/GiB 单位换算回字节
        result_daily_max_count: 250,
        result_daily_max_bytes: 4 * GIB,
        // 上游上传配额保持原值，两者互不占用
        daily_max_count: 100,
        daily_max_bytes: 2 * GIB,
      }),
    )
    expect(showSuccess).toHaveBeenCalledWith('admin.assetStorage.saved')
  })

  it('normalizes a negative or cleared generated byte quota to 0 (unlimited)', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="asset-storage-result-daily-max-bytes"]').setValue('-5')
    await wrapper.get('[data-testid="asset-storage-result-daily-max-bytes"]').setValue('')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({ result_daily_max_bytes: 0 }),
    )
    expect(showError).not.toHaveBeenCalled()
  })

  it('rejects an out-of-range generated result daily count before calling the API', async () => {
    const wrapper = await mountView()
    const countInput = () => wrapper.get('[data-testid="asset-storage-result-daily-max-count"]')

    for (const invalid of ['-1', '1000001', '1.5', 'abc', '']) {
      showError.mockReset()
      await countInput().setValue(invalid)
      await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
      await flushPromises()
      expect(showError).toHaveBeenCalledWith('admin.assetStorage.validation.resultDailyMaxCount')
      expect(updateFileStorageSettings).not.toHaveBeenCalled()
    }

    // 0（不限制）与 1000000 都是合法边界值
    showError.mockReset()
    await countInput().setValue('0')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()
    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({ result_daily_max_count: 0 }),
    )

    await countInput().setValue('1000000')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()
    expect(updateFileStorageSettings).toHaveBeenLastCalledWith(
      expect.objectContaining({ result_daily_max_count: 1000000 }),
    )
    expect(showError).not.toHaveBeenCalled()
  })

  it('never sends the removed result_cleanup_policy field', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="asset-storage-capacity-reserve-percent"]').setValue('25')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    const body = updateFileStorageSettings.mock.calls[0][0] as Record<string, unknown>
    expect(body.capacity_reserve_percent).toBe(25)
    expect(body).not.toHaveProperty('result_cleanup_policy')
    // 两类素材各自的预算照旧独立提交
    expect(body.max_total_bytes).toBe(0)
    expect(body.result_max_total_bytes).toBe(0)
  })

  it('rejects out-of-range generated result retention before calling the API', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="asset-storage-result-retention-hours"]').setValue('0')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('admin.assetStorage.validation.resultRetentionHours')
    expect(updateFileStorageSettings).not.toHaveBeenCalled()

    showError.mockReset()
    await wrapper.get('[data-testid="asset-storage-result-retention-hours"]').setValue('8761')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('admin.assetStorage.validation.resultRetentionHours')
    expect(updateFileStorageSettings).not.toHaveBeenCalled()

    // 8760（一年）是允许的上限
    await wrapper.get('[data-testid="asset-storage-result-retention-hours"]').setValue('8760')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()
    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({ result_retention_hours: 8760 }),
    )
  })

  it('only shows the S3 fields for the s3 backend and requires credentials', async () => {
    const wrapper = await mountView()

    expect(wrapper.find('[data-testid="asset-storage-s3-fields"]').exists()).toBe(false)

    await wrapper.get('[data-testid="asset-storage-backend-s3"]').setValue()
    expect(wrapper.find('[data-testid="asset-storage-s3-fields"]').exists()).toBe(true)

    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('admin.assetStorage.validation.s3Required')
    expect(updateFileStorageSettings).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="asset-storage-s3-bucket"]').setValue('assets')
    await wrapper.get('[data-testid="asset-storage-s3-access-key-id"]').setValue('ak')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        backend: 's3',
        s3: expect.objectContaining({
          bucket: 'assets',
          access_key_id: 'ak',
          // 已保存的密钥保持不变
          secret_access_key: '',
        }),
      }),
    )
  })

  it('rejects out-of-range retention hours before calling the API', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="asset-storage-retention-hours"]').setValue('0')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.assetStorage.validation.retentionHours')
    expect(updateFileStorageSettings).not.toHaveBeenCalled()
  })

  it('accepts localhost over HTTP but rejects other non-HTTPS public base URLs', async () => {
    const wrapper = await mountView()

    await wrapper.get('[data-testid="asset-storage-public-base-url"]').setValue('http://example.com')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('admin.assetStorage.validation.publicBaseUrl')
    expect(updateFileStorageSettings).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="asset-storage-public-base-url"]').setValue('http://localhost:8080')
    await wrapper.get('[data-testid="asset-storage-save"]').trigger('click')
    await flushPromises()

    expect(updateFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({ public_base_url: 'http://localhost:8080' }),
    )
  })

  it('reports the outcome of the connection test', async () => {
    testFileStorageSettings.mockResolvedValueOnce({
      ok: true,
      backend: 'local',
      message: 'connection successful',
    })
    testFileStorageSettings.mockResolvedValueOnce({
      ok: false,
      backend: 'local',
      message: 'permission denied',
    })

    const wrapper = await mountView()

    await wrapper.get('[data-testid="asset-storage-test"]').trigger('click')
    await flushPromises()
    expect(testFileStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        backend: 'local',
        result_retention_hours: 24,
        result_max_total_bytes: 0,
        result_daily_max_count: 0,
        result_daily_max_bytes: 0,
        capacity_reserve_percent: 10,
      }),
    )
    expect(showSuccess).toHaveBeenCalledWith('connection successful')

    await wrapper.get('[data-testid="asset-storage-test"]').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('permission denied')
  })

  it('falls back to a localized error when loading the settings fails', async () => {
    getFileStorageSettings.mockRejectedValue({})

    await mountView()

    expect(showError).toHaveBeenCalledWith('admin.assetStorage.loadFailed')
  })
})
