<template>
  <div v-if="seedanceModels.length" class="mt-4 space-y-3">
    <label class="input-label">{{ t('videoChannels.title') }}</label>
    <p class="text-xs text-gray-500">{{ t('videoChannels.accountHint') }}</p>
    <div v-for="model in seedanceModels" :key="model" class="flex items-center gap-3">
      <span class="w-40 text-sm">{{ model }}</span>
      <select class="input flex-1" :value="modelValue[model] || ''" :aria-label="model + ' ' + t('videoChannels.title')" @change="setChannel(model, ($event.target as HTMLSelectElement).value)">
        <option v-for="channel in channels" :key="channel.id" :value="channel.id">{{ channel.name }}{{ channel.enabled ? '' : ` (${t('videoChannels.disabled')})` }}</option>
        <option v-if="modelValue[model] && !channels.some(c => c.id === modelValue[model])" :value="modelValue[model]">{{ modelValue[model] }}</option>
      </select>
    </div>
    <div class="flex gap-2">
      <input v-model="newName" class="input flex-1" :placeholder="t('videoChannels.newName')" maxlength="80" />
      <button type="button" class="btn btn-secondary" :disabled="busy || !newName.trim()" @click="createChannel">{{ t('videoChannels.create') }}</button>
    </div>
    <p v-if="error" class="text-sm text-red-500">{{ error }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { listVideoChannels, saveVideoChannel, type VideoChannel } from '@/api/admin/videoChannels'
const props = defineProps<{ models: string[]; modelValue: Record<string, string> }>()
const emit = defineEmits<{ 'update:modelValue': [Record<string, string>] }>()
const { t } = useI18n()
const channels = ref<VideoChannel[]>([{ id: '', name: t('videoChannels.default'), enabled: true }])
const seedanceModels = computed(() => props.models.filter(m => m.startsWith('seedance-')))
const newName = ref(''); const busy = ref(false); const error = ref('')
function setChannel(model: string, id: string) { emit('update:modelValue', { ...props.modelValue, [model]: id }) }
async function createChannel() {
  busy.value = true; error.value = ''
  try { const channel = await saveVideoChannel({ id: '', name: newName.value.trim(), enabled: true }, true); channels.value.push(channel); newName.value = '' }
  catch (e) { error.value = e instanceof Error ? e.message : t('videoChannels.saveFailed') }
  finally { busy.value = false }
}
onMounted(async () => { try { channels.value = await listVideoChannels() } catch { error.value = t('videoChannels.loadFailed') } })
</script>
