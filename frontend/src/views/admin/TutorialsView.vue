<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="text-xl font-semibold">教程管理</h2><p class="mt-1 text-sm text-gray-500">管理一级分类、二级教程和飞书正文地址，按列表顺序展示。</p></div>
        <div class="flex gap-2">
          <a href="/tutorials" target="_blank" rel="noopener noreferrer" class="btn btn-secondary">查看教程 ↗</a>
          <button class="btn btn-primary" :disabled="loading || saving || !loaded" @click="save">{{ saving ? '保存中…' : '保存配置' }}</button>
        </div>
      </div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
      <p v-if="message" role="status" class="text-sm text-green-600">{{ message }}</p>
      <div v-if="loading" role="status" class="py-16 text-center text-gray-500">正在加载教程配置…</div>
      <div v-else-if="!loaded" class="py-12 text-center"><button class="btn btn-secondary" @click="load">重新加载</button></div>
      <template v-else>
        <section class="card space-y-5 p-6">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <label class="flex items-center gap-3 font-medium"><input v-model="config.enabled" type="checkbox" class="h-4 w-4" :disabled="saving" />开启教程板块</label>
            <span class="text-sm text-gray-500">{{ dirty ? '有未保存的修改' : '配置已同步' }}</span>
          </div>
          <p class="text-sm text-gray-500">关闭并保存后，前端隐藏教程导航和内容，直接访问教程地址将返回首页。已配置的目录和正文地址会保留。</p>
          <fieldset :disabled="saving" class="space-y-4">
            <legend class="mb-3 text-sm font-medium">正文展示方式</legend>
            <label class="flex items-start gap-3"><input v-model="config.mode" type="radio" value="iframe" class="mt-1"/><span><strong class="text-sm">直接嵌入</strong><span class="mt-1 block text-xs leading-5 text-gray-500">仅需飞书文档地址，保留飞书导航与样式。请将文档设置为允许持有链接的人阅读，并验证外部访问权限。</span></span></label>
            <label class="flex items-start gap-3"><input v-model="config.mode" type="radio" value="sdk" class="mt-1"/><span><strong class="text-sm">飞书云文档组件</strong><span class="mt-1 block text-xs leading-5 text-gray-500">隐藏飞书顶部栏、自带目录和评论，统一阅读背景。需要企业自建应用及文档访问授权；应用身份访问受飞书 100 次/分钟限制。</span></span></label>
          </fieldset>
          <div v-if="config.mode === 'sdk'" class="space-y-4 border-t border-gray-200 pt-5 dark:border-dark-700">
            <p class="text-sm leading-6 text-gray-500">创建仅供教程使用的飞书自建应用，开通云文档权限并将教程文档授权给该应用。服务器仅交换鉴权数据，正文和媒体由读者浏览器直连飞书加载。</p>
            <div class="grid gap-4 md:grid-cols-2">
              <label class="space-y-2 text-sm"><span>App ID</span><input v-model="config.app_id" class="input" placeholder="cli_…" autocomplete="off" :disabled="saving"/></label>
              <label class="space-y-2 text-sm"><span>App Secret</span><input v-model="config.app_secret" class="input" type="password" :placeholder="config.secret_configured ? '已配置，留空保留现有密钥' : '输入应用密钥'" autocomplete="new-password" :disabled="saving"/></label>
            </div>
            <label class="block space-y-2 text-sm"><span>允许嵌入的阅读站点（每行一个域名，包含 https://）</span><textarea v-model="origins" class="input min-h-24" placeholder="https://yingzo.art" :disabled="saving"/><span class="block text-xs text-gray-500">只允许这些站点的 /tutorials 页面申请签名。本地调试可填 http://127.0.0.1:5173。</span></label>
            <a class="text-sm text-primary-600 underline" href="https://open.feishu.cn/document/common-capabilities/web-components/uYDO3YjL2gzN24iN3cjN/introduction" target="_blank" rel="noopener noreferrer">飞书官方接入说明 ↗</a>
          </div>
        </section>
        <section class="space-y-4">
          <div class="flex items-center justify-between"><h3 class="font-semibold">教程目录 <span class="ml-2 text-sm font-normal text-gray-500">{{ config.categories.length }} 个分类 · {{ articleCount }} 篇教程</span></h3><button class="btn btn-secondary" :disabled="saving" @click="addCategory">添加一级分类 +</button></div>
          <div v-if="!config.categories.length" class="card py-16 text-center text-gray-500"><p>尚未添加教程</p><p class="mt-2 text-sm">先创建一级分类，再添加二级教程和正文地址。</p></div>
          <fieldset v-for="(category, ci) in config.categories" :key="category.id" class="card space-y-5 p-5" :disabled="saving">
            <div class="flex flex-wrap items-center gap-3">
              <span class="text-sm text-gray-400">{{ String(ci + 1).padStart(2, '0') }}</span>
              <input v-model="category.title" class="input min-w-32 flex-1 font-medium" placeholder="一级分类名称" maxlength="100" :aria-label="`第 ${ci + 1} 个分类名称`"/>
              <label class="flex items-center gap-2 text-sm"><input v-model="category.enabled" type="checkbox"/>显示</label>
              <button class="btn btn-secondary px-3" :disabled="ci === 0" aria-label="上移分类" @click="move(config.categories, ci, -1)">↑</button>
              <button class="btn btn-secondary px-3" :disabled="ci === config.categories.length - 1" aria-label="下移分类" @click="move(config.categories, ci, 1)">↓</button>
              <button class="text-sm text-red-600" @click="removeCategory(ci)">删除分类</button>
            </div>
            <div v-for="(article, ai) in category.articles" :key="article.id" class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700">
              <div class="flex flex-wrap items-center gap-3">
                <input v-model="article.title" class="input min-w-32 flex-1" placeholder="二级教程标题" maxlength="100" :aria-label="`分类 ${ci + 1} 的第 ${ai + 1} 篇教程标题`"/>
                <label class="flex items-center gap-2 text-sm"><input v-model="article.enabled" type="checkbox"/>显示</label>
                <button class="btn btn-secondary px-3" :disabled="ai === 0" aria-label="上移教程" @click="move(category.articles, ai, -1)">↑</button>
                <button class="btn btn-secondary px-3" :disabled="ai === category.articles.length - 1" aria-label="下移教程" @click="move(category.articles, ai, 1)">↓</button>
                <button class="text-sm text-red-600" @click="removeArticle(category, ai)">删除</button>
              </div>
              <label class="block space-y-2 text-xs text-gray-500"><span>飞书正文地址</span><input v-model="article.url" type="url" class="input text-sm" placeholder="https://my.feishu.cn/docx/…" :aria-label="`${article.title || '教程'}正文地址`"/></label>
              <a v-if="validURL(article.url)" :href="article.url" target="_blank" rel="noopener noreferrer" class="inline-block text-xs text-primary-600 underline">检查原文 ↗</a>
            </div>
            <button class="btn btn-secondary" @click="addArticle(category)">添加二级教程 +</button>
          </fieldset>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import { getTutorialConfig, saveTutorialConfig, type TutorialConfig, type TutorialCategory } from '@/api/admin/tutorials'

