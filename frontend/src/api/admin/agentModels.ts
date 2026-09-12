/**
 * Yingzo Agent（系统内置聚合分组）模型目录与计价 API。
 *
 * 后端接口（均为分组维度，分组必须真的是 agent 分组，否则返回错误）：
 * - GET    /admin/groups/:id/agent-models            读取当前目录（模型 + 倍率 + 单价）
 * - POST   /admin/groups/:id/agent-models/sync       从分组内可用账号重新发现模型
 * - PUT    /admin/groups/:id/agent-models/:model_id  启用/停用 + 配置价格
 * - DELETE /admin/groups/:id/agent-models/:model_id  排除模型（软删除，重新同步也不会回来）
 */
import { apiClient } from '../client'

export type AgentMediaType = 'text' | 'image' | 'video'
export type AgentBillingUnit = 'image' | 'second'

/** 图片按 1K/2K/4K 每张计价；视频按分辨率每秒计价。 */
export interface AgentModelPrice {
  id?: number
  agent_model_id?: number
  resolution: string
  billing_unit?: AgentBillingUnit
  unit_price: number
  created_at?: string
  updated_at?: string
}

export interface AgentGroupModel {
  id: number
  group_id: number
  platform: string
  model_code: string
  media_type: AgentMediaType
  enabled: boolean
  /** 分组内当前仍有可用账号提供该模型；false 表示账号已移出分组。 */
  available: boolean
  excluded: boolean
  excluded_at?: string | null
  discovered_at: string
  last_seen_at: string
  created_at: string
  updated_at: string
  prices: AgentModelPrice[]
  /** 文本模型在源渠道价之上的下游倍率；null 表示尚未配置（该模型不可调用）。 */
  rate_multiplier: number | null
}

export interface AgentModelCatalogConfig {
  models: AgentGroupModel[]
}

export interface UpdateAgentModelPayload {
  media_type: AgentMediaType
  enabled: boolean
  /** 仅文本模型：源渠道价之上的下游倍率。 */
  rate_multiplier?: number | null
  prices?: AgentModelPrice[]
}

export async function getAgentModels(groupId: number): Promise<AgentModelCatalogConfig> {
  const { data } = await apiClient.get<AgentModelCatalogConfig>(
    `/admin/groups/${groupId}/agent-models`,
  )
  return data
}

export async function syncAgentModels(groupId: number): Promise<AgentModelCatalogConfig> {
  const { data } = await apiClient.post<AgentModelCatalogConfig>(
    `/admin/groups/${groupId}/agent-models/sync`,
  )
  return data
}

export async function updateAgentModel(
  groupId: number,
  modelId: number,
  payload: UpdateAgentModelPayload,
): Promise<AgentModelCatalogConfig> {
  const { data } = await apiClient.put<AgentModelCatalogConfig>(
    `/admin/groups/${groupId}/agent-models/${modelId}`,
    payload,
  )
  return data
}

export async function deleteAgentModel(
  groupId: number,
  modelId: number,
): Promise<{ deleted: boolean }> {
  const { data } = await apiClient.delete<{ deleted: boolean }>(
    `/admin/groups/${groupId}/agent-models/${modelId}`,
  )
  return data
}

export const agentModelsAPI = {
  getAgentModels,
  syncAgentModels,
  updateAgentModel,
  deleteAgentModel,
}

export default agentModelsAPI
