<template>
  <section class="card p-6" data-testid="media-input-audit">
    <details :open="!!route?.query.media_event">
      <summary class="cursor-pointer text-lg font-semibold">
        {{ t('mediaInputAudit.title') }}
      </summary>
      <p class="mt-2 text-sm text-gray-500">{{ t('mediaInputAudit.hint') }}</p>
      <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
      <p v-if="notice" role="status" class="mt-3 text-sm text-green-600">{{ notice }}</p>
      <form v-if="config" class="mt-5 space-y-5" @submit.prevent="save">
        <div class="grid gap-5 lg:grid-cols-2">
          <fieldset
            v-for="slot in slots"
            :key="slot"
            class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600"
          >
            <legend class="px-1 font-medium">{{ t('mediaInputAudit.' + slot) }}</legend>
            <label class="block text-sm"
              >{{ t('mediaInputAudit.protocol')
              }}<select v-model="config[slot].protocol" class="input mt-1">
                <option value="openai">OpenAI Chat Completions</option>
                <option value="gemini">Gemini</option>
              </select></label
            >
            <label class="block text-sm"
              >{{ t('mediaInputAudit.domain')
              }}<input
                v-model="config[slot].domain"
                class="input mt-1"
                placeholder="https://api.example.com"
                :required="slot === 'primary'"
            /></label>
            <label class="block text-sm"
              >{{ t('mediaInputAudit.model')
              }}<input
                v-model="config[slot].model"
                class="input mt-1"
                :required="slot === 'primary'"
            /></label>
            <label class="block text-sm"
              >{{ t('mediaInputAudit.key')
              }}<input
                v-model="config[slot].api_key"
                type="password"
                autocomplete="new-password"
                class="input mt-1"
                :placeholder="config[slot].key_configured ? t('mediaInputAudit.keySaved') : ''"
            /></label>
            <label v-if="config[slot].key_configured" class="flex items-center gap-2 text-sm"
              ><input v-model="config[slot].clear_key" type="checkbox" />{{
                t('mediaInputAudit.clearKey')
              }}</label
            >
            <label class="block text-sm"
              >{{ t('mediaInputAudit.timeout')
              }}<input
                v-model.number="config[slot].timeout_seconds"
                type="number"
                min="5"
                max="180"
                required
                class="input mt-1"
            /></label>
          </fieldset>
        </div>
        <div class="grid gap-4 md:grid-cols-2">
          <label class="block text-sm"
            >{{ t('mediaInputAudit.emails')
            }}<textarea v-model="emails" rows="3" required class="input mt-1" />
          </label>
          <label class="block text-sm"
            >{{ t('mediaInputAudit.retention')
            }}<input
              v-model.number="config.retention_days"
              type="number"
              min="1"
              max="365"
              required
              class="input mt-1"
          /></label>
        </div>
        <div class="flex flex-wrap gap-4 text-sm">
          <label
            ><input v-model="config.political_names" type="checkbox" />
            {{ t('mediaInputAudit.political') }}</label
          >
          <label
            ><input v-model="config.public_figure_names" type="checkbox" />
            {{ t('mediaInputAudit.public') }}</label
          >
          <label
            ><input v-model="config.nudity" type="checkbox" />
            {{ t('mediaInputAudit.nudity') }}</label
          >
        </div>
        <p class="text-sm text-gray-500">{{ t('mediaInputAudit.rulesHint') }}</p>
        <button class="btn btn-primary" :disabled="busy">
          {{ busy ? t('mediaInputAudit.loading') : t('mediaInputAudit.save') }}
        </button>
      </form>
      <div v-if="config" class="mt-6 space-y-3 border-t border-gray-100 pt-5 dark:border-dark-700">
        <p class="text-sm text-gray-500">{{ t('mediaInputAudit.probeHint') }}</p>
        <label class="block text-sm"
          >{{ t('mediaInputAudit.prompt')
          }}<textarea v-model="probePrompt" rows="2" class="input mt-1" />
        </label>
        <label class="block text-sm"
          >{{ t('mediaInputAudit.image')
          }}<textarea v-model="probeImages" rows="2" class="input mt-1" />
        </label>
        <div class="flex gap-2">
          <button
            v-for="slot in slots"
            :key="slot"
            type="button"
            class="btn btn-secondary"
            :disabled="busy"
            @click="probe(slot)"
          >
            {{ t('mediaInputAudit.test') }} · {{ t('mediaInputAudit.' + slot) }}
          </button>
        </div>
        <pre
          v-if="probeResult"
          class="overflow-auto whitespace-pre-wrap rounded bg-gray-50 p-3 text-sm dark:bg-dark-700"
          >{{ probeResult }}</pre>
      </div>
      <div class="mt-6 border-t border-gray-100 pt-5 dark:border-dark-700">
        <div class="flex items-center justify-between gap-3">
          <h3 class="font-medium">{{ t('mediaInputAudit.events') }}</h3>
          <button type="button" class="btn btn-secondary" :disabled="busy" @click="loadEvents">
            {{ t('mediaInputAudit.refresh') }}
          </button>
        </div>
        <div class="mt-3 overflow-auto">
          <table class="w-full text-left text-sm">
            <thead>
              <tr>
                <th class="p-2">{{ t('mediaInputAudit.time') }}</th>
                <th class="p-2">{{ t('mediaInputAudit.user') }}</th>
                <th class="p-2">{{ t('mediaInputAudit.account') }}</th>
                <th class="p-2">{{ t('mediaInputAudit.result') }}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="event in events"
                :key="event.id"
                class="border-t border-gray-100 dark:border-dark-700"
              >
                <td class="p-2 whitespace-nowrap">{{ date(event.created_at) }}</td>
                <td class="p-2">{{ event.user_email || event.user_id }}</td>
                <td class="p-2">{{ event.account_name }}<br />{{ event.model }}</td>
                <td class="p-2">
                  {{
                    t(
                      event.decision === 'block' ? 'mediaInputAudit.block' : 'mediaInputAudit.error'
                    )
                  }}<br />{{ event.category }}
                </td>
                <td class="p-2">
                  <button type="button" class="text-primary-600" @click="openEvent(event.id)">
                    {{ t('mediaInputAudit.detail') }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
          <p v-if="!events.length" class="py-5 text-center text-gray-500">
            {{ t('mediaInputAudit.empty') }}
          </p>
        </div>
        <div class="mt-3 flex items-center justify-end gap-3">
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="page === 1 || busy"
            @click="changePage(-1)"
          >
            {{ t('mediaInputAudit.prev') }}</button
          ><span>{{ page }}</span
          ><button
            type="button"
            class="btn btn-secondary"
            :disabled="events.length < 20 || busy"
            @click="changePage(1)"
          >
            {{ t('mediaInputAudit.next') }}
          </button>
        </div>
      </div>
    </details>
    <BaseDialog
      :show="!!selected"
      :title="t('mediaInputAudit.event')"
      width="wide"
      @close="selected = null"
    >
      <div v-if="selected" class="space-y-4 text-sm">
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 break-all">
          <dt>ID</dt>
          <dd>{{ selected.id }}</dd>
          <dt>{{ t('mediaInputAudit.user') }}</dt>
          <dd>{{ selected.user_email }} · {{ selected.user_id }}</dd>
          <dt>{{ t('mediaInputAudit.apiKeyId') }}</dt>
          <dd>{{ selected.api_key_id }}</dd>
          <dt>{{ t('mediaInputAudit.request') }}</dt>
          <dd>{{ selected.request_id }}</dd>
          <dt>{{ t('mediaInputAudit.account') }}</dt>
          <dd>{{ selected.account_name }} · {{ selected.account_id }} · {{ selected.model }}</dd>
          <dt>{{ t('mediaInputAudit.channel') }}</dt>
          <dd>{{ selected.evidence?.input.video_channel_id || '—' }}</dd>
          <dt>{{ t('mediaInputAudit.time') }}</dt>
          <dd>{{ date(selected.evidence?.input.requested_at || selected.created_at) }}</dd>
          <dt>{{ t('mediaInputAudit.expires') }}</dt>
          <dd>{{ date(selected.expires_at) }}</dd>
          <dt>{{ t('mediaInputAudit.reviewer') }}</dt>
          <dd>{{ selected.reviewer || '—' }} · {{ selected.evidence?.result.policy_version }}</dd>
          <dt>{{ t('mediaInputAudit.notification') }}</dt>
          <dd>
            {{
              t(
                selected.notified_at
                  ? 'mediaInputAudit.sent'
                  : selected.notify_pending
                    ? 'mediaInputAudit.pending'
                    : 'mediaInputAudit.disabled'
              )
            }}
            · {{ selected.notify_attempts }}
          </dd>
          <dt>{{ t('mediaInputAudit.reason') }}</dt>
          <dd>{{ selected.category }} · {{ selected.reason }}</dd>
        </dl>
        <div
          v-for="item in [
            { label: 'original', value: selected.evidence?.input.prompt },
            { label: 'normalized', value: selected.evidence?.normalized_prompt }
          ]"
          :key="item.label"
        >
          <h4 class="font-medium">{{ t('mediaInputAudit.' + item.label) }}</h4>
          <pre
            class="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-gray-50 p-3 dark:bg-dark-700"
            >{{ item.value }}</pre>
        </div>
        <pre class="max-h-48 overflow-auto whitespace-pre-wrap">{{
          selected.evidence?.result
        }}</pre>
        <div
          v-for="(asset, index) in selected.evidence?.assets"
          :key="index"
          class="space-y-2 rounded border border-gray-200 p-3 dark:border-dark-600"
        >
          <p class="break-all">#{{ index }} · {{ asset.original_url }}</p>
          <p class="break-all font-mono text-xs">SHA-256: {{ asset.sha256 }}</p>
          <button type="button" class="btn btn-secondary" @click="download(index)">
            {{ t('mediaInputAudit.download') }}
          </button>
        </div>
        <p v-if="!selected.evidence?.assets.length">{{ t('mediaInputAudit.noAssets') }}</p>
      </div>
    </BaseDialog>
  </section>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import {
  mediaInputAuditAPI,
  type MediaAuditConfig,
  type MediaAuditEvent
} from '@/api/admin/mediaInputAudit'
import { mediaAuditMessages } from './messages'
import { extractApiErrorMessage } from '@/utils/apiError'
const { t } = useI18n({ useScope: 'local', messages: mediaAuditMessages })
const route = useRoute()
const slots = ['primary', 'fallback'] as const
const config = ref<MediaAuditConfig | null>(null)
const emails = ref(''),
  error = ref(''),
  notice = ref(''),
  busy = ref(false)
