import zh from '@/i18n/locales/zh/mediaInputAudit'
import en from '@/i18n/locales/en/mediaInputAudit'

// The app uses vue-i18n's runtime-only build. These static labels need no runtime
// compilation or unsafe-eval; global string catalogs still support schema checks.
const staticMessages = (labels: Record<string, string>) =>
  Object.fromEntries(Object.entries(labels).map(([key, value]) => [key, () => value]))
export const mediaAuditMessages = {
  zh: { mediaInputAudit: staticMessages(zh) },
  en: { mediaInputAudit: staticMessages(en) }
}
