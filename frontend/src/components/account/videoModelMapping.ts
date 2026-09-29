import { buildModelMappingObject } from '@/composables/useModelWhitelist'

/** Only selected downstream models may be saved; identity entries use the adapter's default. */
export function buildVideoModelMapping(
  models: string[],
  upstreamModels: Record<string, string>
): Record<string, string> | null {
  return buildModelMappingObject(
    'combined',
    models,
    models.map((from) => ({ from, to: upstreamModels[from] ?? '' }))
  )
}
