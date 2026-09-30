export const MIDJOURNEY_MODEL = 'midjourney-v8.2'
export const MIDJOURNEY_PROVIDER = 'apimart_midjourney'
export const MIDJOURNEY_PRICE_TIERS = ['generation', 'upscale']
export const isMidjourneyAccount = (account: { extra?: Record<string, unknown> | null } | null | undefined): boolean =>
  account?.extra?.image_provider === MIDJOURNEY_PROVIDER
