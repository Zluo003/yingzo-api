export const SEEDREAM_55_MODEL = 'seedream-5-5'
export const SEEDREAM_MODELS = ['seedream-5-0-pro', 'seedream-5-0-flash', SEEDREAM_55_MODEL]
export const SEEDREAM_PROVIDER = 'apimart_seedream'
export const isSeedreamModel = (model: string): boolean => SEEDREAM_MODELS.includes(model)
export const isSeedreamAccount = (account: { extra?: Record<string, unknown> | null } | null | undefined): boolean =>
  account?.extra?.image_provider === SEEDREAM_PROVIDER
