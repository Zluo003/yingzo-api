import { apiClient } from '../client'

export interface TutorialArticle {
  id: string
  title: string
  url: string
  enabled: boolean
}
export interface TutorialCategory {
  id: string
  title: string
  enabled: boolean
  articles: TutorialArticle[]
}
export interface TutorialConfig {
  enabled: boolean
  mode: 'iframe' | 'sdk'
  categories: TutorialCategory[]
  app_id?: string
  app_secret?: string
  secret_configured?: boolean
  allowed_origins?: string[]
}

export async function getTutorialConfig(): Promise<TutorialConfig> {
  return (await apiClient.get<TutorialConfig>('/admin/tutorials')).data
}

export async function saveTutorialConfig(config: TutorialConfig): Promise<TutorialConfig> {
  return (await apiClient.put<TutorialConfig>('/admin/tutorials', config)).data
}
