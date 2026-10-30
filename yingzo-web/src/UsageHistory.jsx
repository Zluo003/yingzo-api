import { message, t as tr } from "./i18n/index.js";
import { Trans } from "react-i18next";
import { useCallback, useEffect, useState } from 'react';
import { request } from './lib.js';
import UsageRecords from './UsageRecords.jsx';
import { usageRecordsQuery } from './usage-records.js';
export default function UsageHistory({
  query,
  setQuery
}) {
  const [rows, setRows] = useState([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const onOutputsChange = useCallback((id, outputs) => {
    setRows(current => current.map(row => row.id === id ? {
      ...row,
      task_outputs: outputs
    } : row));
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    let inFlight = false;
    function load(background = false) {
      if (inFlight) return;
      inFlight = true;
      request(`/usage?${usageRecordsQuery(query)}`, {
        signal: controller.signal
      }).then(data => {
        if (controller.signal.aborted) return;
        const items = Array.isArray(data) ? data : data?.items || [];
        const count = Number(data?.total ?? items.length);
        const lastPage = Math.max(1, Math.ceil(count / query.pageSize));
        if (query.page > lastPage) {
          setQuery(current => ({
            ...current,
            page: lastPage
          }));
          return;
        }
        setRows(items);
        setTotal(count);
        setError('');
      }).catch(err => {
        if (!controller.signal.aborted && !background) {
          setRows([]);
          setTotal(0);
          setError(err.message || message("请稍后重试"));
        }
      }).finally(() => {
        inFlight = false;
        if (!controller.signal.aborted) setLoading(false);
      });
    }
    load();
    const refresh = () => {
      if (document.visibilityState === 'visible') load(true);
    };
    const timer = setInterval(refresh, 30000);
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      controller.abort();
      clearInterval(timer);
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, [query]);
  const pages = Math.max(1, Math.ceil(total / query.pageSize));
  const filtered = Object.values(query.filters).some(Boolean);
  return <section className="panel usage-history" aria-label={tr("使用记录明细")}>
    <div aria-busy={loading}>
      {loading ? <div className="empty" role="status">{tr("正在加载使用记录…")}</div> : error ? <div className="usage-load-error" role="alert"><p><Trans i18nKey="使用记录加载失败：<0/>" components={[<>{tr(error)}</>]} /></p><button className="outline" onClick={() => setQuery(current => ({
          ...current
        }))}>{tr("重试")}</button></div> : <UsageRecords rows={rows} onOutputsChange={onOutputsChange} emptyMessage={filtered ? tr("没有符合筛选条件的使用记录") : tr("暂无使用记录")} />}
    </div>
    <div className="usage-foot">
      <span aria-live="polite">{loading ? tr("正在查询…") : error ? tr("未能加载记录") : tr("{{value1}}共 {{value2}} 条记录", {
          value1: filtered ? tr("筛选结果 · ") : '',
          value2: total
        })}</span>
      <div className="pager">
        <label className="page-size">{tr("每页 ")}<select value={query.pageSize} onChange={event => setQuery(current => ({
            ...current,
            pageSize: Number(event.target.value),
            page: 1
          }))}>{[20, 50, 100].map(size => <option key={size} value={size}><Trans i18nKey="<0/> 条" components={[<>{size}</>]} /></option>)}</select></label>
        <button disabled={loading || !!error || query.page <= 1} onClick={() => setQuery(current => ({
          ...current,
          page: current.page - 1
        }))}>{tr("上一页")}</button>
        <span><Trans i18nKey="第 <0/> / <1/> 页" components={[<>{query.page}</>, <>{pages}</>]} /></span>
        <button disabled={loading || !!error || query.page >= pages} onClick={() => setQuery(current => ({
          ...current,
          page: current.page + 1
        }))}>{tr("下一页")}</button>
      </div>
    </div>
  </section>;
}
