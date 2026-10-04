import { useEffect, useRef, useState } from 'react'
import { isRefund, refundError, usageType } from './usage-records.js'

function dateTime(value) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}

function duration(value) {
  if (value == null) return '—'
  const seconds = Number(value) / 1000
  return seconds < 1 ? `${Math.round(Number(value))} ms` : seconds < 60 ? `${seconds.toFixed(1)} 秒` : `${Math.floor(seconds / 60)} 分 ${Math.round(seconds % 60)} 秒`
}

export default function UsageRecords({ rows, emptyMessage = '暂无使用记录' }) {
  const [selected, setSelected] = useState(null)
  const dialog = useRef(null)
  useEffect(() => {
    if (selected) dialog.current?.showModal()
  }, [selected])

  return <>
    <div className="usage-table-scroll">
      <div className="usage-table">
        <div className="table-head usage-head"><span>时间</span><span>类型</span><span>模型</span><span>用时</span><span>消耗</span><span aria-hidden="true" /></div>
        {rows.length ? rows.map((row, i) => {
          const refunded = isRefund(row)
          const error = refundError(row)
          return <div className="table-row usage-row" key={row.id || i}>
            <span>{dateTime(row.created_at)}</span>
            <span>{usageType(row)}</span>
            <span className="usage-model">{row.model || '—'}</span>
            <span>{refunded ? <span className="usage-refund" title={row.funds_event === 'settlement_refund' ? '结算差额已退回' : '费用已退回'}>已退费</span> : duration(row.duration_ms)}</span>
            <span>{`¥${Number(row.actual_cost ?? row.total_cost ?? row.cost ?? 0).toFixed(5)}`}</span>
            <span className="usage-error-cell">{error && <button className="usage-error-code" aria-label={`查看${error.code || ''}错误详情`} onClick={() => setSelected({ row, error })}>{error.code || '详情'}</button>}</span>
          </div>
        }) : <div className="empty">{emptyMessage}</div>}
      </div>
    </div>
    <dialog ref={dialog} className="modal-card usage-error-dialog" aria-labelledby="usage-error-title" onClose={() => setSelected(null)} onClick={event => { if (event.target === event.currentTarget) dialog.current.close() }}>
      {selected && <div>
        <h2 id="usage-error-title">任务失败{selected.error.code ? ` · ${selected.error.code}` : ''}</h2>
        <p className="muted">{selected.row.model} · 费用已退回</p>
        <p className="usage-error-label">{selected.error.code ? '上游报错信息' : '错误信息'}</p>
        <pre className="usage-error-message">{selected.error.message || '未保存上游错误详情。'}</pre>
        <div className="modal-actions"><button className="dark" autoFocus onClick={() => dialog.current.close()}>关闭</button></div>
      </div>}
    </dialog>
  </>
}
