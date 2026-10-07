/** Build a document navigation URL within this frontend's deployment base. */
export function buildFrontendUrl(path: string): string {
  const base = import.meta.env.BASE_URL.replace(/\/+$/, '')
  return `${base}/${path.replace(/^\/+/, '')}`
}
