// request_type describes transport (sync/stream), not the generated media.
export function usageType(row) {
  if (row.video_task_id || row.billing_mode === 'video_duration' || row.media_type === 'video' || row.request_type === 'video' || Number(row.video_count) > 0 || row.request_id?.startsWith('video:')) return '视频'
  if (row.image_task_id || row.billing_mode === 'image' || row.media_type === 'image' || Number(row.image_count) > 0 || Number(row.image_output_tokens) > 0 || Number(row.image_output_cost) > 0 || row.request_id?.startsWith('image:')) return '图片'
  return '文本'
}

export function isRefund(row) {
  return row.funds_event === 'failure_refund' || row.funds_event === 'settlement_refund' ||
    row.request_id?.endsWith(':refund') === true || row.request_id?.endsWith(':failure_refund') === true ||
    Number(row.actual_cost ?? row.total_cost ?? row.cost ?? 0) < 0
}

export function refundError(row) {
  if (!isRefund(row) || row.funds_event === 'settlement_refund') return null
  if (row.task_error) return row.task_error
  if (row.funds_event === 'failure_refund' || row.image_task_status === 'failed' || row.request_id?.endsWith(':refund') || row.request_id?.endsWith(':failure_refund')) {
    return { code: 0, message: '这条历史记录未保存上游错误详情。' }
  }
  return null
}
