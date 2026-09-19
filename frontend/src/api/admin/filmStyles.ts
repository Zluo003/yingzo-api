import { apiClient } from '../client'

export interface FilmStyleTemplate {
  id: string
  category: 'realistic' | '3d' | '2d'
  name: string
  prompt: string
  mimeType: string
  sha256: string
  width: number
  height: number
  revision: number
  sortOrder: number
  status: 'active' | 'archived'
  updatedAt: string
}

export async function list(): Promise<FilmStyleTemplate[]> {
  const { data } = await apiClient.get<{ items: FilmStyleTemplate[] }>('/admin/film-style-templates')
  return data.items
}

export async function create(input: { category: string; name: string; prompt: string; preview: File }): Promise<FilmStyleTemplate> {
  const body = new FormData()
  body.append('category', input.category)
  body.append('name', input.name)
  body.append('prompt', input.prompt)
  body.append('preview', input.preview)
  const { data } = await apiClient.post<FilmStyleTemplate>('/admin/film-style-templates', body)
  return data
}

export async function update(id: string, input: { category?: string; name?: string; prompt?: string; preview?: File }): Promise<FilmStyleTemplate> {
  const body = new FormData()
  if (input.category) body.append('category', input.category)
  if (input.name) body.append('name', input.name)
  if (input.prompt) body.append('prompt', input.prompt)
  if (input.preview) body.append('preview', input.preview)
  const { data } = await apiClient.put<FilmStyleTemplate>(`/admin/film-style-templates/${id}`, body)
  return data
}

export async function archive(id: string): Promise<void> {
  await apiClient.delete(`/admin/film-style-templates/${id}`)
}

export const filmStylesAdminApi = { list, create, update, archive }
export default filmStylesAdminApi
