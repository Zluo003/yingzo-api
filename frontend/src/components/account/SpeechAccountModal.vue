<template>
  <BaseDialog :show="show" :title="t(account ? 'speech.editTitle' : 'speech.createTitle')" width="wide" @close="emit('close')">
    <form id="speech-account-form" class="space-y-5" @submit.prevent="save">
      <div class="rounded-xl bg-primary-50 p-4 dark:bg-primary-900/20">
        <p class="font-semibold">MiMo V2.5 TTS · Xiaomi</p>
        <p class="input-hint">{{ t('speech.scope') }}</p>
      </div>
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="input-label">{{ t('admin.accounts.accountName') }}<input v-model="form.name" class="input mt-1" required maxlength="100" data-testid="speech-name" /></label>
        <label class="input-label">Base URL<input v-model="form.baseUrl" class="input mt-1" type="url" required placeholder="https://api.xiaomimimo.com/v1" data-testid="speech-base-url" /></label>
      </div>
      <label class="input-label block">MiMo API Key
        <input v-model="form.apiKey" class="input mt-1" type="password" autocomplete="new-password" :required="!account" :placeholder="account ? t('speech.keepKey') : 'sk-…'" data-testid="speech-api-key" />
      </label>
      <div class="grid gap-4 sm:grid-cols-3">
        <label class="input-label">{{ t('admin.accounts.concurrency') }}<input v-model.number="form.concurrency" class="input mt-1" type="number" min="1" max="10000" required /></label>
        <label class="input-label">{{ t('admin.accounts.priority') }}<input v-model.number="form.priority" class="input mt-1" type="number" min="0" required /></label>
        <label class="input-label">{{ t('speech.proxy') }}
          <select v-model="form.proxyId" class="input mt-1" data-testid="speech-proxy"><option :value="null">{{ t('speech.noProxy') }}</option><option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">{{ proxy.name }}</option></select>
        </label>
      </div>
      <fieldset>
        <legend class="input-label">{{ t('speech.groups') }}</legend>
        <div class="mt-2 flex flex-wrap gap-3"><label v-for="group in compatibleGroups" :key="group.id" class="flex items-center gap-2 text-sm"><input v-model="form.groupIds" type="checkbox" :value="group.id" />{{ group.name }}</label></div>
        <p class="input-hint">{{ t('speech.pricingHint') }}</p>
      </fieldset>
      <label class="input-label block">{{ t('admin.accounts.notes') }}<textarea v-model="form.notes" class="input mt-1" rows="2" /></label>
      <p v-if="error" role="alert" class="text-sm text-red-500">{{ error }}</p>
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button form="speech-account-form" type="submit" class="btn btn-primary" :disabled="saving" data-testid="speech-save">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api'
import type { Account, AdminGroup, Proxy } from '@/types'
import { MIMO_SPEECH_MODELS, MIMO_SPEECH_PROVIDER } from './speech'

const props = defineProps<{ show: boolean; account?: Account | null; groups: AdminGroup[]; proxies: Proxy[] }>()
const emit = defineEmits<{ close: []; created: []; updated: [account: Account] }>()
const { t } = useI18n()
const saving = ref(false)
const error = ref('')
const compatibleGroups = computed(() => props.groups.filter(g => g.platform === 'openai' || g.kind === 'agent'))
const form = reactive({ name: '', notes: '', baseUrl: 'https://api.xiaomimimo.com/v1', apiKey: '', concurrency: 1, priority: 1, proxyId: null as number | null, groupIds: [] as number[] })
watch(() => [props.show, props.account] as const, () => {
  if (!props.show) return
  const a = props.account
  Object.assign(form, { name: a?.name ?? '', notes: a?.notes ?? '', baseUrl: String(a?.credentials?.base_url || 'https://api.xiaomimimo.com/v1'), apiKey: '', concurrency: a?.concurrency ?? 1, priority: a?.priority ?? 1, proxyId: a?.proxy_id ?? null, groupIds: a?.group_ids ? [...a.group_ids] : compatibleGroups.value.filter(g => g.kind === 'agent').map(g => g.id) })
  error.value = ''
}, { immediate: true })
async function save() {
  if (saving.value) return
  error.value = ''
  if (!form.name.trim() || (!props.account && !form.apiKey.trim()) || !form.groupIds.length) { error.value = t('speech.required'); return }
  try {
    const u = new URL(form.baseUrl.trim())
    if (u.protocol !== 'https:' || u.username || u.password || u.search || u.hash || !['', '/', '/v1', '/v1/'].includes(u.pathname)) throw new Error()
  } catch { error.value = t('speech.invalidUrl'); return }
  saving.value = true
  try {
    const credentials: Record<string, unknown> = { ...props.account?.credentials, base_url: form.baseUrl.trim(), model_mapping: Object.fromEntries(MIMO_SPEECH_MODELS.map(model => [model, model])) }
    // Redacted credentials returned by the API must never overwrite a saved key.
    delete credentials.api_key
    if (form.apiKey.trim()) credentials.api_key = form.apiKey.trim()
    const payload = { name: form.name.trim(), notes: form.notes, credentials, extra: { ...props.account?.extra, speech_account: true, speech_provider: MIMO_SPEECH_PROVIDER }, concurrency: form.concurrency, priority: form.priority, proxy_id: form.proxyId ?? (props.account ? 0 : null), group_ids: form.groupIds }
    if (props.account) {
      const updated = await adminAPI.accounts.update(props.account.id, payload)
      emit('updated', updated)
    } else {
      await adminAPI.accounts.create({ ...payload, platform: 'openai', type: 'apikey' })
      emit('created')
    }
    emit('close')
  } catch (e: any) { error.value = e?.response?.data?.message || e?.message || t('speech.saveFailed') }
  finally { saving.value = false }
}
</script>
