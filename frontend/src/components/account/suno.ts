import type { Account } from '@/types'
export const SUNO_MODEL = 'suno-v6'
export const SUNO_PROVIDER = 'apimart_suno'
// Yingzo Agent 计费配置只暴露这 8 个基础档位：自定义模型变体（`:custom`）是
// 内部计费键，不作为配置入口下发；uploadTask 上传是 sample/uploadCover/
// createModel 的内部步骤，费用已含在对应操作价格中，不设 upload 档位
//（产品决策，见 docs/SUNO_AGENT_INTEGRATION.md）。公共版本仅 v6，无版本后缀档位。
export const SUNO_BILLING_TIERS = ["instrumental", "song", "coverSong", "create_model", "persona", "sample", "sounds", "upload_cover"]

export const SUNO_PRICE_TIERS = ["instrumental", "song", "addInstrumental", "addStem", "addVocals", "adjustSpeed", "alignedLyrics", "bpm", "concat", "coverSong", "create_model", "createVoice", "crop", "download", "extend", "fadeIn", "fadeOut", "generateMp4", "inspo", "lyrics", "mashup", "midi", "persona", "remaster", "removeSection", "replaceMusic", "sample", "sounds", "stems", "stemsAll", "upload_cover", "uploadExtend", "upsampleTags", "vox", "instrumental:custom", "song:custom", "addInstrumental:custom", "addStem:custom", "addVocals:custom", "coverSong:custom", "extend:custom", "inspo:custom", "mashup:custom", "replaceMusic:custom", "sample:custom", "upload_cover:custom", "uploadExtend:custom"]
export function isSunoAccount(account?: Pick<Account, 'extra'> | null): boolean { return account?.extra?.music_provider === SUNO_PROVIDER }
