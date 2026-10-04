import { useState } from 'react'
import { defaultUsageFilters } from './usage-records.js'

export default function UsageFilters({ filters, models, onApply }) {
  const [draft, setDraft] = useState(filters)
  const [error, setError] = useState('')
  function update(key, value) {
    setDraft(current => ({ ...current, [key]: value }))
    setError('')
  }
  function submit(event) {
    event.preventDefault()
    if (!draft.startDate || !draft.endDate) return setError('请选择开始日期和结束日期')
    if (draft.startDate > draft.endDate) return setError('开始日期不能晚于结束日期')
    onApply({ ...draft, model: draft.model.trim() })
  }
  function reset() {
    const next = defaultUsageFilters()
    setDraft(next)
    setError('')
    onApply(next)
  }
  return <section className="panel usage-filter-panel" aria-label="使用记录筛选">
    <form className="usage-filters" onSubmit={submit} noValidate>
      <label>开始日期<input type="date" required value={draft.startDate} max={draft.endDate || undefined} onChange={event => update('startDate', event.target.value)} /></label>
      <label>结束日期<input type="date" required value={draft.endDate} min={draft.startDate || undefined} onChange={event => update('endDate', event.target.value)} /></label>
      <label className="usage-model-filter">模型<input type="text" list="usage-model-options" placeholder="全部模型，可选择或输入" value={draft.model} onChange={event => update('model', event.target.value)} autoComplete="off" /></label>
      <datalist id="usage-model-options">{models.map(model => <option key={model} value={model} />)}</datalist>
      <label>类型<select value={draft.type} onChange={event => update('type', event.target.value)}><option value="">全部类型</option><option value="text">文本</option><option value="image">图片</option><option value="video">视频</option></select></label>
      <div className="usage-filter-actions"><button className="dark" type="submit">筛选</button><button className="outline" type="button" onClick={reset}>重置</button></div>
    </form>
    {error && <p className="error" role="alert">{error}</p>}
    <p className="usage-range">统计范围：{filters.startDate} 至 {filters.endDate} · 图表与明细同步筛选</p>
  </section>
}
