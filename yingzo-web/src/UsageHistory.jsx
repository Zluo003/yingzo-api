import { useEffect, useState } from 'react'
import { request } from './lib.js'
import UsageRecords from './UsageRecords.jsx'
import { usageRecordsQuery } from './usage-records.js'

export default function UsageHistory({ query, setQuery }) {
  const [rows, setRows] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    request(`/usage?${usageRecordsQuery(query)}`, { signal: controller.signal })
      .then(data => {
        if (controller.signal.aborted) return
        const items = Array.isArray(data) ? data : data?.items || []
        const count = Number(data?.total ?? items.length)
        const lastPage = Math.max(1, Math.ceil(count / query.pageSize))
        if (query.page > lastPage) {
          setQuery(current => ({ ...current, page: lastPage }))
          return
        }
        setRows(items)
        setTotal(count)
      })
      .catch(err => { if (!controller.signal.aborted) { setRows([]); setTotal(0); setError(err.message || '请稍后重试') } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [query])

  const pages = Math.max(1, Math.ceil(total / query.pageSize))
  const filtered = Object.values(query.filters).some(Boolean)

  return <section className="panel usage-history" aria-label="使用记录明细">
    <div aria-busy={loading}>
      {loading ? <div className="empty" role="status">正在加载使用记录…</div> : error ? <div className="usage-load-error" role="alert"><p>使用记录加载失败：{error}</p><button className="outline" onClick={() => setQuery(current => ({ ...current }))}>重试</button></div> : <UsageRecords rows={rows} emptyMessage={filtered ? '没有符合筛选条件的使用记录' : '暂无使用记录'} />}
    </div>
    <div className="usage-foot">
      <span aria-live="polite">{loading ? '正在查询…' : error ? '未能加载记录' : `${filtered ? '筛选结果 · ' : ''}共 ${total} 条记录`}</span>
      <div className="pager">
        <label className="page-size">每页 <select value={query.pageSize} onChange={event => setQuery(current => ({ ...current, pageSize: Number(event.target.value), page: 1 }))}>{[20, 50, 100].map(size => <option key={size} value={size}>{size} 条</option>)}</select></label>
        <button disabled={loading || !!error || query.page <= 1} onClick={() => setQuery(current => ({ ...current, page: current.page - 1 }))}>上一页</button>
        <span>第 {query.page} / {pages} 页</span>
        <button disabled={loading || !!error || query.page >= pages} onClick={() => setQuery(current => ({ ...current, page: current.page + 1 }))}>下一页</button>
      </div>
    </div>
  </section>
}
