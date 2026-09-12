import type { UsageLog } from '@/types'

type VideoUsageRow = Pick<
  UsageLog,
  | 'billing_mode'
  | 'video_task_id'
  | 'video_resolution'
  | 'video_duration_seconds'
  | 'video_reference_duration_seconds'
  | 'video_billable_seconds'
  | 'video_result_url'
  | 'request_id'
  | 'actual_cost'
>

export function isVideoUsage(row: VideoUsageRow | null | undefined): boolean {
  return row?.billing_mode === 'video_duration' || Boolean(row?.video_task_id)
}

export function isVideoRefundUsage(row: VideoUsageRow | null | undefined): boolean {
  return isVideoUsage(row) && ((row?.actual_cost ?? 0) < 0 || row?.request_id?.endsWith(':refund') === true)
}

export function formatVideoDurationParts(row: VideoUsageRow | null | undefined): string {
  const generated = row?.video_duration_seconds ?? 0
  const reference = row?.video_reference_duration_seconds ?? 0
  const billable = row?.video_billable_seconds ?? generated + reference
  if (reference > 0) {
    return `${generated}s + ${reference}s = ${billable}s`
  }
  return `${billable}s`
}

export function videoUnitPrice(row: Pick<UsageLog, 'total_cost' | 'video_billable_seconds'> | null | undefined): number {
  const seconds = row?.video_billable_seconds ?? 0
  if (seconds <= 0) return 0
  const price = (row?.total_cost ?? 0) / seconds
  return Number.isFinite(price) ? price : 0
}
