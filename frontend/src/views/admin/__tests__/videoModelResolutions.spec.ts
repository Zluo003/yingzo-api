import { describe, expect, it } from 'vitest'

import {
  VIDEO_MODEL_CODES,
  VIDEO_MODEL_RESOLUTIONS,
  VIDEO_PROVIDER_RESOLUTIONS,
  defaultVideoModelResolutions,
  isVideoResolutionServable,
  parseVideoModelResolutions,
  pruneVideoModelResolutions,
  serializeVideoModelResolutions,
  toggleVideoResolution,
  unsupportedVideoResolutions,
  videoModelResolutions,
  videoProviderModelResolutions
} from '../videoModelResolutions'

describe('video model resolutions', () => {
  it('mirrors the backend official resolutions, including the uppercase 4K', () => {
    expect(VIDEO_MODEL_CODES).toEqual([
      'seedance-2.0',
      'seedance-2.0-fast',
      'seedance-2.5'
    ])
    expect(videoModelResolutions('seedance-2.0')).toEqual([
      '480p',
      '720p',
      '1080p',
      '4K'
    ])
    expect(videoModelResolutions('seedance-2.0-fast')).toEqual(['480p', '720p'])
    expect(videoModelResolutions('seedance-2.5')).toEqual([
      '480p',
      '720p',
      '1080p'
    ])
    expect(videoModelResolutions('seedance-9.9')).toEqual([])
    expect(VIDEO_MODEL_RESOLUTIONS.map((entry) => entry.model)).toEqual(
      VIDEO_MODEL_CODES
    )
  })

  it('knows which resolutions each upstream can actually serve', () => {
    expect(videoProviderModelResolutions('aigod', 'seedance-2.0')).toEqual([
      '480p',
      '720p',
      '1080p',
      // aigod 目录含 seedance-2.0-4k
      '4K'
    ])
    expect(videoProviderModelResolutions('aigod', 'seedance-2.0-fast')).toEqual([
      '480p',
      '720p'
    ])
    expect(videoProviderModelResolutions('newtoken', 'seedance-2.0')).toEqual([
      '720p',
      '1080p'
    ])
    expect(videoProviderModelResolutions('newtoken', 'seedance-2.0-fast')).toEqual([
      '720p'
    ])
    expect(videoProviderModelResolutions('newtoken', 'seedance-2.5')).toEqual([
      '720p',
      // newtoken 目录含 sd2.5-1080p-official
      '1080p'
    ])
    expect(videoProviderModelResolutions('aigod', 'seedance-2.5')).toEqual([
      '480p',
      '720p',
      '1080p'
    ])
  })

  it('never claims a provider serves a resolution outside the official list', () => {
    for (const [provider, byModel] of Object.entries(VIDEO_PROVIDER_RESOLUTIONS)) {
      for (const [model, resolutions] of Object.entries(byModel)) {
        for (const resolution of resolutions) {
          expect(videoModelResolutions(model)).toContain(resolution)
          expect(
            isVideoResolutionServable(
              provider as 'aigod' | 'newtoken',
              model,
              resolution
            )
          ).toBe(true)
        }
      }
    }
    // aigod 目录含 seedance-2.0-4k，因此 4K 对它可服务。
    expect(isVideoResolutionServable('aigod', 'seedance-2.0', '4K')).toBe(true)
    expect(unsupportedVideoResolutions('aigod', 'seedance-2.0')).toEqual([])
    // newtoken 目录含 sd2.5-1080p-official。
    expect(isVideoResolutionServable('newtoken', 'seedance-2.5', '1080p')).toBe(
      true
    )
    // 官方档位之外的分辨率一律不可服务：2.0-fast 官方只有 480p/720p。
    expect(isVideoResolutionServable('newtoken', 'seedance-2.0-fast', '1080p')).toBe(
      false
    )
    expect(isVideoResolutionServable('aigod', 'seedance-2.0-fast', '4K')).toBe(
      false
    )
    // newtoken 无 480p 档位；4K 也不在其目录内。
    expect(unsupportedVideoResolutions('newtoken', 'seedance-2.0')).toEqual([
      '480p',
      '4K'
    ])
  })

  it('defaults to every resolution the selected upstream can serve', () => {
    expect(defaultVideoModelResolutions('aigod')).toEqual({
      'seedance-2.0': ['480p', '720p', '1080p', '4K'],
      'seedance-2.0-fast': ['480p', '720p'],
      'seedance-2.5': ['480p', '720p', '1080p']
    })
    expect(defaultVideoModelResolutions('newtoken')).toEqual({
      'seedance-2.0': ['720p', '1080p'],
      'seedance-2.0-fast': ['720p'],
      'seedance-2.5': ['720p', '1080p']
    })
  })

  it('drops resolutions the new upstream cannot serve but keeps the rest', () => {
    const aigodDefaults = defaultVideoModelResolutions('aigod')
    expect(pruneVideoModelResolutions(aigodDefaults, 'newtoken')).toEqual({
      'seedance-2.0': ['720p', '1080p'],
      'seedance-2.0-fast': ['720p'],
      'seedance-2.5': ['720p', '1080p']
    })
    // 只减不增：切回 aigod 不会把用户没勾过的 480p 补回来。
    const narrowed = { 'seedance-2.0': ['1080p'] }
    expect(pruneVideoModelResolutions(narrowed, 'aigod')).toEqual({
      'seedance-2.0': ['1080p']
    })
    expect(pruneVideoModelResolutions(narrowed, 'newtoken')).toEqual({
      'seedance-2.0': ['1080p']
    })
  })

  it('removes entries that no longer hold a servable resolution', () => {
    const pruned = pruneVideoModelResolutions(
      { 'seedance-2.0': ['4K'], 'seedance-2.5': ['720p'] },
      'newtoken'
    )
    expect(pruned).toEqual({ 'seedance-2.5': ['720p'] })
    expect(Object.keys(pruned)).not.toContain('seedance-2.0')
  })

  it('toggles a resolution in official order and ignores unservable ones', () => {
    const toggled = toggleVideoResolution(
      { 'seedance-2.0': ['1080p'] },
      'aigod',
      'seedance-2.0',
      '480p'
    )
    expect(toggled['seedance-2.0']).toEqual(['480p', '1080p'])

    const off = toggleVideoResolution(toggled, 'aigod', 'seedance-2.0', '1080p')
    expect(off['seedance-2.0']).toEqual(['480p'])

    // 4K 对 aigod 可服务，可以继续勾上（官方顺序：480p → 4K）。
    const with4k = toggleVideoResolution(off, 'aigod', 'seedance-2.0', '4K')
    expect(with4k['seedance-2.0']).toEqual(['480p', '4K'])

    // 只剩一档时不能再取消：空条目在下游等价于"不限制"。
    const lastOne = { 'seedance-2.0': ['4K'] }
    const refused = toggleVideoResolution(lastOne, 'aigod', 'seedance-2.0', '4K')
    expect(refused['seedance-2.0']).toEqual(['4K'])
  })

  it('omits models without a checked resolution and the whole key when empty', () => {
    expect(
      serializeVideoModelResolutions({
        'seedance-2.0': ['720p', '1080p'],
        'seedance-2.0-fast': [],
        'seedance-2.5': ['720p']
      })
    ).toEqual({
      'seedance-2.0': ['720p', '1080p'],
      'seedance-2.5': ['720p']
    })

    expect(serializeVideoModelResolutions({})).toBeUndefined()
    expect(
      serializeVideoModelResolutions({
        'seedance-2.0': [],
        'seedance-2.0-fast': [],
        'seedance-2.5': []
      })
    ).toBeUndefined()
  })

  it('serializes in official order, de-duplicated and canonical', () => {
    expect(
      serializeVideoModelResolutions({
        // 故意乱序 + 重复 + 小写 4k：输出必须是官方顺序与官方写法。
        'seedance-2.0': ['4k', '1080p', '1080P', '4K']
      })
    ).toEqual({ 'seedance-2.0': ['1080p', '4K'] })
  })

  it('only submits the models the caller whitelists', () => {
    const selection = defaultVideoModelResolutions('newtoken')
    expect(
      serializeVideoModelResolutions(selection, ['seedance-2.5'])
    ).toEqual({ 'seedance-2.5': ['720p', '1080p'] })
    expect(serializeVideoModelResolutions(selection, [])).toBeUndefined()
  })

  it('parses the stored extra map and keeps only servable official resolutions', () => {
    const parsed = parseVideoModelResolutions(
      {
        'seedance-2.0': ['1080p', '720p'],
        'seedance-2.0-fast': ['1080P', '720p'],
        'seedance-2.5': 'not-an-array'
      },
      'newtoken'
    )
    expect(parsed).toEqual({
      'seedance-2.0': ['720p', '1080p'],
      'seedance-2.0-fast': ['720p'],
      // 未配置 / 值非法 => 后端语义是"不限制"，界面必须显示该上游可服务的全部档位，
      // 而不是留空（留空会读成"什么都不支持"，与后端相反）。
      'seedance-2.5': ['720p', '1080p']
    })
  })

  it('ignores unknown models, unknown resolutions and non-object values', () => {
    const aigodDefaults = {
      'seedance-2.0': ['480p', '720p', '1080p', '4K'],
      'seedance-2.0-fast': ['480p', '720p'],
      'seedance-2.5': ['480p', '720p', '1080p']
    }
    expect(
      parseVideoModelResolutions(
        { 'seedance-9.9': ['720p'], 'seedance-2.0': ['720p', '2160p', 42] },
        'aigod'
      )
    ).toEqual({
      'seedance-2.0': ['720p'],
      // 未列出的模型（未知模型键被忽略）保持"不限制"的默认呈现
      'seedance-2.0-fast': aigodDefaults['seedance-2.0-fast'],
      'seedance-2.5': aigodDefaults['seedance-2.5']
    })
    for (const raw of [undefined, null, 'x', 42, ['720p']]) {
      expect(parseVideoModelResolutions(raw, 'aigod')).toEqual(aigodDefaults)
    }
  })

  it('treats a stored empty list as unrestricted rather than as "supports nothing"', () => {
    // 后端保存时会丢掉空条目，但历史数据或手工改库可能留下空数组。
    const parsed = parseVideoModelResolutions(
      { 'seedance-2.0': [], 'seedance-2.5': ['999p'] },
      'aigod'
    )
    expect(parsed['seedance-2.0']).toEqual(['480p', '720p', '1080p', '4K'])
    expect(parsed['seedance-2.5']).toEqual(['480p', '720p', '1080p'])
  })

  it('refuses to uncheck the last resolution of a model', () => {
    // 勾空在下游等价于"不限制该模型的分辨率"，会让一次误操作静默放开限制。
    // 要禁用某个模型应通过模型白名单（model_mapping），而不是清空档位。
    let selection = defaultVideoModelResolutions('newtoken')
    selection = toggleVideoResolution(selection, 'newtoken', 'seedance-2.0-fast', '720p')
    expect(selection['seedance-2.0-fast']).toEqual(['720p'])
    // 再点一次（取消）应被忽略
    selection = toggleVideoResolution(selection, 'newtoken', 'seedance-2.0-fast', '720p')
    expect(selection['seedance-2.0-fast']).toEqual(['720p'])
  })

  it('round-trips through serialize and parse', () => {
    const selection = defaultVideoModelResolutions('aigod')
    const payload = serializeVideoModelResolutions(selection)!
    expect(parseVideoModelResolutions(payload, 'aigod')).toEqual(selection)
  })
})
