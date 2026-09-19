/**
 * 视频模型「官方分辨率档位」与账号级分辨率白名单的配置辅助。
 *
 * 镜像的后端实现（改这里必须同步改后端，反之亦然）：
 * - internal/service/video.go → videoModelSpecs / SupportedVideoModels（模型官方档位）
 * - internal/service/admin_account.go → VideoModelResolutionsExtraKey /
 *   normalizeVideoModelResolutionsExtra（写入 extra.video_model_resolutions 的校验规则）
 *
 * 语义：某模型未列入 => 该模型不限制分辨率（模型维度本身已由 model_mapping 白名单约束）；
 * 列入后只有列出的档位会参与该账号的调度。
 *
 * 模型与上游解耦：这里不维护"哪个上游能服务哪些档位"的矩阵——上游能不能生成由
 * 适配器的渠道闸门在调度时判定，运营在账号上按模型勾选该 key 实际支持的档位即可。
 *
 * 注意 4K 的官方写法是大写 K：后端按大小写不敏感匹配后回写官方写法，
 * 因此前 后端之间只传官方字符串，不要用 ToLower 的副本。
 */

export interface VideoModelResolutionSpec {
  model: string
  resolutions: string[]
}

/** 模型官方档位，顺序即后端 SupportedVideoResolutions() 的顺序（也是提交顺序）。 */
export const VIDEO_MODEL_RESOLUTIONS: VideoModelResolutionSpec[] = [
  { model: 'seedance-2.0', resolutions: ['480p', '720p', '1080p', '4K'] },
  { model: 'seedance-2.0-fast', resolutions: ['480p', '720p'] },
  { model: 'seedance-2.5', resolutions: ['480p', '720p', '1080p'] },
  { model: 'grok-imagine-video-1.5', resolutions: ['480p', '720p', '1080p'] },
  { model: 'kling-v3-omni', resolutions: ['720p', '1080p', '4K'] }
]

/** 对外提供的视频模型清单，与后端 SupportedVideoModels() 一致。 */
export const VIDEO_MODEL_CODES: string[] = VIDEO_MODEL_RESOLUTIONS.map(
  (entry) => entry.model
)

/** 视频上游平台名，与后端 supportedVideoProviders 对齐。 */
export type VideoProviderName = 'aigod' | 'newtoken' | 'mikuapi' | 'jingyu'

/**
 * 各上游适配器当前能路由的模型——仅用于「上游平台」下拉的分组提示，
 * 镜像后端各适配器 Compatible 的支持范围（见 video_model_resolutions_test.go
 * 的服务矩阵固化）。真正的调度判定在适配器渠道闸门：这里不限制任何配置，
 * 只是帮运营在选定模型后快速找到匹配的平台；平台与模型的实际组合以调度为准。
 */
export const VIDEO_PROVIDER_SUPPORTED_MODELS: Record<
  VideoProviderName,
  string[]
> = {
  aigod: ['seedance-2.0', 'seedance-2.0-fast', 'seedance-2.5'],
  newtoken: ['seedance-2.0', 'seedance-2.0-fast', 'seedance-2.5'],
  mikuapi: [...VIDEO_MODEL_CODES],
  jingyu: ['seedance-2.0', 'seedance-2.5']
}

/** 报告哪些上游平台能服务给定的全部模型；未选模型时返回全部平台。 */
export function videoProvidersServing(
  models: readonly string[]
): VideoProviderName[] {
  const providers = Object.keys(
    VIDEO_PROVIDER_SUPPORTED_MODELS
  ) as VideoProviderName[]
  if (models.length === 0) return providers
  return providers.filter((provider) =>
    models.every((model) =>
      VIDEO_PROVIDER_SUPPORTED_MODELS[provider].includes(model)
    )
  )
}

/** 模型官方档位；未接入的模型返回空数组。 */
export function videoModelResolutions(model: string): string[] {
  return (
    VIDEO_MODEL_RESOLUTIONS.find((entry) => entry.model === model)?.resolutions ??
    []
  )
}

/** 默认勾选：模型官方档位全部勾上（运营按账号实际能力收敛）。 */
export function defaultVideoModelResolutions(): Record<string, string[]> {
  const selection: Record<string, string[]> = {}
  for (const model of VIDEO_MODEL_CODES) {
    selection[model] = [...videoModelResolutions(model)]
  }
  return selection
}

/**
 * 勾选/取消一个档位：官方档位之外的取值直接忽略，结果按官方顺序排列。
 * 不允许把某个模型勾到空：空条目在下游等价于"不限制该模型的分辨率"，
 * 与"该账号不服务这个模型"是两回事，后者应通过模型白名单表达。
 */
export function toggleVideoResolution(
  selection: Record<string, readonly string[] | undefined>,
  model: string,
  resolution: string
): Record<string, string[]> {
  const next: Record<string, string[]> = {}
  for (const [key, value] of Object.entries(selection)) {
    if (value) next[key] = [...value]
  }
  const official = videoModelResolutions(model)
  if (!official.includes(resolution)) {
    return next
  }
  const current = next[model] ?? []
  const toggled = current.includes(resolution)
    ? current.filter((item) => item !== resolution)
    : [...current, resolution]
  if (toggled.length === 0) {
    return next
  }
  next[model] = official.filter((item) => toggled.includes(item))
  return next
}

/**
 * 表单勾选 → extra.video_model_resolutions。
 * - `models` 传入时只提交这些模型（弹窗按「已勾选模型」过滤）。
 * - 一个档位都没勾的模型整条删除（后端同样丢弃空条目）；全部为空时返回 undefined，
 *   调用方据此完全不写该键 —— 键缺失即「不限制分辨率」，与旧行为一致。
 * - 大小写不敏感匹配并回写官方写法，避免手写出 "4k" 这类非规范值。
 */
export function serializeVideoModelResolutions(
  selection: Record<string, readonly string[] | undefined>,
  models: readonly string[] = VIDEO_MODEL_CODES
): Record<string, string[]> | undefined {
  const payload: Record<string, string[]> = {}
  for (const model of models) {
    const picked: string[] = []
    for (const resolution of videoModelResolutions(model)) {
      const values = selection[model] ?? []
      if (
        values.some(
          (value) =>
            typeof value === 'string' &&
            value.trim().toLowerCase() === resolution.toLowerCase()
        )
      ) {
        picked.push(resolution)
      }
    }
    if (picked.length > 0) {
      payload[model] = picked
    }
  }
  return Object.keys(payload).length > 0 ? payload : undefined
}

/**
 * extra.video_model_resolutions → 表单勾选。
 * 只保留已接入模型与其官方档位。未配置的模型回落到全部官方档位，而不是留空：
 * 后端把"未配置"解释为不限制，界面上留空却读作"什么都不支持"，
 * 两者相反，会让运营误以为一个其实可用的账号被禁用了。
 * 存了一个空数组（或整份是非法档位）时同样回落到"全部官方档位"：
 * 后端把空列表归一化成"不限制"，界面必须呈现同一含义。
 */
export function parseVideoModelResolutions(
  raw: unknown
): Record<string, string[]> {
  const selection: Record<string, string[]> = defaultVideoModelResolutions()
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
    const official = videoModelResolutions(model)
    const intersected = official.filter((resolution) =>
      list.some(
        (value) =>
          typeof value === 'string' &&
          value.trim().toLowerCase() === resolution.toLowerCase()
      )
    )
    if (intersected.length > 0) {
      selection[model] = intersected
    }
  }
  return selection
}
