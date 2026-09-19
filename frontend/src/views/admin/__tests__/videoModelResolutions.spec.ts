import { describe, expect, it } from 'vitest'

import {
  VIDEO_MODEL_CODES,
  VIDEO_MODEL_RESOLUTIONS,
  defaultVideoModelResolutions,
  parseVideoModelResolutions,
  serializeVideoModelResolutions,
  toggleVideoResolution,
  videoModelResolutions
} from '../videoModelResolutions'

describe('video model resolutions', () => {
  it('mirrors the backend official resolutions, including the uppercase 4K', () => {
    expect(VIDEO_MODEL_CODES).toEqual([
      'seedance-2.0',
      'seedance-2.0-fast',
      'seedance-2.5',
      'grok-imagine-video-1.5',
      'kling-v3-omni'
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
    expect(videoModelResolutions('grok-imagine-video-1.5')).toEqual([
      '480p',
      '720p',
      '1080p'
    ])
    expect(videoModelResolutions('kling-v3-omni')).toEqual([
      '720p',
      '1080p',
      '4K'
    ])
    expect(videoModelResolutions('seedance-9.9')).toEqual([])
    expect(VIDEO_MODEL_RESOLUTIONS.map((entry) => entry.model)).toEqual(
      VIDEO_MODEL_CODES
    )
  })

  it('defaults to every official tier of every model', () => {
    expect(defaultVideoModelResolutions()).toEqual({
      'seedance-2.0': ['480p', '720p', '1080p', '4K'],
      'seedance-2.0-fast': ['480p', '720p'],
      'seedance-2.5': ['480p', '720p', '1080p'],
      'grok-imagine-video-1.5': ['480p', '720p', '1080p'],
      'kling-v3-omni': ['720p', '1080p', '4K']
    })
  })

  it('toggles a resolution in official order and ignores non-official ones', () => {
    const toggled = toggleVideoResolution(
      { 'seedance-2.0': ['1080p'] },
      'seedance-2.0',
      '480p'
    )
    expect(toggled['seedance-2.0']).toEqual(['480p', '1080p'])

    const off = toggleVideoResolution(toggled, 'seedance-2.0', '1080p')
    expect(off['seedance-2.0']).toEqual(['480p'])

    // 官方档位之外的取值直接忽略（该 chip 根本不会渲染，这里是兜底）。
    const bogus = toggleVideoResolution(off, 'seedance-2.0', '8K')
    expect(bogus['seedance-2.0']).toEqual(['480p'])

    // 只剩一档时不能再取消：空条目在下游等价于"不限制"。
    const lastOne = { 'seedance-2.0': ['4K'] }
    const refused = toggleVideoResolution(lastOne, 'seedance-2.0', '4K')
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
    const selection = defaultVideoModelResolutions()
    expect(
      serializeVideoModelResolutions(selection, ['seedance-2.5'])
    ).toEqual({ 'seedance-2.5': ['480p', '720p', '1080p'] })
    expect(serializeVideoModelResolutions(selection, [])).toBeUndefined()
  })

  it('parses the stored extra map and keeps only official resolutions', () => {
    const parsed = parseVideoModelResolutions({
      'seedance-2.0': ['1080p', '720p'],
      'seedance-2.0-fast': ['1080P', '720p'],
      'seedance-2.5': 'not-an-array'
    })
    expect(parsed).toEqual({
      'seedance-2.0': ['720p', '1080p'],
      'seedance-2.0-fast': ['720p'],
      // 未配置 / 值非法 => 后端语义是"不限制"，界面必须显示全部官方档位，
      // 而不是留空（留空会读成"什么都不支持"，与后端相反）。
      'seedance-2.5': ['480p', '720p', '1080p'],
      'grok-imagine-video-1.5': ['480p', '720p', '1080p'],
      'kling-v3-omni': ['720p', '1080p', '4K']
    })
  })

  it('ignores unknown models, unknown resolutions and non-object values', () => {
    const defaults = defaultVideoModelResolutions()
    expect(
      parseVideoModelResolutions({
        'seedance-9.9': ['720p'],
        'seedance-2.0': ['720p', '2160p', 42]
      })
    ).toEqual({ ...defaults, 'seedance-2.0': ['720p'] })
    for (const raw of [undefined, null, 'x', 42, ['720p']]) {
      expect(parseVideoModelResolutions(raw)).toEqual(defaults)
    }
  })

  it('treats a stored empty list as unrestricted rather than as "supports nothing"', () => {
    // 后端保存时会丢掉空条目，但历史数据或手工改库可能留下空数组。
    const parsed = parseVideoModelResolutions({
      'seedance-2.0': [],
      'seedance-2.5': ['999p']
    })
    expect(parsed['seedance-2.0']).toEqual(['480p', '720p', '1080p', '4K'])
    expect(parsed['seedance-2.5']).toEqual(['480p', '720p', '1080p'])
  })

  it('round-trips through serialize and parse', () => {
    const selection = defaultVideoModelResolutions()
    const payload = serializeVideoModelResolutions(selection)!
    expect(parseVideoModelResolutions(payload)).toEqual(selection)
  })
})
