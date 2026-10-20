import { apiClient } from '../client'

export interface MediaAuditEndpoint {
  protocol: 'openai' | 'gemini'
  domain: string
  api_key?: string
  clear_key?: boolean
  key_configured: boolean
  model: string
  timeout_seconds: number
}
export interface MediaAuditConfig {
  primary: MediaAuditEndpoint
  fallback: MediaAuditEndpoint
  admin_emails: string[]
  retention_days: number
  political_names: boolean
  public_figure_names: boolean
  nudity: boolean
}
export interface MediaAuditResult {
  decision: string
  category: string
  reason: string
  matched_text: string
  matched_image_indexes: number[]
  reviewer: string
  used_fallback: boolean
  policy_version: string
}
export interface MediaAuditEvent {
  id: string
  request_id: string
  user_id: number
  user_email: string
  api_key_id: number
  account_id: number
  account_name: string
  model: string
  media_type: string
  decision: string
  category: string
  reason: string
  reviewer: string
  created_at: string
  expires_at: string
  notified_at: string | null
  notify_attempts: number
  notify_pending: boolean
  evidence?: {
    input: { requested_at?: string; prompt: string; images: string[]; video_channel_id: string }
    normalized_prompt: string
    assets: { original_url: string; mime: string; sha256: string; backup_url?: string }[]
    result: MediaAuditResult
  }
}
const base = '/admin/risk-control/media-input'
export const mediaInputAuditAPI = {
  getConfig: async () => (await apiClient.get<MediaAuditConfig>(`${base}/config`)).data,
  saveConfig: async (config: MediaAuditConfig) =>
    (await apiClient.put<MediaAuditConfig>(`${base}/config`, config)).data,
  probe: async (slot: string, prompt: string, images: string[]) =>
    (
      await apiClient.post<MediaAuditResult>(
        `${base}/probe`,
        { slot, prompt, images },
        { timeout: 240000 }
      )
    ).data,
  listEvents: async (page: number) =>
    (await apiClient.get<MediaAuditEvent[]>(`${base}/events`, { params: { page } })).data,
  getEvent: async (id: string) =>
    (await apiClient.get<MediaAuditEvent>(`${base}/events/${encodeURIComponent(id)}`)).data,
  getAsset: async (id: string, index: number) =>
    (
      await apiClient.get<Blob>(`${base}/events/${encodeURIComponent(id)}/assets/${index}`, {
        responseType: 'blob'
      })
    ).data
}
