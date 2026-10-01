import type { Account } from '@/types'
export const SUNO_MODEL = 'suno-v6'
export const SUNO_PROVIDER = 'apimart_suno'
export const SUNO_PRICE_TIERS = ['instrumental', 'song']
export function isSunoAccount(account?: Pick<Account, 'extra'> | null): boolean { return account?.extra?.music_provider === SUNO_PROVIDER }
