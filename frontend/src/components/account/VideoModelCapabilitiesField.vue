<template>
  <div>
    <label class="input-label">{{ t('admin.accounts.video.capabilities') }}</label>
    <p class="input-hint">{{ t('admin.accounts.video.capabilitiesHint') }}</p>
    <div class="mt-2 space-y-4">
      <div v-for="model in models" :key="model" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
        <div class="mb-3 text-sm font-medium text-gray-900 dark:text-white">{{ model }}</div>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <label v-for="field in countFields" :key="field" class="text-xs text-gray-600 dark:text-gray-400">
            {{ t(`admin.accounts.video.${field}`) }}
            <input
              :data-testid="`video-capability-${model}-${field}`"
              type="number"
              min="0"
              :max="limit(model, field)"
              step="1"
              class="input mt-1"
              :value="modelValue[model]?.[field] ?? limit(model, field)"
              @input="setCount(model, field, $event)"
            />
          </label>
        </div>
        <div class="mt-3 flex flex-wrap gap-x-5 gap-y-2">
          <label v-for="field in modeFields" :key="field" class="flex items-center gap-2 text-xs text-gray-700 dark:text-gray-300">
            <input
              :data-testid="`video-capability-${model}-${field}`"
              type="checkbox"
              :checked="modelValue[model]?.[field] ?? true"
              @change="setMode(model, field, $event)"
            />
            {{ t(`admin.accounts.video.${field}`) }}
          </label>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import {
  VIDEO_MODEL_REFERENCE_LIMITS,
  defaultVideoModelCapabilities,
  type VideoModelCapabilities
} from '@/views/admin/videoModelCapabilities'

const { t } = useI18n()
const props = defineProps<{
  models: readonly string[]
  modelValue: Record<string, VideoModelCapabilities>
}>()
const emit = defineEmits<{
  'update:modelValue': [value: Record<string, VideoModelCapabilities>]
}>()

const countFields = ['max_reference_images', 'max_reference_videos', 'max_reference_audios'] as const
const modeFields = ['text_to_video', 'image_to_video', 'start_end_to_video', 'reference_to_video'] as const

function limit(model: string, field: typeof countFields[number]): number {
  const index = countFields.indexOf(field)
  return VIDEO_MODEL_REFERENCE_LIMITS[model]?.[index] ?? 0
}

function setCount(model: string, field: typeof countFields[number], event: Event) {
  const raw = (event.target as HTMLInputElement).value
  const value = Number(raw)
  if (raw === '' || !Number.isInteger(value) || value < 0 || value > limit(model, field)) return
  emit('update:modelValue', {
    ...props.modelValue,
    [model]: { ...(props.modelValue[model] ?? defaultVideoModelCapabilities(model)), [field]: value }
  })
}

function setMode(model: string, field: typeof modeFields[number], event: Event) {
  emit('update:modelValue', {
    ...props.modelValue,
    [model]: { ...(props.modelValue[model] ?? defaultVideoModelCapabilities(model)), [field]: (event.target as HTMLInputElement).checked }
  })
}
</script>
