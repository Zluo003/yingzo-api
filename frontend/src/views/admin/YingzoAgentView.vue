<template>
  <AppLayout>
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

    <VideoChannelManager @changed="refreshChannelMetadata" />
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
        {{ activeTab === 'audio' ? t('suno.billingHint') : t(`admin.yingzoAgent.tabs.${activeTab}.hint`) }}
      </p>

      <p v-if="activeTab === 'video' && modelsByTab.video.some(hasInputImagePricing)" class="mt-2 text-xs text-primary-600">{{ t('admin.yingzoAgent.inputImagePricingHint') }}</p>
      <p v-if="activeTab === 'image' && modelsByTab.image.some(m => isSeedreamModel(m.model_code))" class="mt-2 text-xs text-primary-600">{{ t('seedream.billingHint') }}</p>
      <p v-if="activeTab === 'image'  && modelsByTab.image.some(m => m.model_code === MIDJOURNEY_MODEL)" class="mt-2 text-xs text-primary-600">{{ t('midjourney.billingHint') }}</p>
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
                    activeTab === 'audio' ? t(`suno.prices.${resolution}`) : activeTab === 'image'
                      ? (MIDJOURNEY_PRICE_TIERS.includes(resolution) ? t(`midjourney.prices.${resolution}`) : t('admin.yingzoAgent.columns.pricePerImage', { resolution }))
                      : resolution
                  }}
                </th>
              </template>
              <template v-if="activeTab === 'video' || activeTab === 'image'">
                <th class="py-2 pr-3">{{ t('admin.yingzoAgent.freeInputImages') }}</th>
                <th class="py-2 pr-3">{{ t('admin.yingzoAgent.extraImages') }}</th>
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
                <p v-if="isSeedreamModel(model.model_code)" class="mt-1 max-w-xs text-xs font-normal text-gray-500 dark:text-gray-400" :data-testid="`yingzo-agent-layer-billing-${model.id}`">
                  {{ t('seedream.layerBillingHint') }}
                </p>
              </td>
              <td class="py-2 pr-3 text-gray-500 dark:text-gray-400">{{ model.platform }}</td>
              <td class="py-2 pr-3">
                <select
                  v-model="drafts[model.id].mediaType"
                  class="input w-24"
                  :data-testid="`yingzo-agent-media-type-${model.id}`"
                  :disabled="model.model_code === MIDJOURNEY_MODEL || model.model_code === 'suno-v6'"
                >
                  <option value="text">{{ t('admin.yingzoAgent.mediaType.text') }}</option>
                  <option value="image">{{ t('admin.yingzoAgent.mediaType.image') }}</option>
                  <option value="audio">{{ t('suno.audio') }}</option>
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
              <td v-else-if="isSeedance(model)" :colspan="resolutionColumns.length" class="py-3 pr-3">
                <SeedanceChannelPrices v-model="drafts[model.id].channelPrices" :channels="model.video_channels ?? [{id: '', name: t('videoChannels.default'), enabled:true, available:model.available}]" :resolutions="resolutionColumnsForDraft(model)" />
              </td>
              <template v-else>
                <td
                  v-for="resolution in (activeTab === 'text' ? resolutionColumnsForDraft(model) : resolutionColumns)"
                  :key="resolution"
                  class="py-2 pr-3"
                >
                  <div v-if="resolutionColumnsForDraft(model).includes(resolution)" class="flex items-center gap-2">
                    <input
                      v-model="drafts[model.id].resolutionEnabled[resolution]"
                      type="checkbox"
                      :aria-label="`${resolution} ${t('admin.yingzoAgent.columns.enabled')}`"
                      :data-testid="`yingzo-agent-resolution-enabled-${model.id}-${resolution}`"
                    />
                    <input
                      v-model="drafts[model.id].prices[resolution]"
                      class="input w-24"
                      type="number"
                      min="0"
                      step="0.001"
                      :placeholder="t('admin.yingzoAgent.pricePlaceholder')"
                      :data-testid="`yingzo-agent-price-${model.id}-${resolution}`"
                    />
                  </div>
                  <span v-else class="text-gray-400">—</span>
                </td>
              </template>
              <template v-if="activeTab === 'video' || activeTab === 'image'">
                <td class="py-2 pr-3">
                  <input v-if="hasInputImagePricing(model)" v-model="drafts[model.id].freeInputImages" class="input w-24" type="number" min="0" step="1" :placeholder="String(defaultFreeImages(model))" :aria-label="t('admin.yingzoAgent.freeInputImages')" :data-testid="'yingzo-agent-free-images-' + model.id" />
                  <span v-else class="text-gray-400">—</span>
                </td>
                <td class="py-2 pr-3">
                  <input v-if="hasInputImagePricing(model)" v-model="drafts[model.id].inputImageUnitPrice" class="input w-24" type="number" min="0" step="0.001" :placeholder="isSeedreamModel(model.model_code) ? '0' : t('admin.yingzoAgent.inputImagePricePlaceholder')" :aria-label="t('admin.yingzoAgent.inputImageUnitPrice')" :data-testid="'yingzo-agent-input-image-price-' + model.id" />
                  <span v-else class="text-gray-400">—</span>
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
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import VideoChannelManager from '@/components/admin/VideoChannelManager.vue'
import SeedanceChannelPrices from '@/components/admin/SeedanceChannelPrices.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { adminAPI } from '@/api'
import {
  agentModelsAPI,
  type AgentGroupModel,
  type AgentMediaType,
  type AgentModelPrice,
} from '@/api/admin/agentModels'
import { isSeedreamModel } from '@/components/account/seedream'
import { MIDJOURNEY_MODEL, MIDJOURNEY_PRICE_TIERS } from '@/components/account/midjourney'
import { VIDEO_MODEL_RESOLUTIONS } from '@/views/admin/videoModelResolutions'
import type { AdminGroup } from '@/types'

