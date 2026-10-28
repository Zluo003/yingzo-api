<template>
  <AppLayout>
    <div class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="text-xl font-semibold">邀请返利 · 提现管理</h2><p class="mt-1 text-sm text-gray-500">支付宝手动支付后标记已支付；驳回申请将自动退回返利余额。点击用户查看其完整记录。</p></div>
        <button class="btn btn-secondary" :disabled="loading" @click="load">刷新列表</button>
      </div>
      <div class="card overflow-x-auto">
        <table class="w-full whitespace-nowrap text-left text-sm"><thead class="bg-gray-50 dark:bg-dark-800"><tr><th class="p-4">申请时间</th><th class="p-4">申请用户</th><th class="p-4">金额（元）</th><th class="p-4">支付宝账号 / 实名</th><th class="p-4">状态</th><th class="p-4">处理备注 / 时间</th><th class="p-4">操作</th></tr></thead>
          <tbody><tr v-if="loading"><td colspan="7" class="p-8 text-center">加载中…</td></tr><tr v-else-if="!rows.length"><td colspan="7" class="p-8 text-center text-gray-500">暂无提现申请</td></tr>
            <tr v-for="row in rows" :key="row.id" class="border-t border-gray-100 dark:border-dark-700"><td class="p-4">{{ date(row.created_at) }}</td><td class="p-4"><button class="text-primary-600 underline" @click="openDetail(row)">{{ row.email }} · #{{ row.user_id }}</button></td><td class="p-4 font-semibold">{{ money(row.amount) }}</td><td class="p-4">{{ row.alipay_account }}<br>{{ row.alipay_name }}</td><td class="p-4">{{ statusLabel(row.status) }}</td><td class="max-w-xs whitespace-normal p-4">{{ row.admin_note || '—' }}<br>{{ date(row.processed_at) }}</td><td class="p-4"><div v-if="row.status === 'pending'" class="flex gap-2"><button class="btn btn-primary btn-sm" @click="action = { row, status: 'paid', note: '' }">标记已支付</button><button class="btn btn-secondary btn-sm" @click="action = { row, status: 'rejected', note: '' }">驳回</button></div><span v-else>已处理</span></td></tr>
          </tbody></table>
      </div>
      <div class="flex items-center justify-end gap-3 text-sm"><span>共 {{ total }} 条</span><button class="btn btn-secondary btn-sm" :disabled="loading || page === 1" @click="page--; load()">上一页</button><span>{{ page }} / {{ Math.max(1, Math.ceil(total / 20)) }}</span><button class="btn btn-secondary btn-sm" :disabled="loading || page * 20 >= total" @click="page++; load()">下一页</button></div>
    </div>
    <div v-if="selected" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" @click.self="selected = null">
      <div class="max-h-[90vh] w-full max-w-6xl overflow-y-auto rounded-2xl bg-white p-6 dark:bg-dark-900" role="dialog" aria-modal="true" aria-labelledby="wallet-title">
        <div class="mb-5 flex items-center justify-between gap-3"><h2 id="wallet-title" class="text-lg font-semibold">{{ selected.email }} · 返利账户</h2><button class="btn btn-secondary btn-sm" @click="selected = null">关闭</button></div>
        <div v-if="detail" class="mb-6 grid gap-4 sm:grid-cols-4"><div>可用返利<p class="mt-2 text-xl font-semibold">{{ money(detail.aff_quota) }}</p></div><div>冻结返利<p class="mt-2 text-xl font-semibold">{{ money(detail.aff_frozen_quota) }}</p></div><div>累计返利<p class="mt-2 text-xl font-semibold">{{ money(detail.aff_history_quota) }}</p></div><div>邀请人数<p class="mt-2 text-xl font-semibold">{{ detail.aff_count }}</p></div><div class="sm:col-span-4 break-all text-sm text-gray-500">专属邀请码：{{ detail.aff_code }} · 活动{{ detail.enabled ? '开启' : '已关闭' }} · 当前返利冻结期：{{ (detail.rebate_freeze_hours || 0) / 24 }} 天<span v-if="detail.next_thaw_at"> · 最近一笔解冻：{{ date(detail.next_thaw_at) }}</span></div></div>
        <p v-if="detail?.aff_debt_quota" class="mb-4 rounded-lg bg-amber-50 p-3 text-amber-800">待追回返利：{{ money(detail.aff_debt_quota) }}，后续返利结算时优先抵扣。</p><div class="mb-4 flex flex-wrap gap-2"><button v-for="tab in tabs" :key="tab[0]" class="btn btn-sm" :class="kind === tab[0] ? 'btn-primary' : 'btn-secondary'" @click="kind = tab[0]; detailPage = 1; loadRecords()">{{ tab[1] }}</button></div>
        <div class="overflow-x-auto"><table class="w-full whitespace-nowrap text-left text-sm">
          <thead><tr><th class="p-3">时间</th><th class="p-3">用户 / 类型</th><th class="p-3">{{ kind === 'recharges' ? '充值实付' : '金额' }}</th><th class="p-3">{{ kind === 'recharges' || kind === 'invites' ? '返利金额' : '状态 / 备注' }}</th><th v-if="kind === 'ledger' || kind === 'recharges'" class="p-3">返利状态 / 解冻时间</th></tr></thead>
          <tbody><tr v-if="detailLoading"><td colspan="5" class="p-8 text-center">加载中…</td></tr><tr v-else-if="!detailRows.length"><td colspan="5" class="p-8 text-center">暂无记录</td></tr><tr v-for="row in detailRows" :key="row.id" class="border-t dark:border-dark-700">
            <td class="p-3">{{ date(row.created_at) }}</td><td class="p-3">{{ kind === 'invites' || kind === 'recharges' ? row.email : actionLabel(row.action) }}<small v-if="row.source_order_id" class="block text-gray-500">订单 #{{ row.source_order_id }}</small></td><td class="p-3">{{ kind === 'invites' ? '—' : money(kind === 'recharges' ? row.pay_amount : row.amount) }}</td><td class="p-3">{{ kind === 'recharges' || kind === 'invites' ? money(row.rebate_amount) : `${statusLabel(row.status)} ${row.admin_note}` }}<template v-if="kind === 'withdrawals'"><br>{{ row.alipay_account }} · {{ row.alipay_name }}</template></td>
            <td v-if="kind === 'ledger' || kind === 'recharges'" class="p-3"><template v-if="row.action === 'accrue' || row.rebate_amount > 0 || row.reversed_amount > 0"><span :class="isFrozen(row) ? 'text-amber-700' : 'text-green-700'">{{ rebateStatus(row) }}</span><small v-if="row.reversed_amount > 0" class="block">已撤销 {{ money(row.reversed_amount) }}</small><template v-if="isFrozen(row) && !isRevoked(row)"><br>{{ date(row.frozen_until) }} 解冻</template></template><span v-else>—</span></td>
          </tr></tbody>
        </table></div>
        <div class="mt-4 flex items-center justify-end gap-3 text-sm"><span>共 {{ detailTotal }} 条</span><button class="btn btn-secondary btn-sm" :disabled="detailLoading || detailPage === 1" @click="detailPage--; loadRecords()">上一页</button><span>{{ detailPage }}</span><button class="btn btn-secondary btn-sm" :disabled="detailLoading || detailPage * 20 >= detailTotal" @click="detailPage++; loadRecords()">下一页</button></div>
      </div>
    </div>
    <div v-if="action" class="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4" @click.self="!busy && (action = null)">
      <form class="w-full max-w-lg space-y-4 rounded-2xl bg-white p-6 dark:bg-dark-900" role="dialog" aria-modal="true" aria-labelledby="action-title" @submit.prevent="process">
        <h2 id="action-title" class="text-lg font-semibold">{{ action.status === 'paid' ? '确认已手动支付' : '驳回提现申请' }}</h2>
        <p>{{ action.row.email }} · {{ money(action.row.amount) }}<br>支付宝：{{ action.row.alipay_account }} · {{ action.row.alipay_name }}</p>
        <p class="text-sm text-gray-500">{{ action.status === 'paid' ? '请核实支付宝转账已成功。此操作只记录支付结果，不会发起转账。' : '驳回后，预扣金额将退回用户的可用返利余额。' }}</p>
        <label class="block text-sm">处理备注 / 支付凭证号<textarea v-model="action.note" class="input mt-2" maxlength="500" :required="action.status === 'rejected'" /></label>
        <div class="flex justify-end gap-3"><button type="button" class="btn btn-secondary" :disabled="busy" @click="action = null">取消</button><button class="btn btn-primary" :disabled="busy">{{ busy ? '处理中…' : '确认' }}</button></div>
      </form>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { listWithdrawals, processWithdrawal, getWalletDetail, listWalletRecords, type AffiliateWalletRecord, type AffiliateWalletKind } from '@/api/admin/affiliates'
