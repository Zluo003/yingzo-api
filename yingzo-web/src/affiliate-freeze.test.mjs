import test from 'node:test'
import assert from 'node:assert/strict'
import { affiliateRebateStatus, formatAffiliateFreezePeriod } from './affiliate-freeze.js'

test('freeze periods display whole days and retain existing partial-day settings', () => {
  assert.equal(formatAffiliateFreezePeriod(168), '7 天')
  assert.equal(formatAffiliateFreezePeriod(36), '1 天 12 小时')
  assert.equal(formatAffiliateFreezePeriod(1), '1 小时')
  assert.equal(formatAffiliateFreezePeriod(0), '不冻结')
})

test('rebates stay frozen until their individual deadlines, including the boundary', () => {
  const deadline = '2026-11-05T10:00:00Z'
  const row = { action: 'accrue', amount: 100, rebate_amount: 100, frozen_until: deadline }
  for (const kind of ['ledger', 'recharges']) {
    assert.deepEqual(affiliateRebateStatus(row, kind, Date.parse(deadline) - 1), { frozen: true, label: '冻结中', until: deadline })
    assert.deepEqual(affiliateRebateStatus(row, kind, Date.parse(deadline)), { frozen: false, label: '已结算', until: null })
  }
  assert.equal(affiliateRebateStatus({ action: 'transfer', amount: 100 }, 'ledger'), null)
  assert.equal(affiliateRebateStatus({ rebate_amount: 0 }, 'recharges'), null)
  assert.equal(affiliateRebateStatus({ action: 'accrue' }, 'ledger').frozen, false)
})


test('refund reversals show full or partial revocation independently of deadline', () => {
  const future = '2099-01-01T00:00:00Z'
  assert.deepEqual(affiliateRebateStatus({ action: 'accrue', amount: 100, reversed_amount: 100, frozen_until: future }, 'ledger'), { frozen: false, label: '退款已撤销', until: null })
  assert.deepEqual(affiliateRebateStatus({ rebate_amount: 0, reversed_amount: 100, frozen_until: future }, 'recharges'), { frozen: false, label: '退款已撤销', until: null })
  assert.equal(affiliateRebateStatus({ action: 'accrue', amount: 100, reversed_amount: 40, frozen_until: future }, 'ledger').label, '部分退款撤销 · 冻结中')
  assert.equal(affiliateRebateStatus({ rebate_amount: 60, reversed_amount: 40 }, 'recharges').label, '部分退款撤销 · 已结算')
  assert.equal(affiliateRebateStatus({ action: 'accrue', amount: 100, status: 'frozen', frozen_until: '2000-01-01T00:00:00Z' }, 'ledger').frozen, true)
})