const { t } = useI18n()

import { SUNO_BILLING_TIERS } from '@/components/account/suno'
const IMAGE_RESOLUTIONS = ['1K', '2K', '4K']

interface ModelDraft {
  channelPrices: AgentModelPrice[]
  enabled: boolean
  /** 草稿里的媒体类型：目录识别不准时管理员可以在这里改，保存后立即生效。 */
  mediaType: AgentMediaType
  rateMultiplier: string
  prices: Record<string, string>
  /** 分辨率独立开关；未出现在接口响应里的档位默认关闭。 */
  resolutionEnabled: Record<string, boolean>
  freeInputImages: string
  inputImageUnitPrice: string
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
  { key: 'audio', labelKey: 'suno.billingTitle' },
]

/** 系统内置聚合分组：由后端 kind/system_code 标识，管理员不能删除。 */
const agentGroup = computed(
  () => groups.value.find((group) => group.kind === 'agent' && group.system_code === 'yingzo') ?? null,
)
const group = agentGroup
const agentGroupId = computed(() => agentGroup.value?.id ?? 0)

const modelsByTab = computed<Record<AgentMediaType, AgentGroupModel[]>>(() => {
  const result: Record<AgentMediaType, AgentGroupModel[]> = { text: [], image: [], video: [], audio: [] }
  for (const model of models.value) {
    result[model.media_type]?.push(model)
  }
  return result
})

const enabledCount = computed(
  () => models.value.filter((model) => drafts[model.id]?.enabled).length,
)

/** 按当前模型的计费档位生成表头，Seedream 和 Midjourney 使用各自的档位。 */
const resolutionColumns = computed(() => {
  const union = new Set<string>()
  for (const model of modelsByTab.value[activeTab.value]) {
    const mediaType = drafts[model.id]?.mediaType ?? model.media_type
    if (mediaType === 'text') continue
    for (const resolution of resolutionColumnsForType(mediaType, model.model_code)) union.add(resolution)
  }
  return [...union]
})

function resolutionColumnsForDraft(model: AgentGroupModel): string[] {
  return resolutionColumnsForType(drafts[model.id]?.mediaType ?? model.media_type, model.model_code)
}

