<template>
  <div class="flex flex-wrap items-center gap-3">
    <slot name="before"></slot>
    <button @click="$emit('refresh')" :disabled="loading" class="btn btn-secondary">
      <Icon name="refresh" size="md" :class="[loading ? 'animate-spin' : '']" />
    </button>
    <slot name="after"></slot>
    <slot name="beforeCreate"></slot>
    <button @click="$emit('create')" class="btn btn-primary">{{ t('admin.accounts.createAccount') }}</button>
    <button
      type="button"
      data-testid="create-video-account"
      @click="$emit('create-video')"
      class="btn btn-secondary"
    >
      <Icon name="video" size="sm" class="mr-1.5" />
      {{ t('admin.accounts.createVideoAccount') }}
    </button>
    <button
      type="button"
      data-testid="create-image-account"
      @click="$emit('create-image')"
      class="btn btn-secondary"
    >
      <Icon name="sparkles" size="sm" class="mr-1.5" />
      {{ t('admin.accounts.createImageAccount') }}
    </button>
    <slot name="afterCreate"></slot>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

defineProps(['loading'])
defineEmits(['refresh', 'create', 'create-video', 'create-image'])

const { t } = useI18n()
</script>
