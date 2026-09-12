/**
 * 二开 i18n 覆盖层与上游语言包按命名空间深合并。
 *
 * 为什么需要深合并：fork.ts 的顶层键包含 `admin`，而上游的 locales/<lang>/index.ts
 * 已经把各 admin 子模块展开成一个 `admin` 命名空间。直接 `...fork` 会整体覆盖
 * 上游的 admin 命名空间，并被 localesNoKeyCollision 用例判定为顶层键冲突。
 */
type Messages = Record<string, unknown>

function isPlainObject(value: unknown): value is Messages {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function deepMergeMessages(base: Messages, overlay: Messages): Messages {
  const merged: Messages = { ...base }
  for (const [key, overlayValue] of Object.entries(overlay)) {
    const baseValue = merged[key]
    if (isPlainObject(baseValue) && isPlainObject(overlayValue)) {
      merged[key] = deepMergeMessages(baseValue, overlayValue)
      continue
    }
    merged[key] = overlayValue
  }
  return merged
}
