<template>
  <div class="space-y-6" data-testid="yingzo-agent-view">
    <!-- 分组概览 -->
    <div class="card p-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div class="flex items-center gap-2">
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.yingzoAgent.title') }}
            </h3>
            <span
              class="rounded bg-primary-50 px-2 py-0.5 text-xs font-medium text-primary-600 dark:bg-primary-900/30 dark:text-primary-300"
              data-testid="yingzo-agent-system-badge"
            >
              {{ t('admin.yingzoAgent.systemBadge') }}
            </span>
          </div>
          <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.yingzoAgent.description') }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="loading || syncing"
            data-testid="yingzo-agent-sync"
            @click="syncModels"
          >
            {{ syncing ? t('admin.yingzoAgent.syncing') : t('admin.yingzoAgent.sync') }}
          </button>
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="loading"
            data-testid="yingzo-agent-refresh"
            @click="loadCatalog"
          >
            {{ loading ? t('common.loading') : t('common.refresh') }}
          </button>
        </div>
      </div>

      <div v-if="group" class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.yingzoAgent.summary.group') }}
          </div>
          <div class="mt-1 text-sm font-semibold text-gray-900 dark:text-white">
            {{ group.name }}
          </div>
        </div>
        <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.yingzoAgent.summary.status') }}
          </div>
          <div class="mt-1 text-sm font-semibold text-gray-900 dark:text-white">
            {{ group.status === 'active' ? t('common.active') : t('common.inactive') }}
          </div>
        </div>
        <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.yingzoAgent.summary.accounts') }}
          </div>
          <div
            class="mt-1 text-sm font-semibold text-gray-900 dark:text-white"
            data-testid="yingzo-agent-account-count"
          >
            {{ group.active_account_count ?? 0 }} / {{ group.account_count ?? 0 }}
          </div>
        </div>
        <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.yingzoAgent.summary.models') }}
          </div>
          <div
            class="mt-1 text-sm font-semibold text-gray-900 dark:text-white"
            data-testid="yingzo-agent-model-count"
          >
            {{ enabledCount }} / {{ models.length }}
          </div>
        </div>
      </div>

      <div class="mt-4 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
        <div class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.yingzoAgent.summary.aggregation') }}
        </div>
        <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
          {{ t('admin.yingzoAgent.aggregationHint') }}
        </p>
        <router-link
          class="mt-2 inline-block text-sm text-primary-600 hover:underline dark:text-primary-400"
          :to="{ path: '/admin/accounts' }"
          data-testid="yingzo-agent-accounts-link"
        >
          {{ t('admin.yingzoAgent.manageAccounts') }}
        </router-link>
      </div>

      <p
        v-if="errorMessage"
        class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-600 dark:bg-red-900/20 dark:text-red-400"
        data-testid="yingzo-agent-error"
      >
        {{ errorMessage }}
      </p>
      <p
        v-if="successMessage"
        class="mt-4 rounded-lg bg-green-50 p-3 text-sm text-green-600 dark:bg-green-900/20 dark:text-green-400"
        data-testid="yingzo-agent-success"
      >
        {{ successMessage }}
      </p>
    </div>

    <!-- 按媒体类型分区 -->
    <div class="card p-6">
      <div class="flex flex-wrap items-center gap-2 border-b border-gray-200 dark:border-dark-600">
        <button
          v-for="tab in tabs"
          :key="tab.key"
          type="button"
          class="px-4 py-2 text-sm font-medium"
          :class="
            activeTab === tab.key
              ? 'border-b-2 border-primary-500 text-primary-600 dark:text-primary-400'
              : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'
          "
          :data-testid="`yingzo-agent-tab-${tab.key}`"
          @click="activeTab = tab.key"
        >
          {{ t(tab.labelKey) }} ({{ modelsByTab[tab.key].length }})
        </button>
        <div class="ml-auto flex items-center gap-2 pb-2">
          <button
            type="button"
            class="btn btn-primary btn-sm"
            :disabled="!dirty || saving"
            data-testid="yingzo-agent-save"
            @click="saveChanges"
          >
            {{ saving ? t('common.saving') : t('admin.yingzoAgent.saveChanges') }}
          </button>
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="!dirty || saving"
            data-testid="yingzo-agent-reset"
            @click="loadCatalog"
          >
            {{ t('admin.yingzoAgent.reset') }}
          </button>
        </div>
      </div>

      <p class="mt-3 text-xs text-gray-500 dark:text-gray-400">
        {{ t(`admin.yingzoAgent.tabs.${activeTab}.hint`) }}
      </p>

      <div v-if="!modelsByTab[activeTab].length" class="py-10 text-center text-sm text-gray-400">
        {{ t('admin.yingzoAgent.empty') }}
      </div>

      <div v-else class="mt-4 overflow-x-auto">
        <table class="min-w-full text-sm">
          <thead>
            <tr class="border-b border-gray-200 text-left text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">
              <th class="py-2 pr-3">{{ t('admin.yingzoAgent.columns.model') }}</th>
              <th class="py-2 pr-3">{{ t('admin.yingzoAgent.columns.platform') }}</th>
              <th class="py-2 pr-3">{{ t('admin.yingzoAgent.columns.mediaType') }}</th>
              <th class="py-2 pr-3">{{ t('admin.yingzoAgent.columns.source') }}</th>
              <th class="py-2 pr-3">{{ t('admin.yingzoAgent.columns.enabled') }}</th>
              <template v-if="activeTab === 'text'">
                <th class="py-2 pr-3">{{ t('admin.yingzoAgent.columns.rateMultiplier') }}</th>
              </template>
              <template v-else>
                <th
                  v-for="resolution in resolutionColumns"
                  :key="resolution"
                  class="py-2 pr-3"
                >
                  {{
                    activeTab === 'image'
                      ? t('admin.yingzoAgent.columns.pricePerImage', { resolution })
                      : t('admin.yingzoAgent.columns.pricePerSecond', { resolution })
                  }}
                </th>
              </template>
              <th class="py-2 pr-3 text-right">{{ t('admin.yingzoAgent.columns.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="model in modelsByTab[activeTab]"
              :key="model.id"
              class="border-b border-gray-100 last:border-0 dark:border-dark-700"
              :data-testid="`yingzo-agent-row-${model.id}`"
            >
              <td class="py-2 pr-3 font-medium text-gray-900 dark:text-white">
                {{ model.model_code }}
              </td>
              <td class="py-2 pr-3 text-gray-500 dark:text-gray-400">{{ model.platform }}</td>
              <td class="py-2 pr-3">
                <select
                  v-model="drafts[model.id].mediaType"
                  class="input w-24"
                  :data-testid="`yingzo-agent-media-type-${model.id}`"
                >
                  <option value="text">{{ t('admin.yingzoAgent.mediaType.text') }}</option>
                  <option value="image">{{ t('admin.yingzoAgent.mediaType.image') }}</option>
                  <option value="video">{{ t('admin.yingzoAgent.mediaType.video') }}</option>
                </select>
              </td>
              <td class="py-2 pr-3">
                <span
                  v-if="model.available"
                  class="rounded bg-green-50 px-2 py-0.5 text-xs text-green-600 dark:bg-green-900/20 dark:text-green-400"
                >
                  {{ t('admin.yingzoAgent.source.available') }}
                </span>
                <span
                  v-else
                  class="rounded bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-400"
                  :title="t('admin.yingzoAgent.sourceUnavailableHint')"
                >
                  {{ t('admin.yingzoAgent.source.unavailable') }}
                </span>
              </td>
              <td class="py-2 pr-3">
                <input
                  v-model="drafts[model.id].enabled"
                  type="checkbox"
                  :data-testid="`yingzo-agent-enabled-${model.id}`"
                />
              </td>
              <template v-if="drafts[model.id].mediaType === 'text'">
                <td class="py-2 pr-3">
                  <input
                    v-model="drafts[model.id].rateMultiplier"
                    class="input w-28"
                    type="number"
                    min="0"
                    step="0.01"
                    :placeholder="t('admin.yingzoAgent.ratePlaceholder')"
                    :data-testid="`yingzo-agent-rate-${model.id}`"
                  />
                </td>
              </template>
              <template v-else>
                <td
                  v-for="resolution in resolutionColumnsForDraft(model)"
                  :key="resolution"
                  class="py-2 pr-3"
                >
                  <input
                    v-model="drafts[model.id].prices[resolution]"
                    class="input w-24"
                    type="number"
                    min="0"
                    step="0.001"
                    :placeholder="t('admin.yingzoAgent.pricePlaceholder')"
                    :data-testid="`yingzo-agent-price-${model.id}-${resolution}`"
                  />
                </td>
              </template>
              <td class="py-2 pr-3 text-right">
                <button
                  type="button"
                  class="text-xs text-red-500 hover:underline"
                  :data-testid="`yingzo-agent-delete-${model.id}`"
                  @click="removeModel(model)"
                >
                  {{ t('common.delete') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import {
  agentModelsAPI,
  type AgentGroupModel,
  type AgentMediaType,
  type AgentModelPrice,
} from '@/api/admin/agentModels'
import { VIDEO_MODEL_RESOLUTIONS } from '@/views/admin/videoModelResolutions'
import type { AdminGroup } from '@/types'

const { t } = useI18n()

const IMAGE_RESOLUTIONS = ['1K', '2K', '4K']

interface ModelDraft {
  enabled: boolean
  /** 草稿里的媒体类型：目录识别不准时管理员可以在这里改，保存后立即生效。 */
  mediaType: AgentMediaType
  rateMultiplier: string
  prices: Record<string, string>
}

const loading = ref(false)
const syncing = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const successMessage = ref('')
const activeTab = ref<AgentMediaType>('text')
const groups = ref<AdminGroup[]>([])
const models = ref<AgentGroupModel[]>([])
const drafts = reactive<Record<number, ModelDraft>>({})

const tabs: { key: AgentMediaType; labelKey: string }[] = [
  { key: 'text', labelKey: 'admin.yingzoAgent.tabs.text.title' },
  { key: 'image', labelKey: 'admin.yingzoAgent.tabs.image.title' },
  { key: 'video', labelKey: 'admin.yingzoAgent.tabs.video.title' },
]

/** 系统内置聚合分组：由后端 kind/system_code 标识，管理员不能删除。 */
const agentGroup = computed(
  () => groups.value.find((group) => group.kind === 'agent' && group.system_code === 'yingzo') ?? null,
)
const group = agentGroup
const agentGroupId = computed(() => agentGroup.value?.id ?? 0)

const modelsByTab = computed<Record<AgentMediaType, AgentGroupModel[]>>(() => {
  const result: Record<AgentMediaType, AgentGroupModel[]> = { text: [], image: [], video: [] }
  for (const model of models.value) {
    result[model.media_type]?.push(model)
  }
  return result
})

const enabledCount = computed(
  () => models.value.filter((model) => drafts[model.id]?.enabled).length,
)

/** 图片固定 1K/2K/4K；视频按该模型官方支持的档位，避免让管理员配出永远用不上的价。 */
const resolutionColumns = computed(() => {
  if (activeTab.value === 'image') {
    return IMAGE_RESOLUTIONS
  }
  const union = new Set<string>()
  for (const model of modelsByTab.value.video) {
    for (const resolution of resolutionColumnsForType('video', model.model_code)) {
      union.add(resolution)
    }
  }
  return [...union]
})

function resolutionColumnsForDraft(model: AgentGroupModel): string[] {
  return resolutionColumnsForType(drafts[model.id]?.mediaType ?? model.media_type, model.model_code)
}

function resolutionColumnsForType(mediaType: AgentMediaType, modelCode: string): string[] {
  if (mediaType === 'image') {
    return IMAGE_RESOLUTIONS
  }
  const spec = VIDEO_MODEL_RESOLUTIONS.find((entry) => entry.model === modelCode)
  // 账号 model_mapping 里自定义的视频模型不在官方清单里，回退到通用档位，
  // 否则这类模型永远配不出价格（启用后必然在请求时失败）。
  return spec ? spec.resolutions : ['480p', '720p', '1080p']
}

const dirty = computed(() => models.value.some((model) => isDirty(model)))

function isDirty(model: AgentGroupModel): boolean {
  const draft = drafts[model.id]
  if (!draft) {
    return false
  }
  if (draft.enabled !== model.enabled || draft.mediaType !== model.media_type) {
    return true
  }
  if (draft.mediaType === 'text') {
    return normalizeNumber(draft.rateMultiplier) !== (model.rate_multiplier ?? null)
  }
  const stored = new Map(model.prices.map((price) => [price.resolution, price.unit_price]))
  for (const resolution of Object.keys(draft.prices)) {
    if (normalizeNumber(draft.prices[resolution]) !== (stored.get(resolution) ?? null)) {
      return true
    }
  }
  for (const [resolution, price] of stored) {
    if (normalizeNumber(draft.prices[resolution] ?? '') !== price) {
      return true
    }
  }
  return false
}

function normalizeNumber(raw: string | number | null | undefined): number | null {
  if (raw === null || raw === undefined) {
    return null
  }
  const text = String(raw).trim()
  if (text === '') {
    return null
  }
  const value = Number(text)
  return Number.isFinite(value) ? value : null
}

function buildDrafts(): void {
  for (const key of Object.keys(drafts)) {
    delete drafts[Number(key)]
  }
  for (const model of models.value) {
    const prices: Record<string, string> = {}
    for (const resolution of resolutionColumnsForType(model.media_type, model.model_code)) {
      prices[resolution] = ''
    }
    for (const price of model.prices) {
      prices[price.resolution] = String(price.unit_price)
    }
    drafts[model.id] = {
      enabled: model.enabled,
      mediaType: model.media_type,
      rateMultiplier: model.rate_multiplier === null ? '' : String(model.rate_multiplier),
      prices,
    }
  }
}

async function loadGroup(): Promise<void> {
  const all = await adminAPI.groups.getAll()
  groups.value = all
  if (!agentGroup.value) {
    // 系统分组由迁移种入。真找不到时要说清楚，否则页面只会显示一个空目录，
    // 让人误以为"还没有模型"而不是"分组不存在"。
    errorMessage.value = t('admin.yingzoAgent.groupMissing')
  }
}

async function loadCatalog(): Promise<void> {
  if (!agentGroupId.value) {
    return
  }
  loading.value = true
  errorMessage.value = ''
  try {
    const config = await agentModelsAPI.getAgentModels(agentGroupId.value)
    models.value = config.models
    buildDrafts()
  } catch (error) {
    errorMessage.value = extractError(error, t('admin.yingzoAgent.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function syncModels(): Promise<void> {
  if (!agentGroupId.value) {
    return
  }
  syncing.value = true
  errorMessage.value = ''
  successMessage.value = ''
  try {
    const config = await agentModelsAPI.syncAgentModels(agentGroupId.value)
    models.value = config.models
    buildDrafts()
    await loadGroup()
    successMessage.value = t('admin.yingzoAgent.syncSuccess', { count: config.models.length })
  } catch (error) {
    errorMessage.value = extractError(error, t('admin.yingzoAgent.syncFailed'))
  } finally {
    syncing.value = false
  }
}

function buildPrices(model: AgentGroupModel): AgentModelPrice[] {
  const draft = drafts[model.id]
  const prices: AgentModelPrice[] = []
  for (const resolution of resolutionColumnsForDraft(model)) {
    const value = normalizeNumber(draft.prices[resolution])
    if (value === null) {
      continue
    }
    prices.push({ resolution, unit_price: value })
  }
  return prices
}

async function saveChanges(): Promise<void> {
  if (!agentGroupId.value || !dirty.value) {
    return
  }
  saving.value = true
  errorMessage.value = ''
  successMessage.value = ''
  try {
    let config = { models: models.value }
    for (const model of models.value) {
      if (!isDirty(model)) {
        continue
      }
      const draft = drafts[model.id]
      // 提交草稿里的媒体类型：管理员在页面上改过类型时，这一次保存就把它落库。
      const payload =
        draft.mediaType === 'text'
          ? {
              media_type: draft.mediaType,
              enabled: draft.enabled,
              rate_multiplier: normalizeNumber(draft.rateMultiplier),
            }
          : {
              media_type: draft.mediaType,
              enabled: draft.enabled,
              prices: buildPrices(model),
            }
      config = await agentModelsAPI.updateAgentModel(agentGroupId.value, model.id, payload)
    }
    models.value = config.models
    buildDrafts()
    successMessage.value = t('admin.yingzoAgent.saveSuccess')
  } catch (error) {
    errorMessage.value = extractError(error, t('admin.yingzoAgent.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function removeModel(model: AgentGroupModel): Promise<void> {
  if (!agentGroupId.value) {
    return
  }
  errorMessage.value = ''
  successMessage.value = ''
  try {
    await agentModelsAPI.deleteAgentModel(agentGroupId.value, model.id)
    await loadCatalog()
    successMessage.value = t('admin.yingzoAgent.deleteSuccess', { model: model.model_code })
  } catch (error) {
    errorMessage.value = extractError(error, t('admin.yingzoAgent.deleteFailed'))
  }
}

// apiClient 在业务错误时 reject 的是扁平对象 {status, code, message, reason}，
// 只有网络层错误才带 response.data，两种形状都要能取到后端的真实原因。
function extractError(error: unknown, fallback: string): string {
  const err = error as {
    message?: string
    response?: { data?: { message?: string; error?: { message?: string } } }
  }
  return (
    err?.message ??
    err?.response?.data?.message ??
    err?.response?.data?.error?.message ??
    fallback
  )
}

onMounted(async () => {
  try {
    await loadGroup()
  } catch (error) {
    errorMessage.value = extractError(error, t('admin.yingzoAgent.loadFailed'))
    return
  }
  await loadCatalog()
})
</script>
