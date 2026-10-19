<template>
  <div v-if="model.startsWith('seedance-')" class="flex gap-2">
    <select class="input" :value="channelId || ''" :aria-label="t('videoChannels.title')" @change="emit('update:channelId', ($event.target as HTMLSelectElement).value)">
      <option v-for="channel in channels" :key="channel.id" :value="channel.id">{{ channel.name }}</option>
      <option v-if="channelId && !channels.some(c => c.id === channelId)" :value="channelId">{{ channelId }}</option>
    </select>
    <select class="input" :value="billingUnit || 'second'" :aria-label="t('videoChannels.billing')" @change="emit('update:billingUnit', ($event.target as HTMLSelectElement).value as 'second' | 'request')">
      <option value="second">{{ t('videoChannels.perSecond') }}</option><option value="request">{{ t('videoChannels.perRequest') }}</option>
    </select>
  </div>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { listVideoChannels, type VideoChannel } from '@/api/admin/videoChannels'
defineProps<{ model: string; channelId?: string; billingUnit?: string }>()
const emit = defineEmits<{ 'update:channelId': [string]; 'update:billingUnit': ['second' | 'request'] }>()
const { t } = useI18n(); const channels = ref<VideoChannel[]>([{ id:'', name:t('videoChannels.default'), enabled:true }])
onMounted(async () => { try { channels.value = await listVideoChannels() } catch { /* Preserve stored IDs on a failed read. */ } })
</script>
