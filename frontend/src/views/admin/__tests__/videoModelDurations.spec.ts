import { describe, expect, it } from 'vitest'

import {
  defaultVideoModelDurations,
  parseVideoModelDurations,
  serializeVideoModelDurations,
  toggleVideoDuration,
  videoModelDurations
} from '../videoModelDurations'

describe('video model durations', () => {
  it('mirrors the backend per-model spec ranges', () => {
    // 与后端 videoModelSpecs 的 Min/MaxSeconds 一致：seedance 4-15/4-30、
    // grok 1-15、可灵 3-15。
    expect(videoModelDurations('seedance-2.0')).toEqual(
      Array.from({ length: 12 }, (_, i) => i + 4)
    )
    expect(videoModelDurations('seedance-2.0-fast')).toEqual(
      Array.from({ length: 12 }, (_, i) => i + 4)
    )
    expect(videoModelDurations('seedance-2.5')).toEqual(
      Array.from({ length: 27 }, (_, i) => i + 4)
    )
    expect(videoModelDurations('grok-imagine-video-1.5')).toEqual(
      Array.from({ length: 15 }, (_, i) => i + 1)
    )
    expect(videoModelDurations('kling-v3-omni')).toEqual(
      Array.from({ length: 13 }, (_, i) => i + 3)
    )
    expect(videoModelDurations('seedance-9.9')).toEqual([])
  })

  it('defaults to every official tier of every model', () => {
    const defaults = defaultVideoModelDurations()
    expect(defaults['seedance-2.0-fast'][0]).toBe(4)
    expect(defaults['seedance-2.0-fast'].at(-1)).toBe(15)
    expect(defaults['grok-imagine-video-1.5'][0]).toBe(1)
    expect(defaults['kling-v3-omni'][0]).toBe(3)
  })

  it('toggles a duration in ascending order and ignores non-official ones', () => {
    const toggled = toggleVideoDuration({ 'seedance-2.0-fast': [5, 10] }, 'seedance-2.0-fast', 8)
    expect(toggled['seedance-2.0-fast']).toEqual([5, 8, 10])

    const off = toggleVideoDuration(toggled, 'seedance-2.0-fast', 8)
    expect(off['seedance-2.0-fast']).toEqual([5, 10])

    // 官方范围之外的取值直接忽略。
    const bogus = toggleVideoDuration(off, 'seedance-2.0-fast', 99)
    expect(bogus['seedance-2.0-fast']).toEqual([5, 10])

    // 只剩一档时不能再取消：空条目在下游等价于"不限制"。
    const refused = toggleVideoDuration({ 'seedance-2.0-fast': [10] }, 'seedance-2.0-fast', 10)
    expect(refused['seedance-2.0-fast']).toEqual([10])
  })

  it('serializes only the requested models, sorted and deduplicated', () => {
    expect(
      serializeVideoModelDurations(
        {
          'seedance-2.0-fast': [10, 5, 5],
          'seedance-2.5': []
        },
        ['seedance-2.0-fast', 'seedance-2.5']
      )
    ).toEqual({ 'seedance-2.0-fast': [5, 10] })
    expect(serializeVideoModelDurations({}, ['seedance-2.0'])).toBeUndefined()
  })

  it('parses the stored extra and falls back to full official ranges', () => {
    const parsed = parseVideoModelDurations({
      'seedance-2.0-fast': [10, 5],
      'seedance-2.5': 'not-an-array',
      'seedance-2.0': [4.5, 99, 8]
    })
    expect(parsed['seedance-2.0-fast']).toEqual([5, 10])
    // 未配置 / 值非法 => 后端语义是"不限制"，界面必须显示全部官方档位。
    expect(parsed['seedance-2.5']).toEqual(videoModelDurations('seedance-2.5'))
    // 非法值被剔除后剩余 8 秒。
    expect(parsed['seedance-2.0']).toEqual([8])
    expect(parsed['kling-v3-omni']).toEqual(videoModelDurations('kling-v3-omni'))
  })

  it('ignores malformed payloads', () => {
    const defaults = defaultVideoModelDurations()
    for (const raw of [undefined, null, 'x', 42, [5]]) {
      expect(parseVideoModelDurations(raw)).toEqual(defaults)
    }
  })

  it('round-trips through serialize and parse', () => {
    const selection = defaultVideoModelDurations()
    const payload = serializeVideoModelDurations(selection)!
    expect(parseVideoModelDurations(payload)).toEqual(selection)
  })
})
