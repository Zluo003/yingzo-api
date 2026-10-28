import test from 'node:test'
import assert from 'node:assert/strict'
import { buildRechargeAmountPresets, getRechargeAmountRange } from './recharge-amounts.js'

test('administrator limits constrain wider or unlimited payment channels', () => {
  for (const global_max of [0, 5000]) {
    const range = getRechargeAmountRange({ min_amount: 10, max_amount: 2000, global_min: 1, global_max })
    assert.deepEqual(range, { min: 10, max: 2000 })
    assert.deepEqual(buildRechargeAmountPresets(range.min, range.max), [10, 20, 50, 100, 200, 500, 1000, 2000])
  }
})

test('stricter channel limits also constrain the recharge range', () => {
  assert.deepEqual(getRechargeAmountRange({ min_amount: 10, max_amount: 2000, global_min: 100, global_max: 1000 }), { min: 100, max: 1000 })
  assert.deepEqual(getRechargeAmountRange({ min_amount: 10, max_amount: 0, global_max: 2000 }), { min: 10, max: 2000 })
  assert.deepEqual(getRechargeAmountRange(), { min: 0, max: 0 })
})

test('recharge presets include both bounds and exactly eight unique ascending amounts', () => {
  for (const [min, max] of [[1, 2000], [100, 2000], [300, 500], [12.34, 987.65], [0.01, 0.08], [0, 5], [10000, 100000]]) {
    const amounts = buildRechargeAmountPresets(min, max)
    assert.equal(amounts.length, 8, `${min}–${max}`)
    assert.equal(new Set(amounts).size, 8)
    assert.equal(amounts[0], min || 0.01)
    assert.equal(amounts.at(-1), max)
    assert.deepEqual(amounts, amounts.toSorted((a, b) => a - b))
    for (const amount of amounts) {
      assert.ok(amount > 0 && amount >= min && amount <= max)
      assert.ok(Math.abs(amount * 100 - Math.round(amount * 100)) < 1e-7)
    }
  }
})

test('narrow ranges never duplicate options or cross the configured bounds', () => {
  assert.deepEqual(buildRechargeAmountPresets(100, 100), [100])
  assert.deepEqual(buildRechargeAmountPresets(1, 1.03), [1, 1.01, 1.02, 1.03])
  assert.deepEqual(buildRechargeAmountPresets(2, 1), [])
  assert.deepEqual(buildRechargeAmountPresets(1.001, 1.039), [1.01, 1.02, 1.03])
})

test('an unlimited maximum uses eight suggestions without imposing an input cap', () => {
  const range = getRechargeAmountRange({ min_amount: 10, max_amount: 0, global_max: 0 })
  assert.equal(range.max, 0)
  assert.deepEqual(buildRechargeAmountPresets(range.min, range.max), [10, 20, 50, 100, 200, 500, 1000, 2000])
})
