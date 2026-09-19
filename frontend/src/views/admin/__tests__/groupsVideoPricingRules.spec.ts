import { describe, expect, it } from 'vitest'

import {
  VIDEO_PRICING_MODELS,
  addVideoPricingRule,
  createVideoPricingRuleRow,
  createVideoPricingRulesForm,
  hasDuplicateVideoPricingRule,
  onVideoPricingModelChange,
  removeVideoPricingRule,
  serializeVideoPricingRules,
  videoPricingResolutions
} from '../groupsVideoPricingRules'

describe('video group pricing rules', () => {
  it('exposes the shipped video models', () => {
    expect(VIDEO_PRICING_MODELS).toEqual([
      'seedance-2.0',
      'seedance-2.0-fast',
      'seedance-2.5',
      'grok-imagine-video-1.5-preview',
      'kling-video-v3-omni'
    ])
  })

  it('offers exactly the official resolutions per model, 4K only for seedance-2.0', () => {
    // 4K 是 seedance-2.0 的官方档位，且 aigod 目录含 seedance-2.0-4k，
    // 因此分组必须能为它配价，否则 4K 请求会以缺少定价失败。
    expect(videoPricingResolutions('seedance-2.0')).toEqual([
      '480p',
      '720p',
      '1080p',
      '4K'
    ])
    expect(videoPricingResolutions('seedance-2.0-fast')).toEqual(['480p', '720p'])
    expect(videoPricingResolutions('seedance-2.5')).toEqual([
      '480p',
      '720p',
      '1080p'
    ])
    for (const model of ['seedance-2.0-fast', 'seedance-2.5'] as const) {
      expect(videoPricingResolutions(model)).not.toContain('4K')
    }
  })

  it('serializes a per-second price with a 1.0 reference multiplier', () => {
    const rows = [
      createVideoPricingRuleRow({
        model_code: 'seedance-2.0',
        resolution: '720p',
        credits_per_second: 1
      })
    ]
    expect(serializeVideoPricingRules(rows)).toEqual([
      {
        model_code: 'seedance-2.0',
        resolution: '720p',
        credits_per_second: 1,
        reference_video_multiplier: 1,
        enabled: true
      }
    ])
  })

  it('rejects duplicate model + resolution pairs before hitting the unique constraint', () => {
    const rows = [
      createVideoPricingRuleRow({ model_code: 'seedance-2.0', resolution: '720p' }),
      createVideoPricingRuleRow({ model_code: 'seedance-2.0', resolution: '720p' })
    ]
    expect(hasDuplicateVideoPricingRule(rows)).toBe(true)
    expect(serializeVideoPricingRules(rows)).toBeNull()
  })

  it('rejects negative and non-numeric prices', () => {
    for (const price of [-1, 'abc', null]) {
      const rows = [
        createVideoPricingRuleRow({
          model_code: 'seedance-2.5',
          resolution: '1080p',
          credits_per_second: price
        })
      ]
      expect(serializeVideoPricingRules(rows)).toBeNull()
    }
  })

  it('round-trips server rules through the edit form', () => {
    const rows = createVideoPricingRulesForm([
      {
        model_code: 'seedance-2.5',
        resolution: '720p',
        credits_per_second: 0.5,
        enabled: false
      }
    ])
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({
      model_code: 'seedance-2.5',
      resolution: '720p',
      credits_per_second: 0.5,
      enabled: false
    })
  })

  it('adds and removes rows on the reactive form', () => {
    const form = { video_pricing_rules: [] as ReturnType<typeof createVideoPricingRuleRow>[] }
    addVideoPricingRule(form)
    addVideoPricingRule(form)
    expect(form.video_pricing_rules).toHaveLength(2)
    removeVideoPricingRule(form, 0)
    expect(form.video_pricing_rules).toHaveLength(1)
  })

  it('narrows the resolution when the model changes', () => {
    const form = {
      video_pricing_rules: [
        createVideoPricingRuleRow({
          model_code: 'seedance-2.0',
          resolution: '1080p'
        })
      ]
    }
    form.video_pricing_rules[0].model_code = 'seedance-2.0-fast'
    onVideoPricingModelChange(form, 0)
    expect(form.video_pricing_rules[0].resolution).toBe('480p')
  })
})
