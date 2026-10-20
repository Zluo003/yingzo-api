<template>
  <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
    <label class="flex items-center gap-2 font-medium">
      <input
        data-testid="media-input-audit-enabled"
        type="checkbox"
        :checked="modelValue"
        @change="emit('update:modelValue', ($event.target as HTMLInputElement).checked)"
      />
      {{ t('mediaInputAudit.accountTitle') }}
    </label>
    <p class="mt-2 text-sm text-gray-500">{{ t('mediaInputAudit.accountHint') }}</p>
    <a class="mt-2 inline-block text-sm text-primary-600" href="/admin/risk-control">{{
      t('mediaInputAudit.settings')
    }}</a>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { mediaAuditMessages } from '@/components/admin/mediaAudit/messages'
defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const { t } = useI18n({ useScope: 'local', messages: mediaAuditMessages })
</script>
