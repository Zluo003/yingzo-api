import { apiClient } from '../client'
import type { TestS3Response } from './backup'

export type FileStorageBackend = 'local' | 's3'

export interface FileStorageS3Config {
  endpoint: string
  region: string
  bucket: string
  access_key_id: string
  /**
   * 留空表示沿用已保存的密钥；后端只在非空时覆盖，返回值永远不含该字段。
   */
  secret_access_key?: string
  prefix: string
  /**
   * 对象存储的自定义公网域名（如 https://cdn.example.com），可选。
   *
   * 配置后参考素材返回给下游的 URL 直接指向对象存储（域名 + 对象前缀 + 素材 ID），
   * 上游可以绕过平台代理直读文件，需要先在对象存储上完成自定义域名绑定并开启公开读取
   * （Cloudflare R2 为自定义域名 + Public Access）。留空时素材仍走平台代理地址分发。
   *
   * 生成产物不受影响：它们固定保存在本地磁盘，始终通过平台代理地址分发。
   */
  custom_domain: string
  force_path_style: boolean
}

export interface FileStorageConfig {
  schema_version: number
  backend: FileStorageBackend
  /**
   * 本地素材根目录，必须是绝对路径；留空表示沿用默认目录 `<data_dir>/agent-assets`
   * （此时返回值里的 local_path 就是那个默认目录）。
   *
   * Docker 部署时它应当指向从宿主机 bind mount 进来的真实目录，而不是容器内的匿名卷：
   * compose 里挂载的是 `${AGENT_ASSETS_HOST_DIR:-./data/agent-assets}:/app/data/agent-assets`，
   * 所以容器内的默认目录是 /app/data/agent-assets，宿主机侧可用 AGENT_ASSETS_HOST_DIR 调整；
   * 容器以 uid 1000 运行，该目录必须对它可写。
   *
   * 保存时后端会真实创建目录并写入探针文件做校验，不可写则保存失败并返回
   * FILE_STORAGE_LOCAL_DIR_UNAVAILABLE；后端同时拒绝非绝对路径、`/` 与系统目录
   * （/bin、/sbin、/lib、/lib64、/usr、/etc、/proc、/sys、/dev、/boot、/root、/var）。
   *
   * 修改只影响之后新写入的素材：数据库里保存的是每个素材的绝对路径，已有文件仍从原目录
   * 读取，直到各自过期。
   */
  local_dir: string
  /** 对外分发素材 URL 的公网地址；留空表示按请求来源推断。 */
  public_base_url: string
  /** 下游上传的参考素材的保存时长（小时），范围 1–720。 */
  retention_hours: number
  /**
   * 单个凭证每天（滚动 24 小时）允许上传的参考素材数量，范围 1–1000000。
   * 只统计下游上传的参考素材；生成产物使用独立的 result_daily_max_count。
   */
  daily_max_count: number
  /**
   * 单个凭证每天（滚动 24 小时）允许上传的参考素材总字节数，必须 ≥ 1。
   * 只统计下游上传的参考素材；生成产物使用独立的 result_daily_max_bytes。
   */
  daily_max_bytes: number
  /** 参考素材的总容量上限，0 表示不限制；超过冗余水位后按最早失效优先驱逐未租用素材。 */
  max_total_bytes: number
  /**
   * 生成产物（网关回捞的生成视频/图片等交付物）的保存时长（小时），范围 1–8760，
   * 与参考素材的 retention_hours 互相独立。
   */
  result_retention_hours: number
  /** 生成产物的总容量上限，0 表示不限制；与参考素材各自独立预算。 */
  result_max_total_bytes: number
  /**
   * 生成产物（上游回捞的交付物）的滚动 24 小时数量配额，按凭证（API Key + 用户）统计，
   * 取值范围 0–1000000 的整数，0 = 不限制（默认），即产物不会因配额被拒绝。
   *
   * 与参考素材的上传配额 daily_max_count 完全独立：后者只统计下游上传的参考素材，
   * 本字段只统计网关回捞的生成产物，两者互不占用。
   */
  result_daily_max_count: number
  /**
   * 生成产物的滚动 24 小时字节配额，按凭证（API Key + 用户）统计，必须 ≥ 0，
   * 0 = 不限制（默认），即产物不会因配额被拒绝。
   *
   * 与参考素材的上传配额 daily_max_bytes 完全独立：后者只统计下游上传的参考素材，
   * 本字段只统计网关回捞的生成产物，两者互不占用。
   */
  result_daily_max_bytes: number
  /**
   * 容量冗余水位（0–50 的整数百分比，默认 10）。
   *
   * 它不是一个"满了就拒绝写入"的开关，而是一条提前清理的水位线：参考素材与生成产物
   * 各按自己的上限独立计算 `上限 × (1 − 冗余比例)`，占用一旦超过这条水位，网关就开
   * 始删除最接近过期的文件（正在被读取/租用的文件永远不会被删除），从而保证新写入
   * 始终有空间可用，而不是等占用撞上硬上限才失败。
   *
   * 0 是合法值，表示不留冗余（占用达到上限才开始清理）；某类上限为 0（不限制）时该
   * 类不触发自动清理。
   */
  capacity_reserve_percent: number
  s3: FileStorageS3Config
}

export interface FileStorageUsage {
  active_files: number
  active_bytes: number
  local_files: number
  s3_files: number
  expiring_within_1_hour: number
  /** 参考素材（下游上传）当前有效文件数与占用字节数。 */
  reference_files: number
  reference_bytes: number
  /** 生成产物（上游回捞交付物）当前有效文件数与占用字节数。 */
  generated_files: number
  generated_bytes: number
}

export interface FileStorageSettings extends FileStorageConfig {
  /** 配置来源：database / environment / default。 */
  source: string
  /**
   * 本地后端实际生效的素材目录：配置了 local_dir 时就是它，否则是默认目录
   * `<data_dir>/agent-assets`。只读，用来展示"当前生效"的目录。
   */
  local_path: string
  secret_access_key_configured: boolean
  usage: FileStorageUsage
  /**
   * 容器内素材目录对应的宿主机目录，由部署时的 AGENT_ASSETS_HOST_DIR 注入（只读）。
   * 用来提示"local_dir 填的是容器内路径，宿主机那侧是这里"，避免把宿主机路径填进 local_dir。
   */
  mounted_host_dir?: string
  effective_public_base_url: string
}

/** 与备份 S3 测试响应同形，额外带上被测试的 backend。 */
export interface FileStorageTestResponse extends TestS3Response {
  backend: FileStorageBackend
}

// Temporary asset storage settings
export async function getFileStorageSettings(): Promise<FileStorageSettings> {
  const { data } = await apiClient.get<FileStorageSettings>('/admin/file-service/settings')
  return data
}

export async function updateFileStorageSettings(
  config: FileStorageConfig,
): Promise<FileStorageSettings> {
  const { data } = await apiClient.put<FileStorageSettings>('/admin/file-service/settings', config)
  return data
}

export async function testFileStorageSettings(
  config: FileStorageConfig,
): Promise<FileStorageTestResponse> {
  const { data } = await apiClient.post<FileStorageTestResponse>('/admin/file-service/test', config)
  return data
}

export const fileStorageAPI = {
  getFileStorageSettings,
  updateFileStorageSettings,
  testFileStorageSettings,
}

export default fileStorageAPI