const config = ref<TutorialConfig>({ enabled: false, mode: 'iframe', categories: [], app_id: '', app_secret: '', allowed_origins: [] })
const origins = ref('')
const loading = ref(true), loaded = ref(false), saving = ref(false)
const error = ref(''), message = ref(''), snapshot = ref('')
const serialized = computed(() => JSON.stringify({ ...config.value, allowed_origins: origins.value.split('\n').map(o => o.trim()).filter(Boolean) }))
const dirty = computed(() => loaded.value && serialized.value !== snapshot.value)
const articleCount = computed(() => config.value.categories.reduce((sum, c) => sum + c.articles.length, 0))
function adopt(value: TutorialConfig) {
  config.value = { ...value, app_secret: '' }
  origins.value = (value.allowed_origins || []).join('\n')
  snapshot.value = serialized.value
}
async function load() {
  loading.value = true; error.value = ''
  try { adopt(await getTutorialConfig()); loaded.value = true }
  catch { error.value = '无法加载教程配置，请重试。' }
  finally { loading.value = false }
}
function validURL(raw: string) {
  try {
    const url = new URL(raw)
    return url.protocol === 'https:' && !url.username && !url.password && !url.port && /\.(feishu\.cn|larksuite\.com)$/.test(url.hostname) && /^\/(docx|docs|wiki)\/[a-zA-Z0-9_-]{1,80}\/?$/.test(url.pathname)
  } catch { return false }
}
async function save() {
  error.value = ''; message.value = ''
  for (const category of config.value.categories) {
    if (!category.title.trim()) { error.value = '请输入一级分类名称。'; return }
    for (const article of category.articles) {
      if (!article.title.trim() || !validURL(article.url.trim())) { error.value = '请填写教程标题和有效的 HTTPS 飞书正文地址。'; return }
    }
  }
  saving.value = true
  try { adopt(await saveTutorialConfig(JSON.parse(serialized.value))); message.value = '教程配置已保存。' }
  catch (err: unknown) { error.value = (err as { response?: { data?: { message?: string } } })?.response?.data?.message || '保存失败，请重试。' }
  finally { saving.value = false }
}
function addCategory() { config.value.categories.push({ id: crypto.randomUUID(), title: '', enabled: true, articles: [] }); message.value = '' }
function addArticle(category: TutorialCategory) { category.articles.push({ id: crypto.randomUUID(), title: '', url: '', enabled: true }); message.value = '' }
function move<T>(items: T[], index: number, delta: number) {
  const target = index + delta
  if (target < 0 || target >= items.length) return
  const [item] = items.splice(index, 1)
  items.splice(target, 0, item!)
}
function removeCategory(index: number) { if (confirm('删除该分类及其下所有教程？保存后生效。')) config.value.categories.splice(index, 1) }
function removeArticle(category: TutorialCategory, index: number) { if (confirm('删除这篇教程？保存后生效。')) category.articles.splice(index, 1) }
function beforeUnload(event: BeforeUnloadEvent) { if (dirty.value) { event.preventDefault(); event.returnValue = '' } }
onBeforeRouteLeave(() => !dirty.value || confirm('教程配置尚未保存，确定离开？'))
onMounted(() => { load(); window.addEventListener('beforeunload', beforeUnload) })
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
</script>