import type { UserAffiliateDetail } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
const app = useAppStore()
const rows = ref<AffiliateWalletRecord[]>([]), total = ref(0), page = ref(1), loading = ref(false), busy = ref(false)
const selected = ref<AffiliateWalletRecord | null>(null), detail = ref<UserAffiliateDetail | null>(null)
const detailRows = ref<AffiliateWalletRecord[]>([]), detailTotal = ref(0), detailPage = ref(1), detailLoading = ref(false)
const kind = ref<AffiliateWalletKind>('recharges')
const tabs: [AffiliateWalletKind, string][] = [['recharges', '下级充值记录'], ['ledger', '个人返现流水'], ['withdrawals', '提现记录'], ['invites', '邀请注册记录']]
const action = ref<{ row: AffiliateWalletRecord; status: 'paid' | 'rejected'; note: string } | null>(null)
const money = (v: number) => `¥${Number(v || 0).toFixed(2)}`
const date = (v?: string) => v ? new Date(v).toLocaleString('zh-CN') : '—'
const statusLabel = (s: string) => ({ pending: '待支付', paid: '已支付', rejected: '已驳回', frozen: '冻结中', available: '已结算', revoked: '退款已撤销' }[s] || s || '—')
const isRevoked = (row: AffiliateWalletRecord) => row.reversed_amount > 0 && (row.action === 'accrue' ? row.amount <= row.reversed_amount : row.rebate_amount <= 0)
const isFrozen = (row: AffiliateWalletRecord) => !isRevoked(row) && (row.status === 'frozen' || Date.parse(row.frozen_until || '') > Date.now())
const rebateStatus = (row: AffiliateWalletRecord) => isRevoked(row) ? '退款已撤销' : `${row.reversed_amount > 0 ? '部分退款撤销 · ' : ''}${isFrozen(row) ? '冻结中' : '已结算'}`
const actionLabel = (s: string) => ({ accrue: '充值返利', transfer: '转个人余额', withdrawal_hold: '提现预扣', withdrawal_release: '驳回退回', withdrawal_paid: '提现已支付', withdrawal: '支付宝提现', refund_revoke: '退款追回' }[s] || s)
async function load() {
  loading.value = true
  try { const data = await listWithdrawals(page.value); rows.value = data.items; total.value = data.total } catch (e) { app.showError(extractApiErrorMessage(e, '加载失败')) } finally { loading.value = false }
}
async function openDetail(row: AffiliateWalletRecord) {
  selected.value = row; detail.value = null; kind.value = 'recharges'; detailPage.value = 1
  try { const data = await getWalletDetail(row.user_id); if (selected.value === row) detail.value = data } catch (e) { app.showError(extractApiErrorMessage(e, '加载账户失败')) }
  await loadRecords()
}
let recordRequest = 0
async function loadRecords() {
  if (!selected.value) return
  const current = ++recordRequest
  detailLoading.value = true; detailRows.value = []; detailTotal.value = 0
  try { const data = await listWalletRecords(selected.value.user_id, kind.value, detailPage.value); if (current === recordRequest) { detailRows.value = data.items; detailTotal.value = data.total } } catch (e) { app.showError(extractApiErrorMessage(e, '加载记录失败')) } finally { if (current === recordRequest) detailLoading.value = false }
}
async function process() {
  if (!action.value) return
  busy.value = true
  try { await processWithdrawal(action.value.row.id, action.value.status, action.value.note); app.showSuccess('提现状态已更新'); action.value = null; await load() } catch (e) { app.showError(extractApiErrorMessage(e, '处理失败')) } finally { busy.value = false }
}
onMounted(load)
</script>
