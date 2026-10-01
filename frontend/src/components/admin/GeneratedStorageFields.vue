<template>
  <div class="mt-5 space-y-4" data-testid="generated-storage-settings">
    <label class="inline-flex items-center gap-2 text-sm text-gray-900 dark:text-white">
      <input v-model="config.async_images_enabled" type="checkbox" data-testid="async-images-enabled" />
      {{ t('admin.backup.imageStorage.enabled') }}
    </label>
    <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.assetStorage.generated.asyncHint') }}</p>
    <label class="flex items-center gap-2 text-sm"><input v-model="config.async_music_enabled" type="checkbox" data-testid="async-music-enabled" />{{ t('suno.enabled') }}</label>
    <p class="text-sm text-gray-500">{{ t('suno.asyncHint') }}</p>
    <p v-if="migrationPending" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-900 dark:bg-amber-950 dark:text-amber-200">{{ t('admin.assetStorage.generated.migrationHint') }}</p>
    <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
      <label class="space-y-1 text-sm text-gray-600 dark:text-gray-400">
        <span>{{ t('admin.assetStorage.backend.label') }}</span>
        <select v-model="config.backend" class="input w-full" data-testid="generated-storage-backend">
          <option value="local">{{ t('admin.assetStorage.backend.local') }}</option>
          <option value="s3">S3 / R2</option>
        </select>
      </label>
      <label class="space-y-1 text-sm text-gray-600 dark:text-gray-400">
        <span>{{ t('admin.assetStorage.backend.localDirLabel') }}</span>
        <input v-model="config.local_dir" class="input w-full" :placeholder="t('admin.assetStorage.backend.localDirPlaceholder')" />
      </label>
      <template v-if="config.backend === 's3'">
        <label v-for="field in s3Fields" :key="field.key" class="space-y-1 text-sm text-gray-600 dark:text-gray-400">
          <span>{{ t(field.label) }}</span>
          <input v-model="config.s3[field.key]" class="input w-full" :type="field.key === 'secret_access_key' ? 'password' : 'text'" :autocomplete="field.key === 'secret_access_key' ? 'new-password' : 'off'" :placeholder="field.key === 'secret_access_key' && config.secret_access_key_configured ? t('admin.backup.s3.secretConfigured') : ''" />
        </label>
        <label class="space-y-1 text-sm text-gray-600 dark:text-gray-400">
          <span>{{ t('admin.backup.imageStorage.presignExpiryHours') }}</span>
          <input v-model.number="config.presign_expiry_hours" class="input w-full" type="number" min="1" max="168" />
        </label>
        <label class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-400">
          <input v-model="config.s3.force_path_style" type="checkbox" />{{ t('admin.backup.s3.forcePathStyle') }}
        </label>
        <p class="text-sm text-gray-500 md:col-span-2">{{ t('admin.assetStorage.generated.directDownloadHint') }}</p>
      </template>
    </div>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { GeneratedStorageConfig } from '@/api/admin/fileStorage'
defineProps<{ migrationPending?: boolean }>()
const config = defineModel<GeneratedStorageConfig>('config', { required: true })
const { t } = useI18n()
const s3Fields = [
  { key: 'bucket', label: 'admin.backup.imageStorage.bucket' },
  { key: 'prefix', label: 'admin.backup.imageStorage.prefix' },
  { key: 'endpoint', label: 'admin.backup.s3.endpoint' },
  { key: 'region', label: 'admin.backup.s3.region' },
  { key: 'access_key_id', label: 'admin.backup.s3.accessKeyId' },
  { key: 'secret_access_key', label: 'admin.backup.s3.secretAccessKey' },
  { key: 'custom_domain', label: 'admin.backup.imageStorage.publicBaseUrl' },
] as const
</script>
