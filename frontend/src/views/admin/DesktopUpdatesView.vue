<template>
  <AppLayout>
    <div class="space-y-6">
      <section class="flex flex-wrap items-end justify-between gap-4 border-b border-gray-200 pb-5 dark:border-dark-700">
        <div>
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">软件升级</h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">管理 Yingzo Windows 和 macOS 稳定版安装包。</p>
        </div>
        <button class="btn btn-secondary" :disabled="loading" @click="load">{{ loading ? '加载中…' : '刷新' }}</button>
      </section>

      <section class="card p-5">
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">存储配置</h3>
        <div class="mt-4 grid gap-4 md:grid-cols-2">
          <label class="field"><span>存储后端</span><select v-model="storage.backend"><option value="local">本地目录</option><option value="r2">Cloudflare R2</option></select></label>
          <label class="field"><span>升级服务公网域名（8081）</span><input v-model="storage.public_base_url" placeholder="https://updata.yingzo.art" /><small>用于访问升级 API；Nginx 应将此域名转发到 8081。</small></label>
          <label v-if="storage.backend === 'local'" class="field md:col-span-2"><span>本地目录（可选）</span><input v-model="storage.local_dir" placeholder="数据目录下 desktop-updates" /><small>当前生效：{{ storage.effective_local_dir }}</small></label>
          <template v-else>
            <label class="field"><span>Endpoint</span><input v-model="storage.r2.endpoint" /><small>填写账户级 S3 API 地址，例如 https://&lt;account_id&gt;.r2.cloudflarestorage.com，不要附加 Bucket 路径。</small></label>
            <label class="field"><span>Bucket</span><input v-model="storage.r2.bucket" /></label>
            <label class="field"><span>Prefix</span><input v-model="storage.r2.prefix" /></label>
            <label class="field"><span>Region</span><input v-model="storage.r2.region" /><small>Cloudflare R2 通常填写 auto。</small></label>
            <label class="field"><span>Access Key ID</span><input v-model="storage.r2.access_key_id" /></label>
            <label class="field"><span>Secret Access Key</span><input v-model="storage.r2.secret_access_key" type="password" placeholder="留空沿用已保存密钥" /></label>
            <label class="field md:col-span-2"><span>R2 自定义域名</span><input v-model="storage.r2.custom_domain" placeholder="https://downloads.example.com" /><small>可选；填写后，安装包下载地址会直接使用该域名，否则通过升级服务生成临时下载地址。</small></label>
            <label class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300"><input v-model="storage.r2.force_path_style" type="checkbox" /> 使用 path-style</label>
          </template>
        </div>
        <div class="mt-4 flex items-center gap-3"><button class="btn btn-primary" :disabled="saving || testing" @click="saveStorage">{{ saving ? '保存中…' : '保存存储配置' }}</button><button v-if="storage.backend === 'r2'" class="btn btn-secondary" :disabled="saving || testing" @click="testStorageConnection">{{ testing ? '测试中…' : '测试连接' }}</button><span v-if="storage.secret_access_key_configured" class="text-xs text-gray-500">R2 密钥已配置</span></div>
      </section>

      <section class="card p-5">
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">上传新版本</h3>
        <form class="mt-4 grid gap-4 md:grid-cols-2" @submit.prevent="uploadRelease">
          <label class="field"><span>版本号</span><input v-model="form.version" required placeholder="1.2.3" /></label>
          <label class="field"><span>平台 / 架构</span><select v-model="form.platform"><option value="win32">Windows x64</option><option value="darwin">macOS Apple Silicon（arm64）</option></select></label>
          <label class="field"><span>在线更新包</span><input ref="fileInput" type="file" :accept="form.platform === 'win32' ? '.exe' : '.zip'" required @change="onFile" /><small>{{ form.platform === 'win32' ? '上传 NSIS .exe，用于首次安装和在线升级。' : '上传 electron-updater 使用的 .zip。' }}</small></label>
          <label v-if="form.platform === 'darwin'" class="field"><span>首次安装包（DMG）</span><input ref="installerInput" type="file" accept=".dmg" required @change="onInstallerFile" /><small>软件下载页面提供此 DMG；在线升级仍使用上面的 ZIP。</small></label>
          <label class="field md:col-span-2"><span>更新说明</span><textarea v-model="form.release_notes" rows="3" placeholder="本次更新内容" /></label>
          <div class="md:col-span-2"><button class="btn btn-primary" :disabled="uploading">{{ uploading ? '上传中…' : '上传版本' }}</button><div v-if="uploading" class="mt-3 max-w-xl"><div class="mb-1 flex justify-between text-xs text-gray-500"><span>{{ uploadStatus }}</span><span>{{ uploadProgress }}%</span></div><div class="h-2 overflow-hidden rounded bg-gray-200 dark:bg-dark-700"><div class="h-full rounded bg-primary-500 transition-all" :style="{ width: `${uploadProgress}%` }" /></div></div></div>
        </form>
      </section>

      <section class="card overflow-x-auto p-5">
        <div class="mb-4 flex items-center justify-between"><h3 class="text-base font-semibold text-gray-900 dark:text-white">版本列表</h3><span class="text-xs text-gray-500">稳定版按平台/架构分别生效</span></div>
        <table class="w-full min-w-[1000px] text-left text-sm"><thead><tr class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700"><th class="py-2">版本</th><th>平台 / 架构</th><th>更新包</th><th>首次安装包</th><th>状态</th><th>发布时间</th><th class="text-right">操作</th></tr></thead><tbody><tr v-for="item in releases" :key="item.id" class="border-b border-gray-100 dark:border-dark-800"><td class="py-3"><div class="font-medium text-gray-900 dark:text-white">{{ item.version }}</div><div class="text-xs text-gray-500">{{ item.release_notes || '无更新说明' }}</div></td><td>{{ item.platform === 'darwin' ? 'macOS Apple Silicon' : 'Windows x64' }}</td><td><div>{{ item.package_filename }}</div><div class="text-xs text-gray-500">{{ formatSize(item.package_size_bytes) }} · {{ item.sha256.slice(0, 12) }}…</div></td><td><div>{{ item.installer_filename || '—' }}</div><div v-if="item.installer_size_bytes" class="text-xs text-gray-500">{{ formatSize(item.installer_size_bytes) }}</div></td><td><span class="rounded px-2 py-1 text-xs" :class="statusClass(item.status)">{{ item.status }}</span></td><td>{{ formatDate(item.published_at || item.created_at) }}</td><td class="space-x-2 text-right"><button v-if="item.status === 'draft' || item.status === 'superseded'" class="btn btn-primary btn-sm" @click="publishRelease(item)">发布</button><button v-if="item.status !== 'published'" class="btn btn-danger btn-sm" @click="deleteRelease(item)">删除</button></td></tr><tr v-if="!loading && releases.length === 0"><td colspan="7" class="py-10 text-center text-gray-500">暂无版本</td></tr></tbody></table>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import desktopUpdatesAPI, { type DesktopRelease, type DesktopUpdateStorage } from '@/api/admin/desktopUpdates'
