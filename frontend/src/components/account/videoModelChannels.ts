export function parseVideoModelChannels(value: unknown): Record<string, string> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return Object.fromEntries(Object.entries(value).filter(([model, id]) => model.startsWith('seedance-') && typeof id === 'string'))
}
export function serializeVideoModelChannels(value: Record<string, string>, models: string[]): Record<string, string> {
  return Object.fromEntries(Object.entries(value).filter(([model, id]) => models.includes(model) && model.startsWith('seedance-') && id !== ''))
}
