import test from 'node:test'
import assert from 'node:assert/strict'
import { modelPriceRange, modelPriceUnit, seedancePriceChannels } from './model-pricing.js'

test('final prices display in RMB, retaining free and small prices', () => {
  assert.equal(modelPriceRange([{ input: 0 }], 'input'), '¥0.00')
  assert.equal(modelPriceRange([{ input: 0.000012 }], 'input'), '¥0.000012')
  assert.equal(modelPriceRange([{ input: 0.0000001 }], 'input'), '¥0.0000001')
  assert.equal(modelPriceRange([{ input: 3 }, { input: 12 }, { input: 3 }], 'input'), '¥3.00 – ¥12.00')
})

test('missing, null and invalid prices are never rendered as free', () => {
  assert.equal(modelPriceRange([{}, { price: null }, { price: NaN }, { price: -1 }], 'price'), '—')
  assert.equal(modelPriceRange([{ price: null }, { price: 0.5 }], 'price'), '¥0.50')
})

test('Seedance retains separate channel, resolution and billing-unit prices, including free tiers', () => {
  const prices = [
    { video_channel_id: 'a', video_channel_name: '官转', resolution: '720p', unit: 'request', unit_price: 4 },
    { video_channel_id: '', video_channel_name: '标准线路', resolution: '720p', unit: 'second', unit_price: 0.2 },
    { video_channel_id: 'a', video_channel_name: '官转', resolution: '1080p', unit: 'request', unit_price: 0 },
    { video_channel_id: 'b', video_channel_name: '官转', resolution: '720p', unit: 'second', unit_price: 0.5 },
    { video_channel_id: 'off', resolution: '720p', unit: 'request', unit_price: 99, enabled: false },
  ]
  const channels = seedancePriceChannels(prices)
  assert.equal(channels.length, 3)
  assert.equal(channels[0].name, '标准线路')
  assert.deepEqual(channels[0].prices, [prices[1]])
  assert.deepEqual(channels.find(channel => channel.id === 'a').prices, [prices[0], prices[2]])
  assert.deepEqual(channels.find(channel => channel.id === 'b').prices, [prices[3]])
  assert.equal(modelPriceRange(channels[1].prices.slice(1), 'unit_price'), '¥0.00')
  assert.equal(modelPriceUnit('request'), '次')
  assert.equal(modelPriceUnit('second'), '秒')
  assert.equal(modelPriceUnit('image'), '张')
  assert.equal(modelPriceUnit('unknown'), '')
})

test('legacy default prices retain explicit billing units without inventing channel prices', () => {
  assert.deepEqual(seedancePriceChannels(), [])
  const prices = [{ resolution: '720p', unit: 'request', unit_price: 2 }, { resolution: '1080p', unit: 'request', unit_price: null }]
  const [channel] = seedancePriceChannels(prices)
  assert.equal(channel.id, '')
  assert.equal(channel.name, '默认通道')
  assert.deepEqual(channel.prices, prices)
  assert.equal(modelPriceRange(channel.prices.slice(1), 'unit_price'), '—')
})