import { useAppStore } from '@/stores'

const appStore = useAppStore()
const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const uploading = ref(false)
const uploadProgress = ref(0)
const uploadStatus = ref('')
const releases = ref<DesktopRelease[]>([])
const fileInput = ref<HTMLInputElement | null>(null)
const installerInput = ref<HTMLInputElement | null>(null)
const storage = reactive<DesktopUpdateStorage>({ backend: 'local', local_dir: '', effective_local_dir: '', public_base_url: 'https://updata.yingzo.art', secret_access_key_configured: false, r2: { endpoint: '', region: 'auto', bucket: '', access_key_id: '', prefix: 'desktop-updates', custom_domain: '', force_path_style: false } })
const form = reactive({ version: '', platform: 'win32' as 'win32' | 'darwin', release_notes: '', package: null as File | null, installerPackage: null as File | null })

async function load() { loading.value = true; try { Object.assign(storage, await desktopUpdatesAPI.getStorage()); releases.value = await desktopUpdatesAPI.list() } catch (error) { appStore.showError(errorMessage(error, '加载升级配置失败')) } finally { loading.value = false } }
async function saveStorage() { saving.value = true; try { Object.assign(storage, await desktopUpdatesAPI.updateStorage(storage)); appStore.showSuccess('存储配置已保存') } catch (error) { appStore.showError(errorMessage(error, '保存存储配置失败')) } finally { saving.value = false } }
async function testStorageConnection() { testing.value = true; try { const result = await desktopUpdatesAPI.testStorage(storage); if (result.ok) appStore.showSuccess('R2 连接成功'); else appStore.showError(result.message || 'R2 连接失败') } catch (error) { appStore.showError(errorMessage(error, '测试 R2 连接失败')) } finally { testing.value = false } }
function onFile(event: Event) { form.package = (event.target as HTMLInputElement).files?.[0] || null }
function onInstallerFile(event: Event) { form.installerPackage = (event.target as HTMLInputElement).files?.[0] || null }
const uploadChunkSize = 256 * 1024
function wait(milliseconds: number) { return new Promise(resolve => window.setTimeout(resolve, milliseconds)) }
function uploadReceived(job: Awaited<ReturnType<typeof desktopUpdatesAPI.getUpload>>, artifact: 'package' | 'installer') { return artifact === 'package' ? job.package_received : job.installer_received }
async function uploadArtifact(id: string, artifact: 'package' | 'installer', file: File, initialOffset: number, totalSize: number, progressBase: number) {
  let offset = initialOffset
  while (offset < file.size) {
    const end = Math.min(offset + uploadChunkSize, file.size)
    const chunk = file.slice(offset, end)
    let completed = false
    let lastError: unknown
    for (let attempt = 0; attempt < 6 && !completed; attempt += 1) {
      try {
        const job = await desktopUpdatesAPI.appendUploadChunk(id, artifact, offset, chunk)
        offset = uploadReceived(job, artifact)
        completed = true
      } catch (error) {
        lastError = error
        try {
          const job = await desktopUpdatesAPI.getUpload(id)
          const serverOffset = uploadReceived(job, artifact)
          if (serverOffset !== offset) {
            offset = serverOffset
            completed = true
            continue
          }
        } catch {
          // Keep retrying the same chunk when the status request is also transient.
        }
        if (attempt < 5) await wait(Math.min(8000, 500 * 2 ** attempt))
      }
    }
    if (!completed) throw lastError instanceof Error ? lastError : new Error('分片上传失败')
    uploadProgress.value = Math.min(99, Math.floor(((progressBase + offset) / totalSize) * 100))
  }
}
async function waitForUpload(id: string) {
  for (let attempt = 0; attempt < 1800; attempt += 1) {
    const job = await desktopUpdatesAPI.getUpload(id)
    if (job.status === 'completed') return
    if (job.status === 'failed') throw new Error(job.error || '服务器后台处理失败')
    uploadStatus.value = '服务器已收到文件，正在后台上传到 R2…'
    await wait(2000)
  }
  throw new Error('服务器后台处理超时，请刷新页面查看状态')
}
async function uploadRelease() {
  if (!form.package || (form.platform === 'darwin' && !form.installerPackage)) return
  uploading.value = true
  uploadProgress.value = 0
  uploadStatus.value = '正在分片上传到服务器…'
  try {
    const packageFile = form.package
    const installerFile = form.installerPackage
    const totalSize = packageFile.size + (installerFile?.size || 0)
    const job = await desktopUpdatesAPI.createUpload({
      version: form.version,
      platform: form.platform,
      arch: form.platform === 'darwin' ? 'arm64' : 'x64',
      release_notes: form.release_notes,
      filename: packageFile.name,
      installer_filename: installerFile?.name,
      package_size: packageFile.size,
      installer_size: installerFile?.size || 0,
    })
    await uploadArtifact(job.id, 'package', packageFile, job.package_received, totalSize, 0)
    if (installerFile) await uploadArtifact(job.id, 'installer', installerFile, job.installer_received, totalSize, packageFile.size)
    uploadProgress.value = 100
    uploadStatus.value = '文件已接收，正在后台上传到 R2…'
    await desktopUpdatesAPI.completeUpload(job.id)
    await waitForUpload(job.id)
    appStore.showSuccess('版本上传成功')
    form.version = ''
    form.release_notes = ''
    form.package = null
    form.installerPackage = null
    if (fileInput.value) fileInput.value.value = ''
    if (installerInput.value) installerInput.value.value = ''
    await load()
  } catch (error) {
    appStore.showError(errorMessage(error, '上传版本失败；服务器可能仍在后台处理，请刷新版本列表查看'))
  } finally {
    uploading.value = false
    uploadStatus.value = ''
  }
}
async function publishRelease(item: DesktopRelease) { if (!window.confirm(`确定发布版本 ${item.version}？同平台旧稳定版会自动标记为 superseded。`)) return; try { await desktopUpdatesAPI.publish(item.id); appStore.showSuccess('版本已发布'); await load() } catch (error) { appStore.showError(errorMessage(error, '发布版本失败')) } }
async function deleteRelease(item: DesktopRelease) { if (!window.confirm(`确定删除 ${item.version} 的安装包和元数据？`)) return; try { await desktopUpdatesAPI.remove(item.id); appStore.showSuccess('版本及安装包已删除'); await load() } catch (error) { appStore.showError(errorMessage(error, '删除版本失败')) } }
function formatSize(value: number) { if (!value) return '0 B'; const units = ['B', 'KB', 'MB', 'GB']; let n = value; let i = 0; while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ } return `${n.toFixed(i ? 1 : 0)} ${units[i]}` }
function formatDate(value: string) { return value ? new Date(value).toLocaleString() : '-' }
function statusClass(status: string) { return status === 'published' ? 'bg-green-100 text-green-700' : status === 'superseded' ? 'bg-yellow-100 text-yellow-700' : 'bg-gray-100 text-gray-600' }
function errorMessage(error: unknown, fallback: string) { const message = error instanceof Error ? error.message : (error as { message?: string })?.message; return message || fallback }
onMounted(load)
</script>

<style scoped>
.field { display: flex; flex-direction: column; gap: 0.375rem; font-size: 0.875rem; color: rgb(75 85 99); }
.field input, .field select, .field textarea { border: 1px solid rgb(209 213 219); border-radius: 0.375rem; background: transparent; padding: 0.5rem 0.625rem; color: inherit; }
.field small { font-size: 0.75rem; color: rgb(156 163 175); }
.dark .field { color: rgb(209 213 219); }
.dark .field input, .dark .field select, .dark .field textarea { border-color: rgb(75 85 99); }
</style>