function resolutionColumnsForType(mediaType: AgentMediaType, modelCode: string): string[] {
  if (mediaType === 'audio') return SUNO_BILLING_TIERS
  if (mediaType === 'image') {
    if (modelCode === MIDJOURNEY_MODEL) return MIDJOURNEY_PRICE_TIERS
    if (isSeedreamModel(modelCode)) return ['1.5K', '2K']
    return IMAGE_RESOLUTIONS
  }
  const spec = VIDEO_MODEL_RESOLUTIONS.find((entry) => entry.model === modelCode)
  // 账号 model_mapping 里自定义的视频模型不在官方清单里，回退到通用档位，
  // 否则这类模型永远配不出价格（启用后必然在请求时失败）。
  return spec ? spec.resolutions : ['480p', '720p', '1080p']
}

const dirty = computed(() => models.value.some((model) => isDirty(model)))

function hasInputImagePricing(model: AgentGroupModel): boolean {
  const mediaType = drafts[model.id]?.mediaType ?? model.media_type
  return (mediaType === 'video' && ['minimax-h3', 'minimax-h3-max'].includes(model.model_code)) || (mediaType === 'image' && isSeedreamModel(model.model_code))
}
function defaultFreeImages(model: AgentGroupModel): number { return isSeedreamModel(model.model_code) ? 1 : model.model_code === 'minimax-h3' ? 5 : 2 }

function displayedPriceResolution(model: AgentGroupModel, resolution: string): string {
  return isSeedreamModel(model.model_code) && resolution === '1K' ? '1.5K' : resolution
}

function storedPriceResolution(model: AgentGroupModel, resolution: string): string {
  return isSeedreamModel(model.model_code) && resolution === '1.5K' ? '1K' : resolution
}

