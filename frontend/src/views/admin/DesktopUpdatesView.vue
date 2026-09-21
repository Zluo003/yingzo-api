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
            <label class="field"><span>Endpoint</span><input v-model="storage.r2.endpoint" /></label>
            <label class="field"><span>Bucket</span><input v-model="storage.r2.bucket" /></label>
            <label class="field"><span>Prefix</span><input v-model="storage.r2.prefix" /></label>
            <label class="field"><span>Region</span><input v-model="storage.r2.region" /></label>
            <label class="field"><span>Access Key ID</span><input v-model="storage.r2.access_key_id" /></label>
            <label class="field"><span>Secret Access Key</span><input v-model="storage.r2.secret_access_key" type="password" placeholder="留空沿用已保存密钥" /></label>
            <label class="field md:col-span-2"><span>R2 自定义域名</span><input v-model="storage.r2.custom_domain" placeholder="https://downloads.example.com" /><small>可选；填写后，安装包下载地址会直接使用该域名，否则通过升级服务生成临时下载地址。</small></label>
            <label class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300"><input v-model="storage.r2.force_path_style" type="checkbox" /> 使用 path-style</label>
          </template>
        </div>
        <div class="mt-4 flex items-center gap-3"><button class="btn btn-primary" :disabled="saving" @click="saveStorage">{{ saving ? '保存中…' : '保存存储配置' }}</button><span v-if="storage.secret_access_key_configured" class="text-xs text-gray-500">R2 密钥已配置</span></div>
      </section>

      <section class="card p-5">
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">上传新版本</h3>
        <form class="mt-4 grid gap-4 md:grid-cols-2" @submit.prevent="uploadRelease">
          <label class="field"><span>版本号</span><input v-model="form.version" required placeholder="1.2.3" /></label>
          <label class="field"><span>平台</span><select v-model="form.platform"><option value="win32">Windows</option><option value="darwin">macOS</option></select></label>
          <label class="field"><span>架构</span><select v-model="form.arch"><option value="x64">x64</option><option value="arm64">arm64</option><option value="universal">Universal</option></select></label>
          <label class="field"><span>安装包</span><input ref="fileInput" type="file" :accept="form.platform === 'win32' ? '.exe' : '.zip'" required @change="onFile" /></label>
          <label class="field md:col-span-2"><span>更新说明</span><textarea v-model="form.release_notes" rows="3" placeholder="本次更新内容" /></label>
          <div class="md:col-span-2"><button class="btn btn-primary" :disabled="uploading">{{ uploading ? '上传中…' : '上传版本' }}</button></div>
        </form>
      </section>

      <section class="card overflow-x-auto p-5">
        <div class="mb-4 flex items-center justify-between"><h3 class="text-base font-semibold text-gray-900 dark:text-white">版本列表</h3><span class="text-xs text-gray-500">稳定版按平台/架构分别生效</span></div>
        <table class="w-full min-w-[900px] text-left text-sm"><thead><tr class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700"><th class="py-2">版本</th><th>平台/架构</th><th>状态</th><th>大小</th><th>SHA-256</th><th>发布时间</th><th class="text-right">操作</th></tr></thead><tbody><tr v-for="item in releases" :key="item.id" class="border-b border-gray-100 dark:border-dark-800"><td class="py-3"><div class="font-medium text-gray-900 dark:text-white">{{ item.version }}</div><div class="text-xs text-gray-500">{{ item.package_filename }}</div></td><td>{{ item.platform }}/{{ item.arch }}</td><td><span class="rounded px-2 py-1 text-xs" :class="statusClass(item.status)">{{ item.status }}</span></td><td>{{ formatSize(item.package_size_bytes) }}</td><td class="max-w-[180px] truncate font-mono text-xs" :title="item.sha256">{{ item.sha256 }}</td><td>{{ formatDate(item.published_at || item.created_at) }}</td><td class="space-x-2 text-right"><button v-if="item.status === 'draft' || item.status === 'superseded'" class="btn btn-primary btn-sm" @click="publishRelease(item)">发布</button><button v-if="item.status !== 'published'" class="btn btn-danger btn-sm" @click="deleteRelease(item)">删除</button></td></tr><tr v-if="!loading && releases.length === 0"><td colspan="7" class="py-10 text-center text-gray-500">暂无版本</td></tr></tbody></table>
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
const uploading = ref(false)
const releases = ref<DesktopRelease[]>([])
const fileInput = ref<HTMLInputElement | null>(null)
const storage = reactive<DesktopUpdateStorage>({ backend: 'local', local_dir: '', effective_local_dir: '', public_base_url: 'https://updata.yingzo.art', secret_access_key_configured: false, r2: { endpoint: '', region: 'auto', bucket: '', access_key_id: '', prefix: 'desktop-updates', custom_domain: '', force_path_style: false } })
const form = reactive({ version: '', platform: 'win32' as 'win32' | 'darwin', arch: 'x64' as 'x64' | 'arm64' | 'universal', release_notes: '', package: null as File | null })

async function load() { loading.value = true; try { Object.assign(storage, await desktopUpdatesAPI.getStorage()); releases.value = await desktopUpdatesAPI.list() } catch (error) { appStore.showError(errorMessage(error, '加载升级配置失败')) } finally { loading.value = false } }
async function saveStorage() { saving.value = true; try { Object.assign(storage, await desktopUpdatesAPI.updateStorage(storage)); appStore.showSuccess('存储配置已保存') } catch (error) { appStore.showError(errorMessage(error, '保存存储配置失败')) } finally { saving.value = false } }
function onFile(event: Event) { form.package = (event.target as HTMLInputElement).files?.[0] || null }
async function uploadRelease() { if (!form.package) return; uploading.value = true; try { await desktopUpdatesAPI.upload({ version: form.version, platform: form.platform, arch: form.arch, release_notes: form.release_notes, package: form.package }); appStore.showSuccess('版本上传成功'); form.version = ''; form.release_notes = ''; form.package = null; if (fileInput.value) fileInput.value.value = ''; await load() } catch (error) { appStore.showError(errorMessage(error, '上传版本失败')) } finally { uploading.value = false } }
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
