export const MIMO_SPEECH_MODELS = ['mimo-v2.5-tts', 'mimo-v2.5-tts-voicedesign']
export const MIMO_SPEECH_PROVIDER = 'mimo'
export const isMiMoSpeechModel = (model: string): boolean => MIMO_SPEECH_MODELS.includes(model)
export const isSpeechAccount = (account: { extra?: Record<string, unknown> | null } | null | undefined): boolean =>
  account?.extra?.speech_provider === MIMO_SPEECH_PROVIDER