function isSeedance(model: AgentGroupModel): boolean { return (drafts[model.id]?.mediaType ?? model.media_type) === 'video' && model.model_code.startsWith('seedance-') }
function channelPriceKey(prices: AgentModelPrice[]): string { return JSON.stringify(prices.map(p => [p.video_channel_id || '',p.resolution,p.billing_unit || 'second',p.unit_price,p.enabled !== false]).sort((a,b) => JSON.stringify(a).localeCompare(JSON.stringify(b)))) }
function isDirty(model: AgentGroupModel): boolean {
  const draft = drafts[model.id]
  if (!draft) {
    return false
  }
  if (draft.enabled !== model.enabled || draft.mediaType !== model.media_type) {
    return true
  }
  if (isSeedance(model)) return channelPriceKey(draft.channelPrices) !== channelPriceKey(model.prices)
  if (draft.mediaType === 'text') {
    return normalizeNumber(draft.rateMultiplier) !== (model.rate_multiplier ?? null)
  }
  if (hasInputImagePricing(model) && normalizeNumber(draft.freeInputImages) !== (model.free_input_images ?? null)) return true
  if (hasInputImagePricing(model) && normalizeNumber(draft.inputImageUnitPrice) !== (model.input_image_unit_price ?? null)) return true
  const stored = new Map(model.prices.map((price) => [displayedPriceResolution(model, price.resolution), price]))
  for (const resolution of Object.keys(draft.prices)) {
    const storedPrice = stored.get(resolution)
    const draftEnabled = draft.resolutionEnabled[resolution] !== false
    const storedEnabled = storedPrice ? storedPrice.enabled !== false : false
    if (draftEnabled !== storedEnabled) {
      return true
    }
    if (!draftEnabled && !storedPrice && normalizeNumber(draft.prices[resolution]) !== null) {
      return true
    }
    if (
      draftEnabled &&
      normalizeNumber(draft.prices[resolution]) !== (storedPrice?.unit_price ?? null)
    ) {
      return true
    }
  }
  for (const [resolution, price] of stored) {
    if (draft.resolutionEnabled[resolution] !== false || price.enabled !== false) {
      if (normalizeNumber(draft.prices[resolution] ?? '') !== price.unit_price) {
        return true
      }
    }
    if (draft.resolutionEnabled[resolution] === false && price.enabled !== false) {
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
    const resolutionEnabled: Record<string, boolean> = {}
    for (const resolution of resolutionColumnsForType(model.media_type, model.model_code)) {
      prices[resolution] = ''
      resolutionEnabled[resolution] = false
    }
    for (const price of model.prices) {
      const resolution = displayedPriceResolution(model, price.resolution)
      prices[resolution] = String(price.unit_price)
      resolutionEnabled[resolution] = price.enabled !== false
    }
    drafts[model.id] = {
      channelPrices: model.prices.map(p => ({...p})),
      enabled: model.enabled,
      mediaType: model.media_type,
      rateMultiplier: model.rate_multiplier === null ? '' : String(model.rate_multiplier),
      prices,
      resolutionEnabled,
      freeInputImages: model.free_input_images == null ? "" : String(model.free_input_images),
      inputImageUnitPrice: model.input_image_unit_price == null ? "" : String(model.input_image_unit_price),
    }
  }
}

// When an administrator changes a model's media type, seed the newly visible
// resolution controls as disabled until the administrator selects them. This
// keeps an empty tier from being exposed accidentally.
function ensureDraftResolutions(): void {
  for (const model of models.value) {
    const draft = drafts[model.id]
    if (!draft || draft.mediaType === 'text') continue
    for (const resolution of resolutionColumnsForType(draft.mediaType, model.model_code)) {
      if (!(resolution in draft.prices)) draft.prices[resolution] = ''
      if (!(resolution in draft.resolutionEnabled)) draft.resolutionEnabled[resolution] = false
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

async function refreshChannelMetadata(): Promise<void> {
  if (!agentGroupId.value) return
  try {
    const config = await agentModelsAPI.getAgentModels(agentGroupId.value)
    // Keep unsaved pricing drafts when only channel names/statuses change.
    models.value = models.value.map(model => {
      const updated = config.models.find(entry => entry.id === model.id)
      return updated ? { ...model, video_channels: updated.video_channels } : model
    })
  } catch (error) {
    errorMessage.value = extractError(error, t('admin.yingzoAgent.loadFailed'))
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
  if (isSeedance(model)) {
    if (draft.channelPrices.some(p => p.enabled !== false && (!Number.isFinite(p.unit_price) || p.unit_price < 0))) throw new Error(t('videoChannels.invalidPrice'))
    return draft.channelPrices.filter(p => Number.isFinite(p.unit_price) && p.unit_price >= 0)
  }
  const prices: AgentModelPrice[] = []
  for (const resolution of resolutionColumnsForDraft(model)) {
    const value = normalizeNumber(draft.prices[resolution])
    if (value === null) {
      continue
    }
    const price: AgentModelPrice = { resolution: storedPriceResolution(model, resolution), unit_price: value }
    if (draft.resolutionEnabled[resolution] === false) {
      price.enabled = false
    }
    prices.push(price)
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
      const freeImages = normalizeNumber(draft.freeInputImages)
      // v-model 在 type="number" 输入框上会把用户输入转成 number，不能直接调 .trim()。
      const rawFreeImages = String(draft.freeInputImages).trim()
      if (hasInputImagePricing(model) && ((freeImages !== null && (!Number.isInteger(freeImages) || freeImages < 0)) || (rawFreeImages !== '' && freeImages === null))) {
        throw new Error(t('admin.yingzoAgent.invalidInputImagePricing'))
      }
      const inputImagePrice = normalizeNumber(draft.inputImageUnitPrice)
      const rawInputImagePrice = String(draft.inputImageUnitPrice).trim()
      if (hasInputImagePricing(model) && ((inputImagePrice !== null && inputImagePrice < 0) || (rawInputImagePrice !== '' && inputImagePrice === null))) {
        throw new Error(t('admin.yingzoAgent.invalidInputImagePricing'))
      }
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
              ...(hasInputImagePricing(model)
                ? { free_input_images: freeImages, input_image_unit_price: inputImagePrice }
                : {}),
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

watch(
  () => models.value.map((model) => `${model.id}:${drafts[model.id]?.mediaType ?? model.media_type}`),
  ensureDraftResolutions,
  { flush: 'post' },
)
</script>
