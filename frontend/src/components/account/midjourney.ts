export const MIDJOURNEY_MODEL = 'midjourney-v8.2'
export const MIDJOURNEY_PROVIDER = 'discord_midjourney'
export const MIDJOURNEY_PRICE_TIERS = ['generation', 'select', 'upscale']
export const isMidjourneyAccount = (account: { extra?: Record<string, unknown> | null } | null | undefined): boolean =>
  [MIDJOURNEY_PROVIDER, 'apimart_midjourney'].includes(String(account?.extra?.image_provider || ''))
