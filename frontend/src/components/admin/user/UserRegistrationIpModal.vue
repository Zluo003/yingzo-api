<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.registrationIp.title')"
    width="normal"
    :close-on-escape="!confirmAction"
    @close="handleClose"
  >
    <div class="space-y-4">
      <div v-if="loading && !inspection" class="flex justify-center py-8">
        <svg class="h-8 w-8 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
        </svg>
      </div>

      <div v-else-if="loadError" class="space-y-3 py-6 text-center" data-test="inspect-error">
        <p class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
        <button type="button" class="btn btn-secondary btn-sm" @click="loadInspection">
          {{ t('common.refresh') }}
        </button>
      </div>

      <template v-else-if="inspection">
        <!-- IP 与封禁状态 -->
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
          <div class="flex flex-wrap items-center gap-2">
            <span class="break-all font-mono text-lg font-semibold text-gray-900 dark:text-white" data-test="inspect-ip">
              {{ inspection.ip }}
            </span>
            <span :class="['badge', inspection.banned ? 'badge-danger' : 'badge-success']" data-test="inspect-status">
              {{ inspection.banned ? t('admin.users.registrationIp.bannedBadge') : t('admin.users.registrationIp.notBanned') }}
            </span>
          </div>
          <div
            v-if="showNetworkKey"
            class="mt-3 border-t border-gray-200/60 pt-3 dark:border-dark-600/60"
            data-test="inspect-network"
          >
            <p class="text-sm text-gray-600 dark:text-gray-300">
              {{ t('admin.users.registrationIp.networkKey') }}:
              <span class="break-all font-mono text-gray-900 dark:text-white">{{ inspection.network_key }}</span>
            </p>
            <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.users.registrationIp.networkKeyHint') }}
            </p>
          </div>
        </div>

        <!-- 该网络注册的账号 -->
        <div>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h4 class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('admin.users.registrationIp.registeredAccounts') }}
            </h4>
            <button
              type="button"
              class="inline-flex items-center gap-1 text-sm text-primary-600 hover:text-primary-500 dark:text-primary-400 dark:hover:text-primary-300"
              data-test="view-users"
              @click="handleViewUsers"
            >
              <Icon name="users" size="sm" />
              {{ t('admin.users.registrationIp.viewUsers') }}
            </button>
          </div>
          <div class="mt-2 grid grid-cols-2 gap-3">
            <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
              <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.users.registrationIp.registeredTotal') }}</p>
              <p class="text-xl font-semibold text-gray-900 dark:text-white" data-test="registered-total">
                {{ inspection.registered_total }}
              </p>
            </div>
            <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
              <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.users.registrationIp.registeredToday') }}</p>
              <p class="text-xl font-semibold text-gray-900 dark:text-white" data-test="registered-today">
                {{ inspection.registered_today }}
              </p>
            </div>
          </div>
          <p class="mt-1.5 text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.users.registrationIp.registeredHint') }}
          </p>
        </div>

        <!-- 已封禁：命中的规则；未封禁：可选的封禁原因，或不可封禁的说明 -->
        <dl
          v-if="inspection.ban"
          class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 rounded-xl border border-red-200 bg-red-50 p-4 text-sm dark:border-red-900/50 dark:bg-red-900/20"
          data-test="ban-detail"
        >
          <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.users.registrationIp.banRule') }}</dt>
          <dd class="break-all font-mono text-gray-900 dark:text-white">{{ inspection.ban.ip }}</dd>
          <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.users.registrationIp.banReason') }}</dt>
          <dd class="break-words text-gray-900 dark:text-white">{{ inspection.ban.reason || '—' }}</dd>
          <dt class="text-gray-500 dark:text-dark-400">{{ t('admin.users.registrationIp.banCreatedAt') }}</dt>
          <dd class="text-gray-900 dark:text-white">{{ formatDateTime(inspection.ban.created_at) }}</dd>
        </dl>
        <div v-else-if="inspection.bannable">
          <label class="input-label" for="registration-ip-ban-reason">
            {{ t('admin.users.registrationIp.reasonLabel') }}
          </label>
          <input
            id="registration-ip-ban-reason"
            v-model="reason"
            type="text"
            maxlength="255"
            class="input"
            :placeholder="t('admin.users.ipBan.reasonPlaceholder')"
            data-test="ban-reason"
          />
        </div>
        <div
          v-else
          class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
          data-test="not-bannable"
        >
          {{ notBannableMessage }}
        </div>

        <p class="text-xs text-gray-400 dark:text-dark-500">{{ t('admin.users.ipBan.propagationHint') }}</p>
      </template>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.close') }}
        </button>
        <button
          v-if="inspection?.banned"
          type="button"
          class="btn btn-primary"
          :disabled="busy"
          data-test="unban"
          @click="confirmAction = 'unban'"
        >
          {{ t('admin.users.registrationIp.unbanAction') }}
        </button>
        <button
          v-else-if="inspection"
          type="button"
          class="btn btn-danger"
          :disabled="busy || !inspection.bannable"
          :title="inspection.bannable ? undefined : notBannableMessage"
          data-test="ban"
          @click="confirmAction = 'ban'"
        >
          {{ t('admin.users.registrationIp.banAction') }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <ConfirmDialog
    :show="confirmAction === 'ban'"
    :title="t('admin.users.ipBan.banConfirmTitle')"
    :message="t('admin.users.ipBan.banConfirmMessage', { target: banTarget })"
    :confirm-text="t('admin.users.registrationIp.banAction')"
    :danger="true"
    @confirm="confirmBan"
    @cancel="confirmAction = null"
  >
    <p v-if="reason.trim()" class="break-words text-sm text-gray-500 dark:text-dark-400">
      {{ t('admin.users.ipBan.banConfirmReason', { reason: reason.trim() }) }}
    </p>
  </ConfirmDialog>
  <ConfirmDialog
    :show="confirmAction === 'unban'"
    :title="t('admin.users.ipBan.unbanConfirmTitle')"
    :message="t('admin.users.ipBan.unbanConfirmMessage', { rule: inspection?.ban?.ip ?? '' })"
    :confirm-text="t('admin.users.registrationIp.unbanAction')"
    @confirm="confirmUnban"
    @cancel="confirmAction = null"
  />
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { IPInspection } from '@/api/admin/ipBans'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ show: boolean; ip: string | null }>()
const emit = defineEmits<{
  close: []
  // 封禁状态已变更，父组件据此刷新用户列表
  success: []
  // 「查看该 IP 注册的用户」
  search: [ip: string]
}>()

