/**
 * 视频模型「官方时长范围」与账号级时长白名单的配置辅助。
 *
 * 镜像的后端实现（改这里必须同步改后端，反之亦然）：
 * - internal/service/video.go → videoModelSpecs 的 MinSeconds / MaxSeconds
 * - internal/service/admin_account.go → VideoModelDurationsExtraKey /
 *   normalizeVideoModelDurationsExtra（写入 extra.video_model_durations 的校验规则）
 *
 * 语义与分辨率白名单一致：某模型未列入 => 该模型不限制时长（按模型规格全范围
 * 参与调度）；列入后只有列出的秒数会调度到该账号。
 *
 * 模型与上游解耦：这里不维护"哪个上游支持哪些时长"的矩阵——上游硬约束由适配器
 * 的渠道闸门在调度时判定，运营在账号上按模型勾选该 key 实际支持的时长即可。
 */
import { VIDEO_MODEL_CODES } from './videoModelResolutions'

interface VideoModelDurationRange {
  min: number
  max: number
}

/** 各模型的官方时长范围，与后端 videoModelSpecs 的 Min/MaxSeconds 一致。 */
const VIDEO_MODEL_DURATION_RANGES: Record<string, VideoModelDurationRange> = {
  'seedance-2.0': { min: 4, max: 15 },
  'seedance-2.0-fast': { min: 4, max: 15 },
  'seedance-2.5': { min: 4, max: 30 },
  'grok-imagine-video-1.5': { min: 1, max: 15 },
  'kling-v3-omni': { min: 3, max: 15 }
}

/** 模型官方时长档位（整数秒，升序）；未接入的模型返回空数组。 */
export function videoModelDurations(model: string): number[] {
  const range = VIDEO_MODEL_DURATION_RANGES[model]
  if (!range || range.max < range.min) return []
  const durations: number[] = []
  for (let seconds = range.min; seconds <= range.max; seconds++) {
    durations.push(seconds)
  }
  return durations
}

/** 默认勾选：模型官方时长全档勾上（运营按账号实际能力收敛）。 */
export function defaultVideoModelDurations(): Record<string, number[]> {
  const selection: Record<string, number[]> = {}
  for (const model of VIDEO_MODEL_CODES) {
    selection[model] = videoModelDurations(model)
  }
  return selection
}

/**
 * 勾选/取消一个时长档：官方范围之外的取值直接忽略，结果按升序排列。
 * 不允许把某个模型勾到空：空条目在下游等价于"不限制该模型的时长"，
 * 与"该账号不服务这个模型"是两回事，后者应通过模型白名单表达。
 */
export function toggleVideoDuration(
  selection: Record<string, readonly number[] | undefined>,
  model: string,
  seconds: number
): Record<string, number[]> {
  const next: Record<string, number[]> = {}
  for (const [key, value] of Object.entries(selection)) {
    if (value) next[key] = [...value]
  }
  const official = videoModelDurations(model)
  if (!official.includes(seconds)) {
    return next
  }
  const current = next[model] ?? []
  const toggled = current.includes(seconds)
    ? current.filter((item) => item !== seconds)
    : [...current, seconds]
  if (toggled.length === 0) {
    return next
  }
  next[model] = official.filter((item) => toggled.includes(item))
  return next
}

/**
 * 表单勾选 → extra.video_model_durations。
 * - `models` 传入时只提交这些模型（弹窗按「已勾选模型」过滤）。
 * - 一个档都没勾的模型整条删除（后端同样丢弃空条目）；全部为空时返回 undefined，
 *   调用方据此完全不写该键 —— 键缺失即「不限制时长」。
 */
export function serializeVideoModelDurations(
  selection: Record<string, readonly number[] | undefined>,
  models: readonly string[] = VIDEO_MODEL_CODES
): Record<string, number[]> | undefined {
  const payload: Record<string, number[]> = {}
  for (const model of models) {
    const official = videoModelDurations(model)
    const picked = Array.from(
      new Set(
        (selection[model] ?? []).filter(
          (value) => typeof value === 'number' && official.includes(value)
        )
      )
    ).sort((a, b) => a - b)
    if (picked.length > 0) {
      payload[model] = picked
    }
  }
  return Object.keys(payload).length > 0 ? payload : undefined
}

/**
 * extra.video_model_durations → 表单勾选。
 * 只保留已接入模型与其官方范围内的整数；未配置的模型回落到全部官方档位
 * （后端把"未配置"解释为不限制，界面必须呈现同一含义）。存了一个空数组
 * （或整份是非法值）时同样回落到"全部官方档位"。
 */
export function parseVideoModelDurations(
  raw: unknown
): Record<string, number[]> {
  const selection: Record<string, number[]> = defaultVideoModelDurations()
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    return selection
  }
  const source = raw as Record<string, unknown>
  const byModel = new Map<string, unknown>()
  for (const [key, value] of Object.entries(source)) {
    byModel.set(key.trim(), value)
  }
  for (const model of VIDEO_MODEL_CODES) {
    const list = byModel.get(model)
    if (!Array.isArray(list)) continue
    const official = videoModelDurations(model)
    const intersected = official.filter((seconds) =>
      list.some(
        (value) =>
          typeof value === 'number' && Number.isInteger(value) && value === seconds
      )
    )
    if (intersected.length > 0) {
      selection[model] = intersected
    }
  }
  return selection
}
