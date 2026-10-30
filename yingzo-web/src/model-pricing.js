import { getIntlLocale, t as tr } from "./i18n/index.js";
// Preserve zero prices; absent or invalid prices must never appear as free.
export function modelPriceRange(prices, field) {
  const values = prices.map(price => price[field]).filter(value => typeof value === 'number' && Number.isFinite(value) && value >= 0);
  if (!values.length) return '—';
  const format = value => `¥${value.toLocaleString(getIntlLocale(), value > 0 && value < 0.000001 ? {
    maximumSignificantDigits: 6
  } : {
    minimumFractionDigits: 2,
    maximumFractionDigits: 6
  })}`;
  const min = Math.min(...values);
  const max = Math.max(...values);
  return format(min) === format(max) ? format(min) : `${format(min)} – ${format(max)}`;
}
export function modelPriceUnit(unit) {
  return {
    second: tr("秒"),
    request: tr("次"),
    image: tr("张")
  }[unit] || '';
}

// Group by the stable channel ID, not its editable display name. Empty IDs
// belong to the legacy/default channel, whose configured name is preserved.
export function seedancePriceChannels(prices = []) {
  const channels = new Map();
  for (const price of prices) {
    if (price.enabled === false) continue;
    const id = price.video_channel_id || '';
    if (!channels.has(id)) channels.set(id, {
      id,
      name: price.video_channel_name || (id ? tr("未命名通道") : tr("默认通道")),
      prices: []
    });
    channels.get(id).prices.push(price);
  }
  return [...channels.values()].sort((a, b) => a.id === '' ? -1 : b.id === '' ? 1 : a.name.localeCompare(b.name, getIntlLocale()));
}
