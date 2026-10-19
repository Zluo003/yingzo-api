import type { VideoGroupPricingRule } from '@/types'

/**
 * 视频分组计费：按 模型 × 分辨率 配置每秒单价（元/秒）。
 *
 * 档位取下游模型规格：4K 属于 seedance-2.0 的官方档位，且 aigod 目录里确实有
 * seedance-2.0-4k，因此必须可配价——否则 4K 请求会在选择账号之前就以
 * video_pricing_rule_not_found 失败。newtoken 不提供 4K，但它是否被选中由账号侧
 * 的分辨率白名单与适配器能力决定，与本表无关。
 */
export const VIDEO_PRICING_MODEL_RESOLUTIONS: {
  model: string
  resolutions: string[]
}[] = [
  { model: 'seedance-2.0', resolutions: ['480p', '720p', '1080p', '4K'] },
  { model: 'seedance-2.0-fast', resolutions: ['480p', '720p'] },
  { model: 'seedance-2.5', resolutions: ['480p', '720p', '1080p'] },
  { model: 'grok-imagine-video-1.5', resolutions: ['480p', '720p', '1080p'] },
  { model: 'minimax-h3', resolutions: ['768p', '2K'] },
  { model: 'minimax-h3-max', resolutions: ['480p', '768p', '1080p'] },
  { model: 'kling-v3-omni', resolutions: ['720p', '1080p', '4K'] },
  { model: 'kling-v4', resolutions: ['720p', '1080p'] }
]

export const VIDEO_PRICING_MODELS = VIDEO_PRICING_MODEL_RESOLUTIONS.map(
  (entry) => entry.model
)

export function videoPricingResolutions(model: string): string[] {
  return (
    VIDEO_PRICING_MODEL_RESOLUTIONS.find((entry) => entry.model === model)
      ?.resolutions ?? []
  )
}

/** 表单行：价格允许为空字符串以支持清空输入框。 */
export interface VideoPricingRuleRow {
  video_channel_id?: string
  billing_unit?: 'second' | 'request'
  free_input_images?: number | string | null
  model_code: string
  resolution: string
  credits_per_second: number | string | null
  enabled: boolean
}

export function createVideoPricingRuleRow(
  overrides: Partial<VideoPricingRuleRow> = {}
): VideoPricingRuleRow {
  const model = overrides.model_code ?? VIDEO_PRICING_MODELS[0]
  return {
    video_channel_id: overrides.video_channel_id ?? '',
    billing_unit: overrides.billing_unit ?? 'second',
    free_input_images: overrides.free_input_images ?? null,
    model_code: model,
    resolution: overrides.resolution ?? videoPricingResolutions(model)[0] ?? '720p',
    credits_per_second: overrides.credits_per_second ?? null,
    enabled: overrides.enabled ?? true
  }
}

/** 后端回传的规则 → 表单行；保持后端顺序，便于编辑时对照。 */
export function createVideoPricingRulesForm(
  rules?: VideoGroupPricingRule[] | null
): VideoPricingRuleRow[] {
  if (!rules || rules.length === 0) return []
  return rules.map((rule) =>
    createVideoPricingRuleRow({
      video_channel_id: rule.video_channel_id,
      billing_unit: rule.billing_unit,
      free_input_images: rule.free_input_images,
      model_code: rule.model_code,
      resolution: rule.resolution,
      credits_per_second: rule.billing_unit === 'request' ? rule.credits_per_request : rule.credits_per_second,
      enabled: rule.enabled ?? true
    })
  )
}

/**
 * 校验并序列化表单行。返回 null 表示存在错误，调用方据此中止提交。
 * 后端 video_group_pricing_rules 对 (group_id, model_code, resolution) 唯一，
 * 因此重复组合必须在前端拦下，否则会拿一个数据库报错。
 */
export function serializeVideoPricingRules(
  rows: VideoPricingRuleRow[]
): VideoGroupPricingRule[] | null {
  const seen = new Set<string>()
  const freeImagesByModel = new Map<string, number | null>()
  const out: VideoGroupPricingRule[] = []
  for (const row of rows) {
    const model = row.model_code?.trim()
    const resolution = row.resolution?.trim()
    if (!model || !resolution) return null
    const key = `${model}\u0000${row.video_channel_id || ''}\u0000${resolution}`
    if (seen.has(key)) return null
    seen.add(key)
    // 必须显式拒绝空值：Number(null) === 0 会把留空的输入框静默变成“免费”，
    // 而运营的本意显然是“还没填”。
    const rawPrice = row.credits_per_second
    if (rawPrice === null || rawPrice === undefined || rawPrice === '') {
      return null
    }
    const price = Number(rawPrice)
    if (!Number.isFinite(price) || price < 0) return null
    const freeImages = row.free_input_images === null || row.free_input_images === undefined || row.free_input_images === "" ? null : Number(row.free_input_images)
    if (freeImages !== null && (!Number.isInteger(freeImages) || freeImages < 0)) return null
    if (model === 'minimax-h3' || model === 'minimax-h3-max') {
      const prior = freeImagesByModel.get(model)
      if (freeImagesByModel.has(model) && prior !== freeImages) return null
      freeImagesByModel.set(model, freeImages)
    }
    out.push({
      free_input_images: freeImages,
      model_code: model,
      resolution,
      ...(row.video_channel_id ? { video_channel_id: row.video_channel_id } : {}),
      ...(row.billing_unit === 'request' ? { billing_unit: 'request' as const, credits_per_request: price } : {}),
      credits_per_second: row.billing_unit === 'request' ? 0 : price,
      reference_video_multiplier: 1,
      enabled: row.enabled !== false
    })
  }
  return out
}

export function setVideoModelFreeImages(
  form: { video_pricing_rules: VideoPricingRuleRow[] },
  model: string,
  freeImages: number | string | null
): void {
  for (const row of form.video_pricing_rules) {
    if (row.model_code === model) row.free_input_images = freeImages
  }
}

/** 新增一行：默认取第一个模型与其默认分辨率，价格留空由运营填写。 */
export function addVideoPricingRule(form: { video_pricing_rules: VideoPricingRuleRow[] }): void {
  form.video_pricing_rules.push(createVideoPricingRuleRow())
}

export function removeVideoPricingRule(
  form: { video_pricing_rules: VideoPricingRuleRow[] },
  index: number
): void {
  form.video_pricing_rules.splice(index, 1)
}

/**
 * 切换模型后把分辨率收敛到该模型的可选档位，否则会留下一个不属于该模型的
 * 分辨率，后端虽然照收，但那个组合永远不会被请求命中。
 */
export function onVideoPricingModelChange(
  form: { video_pricing_rules: VideoPricingRuleRow[] },
  index: number
): void {
  const row = form.video_pricing_rules[index]
  if (!row) return
  if (!row.model_code.startsWith('seedance-')) { row.video_channel_id = ''; row.billing_unit = 'second' }
  const allowed = videoPricingResolutions(row.model_code)
  if (!allowed.includes(row.resolution)) {
    row.resolution = allowed[0] ?? row.resolution
  }
}

/** 是否存在重复的 模型 + 分辨率 组合。 */
export function hasDuplicateVideoPricingRule(
  rows: VideoPricingRuleRow[]
): boolean {
  const seen = new Set<string>()
  for (const row of rows) {
    const key = `${row.model_code?.trim()}\u0000${row.video_channel_id || ''}\u0000${row.resolution?.trim()}`
    if (seen.has(key)) return true
    seen.add(key)
  }
  return false
}
