/**
 * 视频账号「本账号每个模型实际支持哪些分辨率」的单一来源。
 *
 * 镜像的后端实现（改这里必须同步改后端，反之亦然）：
 * - internal/service/video.go → videoModelSpecs / SupportedVideoModels（模型官方分辨率）
 * - internal/service/video_provider_aigod.go → Compatible（aigod 明确拒绝 4K）
 * - internal/service/video_provider_newtoken.go → videoNewtokenUpstreamModel
 *   （分辨率编进上游模型 id，其它组合返回 ""，即该渠道不提供）
 * - internal/service/admin_account.go → VideoModelResolutionsExtraKey /
 *   normalizeVideoModelResolutionsExtra（写入 extra.video_model_resolutions 的校验规则）
 *
 * 语义：某模型未列入 => 该模型不限制分辨率（模型维度本身已由 model_mapping 白名单约束）；
 * 列入后只有列出的档位会参与该账号的调度。
 *
 * 注意 4K 的官方写法是大写 K：后端按大小写不敏感匹配后回写官方写法，
 * 因此前后端之间只传官方字符串，不要用 ToLower 的副本。
 */
export type VideoProvider = 'aigod' | 'newtoken'

export interface VideoModelResolutionSpec {
  model: string
  resolutions: string[]
}

/** 模型官方档位，顺序即后端 SupportedVideoResolutions() 的顺序（也是提交顺序）。 */
export const VIDEO_MODEL_RESOLUTIONS: VideoModelResolutionSpec[] = [
  { model: 'seedance-2.0', resolutions: ['480p', '720p', '1080p', '4K'] },
  { model: 'seedance-2.0-fast', resolutions: ['480p', '720p'] },
  { model: 'seedance-2.5', resolutions: ['480p', '720p', '1080p'] }
]

/** 对外提供的视频模型清单，与后端 SupportedVideoModels() 一致。 */
export const VIDEO_MODEL_CODES: string[] = VIDEO_MODEL_RESOLUTIONS.map(
  (entry) => entry.model
)

/**
 * 各上游渠道真正能服务的档位（官方档位的子集）。
 * aigod 提供 2.0 的全部官方档位（含 4K，目录里有 seedance-2.0-4k）；
 * newtoken 只有 720p/1080p 的 official 变体：2.0 两档齐全，2.0 Fast 仅 720p，
 * 2.5 为 720p + 1080p。
 */
export const VIDEO_PROVIDER_RESOLUTIONS: Record<
  VideoProvider,
  Record<string, string[]>
> = {
  aigod: {
    // aigod 目录含 seedance-2.0-4k（4K 仅 2.0 提供）。
    'seedance-2.0': ['480p', '720p', '1080p', '4K'],
    'seedance-2.0-fast': ['480p', '720p'],
    'seedance-2.5': ['480p', '720p', '1080p']
  },
  newtoken: {
    'seedance-2.0': ['720p', '1080p'],
    'seedance-2.0-fast': ['720p'],
    // newtoken 目录含 sd2.5-1080p-official（见其 Seedance OpenAI 兼容文档的
    // official 模型表），因此 2.5 的 1080p 也可服务。
    'seedance-2.5': ['720p', '1080p']
  }
}

/** 模型官方档位；未接入的模型返回空数组。 */
export function videoModelResolutions(model: string): string[] {
  return (
    VIDEO_MODEL_RESOLUTIONS.find((entry) => entry.model === model)?.resolutions ??
    []
  )
}

/** 上游平台可服务的档位（官方 ∩ 上游），保持官方顺序。 */
export function videoProviderModelResolutions(
  provider: VideoProvider,
  model: string
): string[] {
  const servable = VIDEO_PROVIDER_RESOLUTIONS[provider]?.[model] ?? []
  return videoModelResolutions(model).filter((resolution) =>
    servable.includes(resolution)
  )
}

/** 当前上游能否服务该 (模型, 分辨率)；界面上不可服务的档位一律禁用。 */
export function isVideoResolutionServable(
  provider: VideoProvider,
  model: string,
  resolution: string
): boolean {
  return videoProviderModelResolutions(provider, model).includes(resolution)
}

