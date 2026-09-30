import test from 'node:test'
import assert from 'node:assert/strict'
import { usageType, isRefund, refundError } from './usage-records.js'

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
