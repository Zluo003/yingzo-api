<template>
  <div class="image-captcha">
    <label :for="inputId" class="input-label">
      {{ t('auth.imageCaptcha.label') }}
    </label>
    <div class="flex items-center gap-2">
      <div class="relative min-w-0 flex-1">
        <div class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3.5">
          <Icon name="shield" size="md" class="text-gray-400 dark:text-dark-500" />
        </div>
        <!-- 换图期间只读而非禁用：保留焦点，新图到达后可直接继续输入 -->
        <input
          :id="inputId"
          v-model="code"
          type="text"
          maxlength="8"
          autocomplete="off"
          autocapitalize="characters"
          spellcheck="false"
          :readonly="!challenge"
          class="input pl-11 uppercase tracking-widest placeholder:normal-case placeholder:tracking-normal"
          :placeholder="t('auth.imageCaptcha.placeholder')"
        />
      </div>
      <!-- 与右侧刷新按钮功能相同，不重复占用 Tab 焦点 -->
      <button
        type="button"
        tabindex="-1"
        class="box-content flex h-10 w-[120px] shrink-0 items-center justify-center overflow-hidden rounded-xl border border-gray-200 bg-gray-50 dark:border-dark-600 dark:bg-dark-700"
        :title="t('auth.imageCaptcha.refresh')"
        :disabled="loading"
        data-testid="image-captcha-picture"
        @click="refresh"
      >
        <img
          v-if="challenge"
          :src="challenge.image"
          :alt="t('auth.imageCaptcha.imageAlt')"
          width="120"
          height="40"
          class="h-10 w-[120px] select-none"
          draggable="false"
        />
        <span
          v-else-if="loading"
          role="status"
          class="px-1 text-center text-xs leading-tight text-gray-500 dark:text-dark-400"
        >
          {{ t('auth.captchaLoading') }}
        </span>
        <span
          v-else
          class="px-1 text-center text-xs leading-tight text-red-500 dark:text-red-400"
        >
          {{ t('auth.imageCaptcha.loadFailed') }}
        </span>
      </button>
      <button
        type="button"
        class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 disabled:cursor-not-allowed disabled:opacity-50 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-dark-200"
        :aria-label="t('auth.imageCaptcha.refresh')"
        :title="t('auth.imageCaptcha.refresh')"
        :disabled="loading"
        data-testid="image-captcha-refresh"
        @click="refresh"
      >
        <Icon name="refresh" size="md" :class="{ 'animate-spin': loading }" />
      </button>
    </div>
    <p v-if="loadErrorHint" class="input-error-text">{{ loadErrorHint }}</p>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getImageCaptcha } from '@/api/auth'
import { extractApiErrorCode } from '@/utils/apiError'
import type { ImageCaptchaChallenge } from '@/types'

const emit = defineEmits<{
  verify: [captchaId: string, code: string]
  expire: []
}>()

const { t } = useI18n()

// 在验证码过期前提前换图，避免用户提交时恰好过期；有效期很短时至少保留一半时长
const REFRESH_LEAD_SECONDS = 15

const inputId = `image-captcha-${useId()}`
const challenge = ref<ImageCaptchaChallenge | null>(null)
const code = ref('')
const loading = ref(false)
const loadErrorHint = ref('')

// 是否已把凭据交给父组件；撤销时据此决定是否 emit('expire')
let proofEmitted = false
let pendingLoad: Promise<void> | null = null
let refreshTimer: ReturnType<typeof setTimeout> | null = null
let unmounted = false

// 输入非空即把 (captcha_id, 输入) 交给父组件；清空或换图时撤销已交出的凭据。
// 同步触发：父组件 reset 后紧接着清理自身状态时，expire 已先行送达。
watch(
  code,
  (value) => {
    const trimmed = value.trim()
    if (trimmed && challenge.value) {
      proofEmitted = true
      emit('verify', challenge.value.captcha_id, trimmed)
      return
    }
    if (proofEmitted) {
      proofEmitted = false
      emit('expire')
    }
  },
  { flush: 'sync' }
)

function clearRefreshTimer(): void {
  if (refreshTimer !== null) {
    clearTimeout(refreshTimer)
    refreshTimer = null
  }
}

function scheduleAutoRefresh(expiresIn: number): void {
  clearRefreshTimer()
  if (!Number.isFinite(expiresIn) || expiresIn <= 0) return
  const delaySeconds = Math.max(expiresIn - REFRESH_LEAD_SECONDS, expiresIn / 2)
  refreshTimer = setTimeout(() => {
    void refresh()
  }, delaySeconds * 1000)
}

// 有对应文案的失败原因（未启用、网络被封禁等）在下方提示；限流、网络错误只保留重试入口
function describeLoadError(error: unknown): string {
  const reason = extractApiErrorCode(error)
  if (!reason) return ''
  const key = `auth.errors.${reason}`
  const message = t(key)
  return message === key ? '' : message
}

// refresh 丢弃当前验证码并拉取新图：验证码一次性，提交过（无论成败）或即将过期都要换新。
// 已有请求在途时直接复用——在途的新图尚未被任何请求使用过。
function refresh(): Promise<void> {
  code.value = ''
  if (pendingLoad) return pendingLoad

  clearRefreshTimer()
  challenge.value = null
  loading.value = true
  loadErrorHint.value = ''
  pendingLoad = getImageCaptcha()
    .then((next) => {
      if (unmounted) return
      if (!next?.captcha_id || !next.image) {
        throw new Error('Invalid image captcha response')
      }
      challenge.value = next
      scheduleAutoRefresh(next.expires_in)
    })
    .catch((error: unknown) => {
      if (unmounted) return
      loadErrorHint.value = describeLoadError(error)
    })
    .finally(() => {
      pendingLoad = null
      loading.value = false
    })
  return pendingLoad
}

// reset 供父组件在每次提交后调用：清空输入并换一张新图
function reset(): void {
  void refresh()
}

defineExpose({ reset })

onMounted(() => {
  void refresh()
})

onBeforeUnmount(() => {
  unmounted = true
  clearRefreshTimer()
})
</script>
