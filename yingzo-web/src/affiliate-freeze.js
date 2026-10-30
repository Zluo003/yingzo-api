import { t as tr } from "./i18n/index.js";
export function formatAffiliateFreezePeriod(hours) {
  const value = Math.max(0, Math.round(Number(hours) || 0));
  if (!value) return tr("不冻结");
  const days = Math.floor(value / 24),
    remainder = value % 24;
  return `${days ? tr("{{value1}} 天", {
    value1: days
  }) : ''}${days && remainder ? ' ' : ''}${remainder ? tr("{{value1}} 小时", {
    value1: remainder
  }) : ''}`;
}
export function affiliateRebateStatus(row, kind, now = Date.now()) {
  if (kind === 'recharges' ? !(row.rebate_amount > 0 || row.reversed_amount > 0) : kind !== 'ledger' || row.action !== 'accrue') return null;
  const reversed = Number(row.reversed_amount) || 0;
  const net = kind === 'recharges' ? Number(row.rebate_amount) : Number(row.amount) - reversed;
  if (reversed > 0 && net <= 0) return {
    frozen: false,
    label: tr("退款已撤销"),
    until: null
  };
  const frozen = row.status === 'frozen' || Date.parse(row.frozen_until || '') > now;
  return {
    frozen,
    label: `${reversed > 0 ? tr("部分退款撤销 · ") : ''}${frozen ? tr("冻结中") : tr("已结算")}`,
    until: frozen ? row.frozen_until : null
  };
}
