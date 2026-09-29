// Preserve zero prices; absent or invalid prices must never appear as free.
export function modelPriceRange(prices, field) {
  const values = prices.map(price => price[field]).filter(value => typeof value === 'number' && Number.isFinite(value) && value >= 0)
  if (!values.length) return '—'
  const format = value => `¥${value.toLocaleString('zh-CN', value > 0 && value < 0.000001
    ? { maximumSignificantDigits: 6 }
    : { minimumFractionDigits: 2, maximumFractionDigits: 6 })}`
  const min = Math.min(...values)
  const max = Math.max(...values)
  return format(min) === format(max) ? format(min) : `${format(min)} – ${format(max)}`
}
