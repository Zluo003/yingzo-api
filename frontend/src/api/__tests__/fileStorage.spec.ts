import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put, post } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  post: vi.fn(),
}))

vi.mock('../client', () => ({
  apiClient: {
    get,
    put,
    post,
  },
}))

import {
  getFileStorageSettings,
  updateFileStorageSettings,
  testFileStorageSettings,
  type FileStorageConfig,
  type FileStorageSettings,
} from '@/api/admin/fileStorage'

const config: FileStorageConfig = {
  schema_version: 1,
  backend: 's3',
  // 宿主机的真实素材目录（绝对路径）；留空表示使用默认目录
  local_dir: '/data/yingzo-assets',
  public_base_url: 'https://api-key.cc',
  retention_hours: 24,
  daily_max_count: 100,
  daily_max_bytes: 2147483648,
  max_total_bytes: 0,
  result_retention_hours: 720,
  result_max_total_bytes: 5368709120,
  // 生成产物自己的每日配额：默认 0 = 不限制，与参考素材上传配额互相独立
  result_daily_max_count: 0,
  result_daily_max_bytes: 0,
  capacity_reserve_percent: 10,
  s3: {
    endpoint: '',
    region: 'auto',
    bucket: 'assets',
    access_key_id: 'ak',
    secret_access_key: '',
    prefix: 'model-assets/',
    force_path_style: false,
  },
}

const settings: FileStorageSettings = {
  ...config,
  s3: { ...config.s3, secret_access_key: undefined },
  source: 'database',
  // local_path 是实际生效的目录：配置了 local_dir 时与它一致
  local_path: '/data/yingzo-assets',
  secret_access_key_configured: true,
  usage: {
    active_files: 3,
    active_bytes: 1024,
    local_files: 1,
    s3_files: 2,
    expiring_within_1_hour: 0,
    reference_files: 2,
    reference_bytes: 512,
    generated_files: 1,
    generated_bytes: 512,
  },
  effective_public_base_url: 'https://api-key.cc',
}

