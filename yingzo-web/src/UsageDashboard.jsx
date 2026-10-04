import { useEffect, useState } from 'react'
import { request } from './lib.js'
import UsageFilters from './UsageFilters.jsx'
import UsageHistory from './UsageHistory.jsx'
import { defaultUsageFilters, trendBarPercent, usageFilterQuery } from './usage-records.js'

const PIE_COLORS = ['#8b6f5e', '#415687', '#a48a78', '#6b856b', '#b78a9d', '#7d6f9b', '#b0975f', '#5f8a8b']
const costOf = row => Number(row.actual_cost ?? row.cost ?? 0)
const money = value => value >= 100 ? value.toFixed(0) : value >= 1 ? value.toFixed(2) : value.toFixed(4)

export default function UsageDashboard() {
  const [query, setQuery] = useState(() => ({ page: 1, pageSize: 20, filters: defaultUsageFilters() }))
  const [data, setData] = useState({ stats: null, trend: [], models: [] })
  const [knownModels, setKnownModels] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const filters = query.filters
  const granularity = filters.startDate === filters.endDate ? 'hour' : 'day'

  useEffect(() => {
    const controller = new AbortController()
    const params = usageFilterQuery(filters)
    setLoading(true)
    setError('')
    Promise.all([
      request(`/usage/stats?${params}`, { signal: controller.signal }),
      request(`/usage/dashboard/snapshot-v2?${params}&granularity=${granularity}`, { signal: controller.signal }),
    ]).then(([stats, snapshot]) => {
      if (controller.signal.aborted) return
      const models = (Array.isArray(snapshot?.models) ? snapshot.models : []).slice().sort((a, b) => costOf(b) - costOf(a))
      setData({ stats, trend: Array.isArray(snapshot?.trend) ? snapshot.trend : [], models })
      setKnownModels(current => [...new Set([...current, ...models.map(model => model.model).filter(Boolean)])].sort())
    }).catch(err => {
      if (!controller.signal.aborted) { setData({ stats: null, trend: [], models: [] }); setError(err.message || '请稍后重试') }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [filters, granularity])

  const { stats, trend, models } = data
  const maxCost = trend.reduce((max, row) => Math.max(max, costOf(row)), 0)
  const positiveModels = models.filter(model => costOf(model) > 0)
  // Keep the distribution complete when more than eight models match.
  const pieModels = positiveModels.length > 8 ? [...positiveModels.slice(0, 7), { model: '其他模型', actual_cost: positiveModels.slice(7).reduce((sum, model) => sum + costOf(model), 0) }] : positiveModels
  const modelTotal = pieModels.reduce((sum, model) => sum + costOf(model), 0)
  let pieOffset = 0

  return <>
    <UsageFilters filters={filters} models={knownModels} onApply={next => setQuery(current => ({ ...current, page: 1, filters: next }))} />
    <div className="stats usage-stats" aria-busy={loading}>
      <div><small>请求数</small><strong>{loading ? '…' : stats?.total_requests ?? '—'}</strong></div>
      <div><small>生成 Tokens</small><strong>{loading ? '…' : stats?.total_tokens ?? '—'}</strong></div>
      <div><small>消耗</small><strong>{loading ? '…' : stats ? `¥${Number(stats.total_actual_cost ?? stats.total_cost ?? 0).toFixed(2)}` : '—'}</strong></div>
    </div>
    {error && <div className="usage-load-error panel" role="alert"><p>统计与图表加载失败：{error}</p><button className="outline" onClick={() => setQuery(current => ({ ...current, filters: { ...current.filters } }))}>重试</button></div>}
    <div className="chart-duo" aria-busy={loading}>
      <div className="panel chart">
        <div className="panel-title">使用趋势 <span className="chart-sub">{granularity === 'hour' ? '按小时' : '按日期'} · 按费用</span></div>
        {loading ? <div className="chart-empty" role="status">正在加载图表…</div> : trend.length ? <div className="chart-series-scroll"><div className="bars" style={{ minWidth: trend.length * 48 }}>{trend.map(row => {
          const cost = costOf(row)
          const label = granularity === 'hour' ? String(row.date || '').slice(-5) : String(row.date || '').slice(5)
          return <div className="bar-item" key={row.date} title={`${row.date}：¥${cost.toFixed(4)} · ${row.requests || 0} 次请求`}>
            <div className="bar-figure" style={{ '--bar-height': `${trendBarPercent(cost, maxCost)}%` }}><span className="bar-value">¥{money(cost)}</span><i /></div>
            <span className="bar-label">{label}</span>
          </div>
        })}</div></div> : <div className="chart-empty">{error ? '图表暂不可用' : '当前筛选条件下暂无消耗记录'}</div>}
      </div>
      <div className="panel chart">
        <div className="panel-title">模型使用分布 <span className="chart-sub">按费用</span></div>
        {loading ? <div className="chart-empty" role="status">正在加载图表…</div> : modelTotal > 0 ? <div className="pie-wrap">
          <svg className="pie" viewBox="0 0 42 42" role="img" aria-label="当前筛选范围内的模型费用分布">{pieModels.map((model, index) => {
            const share = costOf(model) / modelTotal * 100
            const offset = pieOffset
            pieOffset += share
            return <circle key={model.model} r="15.9155" cx="21" cy="21" fill="transparent" stroke={PIE_COLORS[index]} strokeWidth="8" strokeDasharray={`${share} ${100 - share}`} strokeDashoffset={25 - offset} />
          })}</svg>
          <div className="pie-legend">{pieModels.map((model, index) => <div className="pie-legend-item" key={model.model}><span className="dot" style={{ background: PIE_COLORS[index] }} /><span className="pie-model">{model.model}</span><span className="pie-share">{Math.round(costOf(model) / modelTotal * 100)}%</span></div>)}</div>
        </div> : <div className="chart-empty">{error ? '图表暂不可用' : '当前筛选条件下暂无消耗记录'}</div>}
      </div>
    </div>
    <UsageHistory query={query} setQuery={setQuery} />
  </>
}