/** 当前上游服务不了的官方档位，用于渲染「该上游不提供」的提示。 */
export function unsupportedVideoResolutions(
  provider: VideoProvider,
  model: string
): string[] {
  return videoModelResolutions(model).filter(
    (resolution) => !isVideoResolutionServable(provider, model, resolution)
  )
}

/** 新建时的默认勾选：该上游能服务的档位全部勾上。 */
export function defaultVideoModelResolutions(
  provider: VideoProvider
): Record<string, string[]> {
  const selection: Record<string, string[]> = {}
  for (const model of VIDEO_MODEL_CODES) {
    selection[model] = videoProviderModelResolutions(provider, model)
  }
  return selection
}

/**
 * 切换上游后收敛勾选：丢掉新上游服务不了的档位（模型条目为空则整条删除）。
 * 只减不增：用户此前刻意取消的档位不会被重新勾上。
 */
export function pruneVideoModelResolutions(
  selection: Record<string, readonly string[] | undefined>,
  provider: VideoProvider
): Record<string, string[]> {
  const pruned: Record<string, string[]> = {}
  for (const model of VIDEO_MODEL_CODES) {
    const kept = (selection[model] ?? []).filter((resolution) =>
      isVideoResolutionServable(provider, model, resolution)
    )
    if (kept.length > 0) {
      pruned[model] = videoModelResolutions(model).filter((resolution) =>
        kept.includes(resolution)
      )
    }
  }
  return pruned
}

/** 勾选/取消一个档位：不可服务的档位直接忽略，结果按官方顺序排列。 */
export function toggleVideoResolution(
  selection: Record<string, readonly string[] | undefined>,
  provider: VideoProvider,
  model: string,
  resolution: string
): Record<string, string[]> {
  const next: Record<string, string[]> = {}
  for (const [key, value] of Object.entries(selection)) {
    if (value) next[key] = [...value]
  }
  if (!isVideoResolutionServable(provider, model, resolution)) {
    return next
  }
  const current = next[model] ?? []
  const toggled = current.includes(resolution)
    ? current.filter((item) => item !== resolution)
    : [...current, resolution]
  // 不允许把某个模型勾到空：空条目在下游等价于"不限制该模型的分辨率"，
  // 与"该账号不服务这个模型"是两回事，后者应通过模型白名单表达。
  // 若不拦住，运营以为禁用了全部分辨率，实际却把限制放开了。
  if (toggled.length === 0) {
    return next
  }
  next[model] = videoModelResolutions(model).filter((item) =>
    toggled.includes(item)
  )
  return next
}

/**
 * 表单勾选 → extra.video_model_resolutions。
 * - `models` 传入时只提交这些模型（创建弹窗按「已勾选模型」过滤）。
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
 * 只保留已接入模型与其官方档位，并收敛到当前上游能服务的集合：
 * 界面上「已勾选但被禁用」是矛盾状态，而渠道服务不了的档位后端虽接受却永远调度不到。
 */
export function parseVideoModelResolutions(
  raw: unknown,
  provider: VideoProvider
): Record<string, string[]> {
  // 未配置的模型回落到该上游能服务的全部档位，而不是留空：
  // 后端把"未配置"解释为不限制，界面上留空却读作"什么都不支持"，
  // 两者相反，会让运营误以为一个其实可用的账号被禁用了。
  // 配合 toggleVideoResolution 不允许勾空，空状态只在"从未配置"时出现。
  const selection: Record<string, string[]> = defaultVideoModelResolutions(provider)
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
    const allowed = videoProviderModelResolutions(provider, model)
    const intersected = allowed.filter((resolution) =>
      list.some(
        (value) =>
          typeof value === 'string' &&
          value.trim().toLowerCase() === resolution.toLowerCase()
      )
    )
    // 存了一个空数组（或整份是非法档位）时同样回落到"全部可服务"：
    // 后端把空列表归一化成"不限制"，界面必须呈现同一含义。
    if (intersected.length > 0) {
      selection[model] = intersected
    }
  }
  return selection
}
