export const EMPTY_USAGE_FILTERS = { startDate: '', endDate: '', model: '', type: '' }

export function defaultUsageFilters(now = new Date()) {
  const yearMonth = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
  return { ...EMPTY_USAGE_FILTERS, startDate: `${yearMonth}-01`, endDate: `${yearMonth}-${String(now.getDate()).padStart(2, '0')}` }
}

export function usageFilterQuery(filters, timezone = Intl.DateTimeFormat().resolvedOptions().timeZone) {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries({ start_date: filters.startDate, end_date: filters.endDate, model: filters.model.trim(), usage_type: filters.type, timezone })) {
    if (value) query.set(key, value)
  }
  return query.toString()
}

export function usageRecordsQuery({ page, pageSize, filters }, timezone = Intl.DateTimeFormat().resolvedOptions().timeZone) {
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
  for (const [key, value] of new URLSearchParams(usageFilterQuery(filters, timezone))) query.set(key, value)
  return query.toString()
}

export function trendBarPercent(value, maximum) {
  const cost = Number(value)
  return Number.isFinite(cost) && Number.isFinite(maximum) && maximum > 0 ? Math.max(0, Math.min(100, cost / maximum * 100)) : 0
}

// request_type describes transport (sync/stream), not the generated media.
export function usageType(row) {
  if (row.music_task_id || row.media_type === 'audio' || row.request_id?.startsWith('music:')) return '音频'
  if (row.video_task_id || row.billing_mode === 'video_duration' || row.media_type === 'video' || row.request_type === 'video' || Number(row.video_count) > 0 || row.request_id?.startsWith('video:')) return '视频'
  if (row.image_task_id || row.billing_mode === 'image' || row.media_type === 'image' || Number(row.image_count) > 0 || Number(row.image_output_tokens) > 0 || Number(row.image_output_cost) > 0 || row.request_id?.startsWith('image:')) return '图片'
  return '文本'
}

export function availableTaskOutputs(row, now = Date.now()) {
  if (!row || isRefund(row) || !Array.isArray(row.task_outputs)) return []
  return row.task_outputs.filter(output => {
    if (!['image', 'audio', 'video'].includes(output.media_type) || !(Date.parse(output.expires_at) > now)) return false
    try {
      const url = new URL(output.url)
      return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password
    } catch { return false }
  })
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
