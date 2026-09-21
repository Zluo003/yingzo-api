import { apiClient } from '../client'

export type DesktopUpdateBackend = 'local' | 'r2'
export type DesktopUpdatePlatform = 'win32' | 'darwin'
export type DesktopUpdateArch = 'x64' | 'arm64' | 'universal'

export interface DesktopUpdateR2Config {
  endpoint: string
  region: string
  bucket: string
  access_key_id: string
  secret_access_key?: string
  prefix: string
  custom_domain: string
  force_path_style: boolean
}

export interface DesktopUpdateStorage {
  backend: DesktopUpdateBackend
  local_dir: string
  effective_local_dir: string
  public_base_url: string
  secret_access_key_configured: boolean
  r2: DesktopUpdateR2Config
}

export interface DesktopRelease {
  id: string
  version: string
  platform: DesktopUpdatePlatform
  arch: DesktopUpdateArch
  status: 'draft' | 'published' | 'superseded' | 'disabled'
  package_filename: string
  storage_backend: DesktopUpdateBackend
  package_size_bytes: number
  sha256: string
  sha512: string
  release_notes: string
  download_url: string
  metadata_url: string
  installer_filename: string
  installer_size_bytes: number
  installer_sha256: string
  installer_url: string
  created_at: string
  published_at?: string
}

export async function list(): Promise<DesktopRelease[]> {
  const { data } = await apiClient.get<DesktopRelease[]>('/admin/desktop-updates')
  return data
}

export async function getStorage(): Promise<DesktopUpdateStorage> {
  const { data } = await apiClient.get<DesktopUpdateStorage>('/admin/desktop-updates/storage')
  return data
}

export async function updateStorage(input: Partial<DesktopUpdateStorage>): Promise<DesktopUpdateStorage> {
  const { data } = await apiClient.put<DesktopUpdateStorage>('/admin/desktop-updates/storage', input)
  return data
}

export async function testStorage(input: Partial<DesktopUpdateStorage>): Promise<{ ok: boolean; message: string }> {
  const { data } = await apiClient.post<{ ok: boolean; message: string }>('/admin/desktop-updates/storage/test', input)
  return data
}

export async function upload(input: {
  version: string
  platform: DesktopUpdatePlatform
  arch: DesktopUpdateArch
  release_notes: string
  package: File
  installerPackage?: File
}): Promise<DesktopRelease> {
  const form = new FormData()
  form.append('version', input.version)
  form.append('platform', input.platform)
  form.append('arch', input.arch)
  form.append('release_notes', input.release_notes)
  form.append('package', input.package)
  if (input.installerPackage) form.append('installer_package', input.installerPackage)
  const { data } = await apiClient.post<DesktopRelease>('/admin/desktop-updates', form)
  return data
}

export async function publish(id: string): Promise<DesktopRelease> {
  const { data } = await apiClient.post<DesktopRelease>(`/admin/desktop-updates/${id}/publish`)
  return data
}

export async function remove(id: string): Promise<{ deleted: boolean }> {
  const { data } = await apiClient.delete<{ deleted: boolean }>(`/admin/desktop-updates/${id}`)
  return data
}

export default { list, getStorage, updateStorage, testStorage, upload, publish, remove }
