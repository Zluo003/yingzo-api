/**
 * Admin IP Ban API endpoints
 * IP / 网段封禁：命中规则的来源无法访问网站、面板 API 与 API 网关（/health 除外）
 */

import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

export interface IPBan {
  id: number
  // 规范化后的单个 IP 或 CIDR 网段
  ip: string
  reason: string
  created_by: number | null
  created_at: string
}

export interface CreateIPBanRequest {
  // 单个 IP 或 CIDR 网段
  ip: string
  reason?: string
}

export interface CreateIPBanResponse {
  ban: IPBan
  // false 表示该 IP / 网段此前已被封禁（接口幂等，返回已存在的规则）
  created: boolean
}

export interface IPInspection {
  ip: string
  // 注册额度的计数单位，也是建议的封禁目标：IPv4 为地址本身，IPv6 为所在 /64 网段
  network_key: string
  banned: boolean
  ban: IPBan | null
  // 该网络自助注册的账号数，含软删除（累计 / 最近 24 小时）
  registered_total: number
  registered_today: number
  bannable: boolean
  // 不可封禁时的原因码：IP_BAN_RESERVED_TARGET / IP_BAN_SELF
  not_bannable_reason?: string
}

/**
 * List IP ban rules with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param filters - Optional filters (search matches IP / CIDR and reason)
 * @param options - Optional request options (signal)
 * @returns Paginated list of ban rules
 */
export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    search?: string
  },
  options?: {
    signal?: AbortSignal
  }
): Promise<PaginatedResponse<IPBan>> {
  const { data } = await apiClient.get<PaginatedResponse<IPBan>>('/admin/ip-bans', {
    params: { page, page_size: pageSize, search: filters?.search || undefined },
    signal: options?.signal
  })
  return data
}

/**
 * Ban an IP address or CIDR range (idempotent)
 * @param request - Target IP / CIDR and optional reason
 * @returns The stored rule and whether it was newly created
 */
export async function create(request: CreateIPBanRequest): Promise<CreateIPBanResponse> {
  const { data } = await apiClient.post<CreateIPBanResponse>('/admin/ip-bans', request)
  return data
}

/**
 * Remove an IP ban rule
 * @param id - Ban rule ID
 * @returns Success confirmation
 */
export async function remove(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/ip-bans/${id}`)
  return data
}

/**
 * Inspect an IP: ban status, registrations from its network and whether it can be banned
 * @param ip - IP address (usually a user's registration IP)
 * @returns Inspection summary
 */
export async function inspect(ip: string): Promise<IPInspection> {
  const { data } = await apiClient.get<IPInspection>('/admin/ip-bans/inspect', {
    params: { ip }
  })
  return data
}

export const ipBansAPI = {
  list,
  create,
  remove,
  inspect
}

export default ipBansAPI
