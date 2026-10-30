import { message, getIntlLocale, t as tr } from "./i18n/index.js";
import { Trans } from "react-i18next";
import { useEffect, useRef, useState } from 'react';
import { request } from './lib.js';
const labels = {
  get image() {
    return tr("图片");
  },
  get audio() {
    return tr("音频");
  },
  get video() {
    return tr("视频");
  }
};
function OutputPreview({
  output,
  index
}) {
  const [failed, setFailed] = useState(false);
  const [copyMessage, setCopyMessage] = useState('');
  const title = output.title || `${labels[output.media_type]} ${index + 1}`;
  async function copyLink() {
    try {
      await navigator.clipboard.writeText(output.url);
      setCopyMessage(message("链接已复制"));
    } catch {
      setCopyMessage(message("复制失败，请选择下方链接复制"));
    }
  }
  return <article className="usage-output">
    <h3>{title}</h3>
    {failed ? <p className="muted" role="status">{tr("预览暂不可用，可尝试打开产物链接。")}</p> : output.media_type === 'image' ? <img src={output.url} alt={title} onError={() => setFailed(true)} /> : output.media_type === 'audio' ? <audio src={output.url} controls preload="metadata" aria-label={title} onError={() => setFailed(true)} /> : <video src={output.url} controls playsInline preload="metadata" aria-label={title} onError={() => setFailed(true)} />}
    <div className="usage-output-actions"><a href={output.url} target="_blank" rel="noopener noreferrer"><Trans i18nKey="打开<0/>" components={[<>{labels[output.media_type]}</>]} /></a><button type="button" onClick={copyLink}>{tr("复制链接")}</button><span role="status">{tr(copyMessage)}</span></div>
    <a className="usage-output-url" href={output.url} target="_blank" rel="noopener noreferrer">{output.url}</a>
    <p className="usage-output-expiry"><Trans i18nKey="有效期至 <0/>" components={[<>{new Date(output.expires_at).toLocaleString(getIntlLocale(), {
          hour12: false
        })}</>]} /></p>
  </article>;
}
export default function UsageOutputDialog({
  row,
  outputs,
  onOutputsChange,
  onClose
}) {
  const dialog = useRef(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    request(`/usage/${row.id}`, {
      signal: controller.signal
    }).then(fresh => {
      if (!controller.signal.aborted) onOutputsChange(row.id, fresh.task_outputs || []);
    }).catch(err => {
      if (controller.signal.aborted) return;
      if (err.status === 404 || err.status === 410) onOutputsChange(row.id, []);else setError(err.message || message("请稍后重试"));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [row.id, onOutputsChange, attempt]);
  return <dialog ref={dialog} className="modal-card usage-output-dialog" aria-labelledby="usage-output-title" onClose={onClose} onClick={event => {
    if (event.target === event.currentTarget) dialog.current.close();
  }}>
    <h2 id="usage-output-title">{tr("产物详情")}</h2>
    <p className="muted usage-output-model">{row.model}</p>
    <div aria-busy={loading}>
      {loading ? <p className="empty" role="status">{tr("正在加载产物…")}</p> : error ? <div role="alert"><p><Trans i18nKey="产物加载失败：<0/>" components={[<>{tr(error)}</>]} /></p><button className="outline" onClick={() => setAttempt(value => value + 1)}>{tr("重试")}</button></div> : outputs.length ? <div className="usage-output-list">{outputs.map((output, index) => <OutputPreview key={output.url} output={output} index={index} />)}</div> : <p className="empty" role="status">{tr("产物已过期或被删除。")}</p>}
    </div>
    <div className="modal-actions"><button className="dark" autoFocus onClick={() => dialog.current.close()}>{tr("关闭")}</button></div>
  </dialog>;
}
