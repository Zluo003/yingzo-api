<template>
  <div class="space-y-3 min-w-[460px]">
    <div v-for="channel in channels" :key="channel.id" class="rounded border border-gray-200 p-3 dark:border-dark-600">
      <div class="mb-2 flex items-center gap-3">
        <strong>{{ channel.name }}</strong>
        <span v-if="!channel.available" class="text-xs text-gray-400">{{ t('videoChannels.noAccounts') }}</span>
        <select class="input w-28" :value="unit(channel.id)" :aria-label="channel.name + ' ' + t('videoChannels.billing')" @change="setUnit(channel.id, ($event.target as HTMLSelectElement).value)">
          <option value="second">{{ t('videoChannels.perSecond') }}</option><option value="request">{{ t('videoChannels.perRequest') }}</option>
        </select>
      </div>
      <div class="flex flex-wrap gap-3">
        <label v-for="resolution in resolutions" :key="resolution" class="space-y-1 text-xs">
          <span class="flex items-center gap-1"><input type="checkbox" :checked="price(channel.id, resolution)?.enabled !== false && !!price(channel.id, resolution)" @change="change(channel.id,resolution,{enabled:($event.target as HTMLInputElement).checked})" />{{ resolution }}</span>
          <input class="input w-24" type="number" min="0" step="0.001" :value="price(channel.id,resolution)?.unit_price ?? ''" :aria-label="channel.name + ' ' + resolution" :placeholder="t('videoChannels.price')" @input="setPrice(channel.id,resolution,($event.target as HTMLInputElement).value)" />
        </label>
      </div>
      <p v-if="unit(channel.id) === 'request'" class="mt-2 text-xs text-gray-500">{{ t('videoChannels.requestHint') }}</p>
    </div>
  </div>
</template>
<script setup lang="ts">
import { reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AgentModelPrice } from '@/api/admin/agentModels'
import type { VideoChannel } from '@/api/admin/videoChannels'
const props = defineProps<{ channels: VideoChannel[]; resolutions: string[]; modelValue: AgentModelPrice[] }>()
const emit = defineEmits<{ 'update:modelValue': [AgentModelPrice[]] }>()
const { t } = useI18n()
const units = reactive<Record<string, 'second' | 'request'>>({})
function price(id: string, resolution: string) { return props.modelValue.find(p => (p.video_channel_id || '') === id && p.resolution === resolution) }
function unit(id: string): 'second' | 'request' { return units[id] ?? (props.modelValue.find(p => (p.video_channel_id || '') === id)?.billing_unit === 'request' ? 'request' : 'second') }
function setUnit(id: string, value: string) { units[id] = value === 'request' ? 'request' : 'second'; emit('update:modelValue', props.modelValue.map(p => (p.video_channel_id || '') === id ? { ...p, billing_unit: units[id] } : p)) }
function change(id: string, resolution: string, patch: Partial<AgentModelPrice>) {
  const existing = price(id,resolution)
  // An empty enabled tier has no price and must not silently become free.
  const next = { video_channel_id:id, resolution, billing_unit:unit(id), unit_price:Number.NaN, enabled:false, ...existing, ...patch } as AgentModelPrice
  emit('update:modelValue',[...props.modelValue.filter(p => !((p.video_channel_id || '') === id && p.resolution === resolution)),next])
}
function setPrice(id: string, resolution: string, raw: string) { change(id,resolution,{unit_price:raw.trim() === '' ? Number.NaN : Number(raw)}) }
</script>