const probePrompt = ref(''),
  probeImages = ref(''),
  probeResult = ref('')
const events = ref<MediaAuditEvent[]>([]),
  selected = ref<MediaAuditEvent | null>(null),
  page = ref(1)
function failure(e: unknown) {
  error.value = extractApiErrorMessage(e, t('mediaInputAudit.failure'))
}
function lines(value: string) {
  return value
    .split(/\r?\n/)
    .map((v) => v.trim())
    .filter(Boolean)
}
function date(value: string) {
  return new Date(value).toLocaleString()
}
async function save() {
  if (!config.value) return
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    config.value = await mediaInputAuditAPI.saveConfig({
      ...config.value,
      admin_emails: lines(emails.value)
    })
    notice.value = t('mediaInputAudit.saved')
  } catch (e) {
    failure(e)
  } finally {
    busy.value = false
  }
}
async function probe(slot: string) {
  busy.value = true
  error.value = ''
  probeResult.value = ''
  try {
    probeResult.value = JSON.stringify(
      await mediaInputAuditAPI.probe(slot, probePrompt.value, lines(probeImages.value)),
      null,
      2
    )
  } catch (e) {
    failure(e)
  } finally {
    busy.value = false
  }
}
async function loadEvents() {
  try {
    events.value = await mediaInputAuditAPI.listEvents(page.value)
  } catch (e) {
    failure(e)
  }
}
async function changePage(delta: number) {
  page.value += delta
  await loadEvents()
}
async function openEvent(id: string) {
  try {
    selected.value = await mediaInputAuditAPI.getEvent(id)
  } catch (e) {
    failure(e)
  }
}
async function download(index: number) {
  if (!selected.value) return
  try {
    const blob = await mediaInputAuditAPI.getAsset(selected.value.id, index)
    const url = URL.createObjectURL(blob),
      link = document.createElement('a')
    link.href = url
    link.download = `evidence-${selected.value.id}-${index}`
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (e) {
    failure(e)
  }
}
onMounted(async () => {
  try {
    config.value = await mediaInputAuditAPI.getConfig()
    emails.value = config.value.admin_emails.join('\n')
  } catch (e) {
    failure(e)
  }
  await loadEvents()
  const id = route?.query.media_event
  if (typeof id === 'string') await openEvent(id)
})
</script>
