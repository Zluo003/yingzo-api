<template>
  <div>
    <label class="input-label">{{ t('admin.accounts.video.upstreamModels') }}</label>
    <p class="input-hint">{{ t('admin.accounts.video.upstreamModelsHint') }}</p>
    <div class="mt-2 space-y-3">
      <label
        v-for="model in models"
        :key="model"
        class="grid items-center gap-2 text-sm text-gray-700 dark:text-gray-300 sm:grid-cols-2"
      >
        <span class="break-all">{{ model }}</span>
        <input
          :data-testid="`video-upstream-model-${model}`"
          :value="modelValue[model] ?? ''"
          type="text"
          pattern="[^*]*"
          class="input font-mono"
          :placeholder="t('admin.accounts.video.upstreamModelDefault')"
          @input="setUpstreamModel(model, $event)"
        />
      </label>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const props = defineProps<{
  models: readonly string[]
  modelValue: Record<string, string>
}>()
const emit = defineEmits<{
  'update:modelValue': [value: Record<string, string>]
}>()

function setUpstreamModel(model: string, event: Event) {
  emit('update:modelValue', {
    ...props.modelValue,
    [model]: (event.target as HTMLInputElement).value
  })
}
</script>
