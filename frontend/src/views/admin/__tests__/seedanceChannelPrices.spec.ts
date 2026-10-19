import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import SeedanceChannelPrices from '@/components/admin/SeedanceChannelPrices.vue'
import { createVideoPricingRulesForm, serializeVideoPricingRules } from '../groupsVideoPricingRules'
import { parseVideoModelChannels, serializeVideoModelChannels } from '@/components/account/videoModelChannels'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('Seedance channel configuration', () => {
  it('preserves separate default and named prices, including free per-request prices', () => {
    const rules = [
      { model_code:'seedance-2.0',resolution:'720p',credits_per_second:0.8,enabled:true },
      { model_code:'seedance-2.0',resolution:'720p',video_channel_id:'channel-a',billing_unit:'request' as const,credits_per_second:0,credits_per_request:0,enabled:true },
    ]
    const result = serializeVideoPricingRules(createVideoPricingRulesForm(rules))!
    expect(result).toHaveLength(2)
    expect(result[0].credits_per_second).toBe(0.8)
    expect(result[1]).toMatchObject({video_channel_id:'channel-a',billing_unit:'request',credits_per_request:0,credits_per_second:0})
  })
  it('does not turn an empty enabled price into zero', async () => {
    const wrapper=mount(SeedanceChannelPrices,{props:{channels:[{id:'',name:'Default',enabled:true}],resolutions:['720p'],modelValue:[]}})
    await wrapper.get('input[type="checkbox"]').setValue(true)
    const prices=wrapper.emitted('update:modelValue')![0][0] as {unit_price:number}[]
    expect(Number.isNaN(prices[0].unit_price)).toBe(true)
  })
  it('changes one channel billing unit without rewriting another channel', async () => {
    const wrapper=mount(SeedanceChannelPrices,{props:{channels:[{id:'',name:'Default',enabled:true},{id:'a',name:'A',enabled:true}],resolutions:['720p'],modelValue:[{resolution:'720p',unit_price:0.8,billing_unit:'second'},{resolution:'720p',unit_price:4,billing_unit:'second',video_channel_id:'a'}]}})
    await wrapper.findAll('select')[1].setValue('request')
    expect(wrapper.emitted('update:modelValue')![0][0]).toEqual([{resolution:'720p',unit_price:0.8,billing_unit:'second'},{resolution:'720p',unit_price:4,billing_unit:'request',video_channel_id:'a'}])
  })
  it('keeps account model bindings explicit and supports clearing to default', () => {
    const value=parseVideoModelChannels({'seedance-2.0':'a','seedance-2.5':'b'})
    expect(serializeVideoModelChannels({...value,'seedance-2.0':''},['seedance-2.0','seedance-2.5'])).toEqual({'seedance-2.5':'b'})
  })
})
