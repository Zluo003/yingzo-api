<template>
  <div class="space-y-6">
    <div class="card p-6">
      <div class="mb-4 flex items-center justify-between">
        <div><h2 class="text-lg font-semibold text-gray-900 dark:text-white">影视风格模板库</h2><p class="mt-1 text-sm text-gray-500">每个模板锁定一张样片和一份 canonical 风格提示词。</p></div>
        <button class="btn btn-primary" :disabled="saving" @click="submit">{{ editing ? '保存修改' : '上传模板' }}</button>
      </div>
      <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
        <select v-model="form.category" class="input"><option value="realistic">写实</option><option value="3d">3D</option><option value="2d">2D</option></select>
        <input v-model="form.name" class="input" placeholder="风格名称" />
        <input ref="fileInput" type="file" accept="image/jpeg,image/png,image/webp,image/gif" class="input" />
      </div>
      <textarea v-model="form.prompt" class="input mt-3 min-h-32 w-full" placeholder="完整风格提示词（色彩、色调、光影、镜头、虚化、材质和氛围）" />
      <p v-if="error" class="mt-2 text-sm text-red-600">{{ error }}</p>
    </div>
    <div class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
      <article v-for="item in items" :key="item.id" class="card overflow-hidden">
        <img :src="previewUrl(item.id)" class="h-40 w-full object-cover" @error="loadPreview(item.id)" />
        <div class="p-4"><div class="flex items-center justify-between"><h3 class="font-semibold">{{ item.name }}</h3><span class="text-xs text-gray-500">{{ label(item.category) }}</span></div><p class="mt-2 line-clamp-4 whitespace-pre-wrap text-xs text-gray-500">{{ item.prompt }}</p><div class="mt-3 flex gap-2"><button class="btn btn-secondary btn-sm" @click="edit(item)">编辑</button><button class="btn btn-secondary btn-sm" @click="archiveItem(item)">下架</button></div></div>
      </article>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { filmStylesAdminApi, type FilmStyleTemplate } from '@/api/admin/filmStyles'
import { apiClient } from '@/api/client'

const items = ref<FilmStyleTemplate[]>([])
const saving = ref(false)
const error = ref('')
const editing = ref<FilmStyleTemplate | null>(null)
const fileInput = ref<HTMLInputElement>()
const previews = reactive<Record<string, string>>({})
const form = reactive({ category: 'realistic', name: '', prompt: '' })
const load = async () => { items.value = await filmStylesAdminApi.list(); await Promise.all(items.value.map(item => loadPreview(item.id))) }
const loadPreview = async (id: string) => { const response = await apiClient.get(`/admin/film-style-templates/${id}/preview`, { responseType: 'blob' }); previews[id] = URL.createObjectURL(response.data) }
const previewUrl = (id: string) => previews[id] || ''
const label = (category: string) => ({ realistic: '写实', '3d': '3D', '2d': '2D' }[category] || category)
const edit = (item: FilmStyleTemplate) => { editing.value = item; form.category = item.category; form.name = item.name; form.prompt = item.prompt }
const submit = async () => { const file = fileInput.value?.files?.[0]; saving.value = true; error.value = ''; try { if (editing.value) await filmStylesAdminApi.update(editing.value.id, { ...form, preview: file }); else { if (!file) throw new Error('请选择样片'); await filmStylesAdminApi.create({ ...form, preview: file }) }; editing.value = null; form.name = ''; form.prompt = ''; await load() } catch (e) { error.value = e instanceof Error ? e.message : '保存失败' } finally { saving.value = false } }
const archiveItem = async (item: FilmStyleTemplate) => { if (!window.confirm(`确认下架「${item.name}」？`)) return; await filmStylesAdminApi.archive(item.id); await load() }
onMounted(() => void load())
</script>