const { t } = useI18n()
const appStore = useAppStore()

const inspection = ref<IPInspection | null>(null)
const loading = ref(false)
const loadError = ref('')
const submitting = ref(false)
const reason = ref('')
const confirmAction = ref<'ban' | 'unban' | null>(null)
// 只采用最近一次 inspect 的结果，避免关闭后重开时旧响应覆盖新状态
let inspectSeq = 0

const busy = computed(() => loading.value || submitting.value)

// IPv4 的 network_key 就是地址本身；IPv6 为所在 /64 网段，与地址不同时才单独展示
const showNetworkKey = computed(
  () => !!inspection.value?.network_key && inspection.value.network_key !== inspection.value.ip
)

// 封禁目标取 network_key：客户端会在 /64 内轮换 IPv6 地址，只封单个地址没有意义
const banTarget = computed(() => inspection.value?.network_key || inspection.value?.ip || '')

const notBannableMessage = computed(() => {
  switch (inspection.value?.not_bannable_reason) {
    case 'IP_BAN_SELF':
      return t('admin.users.ipBan.errors.IP_BAN_SELF')
    case 'IP_BAN_RESERVED_TARGET':
      return t('admin.users.ipBan.errors.IP_BAN_RESERVED_TARGET')
    default:
      return t('admin.users.registrationIp.notBannable')
  }
})

const errorMessage = (err: unknown, fallback: string) =>
  extractI18nErrorMessage(err, t, 'admin.users.ipBan.errors', fallback)

async function loadInspection() {
  const ip = props.ip
  if (!ip) return
  const seq = ++inspectSeq
  loading.value = true
  loadError.value = ''
  try {
    const result = await adminAPI.ipBans.inspect(ip)
    if (seq !== inspectSeq) return
    inspection.value = result
  } catch (err) {
    if (seq !== inspectSeq) return
    inspection.value = null
    loadError.value = errorMessage(err, t('admin.users.registrationIp.loadFailed'))
  } finally {
    if (seq === inspectSeq) loading.value = false
  }
}

watch(
  () => props.show,
  (visible) => {
    confirmAction.value = null
    if (!visible) return
    inspection.value = null
    loadError.value = ''
    reason.value = ''
    void loadInspection()
  },
  { immediate: true }
)

function handleClose() {
  emit('close')
}

function handleViewUsers() {
  if (props.ip) emit('search', props.ip)
}

async function confirmBan() {
  const target = banTarget.value
  confirmAction.value = null
  if (!target) return
  submitting.value = true
  try {
    const { ban, created } = await adminAPI.ipBans.create({
      ip: target,
      reason: reason.value.trim() || undefined
    })
    const rule = ban?.ip || target
    appStore.showSuccess(
      created
        ? t('admin.users.ipBan.banSuccess', { target: rule })
        : t('admin.users.ipBan.banExisting', { target: rule })
    )
    reason.value = ''
    emit('success')
    await loadInspection()
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.users.ipBan.banFailed')))
  } finally {
    submitting.value = false
  }
}

async function confirmUnban() {
  const ban = inspection.value?.ban
  confirmAction.value = null
  if (!ban) return
  submitting.value = true
  let changed = false
  try {
    await adminAPI.ipBans.remove(ban.id)
    changed = true
    appStore.showSuccess(t('admin.users.ipBan.unbanSuccess', { rule: ban.ip }))
  } catch (err) {
    appStore.showError(errorMessage(err, t('admin.users.ipBan.unbanFailed')))
    // 规则已被解除（例如在封禁管理中操作过）时同样刷新为最新状态
    changed = extractApiErrorCode(err) === 'IP_BAN_NOT_FOUND'
  } finally {
    submitting.value = false
  }
  if (!changed) return
  emit('success')
  await loadInspection()
}
</script>
