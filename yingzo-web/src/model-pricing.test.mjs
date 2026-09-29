import test from 'node:test'
import assert from 'node:assert/strict'
import { modelPriceRange } from './model-pricing.js'

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
