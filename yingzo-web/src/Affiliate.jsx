import { useEffect, useState } from 'react'
import { request } from './lib.js'
import { affiliateRebateStatus, formatAffiliateFreezePeriod } from './affiliate-freeze.js'
import './affiliate.css'

const money = value => `¥${Number(value || 0).toFixed(2)}`
const time = value => value ? new Date(value).toLocaleString('zh-CN') : '—'
const labels = { refund_revoke: '退款追回', accrue: '充值返利', transfer: '转入个人余额', withdrawal_hold: '提现预扣', withdrawal_release: '提现驳回退回', withdrawal_paid: '提现已支付', pending: '待支付', paid: '已支付', rejected: '已驳回' }
const tabs = [['invites', '邀请注册记录'], ['recharges', '受邀用户充值'], ['ledger', '返利流水'], ['withdrawals', '提现记录']]

export default function Affiliate({ setUser }) {
  const [detail, setDetail] = useState(null)
  const [kind, setKind] = useState('invites')
  const [page, setPage] = useState(1)
  const [records, setRecords] = useState({ items: [], total: 0 })
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [revision, setRevision] = useState(0)
  const [confirmTransfer, setConfirmTransfer] = useState(false)
  const [form, setForm] = useState({ amount: '', alipay_account: '', alipay_name: '' })
  useEffect(() => {
    let active = true
    request('/user/aff').then(data => { if (active) setDetail(data) }).catch(e => { if (active) setError(e.message) })
    return () => { active = false }
  }, [revision])
  useEffect(() => {
    let active = true
    setLoading(true)
    setRecords({ items: [], total: 0 })
    request(`/user/aff/records?kind=${kind}&page=${page}&page_size=20`).then(data => { if (active) setRecords(data) }).catch(e => { if (active) setError(e.message) }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [kind, page, revision])
  useEffect(() => {
    const next = Date.parse(detail?.next_thaw_at || '')
    if (!Number.isFinite(next)) return undefined
    const remaining = next - Date.now()
    const delay = remaining <= 0 ? 30000 : Math.min(remaining + 1000, 2147483647)
    const timer = setTimeout(() => setRevision(value => value + 1), delay)
    return () => clearTimeout(timer)
  }, [detail])
  useEffect(() => {
    const refresh = () => setRevision(value => value + 1)
    window.addEventListener('focus', refresh)
    return () => window.removeEventListener('focus', refresh)
  }, [])
  const showRebateStatus = kind === 'ledger' || kind === 'recharges'
  const canTransfer = detail && detail.aff_quota > 0 && detail.aff_quota >= detail.transfer_minimum
  async function transfer() {
    setBusy(true); setError(''); setNotice('')
    try {
      const result = await request('/user/aff/transfer', { method: 'POST' })
      setNotice(`已转入个人余额 ${money(result.transferred_quota)}`)
      setConfirmTransfer(false); setRevision(v => v + 1)
      request('/auth/me').then(data => setUser(data.user || data)).catch(() => {})
    } catch (e) { setError(e.message) } finally { setBusy(false) }
  }
  async function withdraw(event) {
    event.preventDefault(); setBusy(true); setError(''); setNotice('')
    try {
      await request('/user/aff/withdrawals', { method: 'POST', body: JSON.stringify({ ...form, amount: Number(form.amount) }) })
      setNotice('提现申请已提交，申请金额已从可用返利余额预扣。')
      setForm({ ...form, amount: '' }); setKind('withdrawals'); setPage(1); setRevision(v => v + 1)
    } catch (e) { setError(e.message) } finally { setBusy(false) }
  }
  async function copy(value) {
    try { await navigator.clipboard.writeText(value); setNotice('已复制') } catch { setError('复制失败，请手动复制邀请码。') }
  }
  return <section className="affiliate-page">
    <div className="page-head"><div><p className="kicker">YINGZO / INVITATIONS</p><h1>邀请返利</h1><p className="muted">分享创作工具，让每一次邀请都有回响。</p></div></div>
    {error && <div className="error" role="alert">{error}<button className="outline" onClick={() => { setError(''); setRevision(v => v + 1) }}>刷新</button></div>}
    {notice && <p className="affiliate-notice" role="status">{notice}</p>}
    {!detail ? <p role="status">正在加载返利账户…</p> : <>
      {!detail.enabled && <div className="affiliate-closed" role="status"><strong>邀请返利活动已关闭</strong><p>已停止邀请奖励与提现申请，历史记录保留。可用返利余额仍可转换为个人余额，转换门槛为 {money(detail.transfer_minimum)}。</p></div>}
      <div className="affiliate-stats">
        <div><small>可用返利余额</small><strong>{money(detail.aff_quota)}</strong><p>可提现 / 转余额</p></div>
        <div className="affiliate-frozen-stat"><small>冻结中返利</small><strong>{money(detail.aff_frozen_quota)}</strong><p>暂不能提现或转换</p></div>
        <div><small>累计获得返利</small><strong>{money(detail.aff_history_quota)}</strong></div>
        <div><small>已邀请用户</small><strong>{detail.aff_count} <span>人</span></strong></div>
      </div>
      {detail.aff_debt_quota > 0 && <div className="affiliate-closed" role="status"><strong>待追回返利：{money(detail.aff_debt_quota)}</strong><p>关联充值已退款，已提现或转余额的返利不足以扣回。后续返利结算时先抵扣待追回金额，剩余部分才计入可用余额。</p></div>}
      {(detail.rebate_freeze_hours > 0 || detail.aff_frozen_quota > 0) && <div className="affiliate-freeze-info"><strong>{detail.rebate_freeze_hours > 0 ? `返利冻结期：${formatAffiliateFreezePeriod(detail.rebate_freeze_hours)}` : '历史返利仍在冻结期内'}</strong><p>每笔返利单独计时，冻结金额不计入可用余额，不能提现或转余额，到期后自动解冻；如有待追回金额，先抵扣后再计入可用余额。充值退款成功后，对应返利按退款比例撤销。</p>{detail.next_thaw_at && <p>最近一笔解冻时间：<time dateTime={detail.next_thaw_at}>{time(detail.next_thaw_at)}</time></p>}</div>}
      <div className="panel affiliate-code"><div><h2>你的专属邀请码</h2><code>{detail.aff_code}</code><p className="muted">活动期间，受邀用户首次充值赠送 {detail.first_recharge_bonus_percent}% 余额。你按直接受邀用户每次充值实付金额获得 {detail.effective_rebate_rate_percent}% 返利。</p></div><div className="affiliate-buttons"><button className="outline" onClick={() => copy(detail.aff_code)}>复制邀请码</button><button className="dark" disabled={!detail.enabled} onClick={() => copy(`${location.origin}/register?aff=${encodeURIComponent(detail.aff_code)}`)}>复制邀请链接 ↗</button></div></div>
      <div className="form-grid affiliate-funds">
        <div className="panel form-card"><h2>转换为个人余额</h2><p className="muted">一次性将全部可用返利转为个人账户余额，即时到账，用于创作消费。冻结中返利不参与转换。</p><p>最低转换金额：{money(detail.transfer_minimum)}</p><button className="dark" disabled={busy || !canTransfer} onClick={() => setConfirmTransfer(true)}>转换 {money(detail.aff_quota)} →</button>{!canTransfer && <p className="muted">可用返利余额不足或未达到转换门槛。</p>}</div>
        <form className="panel form-card" onSubmit={withdraw}><h2>提现到支付宝</h2><p className="muted">最低提现金额：{money(detail.withdraw_minimum)}。提交后由管理员手动支付，请确认实名信息准确。</p><label>提现金额（元）<input type="number" step="0.01" min={Math.max(0.01, detail.withdraw_minimum)} max={Math.floor(detail.aff_quota * 100) / 100} value={form.amount} onChange={e => setForm({ ...form, amount: e.target.value })} required disabled={busy || !detail.enabled}/></label><label>支付宝账号<input value={form.alipay_account} maxLength={128} onChange={e => setForm({ ...form, alipay_account: e.target.value })} required disabled={busy || !detail.enabled}/></label><label>支付宝实名姓名<input value={form.alipay_name} maxLength={64} onChange={e => setForm({ ...form, alipay_name: e.target.value })} required disabled={busy || !detail.enabled}/></label><button className="dark" disabled={busy || !detail.enabled || detail.aff_quota < Math.max(0.01, detail.withdraw_minimum)}>{busy ? '处理中…' : '提交提现申请 →'}</button></form>
      </div>
    </>}
    <div className="panel affiliate-records"><div className="affiliate-tabs" role="tablist" aria-label="邀请记录">{tabs.map(([id, label]) => <button key={id} role="tab" aria-selected={kind === id} className={kind === id ? 'active' : ''} onClick={() => { setKind(id); setPage(1) }}>{label}</button>)}</div>
      <div className="affiliate-table-scroll"><table>
        <thead><tr><th>时间</th>{kind === 'invites' || kind === 'recharges' ? <th>受邀用户</th> : <th>类型 / 状态</th>}{kind === 'recharges' && <th>实付金额</th>}<th>{kind === 'invites' || kind === 'recharges' ? '产生返利' : '金额'}</th>{showRebateStatus && <th>返利状态 / 解冻时间</th>}{kind === 'withdrawals' && <><th>收款信息</th><th>处理备注</th></>}</tr></thead>
        <tbody>{loading ? <tr><td colSpan={6}>正在加载…</td></tr> : records.items?.length ? records.items.map(row => {
          const rebateState = affiliateRebateStatus(row, kind)
          return <tr key={row.id}>
            <td>{time(row.created_at)}</td><td>{kind === 'invites' || kind === 'recharges' ? row.email : labels[kind === 'ledger' ? row.action : row.status] || row.action}{row.source_order_id && <small className="affiliate-release-time">订单 #{row.source_order_id}</small>}</td>
            {kind === 'recharges' && <td>{money(row.pay_amount)}</td>}<td>{money(kind === 'invites' || kind === 'recharges' ? row.rebate_amount : row.amount)}{row.reversed_amount > 0 && <small className="affiliate-release-time">已撤销 {money(row.reversed_amount)}</small>}</td>
            {showRebateStatus && <td>{rebateState ? <><span className={`affiliate-rebate-state ${rebateState.frozen ? 'frozen' : 'available'}`}>{rebateState.label}</span>{rebateState.until && <small className="affiliate-release-time"><time dateTime={rebateState.until}>{time(rebateState.until)}</time> 解冻</small>}</> : '—'}</td>}
            {kind === 'withdrawals' && <><td>{row.alipay_account}<br/>{row.alipay_name}</td><td>{row.admin_note || '—'}</td></>}
          </tr>
        }) : <tr><td colSpan={6}>暂无记录</td></tr>}</tbody>
      </table></div>
      <div className="affiliate-pagination"><span>共 {records.total || 0} 条</span><button className="outline" disabled={loading || page === 1} onClick={() => setPage(page - 1)}>上一页</button><span>{page} / {Math.max(1, Math.ceil((records.total || 0) / 20))}</span><button className="outline" disabled={loading || page * 20 >= records.total} onClick={() => setPage(page + 1)}>下一页</button></div>
    </div>
    {confirmTransfer && <div className="modal-backdrop" onMouseDown={e => { if (e.target === e.currentTarget && !busy) setConfirmTransfer(false) }}><div className="modal-card" role="dialog" aria-modal="true" aria-labelledby="transfer-title"><h2 id="transfer-title">确认转换返利余额？</h2><p>将 {money(detail.aff_quota)} 转为创作消费余额。转换成功后，这笔余额无法再申请支付宝提现。</p><div className="modal-actions"><button className="outline" disabled={busy} onClick={() => setConfirmTransfer(false)}>取消</button><button className="dark" disabled={busy} onClick={transfer}>{busy ? '转换中…' : '确认转换'}</button></div></div></div>}
  </section>
}
