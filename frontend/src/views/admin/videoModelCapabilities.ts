import { VIDEO_MODEL_CODES } from './videoModelResolutions'

export interface VideoModelCapabilities {
  max_reference_images: number
  max_reference_videos: number
  max_reference_audios: number
  text_to_video: boolean
  image_to_video: boolean
  start_end_to_video: boolean
  reference_to_video: boolean
}

export const VIDEO_MODEL_REFERENCE_LIMITS: Record<string, readonly [number, number, number]> = {
  'seedance-2.0': [9, 3, 3],
  'seedance-2.0-fast': [9, 3, 3],
  'seedance-2.5': [30, 10, 10],
  'grok-imagine-video-1.5': [7, 0, 0],
  'kling-v3-omni': [7, 0, 0]
}

export function defaultVideoModelCapabilities(model: string): VideoModelCapabilities {
  const [images, videos, audios] = VIDEO_MODEL_REFERENCE_LIMITS[model] ?? [0, 0, 0]
  return {
    max_reference_images: images,
    max_reference_videos: videos,
    max_reference_audios: audios,
    text_to_video: true,
    image_to_video: true,
    start_end_to_video: true,
    reference_to_video: true
  }
}

export function parseVideoModelCapabilities(raw: unknown): Record<string, VideoModelCapabilities> {
  const source = raw && typeof raw === 'object' && !Array.isArray(raw)
    ? raw as Record<string, unknown>
    : {}
  const result: Record<string, VideoModelCapabilities> = {}
  for (const model of VIDEO_MODEL_CODES) {
    const defaults = defaultVideoModelCapabilities(model)
    const stored = source[model] && typeof source[model] === 'object' && !Array.isArray(source[model])
      ? source[model] as Record<string, unknown>
      : {}
    const limits = VIDEO_MODEL_REFERENCE_LIMITS[model]
    for (const [index, field] of (['max_reference_images', 'max_reference_videos', 'max_reference_audios'] as const).entries()) {
      const value = stored[field]
      if (typeof value === 'number' && Number.isInteger(value) && value >= 0 && value <= limits[index]) {
        defaults[field] = value
      }
    }
    for (const field of ['text_to_video', 'image_to_video', 'start_end_to_video', 'reference_to_video'] as const) {
      if (typeof stored[field] === 'boolean') defaults[field] = stored[field]
    }
    result[model] = defaults
  }
  return result
}

export function serializeVideoModelCapabilities(
  selection: Record<string, VideoModelCapabilities>,
  models: readonly string[] = VIDEO_MODEL_CODES
): Record<string, VideoModelCapabilities> | undefined {
  const result: Record<string, VideoModelCapabilities> = {}
  for (const model of models) {
    if (!VIDEO_MODEL_REFERENCE_LIMITS[model]) continue
    const current = selection[model] ?? defaultVideoModelCapabilities(model)
    const defaults = defaultVideoModelCapabilities(model)
    if (JSON.stringify(current) !== JSON.stringify(defaults)) {
      result[model] = { ...current }
    }
  }
  return Object.keys(result).length > 0 ? result : undefined
}
