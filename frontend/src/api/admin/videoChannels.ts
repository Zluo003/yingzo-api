import { apiClient } from '../client'

export interface VideoChannel {
  id: string
  name: string
  enabled: boolean
  available?: boolean
}
export async function listVideoChannels(): Promise<VideoChannel[]> {
  return (await apiClient.get<VideoChannel[]>('/admin/video-channels')).data
}
export async function saveVideoChannel(channel: VideoChannel, create = false): Promise<VideoChannel> {
  return (await apiClient.request<VideoChannel>({ url: '/admin/video-channels', method: create ? 'POST' : 'PUT', data: channel })).data
}
