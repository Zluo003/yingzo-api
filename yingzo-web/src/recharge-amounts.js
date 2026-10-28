function positiveAmount(value) {
  const amount = Number(value)
  return Number.isFinite(amount) && amount > 0 ? amount : 0
}

// Checkout's global range describes payment channels; min/max_amount are the
// administrator's recharge limits. A recharge must satisfy both.
export function getRechargeAmountRange(info = {}) {
  const minimum = Math.max(positiveAmount(info.min_amount), positiveAmount(info.global_min))
  const maximums = [info.max_amount, info.global_max].map(positiveAmount).filter(Boolean)
  return { min: minimum, max: maximums.length ? Math.min(...maximums) : 0 }
}

export function buildRechargeAmountPresets(min, max) {
  // With no configured bound, use common amounts while leaving custom input open.
  const minimum = positiveAmount(min) || (positiveAmount(max) ? 0.01 : 10)
  const lower = Math.max(1, Math.ceil(minimum * 100 - 1e-7))
  const upper = positiveAmount(max) ? Math.floor(max * 100 + 1e-7) : Math.max(200000, lower * 100)
  if (upper < lower) return []
  const count = Math.min(8, upper - lower + 1)
  const candidates = new Set([lower, upper])
  for (let scale = 1; scale <= upper; scale *= 10) {
    for (const multiple of [1, 2, 5]) {
      const amount = scale * multiple
      if (amount >= lower && amount <= upper) candidates.add(amount)
    }
  }
  let amounts = [...candidates].sort((a, b) => a - b)
  if (amounts.length > count) {
    amounts = Array.from({ length: count }, (_, i) => amounts[Math.round(i * (amounts.length - 1) / (count - 1))])
  }
  // Fill shorter ranges between common denominations, without duplicate cents.
  while (amounts.length < count) {
    let gapIndex = 0
    for (let i = 1; i < amounts.length - 1; i++) {
      if (amounts[i + 1] - amounts[i] > amounts[gapIndex + 1] - amounts[gapIndex]) gapIndex = i
    }
    const start = amounts[gapIndex], end = amounts[gapIndex + 1]
    const step = 10 ** Math.max(0, Math.floor(Math.log10(end - start)) - 1)
    const midpoint = Math.round((start + (end - start) / 2) / step) * step
    amounts.splice(gapIndex + 1, 0, Math.max(start + 1, Math.min(end - 1, midpoint)))
  }
  return amounts.map(amount => amount / 100)
}