describe('admin file storage API', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    post.mockReset()
  })

  it('getFileStorageSettings reads the admin file-service settings endpoint', async () => {
    get.mockResolvedValue({ data: settings })

    const result = await getFileStorageSettings()

    expect(get).toHaveBeenCalledWith('/admin/file-service/settings')
    expect(result).toEqual(settings)
  })

  it('reads the per-category usage split for reference materials and generated results', async () => {
    get.mockResolvedValue({ data: settings })

    const { usage } = await getFileStorageSettings()

    expect(usage.reference_files).toBe(2)
    expect(usage.reference_bytes).toBe(512)
    expect(usage.generated_files).toBe(1)
    expect(usage.generated_bytes).toBe(512)
  })

  it('reads the capacity reserve waterline as a number next to the per-category budgets', async () => {
    get.mockResolvedValue({ data: settings })

    const result = await getFileStorageSettings()

    // 后端始终返回归一化后的数字，0（不留冗余）也是合法值
    expect(result.capacity_reserve_percent).toBe(10)
    expect(result.max_total_bytes).toBe(0)
    expect(result.result_max_total_bytes).toBe(5368709120)
  })

  it('updateFileStorageSettings puts the config payload and returns the refreshed settings', async () => {
    put.mockResolvedValue({ data: settings })

    const result = await updateFileStorageSettings(config)

    expect(put).toHaveBeenCalledWith('/admin/file-service/settings', config)
    expect(result.usage.active_files).toBe(3)
    expect(result.effective_public_base_url).toBe('https://api-key.cc')
  })

  it('reads the configurable local directory and the effective local path', async () => {
    get.mockResolvedValue({ data: settings })

    const result = await getFileStorageSettings()

    expect(result.local_dir).toBe('/data/yingzo-assets')
    // local_path 是当前生效的目录，配置了 local_dir 时与它一致
    expect(result.local_path).toBe('/data/yingzo-assets')
  })

  it('treats an empty local_dir as the default directory and reports it through local_path', async () => {
    get.mockResolvedValue({
      data: { ...settings, local_dir: '', local_path: '/app/data/agent-assets' },
    })

    const result = await getFileStorageSettings()

    // 空 = 使用默认的 <data_dir>/agent-assets
    expect(result.local_dir).toBe('')
    expect(result.local_path).toBe('/app/data/agent-assets')
  })

  it('always sends local_dir, including the empty string that means the default directory', async () => {
    put.mockResolvedValue({ data: settings })
    post.mockResolvedValue({ data: { ok: true, backend: 'local', message: 'ok' } })

    await updateFileStorageSettings(config)
    await updateFileStorageSettings({ ...config, local_dir: '' })
    await testFileStorageSettings({ ...config, local_dir: '' })

    expect(put).toHaveBeenNthCalledWith(
      1,
      '/admin/file-service/settings',
      expect.objectContaining({ local_dir: '/data/yingzo-assets' }),
    )
    // 空字符串必须照原样提交：它表示"使用默认目录"，不能因为留空而省略字段
    expect(put).toHaveBeenNthCalledWith(
      2,
      '/admin/file-service/settings',
      expect.objectContaining({ local_dir: '' }),
    )
    const putBody = put.mock.calls[1][1] as Record<string, unknown>
    expect(putBody).toHaveProperty('local_dir', '')
    const postBody = post.mock.calls[0][1] as Record<string, unknown>
    expect(postBody).toHaveProperty('local_dir', '')
  })

  it('reads the generated result daily quota, where 0 means unlimited', async () => {
    get.mockResolvedValue({ data: settings })

    const result = await getFileStorageSettings()

    // 0 = 不限制（产物不会因配额被拒）；参考素材的上传配额是另一组字段
    expect(result.result_daily_max_count).toBe(0)
    expect(result.result_daily_max_bytes).toBe(0)
    expect(result.daily_max_count).toBe(100)
    expect(result.daily_max_bytes).toBe(2147483648)
  })

  it('sends the generated result daily quota independently from the reference upload quota', async () => {
    put.mockResolvedValue({ data: settings })

    await updateFileStorageSettings({
      ...config,
      result_daily_max_count: 500,
      result_daily_max_bytes: 5 * 1024 * 1024 * 1024,
    })

    const body = put.mock.calls[0][1] as FileStorageConfig
    // 产物配额与上传配额是两组互不占用的字段
    expect(body.result_daily_max_count).toBe(500)
    expect(body.result_daily_max_bytes).toBe(5 * 1024 * 1024 * 1024)
    expect(body.daily_max_count).toBe(100)
    expect(body.daily_max_bytes).toBe(2147483648)
  })

  it('always sends both generated result daily quota fields, including the unlimited default', async () => {
    put.mockResolvedValue({ data: settings })
    post.mockResolvedValue({ data: { ok: true, backend: 's3', message: 'ok' } })

    await updateFileStorageSettings(config)
    await testFileStorageSettings(config)

    const putBody = put.mock.calls[0][1] as Record<string, unknown>
    const postBody = post.mock.calls[0][1] as Record<string, unknown>
    for (const body of [putBody, postBody]) {
      expect(body).toHaveProperty('result_daily_max_count', 0)
      expect(body).toHaveProperty('result_daily_max_bytes', 0)
    }
  })

  it('sends the generated result retention, capacity and the shared capacity reserve in the payload', async () => {
    put.mockResolvedValue({ data: settings })

    await updateFileStorageSettings(config)

    const body = put.mock.calls[0][1] as FileStorageConfig
    expect(body.result_retention_hours).toBe(720)
    expect(body.result_max_total_bytes).toBe(5368709120)
    // 容量冗余水位是共享字段，对参考素材与生成产物各自的上限分别生效
    expect(body.capacity_reserve_percent).toBe(10)
    // 参考素材与生成产物的预算互相独立
    expect(body.max_total_bytes).toBe(0)
    expect(body.retention_hours).toBe(24)
  })

  it('accepts every capacity reserve from 0 (no reserve) up to 50', async () => {
    put.mockResolvedValue({ data: settings })

    for (const reserve of [0, 10, 50]) {
      await updateFileStorageSettings({ ...config, capacity_reserve_percent: reserve })
    }

    expect(put).toHaveBeenNthCalledWith(
      1,
      '/admin/file-service/settings',
      expect.objectContaining({ capacity_reserve_percent: 0 }),
    )
    expect(put).toHaveBeenNthCalledWith(
      2,
      '/admin/file-service/settings',
      expect.objectContaining({ capacity_reserve_percent: 10 }),
    )
    expect(put).toHaveBeenNthCalledWith(
      3,
      '/admin/file-service/settings',
      expect.objectContaining({ capacity_reserve_percent: 50 }),
    )
  })

  it('never sends the removed result_cleanup_policy field', async () => {
    put.mockResolvedValue({ data: settings })
    post.mockResolvedValue({ data: { ok: true, backend: 's3', message: 'ok' } })

    await updateFileStorageSettings(config)
    await testFileStorageSettings(config)

    const putBody = put.mock.calls[0][1] as Record<string, unknown>
    const postBody = post.mock.calls[0][1] as Record<string, unknown>
    expect(putBody).not.toHaveProperty('result_cleanup_policy')
    expect(postBody).not.toHaveProperty('result_cleanup_policy')
    // 水位字段则是必须携带的
    expect(putBody.capacity_reserve_percent).toBe(10)
    expect(postBody.capacity_reserve_percent).toBe(10)
  })

  it('keeps an empty secret access key in the payload so the stored secret is preserved', async () => {
    put.mockResolvedValue({ data: settings })

    await updateFileStorageSettings(config)

    expect(put).toHaveBeenCalledWith(
      '/admin/file-service/settings',
      expect.objectContaining({
        s3: expect.objectContaining({ secret_access_key: '' }),
      }),
    )
    // usage/source/effective_public_base_url are read-only and never sent.
    const body = put.mock.calls[0][1] as Record<string, unknown>
    expect(body).not.toHaveProperty('usage')
    expect(body).not.toHaveProperty('source')
    expect(body).not.toHaveProperty('effective_public_base_url')
  })

  it('testFileStorageSettings posts the config and surfaces a failed probe', async () => {
    post.mockResolvedValue({ data: { ok: false, backend: 's3', message: 'bucket not found' } })

    const result = await testFileStorageSettings(config)

    expect(post).toHaveBeenCalledWith('/admin/file-service/test', config)
    expect(result.ok).toBe(false)
    expect(result.message).toBe('bucket not found')
  })

  it('testFileStorageSettings posts the generated result fields and the capacity reserve too', async () => {
    post.mockResolvedValue({ data: { ok: true, backend: 's3', message: 'ok' } })

    await testFileStorageSettings({ ...config, capacity_reserve_percent: 25 })

    expect(post).toHaveBeenCalledWith(
      '/admin/file-service/test',
      expect.objectContaining({
        result_retention_hours: 720,
        result_max_total_bytes: 5368709120,
        capacity_reserve_percent: 25,
      }),
    )
  })
})
