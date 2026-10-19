<template>
  <details class="card p-4">
    <summary class="cursor-pointer font-medium">{{ t('videoChannels.manage') }}</summary>
    <div class="mt-3 space-y-2">
      <div v-for="channel in channels" :key="channel.id" class="flex items-center gap-3">
        <input v-model="channel.name" class="input max-w-xs" maxlength="80" :aria-label="t('videoChannels.name')" />
        <label class="text-sm"><input v-model="channel.enabled" type="checkbox" /> {{ t('videoChannels.enabled') }}</label>
        <span v-if="!channel.id" class="text-xs text-gray-500">{{ t('videoChannels.defaultHint') }}</span>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="save(channel)">{{ t('common.save') }}</button>
      </div>
      <p v-if="message" class="text-sm">{{ message }}</p>
    </div>
  </details>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { listVideoChannels, saveVideoChannel, type VideoChannel } from '@/api/admin/videoChannels'
const { t } = useI18n(); const channels = ref<VideoChannel[]>([]); const busy = ref(false); const message = ref('')
const emit = defineEmits<{ changed: [] }>()
async function save(channel: VideoChannel) {
  busy.value = true; message.value = ''
  try { await saveVideoChannel(channel); message.value = t('videoChannels.saved'); emit('changed') }
  catch { message.value = t('videoChannels.saveFailed') } finally { busy.value = false }
}
onMounted(async () => { try { channels.value = await listVideoChannels() } catch { message.value = t('videoChannels.loadFailed') } })
</script>
