<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.ipBan.title')"
    width="wide"
    :close-on-escape="!pendingCreate && !unbanTarget"
    @close="handleClose"
  >
    <div class="space-y-4">
      <p class="text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.users.ipBan.description') }}
      </p>

      <!-- 添加封禁 -->
      <form
        class="rounded-xl border border-gray-200 p-4 dark:border-dark-600"
        data-test="ip-ban-form"
        @submit.prevent="requestCreate"
      >
        <div class="flex flex-col gap-3 md:flex-row md:items-end">
          <div class="md:flex-1">
            <label class="input-label" for="ip-ban-target">{{ t('admin.users.ipBan.targetLabel') }}</label>
            <input
              id="ip-ban-target"
              v-model="form.ip"
              type="text"
              maxlength="64"
              autocomplete="off"
              spellcheck="false"
              class="input font-mono"
              :placeholder="t('admin.users.ipBan.targetPlaceholder')"
              data-test="ip-ban-target"
            />
          </div>
          <div class="md:flex-1">
            <label class="input-label" for="ip-ban-reason">{{ t('admin.users.ipBan.reasonLabel') }}</label>
            <input
              id="ip-ban-reason"
              v-model="form.reason"
              type="text"
              maxlength="255"
              class="input"
              :placeholder="t('admin.users.ipBan.reasonPlaceholder')"
              data-test="ip-ban-reason"
            />
          </div>
          <button
            type="submit"
            class="btn btn-danger shrink-0"
            :disabled="creating || !form.ip.trim()"
            data-test="ip-ban-add"
          >
            <Icon name="ban" size="sm" />
            {{ t('admin.users.ipBan.add') }}
          </button>
        </div>
        <p class="input-hint">{{ t('admin.users.ipBan.targetHint') }}</p>
      </form>

      <!-- 搜索 -->
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="relative w-full sm:w-64">
          <Icon
            name="search"
            size="md"
            class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"
          />
          <input
            v-model="searchQuery"
            type="text"
            class="input pl-10"
            :placeholder="t('admin.users.ipBan.searchPlaceholder')"
            data-test="ip-ban-search"
            @input="handleSearch"
          />
        </div>
        <span class="text-sm text-gray-500 dark:text-dark-400">
          {{ t('admin.users.ipBan.total', { count: total }) }}
        </span>
      </div>

      <!-- 规则列表 -->
      <div v-if="loading && bans.length === 0" class="flex justify-center py-8">
        <svg class="h-8 w-8 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
        </svg>
      </div>

      <div
        v-else-if="bans.length === 0"
        class="py-8 text-center text-sm text-gray-500 dark:text-dark-400"
        data-test="ip-ban-empty"
      >
        {{ t('admin.users.ipBan.empty') }}
      </div>

      <div v-else class="max-h-96 overflow-auto rounded-lg border border-gray-200 dark:border-dark-600">
        <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
          <thead class="sticky top-0 bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                {{ t('admin.users.ipBan.columns.rule') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                {{ t('admin.users.ipBan.columns.reason') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                {{ t('admin.users.ipBan.columns.createdAt') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                {{ t('admin.users.ipBan.columns.actions') }}
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-800">
            <tr
              v-for="ban in bans"
              :key="ban.id"
              class="hover:bg-gray-50 dark:hover:bg-dark-700"
              :data-test="`ip-ban-row-${ban.id}`"
            >
              <td class="whitespace-nowrap px-3 py-2 font-mono text-sm text-gray-900 dark:text-white">
                {{ ban.ip }}
              </td>
              <td class="px-3 py-2">
                <div
                  v-if="ban.reason"
                  class="max-w-xs truncate text-sm text-gray-600 dark:text-gray-300"
                  :title="ban.reason"
                >
                  {{ ban.reason }}
                </div>
                <div v-else class="text-xs text-gray-400 dark:text-gray-600">—</div>
              </td>
              <td class="whitespace-nowrap px-3 py-2 text-sm text-gray-500 dark:text-dark-400">
                {{ formatDateTime(ban.created_at) }}
              </td>
              <td class="whitespace-nowrap px-3 py-2">
                <button
                  type="button"
                  class="text-sm font-medium text-primary-600 hover:text-primary-500 disabled:cursor-not-allowed disabled:opacity-50 dark:text-primary-400 dark:hover:text-primary-300"
                  :disabled="removingId === ban.id"
                  :data-test="`ip-ban-unban-${ban.id}`"
                  @click="unbanTarget = ban"
                >
                  {{ t('admin.users.ipBan.unban') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 分页 -->
      <div v-if="totalPages > 1" class="flex items-center justify-center gap-2">
        <button
          :disabled="page <= 1 || loading"
          class="btn btn-secondary px-3 py-1 text-sm"
          @click="loadBans(page - 1)"
        >
          {{ t('pagination.previous') }}
        </button>
        <span class="text-sm text-gray-500 dark:text-dark-400">{{ page }} / {{ totalPages }}</span>
        <button
          :disabled="page >= totalPages || loading"
          class="btn btn-secondary px-3 py-1 text-sm"
          @click="loadBans(page + 1)"
        >
          {{ t('pagination.next') }}
        </button>
      </div>

      <p class="text-xs text-gray-400 dark:text-dark-500">{{ t('admin.users.ipBan.propagationHint') }}</p>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <ConfirmDialog
    :show="pendingCreate !== null"
    :title="t('admin.users.ipBan.banConfirmTitle')"
    :message="t('admin.users.ipBan.banConfirmMessage', { target: pendingCreate?.ip ?? '' })"
    :confirm-text="t('admin.users.ipBan.add')"
    :danger="true"
    @confirm="confirmCreate"
    @cancel="pendingCreate = null"
  >
    <p v-if="pendingCreate?.reason" class="break-words text-sm text-gray-500 dark:text-dark-400">
      {{ t('admin.users.ipBan.banConfirmReason', { reason: pendingCreate.reason }) }}
    </p>
  </ConfirmDialog>
  <ConfirmDialog
    :show="unbanTarget !== null"
    :title="t('admin.users.ipBan.unbanConfirmTitle')"
    :message="t('admin.users.ipBan.unbanConfirmMessage', { rule: unbanTarget?.ip ?? '' })"
    :confirm-text="t('admin.users.ipBan.unban')"
    @confirm="confirmUnban"
    @cancel="unbanTarget = null"
  />
</template>

<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { IPBan } from '@/api/admin/ipBans'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{
  close: []
  // 封禁规则已变更，父组件据此刷新用户列表的封禁标记
  success: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const PAGE_SIZE = 10

const bans = ref<IPBan[]>([])
const loading = ref(false)
const page = ref(1)
const total = ref(0)
const searchQuery = ref('')
const form = reactive({ ip: '', reason: '' })
const creating = ref(false)
const pendingCreate = ref<{ ip: string; reason: string } | null>(null)
const unbanTarget = ref<IPBan | null>(null)
const removingId = ref<number | null>(null)
// 只采用最近一次列表请求的结果，避免搜索输入过程中的旧响应覆盖新结果
let loadSeq = 0
let searchTimeout: ReturnType<typeof setTimeout> | undefined

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))

const errorMessage = (err: unknown, fallback: string) =>
  extractI18nErrorMessage(err, t, 'admin.users.ipBan.errors', fallback)

async function loadBans(targetPage: number = page.value) {
  const seq = ++loadSeq
  loading.value = true
  try {
    const res = await adminAPI.ipBans.list(targetPage, PAGE_SIZE, {
      search: searchQuery.value.trim() || undefined
    })
    if (seq !== loadSeq) return
    bans.value = res.items || []
    total.value = res.total || 0
    page.value = targetPage
  } catch (err) {
    if (seq !== loadSeq) return
    appStore.showError(errorMessage(err, t('admin.users.ipBan.loadFailed')))
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

watch(
  () => props.show,
  (visible) => {
    clearTimeout(searchTimeout)
    pendingCreate.value = null
    unbanTarget.value = null
    if (!visible) return
    searchQuery.value = ''
    form.ip = ''
    form.reason = ''
    bans.value = []
    total.value = 0
    void loadBans(1)
  },
  { immediate: true }
)

onUnmounted(() => {
  clearTimeout(searchTimeout)
})

function handleClose() {
  emit('close')
}

function handleSearch() {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    void loadBans(1)
  }, 300)
}

function requestCreate() {
  const ip = form.ip.trim()
  if (!ip || creating.value) return
  pendingCreate.value = { ip, reason: form.reason.trim() }
}

async function confirmCreate() {
  const request = pendingCreate.value
  pendingCreate.value = null
  if (!request) return
  creating.value = true
  try {
    const { ban, created } = await adminAPI.ipBans.create({
      ip: request.ip,
      reason: request.reason || undefined
    })
    const rule = ban?.ip || request.ip
    appStore.showSuccess(
      created
        ? t('admin.users.ipBan.banSuccess', { target: rule })
        : t('admin.users.ipBan.banExisting', { target: rule })
    )
    form.ip = ''
    form.reason = ''
    emit('success')
    // 新规则按创建时间排在第一页最前：清空搜索后回到第一页，便于确认结果
    clearTimeout(searchTimeout)
    searchQuery.value = ''
    await loadBans(1)
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.users.ipBan.banFailed')))
  } finally {
    creating.value = false
  }
}

async function confirmUnban() {
  const ban = unbanTarget.value
  unbanTarget.value = null
  if (!ban) return
  removingId.value = ban.id
  let changed = false
  try {
    await adminAPI.ipBans.remove(ban.id)
    changed = true
    appStore.showSuccess(t('admin.users.ipBan.unbanSuccess', { rule: ban.ip }))
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.users.ipBan.unbanFailed')))
    // 规则已被解除（例如在其他页面操作过）时同样刷新列表
    changed = extractApiErrorCode(err) === 'IP_BAN_NOT_FOUND'
  } finally {
    removingId.value = null
  }
  if (!changed) return
  emit('success')
  // 删掉当前页最后一条后回退到上一页，避免停留在空页
  const nextPage = bans.value.length <= 1 && page.value > 1 ? page.value - 1 : page.value
  await loadBans(nextPage)
}
</script>
