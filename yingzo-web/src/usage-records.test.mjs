import test from 'node:test'
import assert from 'node:assert/strict'
import { availableTaskOutputs, usageType, isRefund, refundError, EMPTY_USAGE_FILTERS, defaultUsageFilters, usageFilterQuery, usageRecordsQuery, trendBarPercent } from './usage-records.js'

test('charts and history share the same filters and default to the current month', () => {
  const filters = defaultUsageFilters(new Date(2026, 9, 4, 12))
  assert.deepEqual(filters, { startDate: '2026-10-01', endDate: '2026-10-04', model: '', type: '' })
  const history = new URLSearchParams(usageRecordsQuery({ page: 2, pageSize: 20, filters }, 'Asia/Shanghai'))
  history.delete('page')
  history.delete('page_size')
  assert.equal(history.toString(), usageFilterQuery(filters, 'Asia/Shanghai'))
})

test('usage filters preserve date boundaries, media type, model names and pagination', () => {
  const query = new URLSearchParams(usageRecordsQuery({ page: 3, pageSize: 50, filters: { startDate: '2026-10-01', endDate: '2026-10-03', model: '  vendor/model + preview  ', type: 'image' } }, 'Asia/Shanghai'))
  assert.deepEqual(Object.fromEntries(query), { page: '3', page_size: '50', start_date: '2026-10-01', end_date: '2026-10-03', model: 'vendor/model + preview', usage_type: 'image', timezone: 'Asia/Shanghai' })
  assert.deepEqual(Object.fromEntries(new URLSearchParams(usageRecordsQuery({ page: 1, pageSize: 20, filters: EMPTY_USAGE_FILTERS }, 'Asia/Shanghai'))), { page: '1', page_size: '20', timezone: 'Asia/Shanghai' })
})

test('trend heights retain the ratio between costs, including small and zero values', () => {
  assert.equal(trendBarPercent(17.1, 17.1), 100)
  assert.equal(trendBarPercent(8.55, 17.1), 50)
  assert.ok(Math.abs(trendBarPercent(0.6043, 17.1) - 3.5339181286549706) < 0.000001)
  assert.equal(trendBarPercent(0.001, 10), 0.01)
  for (const value of [0, -1, NaN, Infinity, 'invalid']) assert.equal(trendBarPercent(value, 10), 0)
  assert.equal(trendBarPercent(0, 0), 0)
})

test('image media overrides sync/stream transport, including zero-output refunds', () => {
  for (const row of [
    { request_type: 'sync', billing_mode: 'image' },
    { request_type: 'stream', image_count: 1 },
    { request_type: 'sync', image_output_tokens: 1024 },
    { request_type: 'unknown', image_task_id: 'imgtask_1', actual_cost: -0.2 },
    { request_type: 'sync', request_id: 'image:imgtask_1:failure_refund' },
  ]) assert.equal(usageType(row), '图片')
  assert.equal(usageType({ request_type: 'stream', image_input_tokens: 1024 }), '文本')
})

test('video billing and historical refund IDs override transport', () => {
  assert.equal(usageType({ request_type: 'sync', billing_mode: 'video_duration' }), '视频')
  assert.equal(usageType({ request_id: 'video:video_1:refund' }), '视频')
})

test('async music records are audio, including refunds and records without outputs', () => {
  assert.equal(usageType({ music_task_id: 'music_1' }), '音频')
  assert.equal(usageType({ request_id: 'music:music_1:failure_refund' }), '音频')
})

test('output visibility follows per-file expiry, refunds and safe preview links', () => {
  const now = Date.parse('2026-10-05T12:00:00Z')
  const output = { url: 'https://example.test/media/image/asset.png', media_type: 'image', expires_at: '2026-10-05T12:00:01Z' }
  const audio = { ...output, url: 'https://example.test/media/audio/asset.mp3', media_type: 'audio', expires_at: '2026-10-05T12:00:02Z' }
  const row = { task_outputs: [output, audio] }
  assert.deepEqual(availableTaskOutputs(row, now), [output, audio])
  assert.deepEqual(availableTaskOutputs(row, now + 1000), [audio])
  assert.deepEqual(availableTaskOutputs(row, now + 2000), [])
  for (const funds_event of ['failure_refund', 'settlement_refund']) assert.deepEqual(availableTaskOutputs({ ...row, funds_event }, now), [])
  for (const url of ['javascript:alert(1)', 'data:image/png;base64,a', '//example.test/a', 'https://user:secret@example.test/a']) {
    assert.deepEqual(availableTaskOutputs({ task_outputs: [{ ...output, url }] }, now), [])
  }
  for (const expires_at of [undefined, '', 'invalid']) assert.deepEqual(availableTaskOutputs({ task_outputs: [{ ...output, expires_at }] }, now), [])
  assert.deepEqual(availableTaskOutputs({}), [])
})

test('only refund rows show a refund and failure detail; settlement refunds are not failures', () => {
  const task_error = { code: 451, message: 'Content rejected by upstream' }
  assert.equal(isRefund({ actual_cost: 0.2, image_task_status: 'failed' }), false)
  assert.equal(refundError({ actual_cost: 0.2, task_error }), null)
  for (const row of [
    { actual_cost: -0.2, funds_event: 'failure_refund', task_error },
    { actual_cost: -1, request_id: 'video:video_1:refund', task_error },
  ]) {
    assert.equal(isRefund(row), true)
    assert.equal(refundError(row), task_error)
  }
  const settlement = { actual_cost: -0.1, funds_event: 'settlement_refund', task_error }
  assert.equal(isRefund(settlement), true)
  assert.equal(refundError(settlement), null)
  assert.equal(refundError({ funds_event: 'failure_refund' }).code, 0)
})
