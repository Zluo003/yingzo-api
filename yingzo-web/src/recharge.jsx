import { formatNumber, message, getLocale, t as tr } from "./i18n/index.js";
import { Trans } from "react-i18next";
// Recharge / payment pages — functional parity with frontend/src/views/user/PaymentView.vue,
// UserOrdersView.vue, RedeemView.vue, PaymentResultView.vue, StripePaymentView.vue and
// AirwallexPaymentView.vue, rebuilt in the yingzo-web design language.
import { useCallback, useEffect, useRef, useState } from 'react';
import QRCode from 'qrcode';
import { request, isMobileDevice, isWechatBrowser, getVisibleMethods, sortMethodEntries, normalizeVisibleMethod, METHOD_LABELS, buildCreateOrderPayload, decidePaymentLaunch, buildStripeRouteUrl, buildAirwallexRouteUrl, buildPaymentResultUrl, readPaymentRecoverySnapshot, writePaymentRecoverySnapshot, clearPaymentRecoverySnapshot, buildAlipayDeepLink, currencySymbol, formatPaymentAmount, feeAmountFor, totalAmountFor, formatPaymentDate, relativeTime, renderMarkdown, ORDER_STATUS_LABELS, isTerminalSuccess } from './lib.js';
import { buildRechargeAmountPresets, getRechargeAmountRange } from './recharge-amounts.js';
const POLL_INTERVAL_MS = 3000;
const VERIFY_INTERVAL_MS = 15000;
const VERIFY_MAX_ATTEMPTS = 6;
const PAYMENT_POPUP_FEATURES = (() => {
  const availW = typeof screen !== 'undefined' && screen.availWidth || 1250;
  const availH = typeof screen !== 'undefined' && screen.availHeight || 900;
  const w = Math.min(1250, availW - 40);
  const h = Math.min(900, availH - 40);
  const left = Math.max(0, Math.floor((availW - w) / 2));
  const top = Math.max(0, Math.floor((availH - h) / 2));
  return `width=${w},height=${h},left=${left},top=${top},scrollbars=yes,resizable=yes`;
})();
function openPaymentWindow(url) {
  const win = window.open(url, 'paymentPopup', PAYMENT_POPUP_FEATURES);
  if (!win || win.closed) window.location.href = url;
}
function queryParam(name) {
  return new URLSearchParams(location.search).get(name) || '';
}
function planValiditySuffix(plan) {
  const unit = String(plan.validity_unit || 'day').toLowerCase();
  const days = Number(plan.validity_days || 0);
  if (unit.startsWith('week')) return tr("{{value1}} 周", {
    value1: days
  });
  if (unit.startsWith('month')) return tr("{{value1}} 个月", {
    value1: days
  });
  if (unit.startsWith('year')) return tr("{{value1}} 年", {
    value1: days
  });
  return tr("{{value1}} 天", {
    value1: days
  });
}

// ---------------------------------------------------------------------------
// Paying status panel: QR / redirect / alipay deep link + polling + countdown
// ---------------------------------------------------------------------------
function PayPanel({
  info,
  state,
  phase,
  onPhase,
  onCancel,
  onDone,
  onRefreshBalance
}) {
  const [now, setNow] = useState(Date.now());
  const [copied, setCopied] = useState(false);
  const [deepLinkState, setDeepLinkState] = useState('idle');
  const canvasRef = useRef(null);
  const verifyRef = useRef(null);
  const isBuiltIn = ['alipay', 'wxpay'].includes(normalizeVisibleMethod(state.paymentType));
  const useDeepLink = state.paymentType === 'alipay' && isMobileDevice() && !!state.qrCode && state.alipayMobilePrecreateDeepLink === true;
  const expiresMs = Date.parse(state.expiresAt);
  const remainingMs = Number.isFinite(expiresMs) ? expiresMs - now : 0;
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  // Render the payment QR (content string) onto a canvas, with save-as-PNG support.
  const showQr = !!state.qrCode && (!useDeepLink || deepLinkState === 'fallback');
  useEffect(() => {
    if (showQr && canvasRef.current) {
      QRCode.toCanvas(canvasRef.current, state.qrCode, {
        width: 208,
        margin: 2,
        color: {
          dark: '#30241e',
          light: '#faf7f2'
        }
      }, () => {});
    }
  }, [showQr, state.qrCode]);

  // Poll order status; fall back to an upstream verify every 15s (max 6) for built-in methods.
  useEffect(() => {
    if (phase !== 'paying') return undefined;
    let alive = true;
    async function tick() {
      try {
        const order = await request(`/payment/orders/${state.orderId}`);
        if (!alive) return;
        const status = String(order?.status || '').toUpperCase();
        if (isTerminalSuccess(status)) onPhase('success');else if (status === 'CANCELLED') onPhase('cancelled');else if (['EXPIRED', 'FAILED'].includes(status)) onPhase('expired');
      } catch {/* transient poll errors are ignored */}
    }
    const pollTimer = setInterval(tick, POLL_INTERVAL_MS);
    tick();
    if (isBuiltIn && state.outTradeNo) {
      let attempts = 0;
      verifyRef.current = setInterval(async () => {
        attempts += 1;
        if (attempts >= VERIFY_MAX_ATTEMPTS && verifyRef.current) clearInterval(verifyRef.current);
        try {
          await request('/payment/orders/verify', {
            method: 'POST',
            body: JSON.stringify({
              out_trade_no: state.outTradeNo
            })
          });
        } catch {/* verify is best-effort */}
      }, VERIFY_INTERVAL_MS);
    }
    return () => {
      alive = false;
      clearInterval(pollTimer);
      if (verifyRef.current) clearInterval(verifyRef.current);
    };
  }, [phase, state.orderId, state.outTradeNo, isBuiltIn, onPhase]);

  // Local countdown: when it hits zero, the order has expired.
  useEffect(() => {
    if (phase === 'paying' && Number.isFinite(expiresMs) && remainingMs <= 0) onPhase('expired');
  }, [phase, expiresMs, remainingMs, onPhase]);

  // Mobile Alipay deep-link launcher: try the app once, fall back to the QR after a delay.
  const deepLink = useDeepLink ? buildAlipayDeepLink(state.qrCode) : '';
  useEffect(() => {
    if (phase !== 'paying' || !deepLink || deepLinkState !== 'idle') return undefined;
    setDeepLinkState('launching');
    const restricted = /MicroMessenger|MQQBrowser|\bQQ\//i.test(navigator.userAgent);
    const timer = setTimeout(() => {
      setDeepLinkState(document.hidden ? 'backgrounded' : 'fallback');
    }, restricted ? 300 : 2200);
    const onVis = () => {
      if (document.hidden) setDeepLinkState('backgrounded');
    };
    document.addEventListener('visibilitychange', onVis);
    try {
      window.location.href = deepLink;
    } catch {
      setDeepLinkState('fallback');
    }
    return () => {
      clearTimeout(timer);
      document.removeEventListener('visibilitychange', onVis);
    };
  }, [phase, deepLink, deepLinkState]);

  // Terminal states clear the recovery snapshot; success also refreshes the balance.
  useEffect(() => {
    if (phase === 'success') {
      clearPaymentRecoverySnapshot();
      onRefreshBalance();
    } else if (phase === 'expired' || phase === 'cancelled') {
      clearPaymentRecoverySnapshot();
    }
  }, [phase, onRefreshBalance]);
  function saveQr() {
    if (!canvasRef.current) return;
    try {
      const link = document.createElement('a');
      link.download = `yingzo-payment-${state.orderId}.png`;
      link.href = canvasRef.current.toDataURL('image/png');
      link.click();
    } catch {/* saving is best-effort */}
  }
  const statusText = {
    paying: tr("等待支付完成…"),
    success: tr("支付成功，余额即将到账"),
    cancelled: tr("订单已取消"),
    expired: tr("订单已过期")
  }[phase] || '';
  const amountLine = state.payAmount ? `${formatPaymentAmount(state.payAmount, state.currency)}${state.orderType === 'subscription' ? tr(" · 订阅") : tr(" · 余额充值")}` : '';
  return <div className="panel pay-panel">
    <div className={`pay-status ${phase}`}>
      <span className={`pay-status-dot ${phase}`} />
      {statusText}
      {phase === 'paying' && remainingMs > 0 && <span className="pay-countdown"><Trans i18nKey="剩余 <0/>:<1/>" components={[<>{Math.floor(remainingMs / 60000)}</>, <>{String(Math.floor(remainingMs % 60000 / 1000)).padStart(2, '0')}</>]} /></span>}
    </div>
    {amountLine && <p className="pay-amount-line">{amountLine}{state.outTradeNo ? <span className="pay-trade-no"><Trans i18nKey=" · 单号 <0/>" components={[<>{state.outTradeNo}</>]} /></span> : null}</p>}

    {phase === 'paying' && <>
      {useDeepLink && deepLinkState !== 'fallback' && <div className="pay-deeplink">
        <p className="muted">{deepLinkState === 'backgrounded' ? tr("已跳转到支付宝，完成支付后本页会自动确认。") : tr("正在为你拉起支付宝…")}</p>
        <button className="outline" onClick={() => {
          window.location.href = deepLink;
        }}>{tr("没反应？重新拉起支付宝")}</button>
      </div>}
      {showQr && <div className="pay-qr-wrap">
        <canvas ref={canvasRef} className="pay-qr" />
        <p className="muted pay-hint"><Trans i18nKey="请使用<0/>扫码支付" components={[<>{state.paymentType === 'wxpay' ? tr("微信") : tr("支付宝")}</>]} /></p>
        <div className="pay-qr-actions">
          <button className="outline" onClick={saveQr}>{tr("保存二维码")}</button>
          <button className="outline" onClick={() => {
            navigator.clipboard?.writeText(state.qrCode);
            setCopied(true);
            setTimeout(() => setCopied(false), 1600);
          }}>{copied ? tr("已复制") : tr("复制支付链接")}</button>
        </div>
      </div>}
      {state.payUrl && <div className="pay-url-row">
        <button className="dark" onClick={() => openPaymentWindow(state.payUrl)}>{tr("打开支付窗口")}</button>
        <span className="muted">{tr("若窗口被拦截，点击按钮重新打开")}</span>
      </div>}
      <button className="text-link" onClick={onCancel}>{tr("取消订单，返回")}</button>
    </>}

    {phase === 'success' && <div className="pay-final">
      <p className="muted">{tr("感谢支持，可以继续创作了。")}</p>
      <button className="dark" onClick={onDone}>{tr("完成")}</button>
    </div>}
    {(phase === 'cancelled' || phase === 'expired') && <div className="pay-final">
      <p className="muted">{phase === 'cancelled' ? tr("订单已取消，可以重新发起支付。") : tr("订单已过期或支付未完成，可重新下单。")}</p>
      <button className="dark" onClick={onDone}>{tr("返回重新下单")}</button>
    </div>}
    {info?.help_text && <details className="pay-help"><summary>{tr("支付遇到问题？")}</summary><div className="md-body muted" dangerouslySetInnerHTML={{
        __html: renderMarkdown(info.help_text)
      }} /></details>}
  </div>;
}

// ---------------------------------------------------------------------------
// Orders panel: pagination + status filter + cancel + refund request
// ---------------------------------------------------------------------------
function OrdersPanel({
  refreshKey
}) {
  const [rows, setRows] = useState([]);
  const [total, setTotal] = useState(0);
  const [pages, setPages] = useState(1);
  const [page, setPage] = useState(1);
  const [status, setStatus] = useState('');
  const [loading, setLoading] = useState(true);
  const [eligible, setEligible] = useState([]);
  const [modal, setModal] = useState(null); // {type:'cancel'|'refund', order}
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    request('/payment/orders/refund-eligible-providers').then(d => setEligible(d?.provider_instance_ids || [])).catch(() => {});
  }, []);
  useEffect(() => {
    setLoading(true);
    const params = new URLSearchParams({
      page: String(page),
      page_size: '10'
    });
    if (status) params.set('status', status);
    request(`/payment/orders/my?${params.toString()}`).then(d => {
      setRows(d?.items || []);
      setTotal(Number(d?.total || 0));
      setPages(Number(d?.pages || 1));
    }).catch(e => setError(e.message)).finally(() => setLoading(false));
  }, [page, status, refreshKey]);
  async function confirmAction() {
    if (!modal) return;
    setBusy(true);
    setError('');
    try {
      if (modal.type === 'cancel') {
        await request(`/payment/orders/${modal.order.id}/cancel`, {
          method: 'POST'
        });
      } else {
        if (!reason.trim()) {
          setError(message("请填写退款原因"));
          setBusy(false);
          return;
        }
        await request(`/payment/orders/${modal.order.id}/refund-request`, {
          method: 'POST',
          body: JSON.stringify({
            reason: reason.trim()
          })
        });
      }
      setModal(null);
      setReason('');
      setPage(1);
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  const canRefund = o => o.status === 'COMPLETED' && o.provider_instance_id && eligible.includes(o.provider_instance_id);
  return <div className="panel">
    <div className="orders-head">
      <h2>{tr("充值记录")}</h2>
      <select value={status} onChange={e => {
        setStatus(e.target.value);
        setPage(1);
      }}>
        <option value="">{tr("全部状态")}</option>
        {['PENDING', 'COMPLETED', 'FAILED', 'REFUNDED'].map(s => <option key={s} value={s}>{ORDER_STATUS_LABELS[s]}</option>)}
      </select>
    </div>
    {error && <div className="error">{tr(error)}</div>}
    <div className="table-head orders-head-cols"><span>{tr("单号")}</span><span>{tr("方式")}</span><span>{tr("金额")}</span><span>{tr("状态")}</span><span>{tr("时间")}</span><span>{tr("操作")}</span></div>
    {loading ? <div className="empty">{tr("正在加载…")}</div> : rows.length ? rows.map(o => <div className="table-row orders-row" key={o.id}>
        <span className="mono">{o.out_trade_no || `#${o.id}`}</span>
        <span>{METHOD_LABELS[normalizeVisibleMethod(o.payment_type)] || o.payment_type}</span>
        <span>{formatPaymentAmount(o.pay_amount, o.currency)}{Number(o.fee_rate) > 0 && <small className="muted"><Trans i18nKey="（含 <0/>% 手续费）" components={[<>{Number(o.fee_rate)}</>]} /></small>}</span>
        <span className={`order-status st-${String(o.status).toLowerCase()}`}>{ORDER_STATUS_LABELS[o.status] || o.status}</span>
        <span>{formatPaymentDate(o.created_at)}</span>
        <span className="actions">
          {o.status === 'PENDING' && <button onClick={() => setModal({
          type: 'cancel',
          order: o
        })}>{tr("取消")}</button>}
          {canRefund(o) && <button onClick={() => {
          setReason('');
          setModal({
            type: 'refund',
            order: o
          });
        }}>{tr("申请退款")}</button>}
        </span>
      </div>) : <div className="empty"><strong>{tr("还没有充值记录")}</strong><p>{tr("第一笔充值完成后会展示在这里。")}</p></div>}
    {total > 10 && <div className="usage-foot">
      <span><Trans i18nKey="共 <0/> 条记录" components={[<>{total}</>]} /></span>
      <div className="pager">
        <button disabled={page <= 1} onClick={() => setPage(p => p - 1)}>{tr("上一页")}</button>
        <span><Trans i18nKey="第 <0/> / <1/> 页" components={[<>{page}</>, <>{pages}</>]} /></span>
        <button disabled={page >= pages} onClick={() => setPage(p => p + 1)}>{tr("下一页")}</button>
      </div>
    </div>}
    {modal && <div className="modal-backdrop" onMouseDown={e => e.target === e.currentTarget && !busy && setModal(null)}>
      <div className="modal-card">
        {modal.type === 'cancel' ? <>
          <h2>{tr("取消这笔订单？")}</h2>
          <p className="muted"><Trans i18nKey="订单 <0/>（<1/>）尚未支付，取消后将无法继续支付。" components={[<>{modal.order.out_trade_no || `#${modal.order.id}`}</>, <>{formatPaymentAmount(modal.order.pay_amount, modal.order.currency)}</>]} /></p>
        </> : <>
          <h2>{tr("申请退款")}</h2>
          <p className="muted"><Trans i18nKey="订单 <0/>（<1/>）。请填写退款原因，提交后由平台审核。" components={[<>{modal.order.out_trade_no || `#${modal.order.id}`}</>, <>{formatPaymentAmount(modal.order.pay_amount, modal.order.currency)}</>]} /></p>
          <label>{tr("退款原因")}<textarea rows="3" value={reason} onChange={e => setReason(e.target.value)} placeholder={tr("请说明退款原因")} /></label>
        </>}
        {error && <div className="error">{tr(error)}</div>}
        <div className="modal-actions">
          <button className="outline" onClick={() => setModal(null)}>{tr("取消")}</button>
          <button className={`dark ${modal.type === 'cancel' ? 'danger-button' : ''}`} onClick={confirmAction} disabled={busy}>{busy ? tr("处理中…") : modal.type === 'cancel' ? tr("确认取消订单") : tr("提交申请")}</button>
        </div>
      </div>
    </div>}
  </div>;
}

// ---------------------------------------------------------------------------
// Redeem card
// ---------------------------------------------------------------------------
function RedeemCard({
  onRedeemed
}) {
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');
  const [error, setError] = useState('');
  const [history, setHistory] = useState([]);
  const [contact, setContact] = useState('');
  useEffect(() => {
    request('/redeem/history').then(list => setHistory((Array.isArray(list) ? list : []).slice(0, 5))).catch(() => {});
    request('/settings/public').then(s => setContact(s?.contact_info || '')).catch(() => {});
  }, []);
  async function redeem() {
    if (!code.trim() || busy) return;
    setBusy(true);
    setMsg('');
    setError('');
    try {
      const result = await request('/redeem', {
        method: 'POST',
        body: JSON.stringify({
          code: code.trim()
        })
      });
      const typeText = {
        balance: tr("余额"),
        concurrency: tr("并发额度"),
        subscription: tr("订阅")
      }[result.type] || result.type;
      setMsg(message("兑换成功：{{value1}} {{value2}}{{value3}}", {
        value1: typeText,
        value2: result.value ?? '',
        value3: result.new_balance != null ? message("，当前余额 {{value1}}{{value2}}", {
          value1: currencySymbol(),
          value2: formatNumber(Number(result.new_balance), {
            minimumFractionDigits: 2,
            maximumFractionDigits: 2
          })
        }) : ''
      }));
      setCode('');
      onRedeemed();
      request('/redeem/history').then(list => setHistory((Array.isArray(list) ? list : []).slice(0, 5))).catch(() => {});
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  return <div className="panel form-card redeem-card">
    <h2>{tr("兑换码")}</h2>
    <p className="muted">{tr("输入兑换码直接为账户充值或开通订阅。")}</p>
    <label>{tr("兑换码")}<input value={code} onChange={e => setCode(e.target.value)} onKeyDown={e => e.key === 'Enter' && redeem()} placeholder={tr("例如：YINGZO-XXXX")} />
    </label>
    <button className="dark" onClick={redeem} disabled={busy || !code.trim()}>{busy ? tr("兑换中…") : tr("立即兑换 →")}</button>
    {msg && <div className="redeem-ok">{tr(msg)}</div>}
    {error && <div className="error">{tr(error)}</div>}
    {contact && <p className="muted small-note"><Trans i18nKey="需要购买兑换码？联系我们：<0/>" components={[<>{contact}</>]} /></p>}
    {history.length > 0 && <div className="redeem-history">
      <span className="chart-sub">{tr("最近兑换")}</span>
      {history.map(h => <div className="table-row redeem-history-row" key={h.id}><span>{h.code}</span><span>{h.type}</span><span>{h.value}</span><span>{relativeTime(h.used_at || h.created_at)}</span></div>)}
    </div>}
  </div>;
}

// ---------------------------------------------------------------------------
// Recharge page
// ---------------------------------------------------------------------------
export function Recharge({
  user,
  setUser
}) {
  const [info, setInfo] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [balance, setBalance] = useState(user?.balance ?? null);
  const [tab, setTab] = useState('recharge');
  const [amount, setAmount] = useState(50);
  const [methods, setMethods] = useState([]);
  const [method, setMethod] = useState('');
  const [selectedPlan, setSelectedPlan] = useState(null);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');
  const [paying, setPaying] = useState(null); // {state, phase}
  const [ordersRefreshKey, setOrdersRefreshKey] = useState(0);
  const bootstrapped = useRef(false);
  const refreshBalance = useCallback(() => {
    return request('/auth/me').then(x => {
      const u = x?.user || x;
      if (u?.balance != null) setBalance(u.balance);
      setUser(prev => {
        const next = {
          ...(prev || {}),
          ...u
        };
        try {
          localStorage.setItem('auth_user', JSON.stringify(next));
        } catch {/* ignore */}
        return next;
      });
    }).catch(() => {});
  }, [setUser]);
  const handlePhase = useCallback(phase => {
    setPaying(p => p ? {
      ...p,
      phase
    } : p);
  }, []);
  useEffect(() => {
    if (bootstrapped.current) return;
    bootstrapped.current = true;
    refreshBalance();
    const params = new URLSearchParams(location.search);
    (async () => {
      try {
        const data = await request('/payment/checkout-info');
        setInfo(data);
        const range = getRechargeAmountRange(data);
        const presets = buildRechargeAmountPresets(range.min, range.max);
        setAmount(current => presets.includes(current) ? current : presets[0] ?? '');
        const visible = sortMethodEntries(Object.entries(getVisibleMethods(data.methods || {})).map(([name, limit]) => {
          const methodRange = getRechargeAmountRange({
            ...data,
            global_min: limit.single_min,
            global_max: limit.single_max
          });
          return [name, {
            ...limit,
            single_min: methodRange.min,
            single_max: methodRange.max
          }];
        }));
        setMethods(visible);
        setMethod(visible[0]?.[0] || '');
        // Wechat OAuth resume: re-create the order with the resume token / openid.
        const resume = parseWechatResume(params, data.plans || []);
        if (resume) {
          history.replaceState({}, '', location.pathname);
          clearPaymentRecoverySnapshot();
          if (resume.orderType === 'subscription') setTab('subscription');
          await createOrder(resume.amount, resume.orderType, resume.planId, {
            paymentType: resume.paymentType,
            wechatResumeToken: resume.token,
            openid: resume.openid
          });
          return;
        }
        // Restore an in-flight payment after a refresh (validates expiry + resume token).
        const routeResumeToken = params.get('resume_token') || params.get('wechat_resume_token') || '';
        const restored = readPaymentRecoverySnapshot({
          resumeToken: routeResumeToken || undefined
        });
        if (restored) {
          setPaying({
            state: restored,
            phase: 'paying'
          });
          const m = normalizeVisibleMethod(restored.paymentType);
          if (m) setMethod(m);
        } else {
          clearPaymentRecoverySnapshot();
        }
        if (data.balance_disabled) setTab('subscription');
        if (params.get('tab') === 'subscription') {
          setTab('subscription');
          const groupId = Number(params.get('group'));
          if (groupId) {
            const groupPlans = (data.plans || []).filter(p => p.group_id === groupId);
            if (groupPlans.length === 1) setSelectedPlan(groupPlans[0]);
          }
        }
      } catch (e) {
        setError(e.message);
      } finally {
        setLoading(false);
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ---- order creation + launch decision (ported from PaymentView.createOrder) ----
  async function createOrder(orderAmount, orderType, planId, options = {}) {
    setSubmitting(true);
    setFormError('');
    const requestType = options.paymentType || method;
    try {
      const payload = buildCreateOrderPayload({
        amount: orderAmount,
        paymentType: requestType,
        orderType,
        planId,
        isMobile: isMobileDevice(),
        isWechatBrowser: isWechatBrowser(),
        forceQRCode: !!(info?.alipay_force_qrcode && normalizeVisibleMethod(requestType) === 'alipay'),
        mobilePrecreateDeepLink: info?.alipay_mobile_precreate_deep_link === true
      });
      if (options.openid) payload.openid = options.openid;
      if (options.wechatResumeToken) payload.wechat_resume_token = options.wechatResumeToken;
      const result = await request('/payment/orders', {
        method: 'POST',
        body: JSON.stringify(payload)
      });
      const visibleMethod = normalizeVisibleMethod(requestType) || requestType;
      // Blank method for the dedicated Stripe button → full Payment Element on the landing page.
      const stripeMethod = visibleMethod === 'stripe' ? '' : visibleMethod === 'wxpay' ? 'wechat_pay' : 'alipay';
      const stripeRouteUrl = result.client_secret && visibleMethod !== 'airwallex' ? buildStripeRouteUrl({
        orderId: result.order_id,
        clientSecret: result.client_secret,
        method: stripeMethod || undefined,
        resumeToken: result.resume_token
      }) : '';
      const airwallexRouteUrl = result.client_secret && result.intent_id ? buildAirwallexRouteUrl({
        orderId: result.order_id,
        outTradeNo: result.out_trade_no,
        resumeToken: result.resume_token
      }) : '';
      const decision = decidePaymentLaunch(result, {
        visibleMethod,
        orderType,
        isMobile: isMobileDevice(),
        isWechatBrowser: isWechatBrowser(),
        forceQRCode: !!(info?.alipay_force_qrcode && visibleMethod === 'alipay'),
        mobilePrecreateDeepLink: info?.alipay_mobile_precreate_deep_link === true,
        stripePopupUrl: stripeRouteUrl,
        stripeRouteUrl,
        airwallexRouteUrl
      });
      if (decision.kind === 'wechat_oauth' && decision.oauth?.authorize_url) {
        window.location.href = buildWechatOAuthAuthorizeUrl(decision.oauth.authorize_url, {
          paymentType: visibleMethod,
          orderType,
          planId,
          orderAmount
        });
        return;
      }
      if (decision.kind === 'unhandled') {
        setFormError(message("当前支付方式暂不可用，请更换支付方式或联系客服。"));
        return;
      }
      writePaymentRecoverySnapshot(decision.paymentState);
      if (decision.kind === 'stripe_popup') openPaymentWindow(decision.paymentState.payUrl);
      if (decision.kind === 'stripe_route' || decision.kind === 'airwallex_route') {
        window.location.href = decision.paymentState.payUrl;
        return;
      }
      if (decision.kind === 'wechat_jsapi' && decision.jsapi) {
        try {
          const jsapiResult = await invokeWechatJsapiPayment(decision.jsapi);
          const errMsg = String(jsapiResult.err_msg || '').toLowerCase();
          if (errMsg.includes('cancel')) {
            clearPaymentRecoverySnapshot();
            setFormError(message("已取消支付。"));
          } else if (errMsg && !errMsg.includes('ok')) {
            const ok = await attemptMobileQrFallback({
              reason: 'WECHAT_JSAPI_FAILED',
              message: errMsg
            }, {
              orderAmount,
              orderType,
              planId,
              paymentType: visibleMethod
            });
            if (!ok) setFormError(message("微信支付失败，请重试或更换支付方式。"));
          } else {
            clearPaymentRecoverySnapshot();
            location.href = buildPaymentResultUrl(decision.paymentState);
          }
        } catch (err) {
          const ok = await attemptMobileQrFallback(err, {
            orderAmount,
            orderType,
            planId,
            paymentType: visibleMethod
          });
          if (!ok) setFormError(message("当前环境无法唤起微信支付，请尝试扫码支付。"));
        }
        return;
      }
      // qr_waiting / alipay_deep_link / desktop redirect_waiting land in the pay panel.
      if (decision.kind === 'redirect_waiting' && isMobileDevice() && decision.paymentState.payUrl) {
        window.location.href = decision.paymentState.payUrl;
        return;
      }
      setPaying({
        state: decision.paymentState,
        phase: 'paying'
      });
    } catch (err) {
      if (err?.reason === 'TOO_MANY_PENDING') {
        setFormError(message("待支付订单过多（上限 {{value1}}），请先完成或取消现有订单。", {
          value1: err.metadata?.max ?? '…'
        }));
      } else if (err?.reason === 'CANCEL_RATE_LIMITED') {
        setFormError(message("操作过于频繁，请稍后再试。"));
      } else if (await attemptMobileQrFallback(err, {
        orderAmount,
        orderType,
        planId,
        paymentType: requestType
      })) {
        return;
      } else {
        setFormError(err.message || message("下单失败，请稍后重试。"));
      }
    } finally {
      setSubmitting(false);
    }
  }

  // Mobile-only: on specific gateway errors, re-order as desktop to get a scannable QR.
  async function attemptMobileQrFallback(err, context) {
    if (!isMobileDevice() || context.attempted) return false;
    const reason = err?.reason || '';
    const message = String(err?.message || '').toLowerCase();
    const normalized = normalizeVisibleMethod(context.paymentType);
    const hit = normalized === 'wxpay' ? ['WECHAT_H5_NOT_AUTHORIZED', 'WECHAT_PAYMENT_MP_NOT_CONFIGURED', 'WECHAT_JSAPI_FAILED', 'PAYMENT_GATEWAY_ERROR', 'UNHANDLED_PAYMENT_SCENARIO'].includes(reason) || message.includes('weixinjsbridge is unavailable') || message.includes('wechat_jsapi_unavailable') : normalized === 'alipay' ? ['PAYMENT_GATEWAY_ERROR', 'UNHANDLED_PAYMENT_SCENARIO'].includes(reason) : false;
    if (!hit) return false;
    try {
      const payload = buildCreateOrderPayload({
        amount: context.orderAmount,
        paymentType: normalized || context.paymentType,
        orderType: context.orderType,
        planId: context.planId,
        isMobile: false,
        isWechatBrowser: false
      });
      const result = await request('/payment/orders', {
        method: 'POST',
        body: JSON.stringify(payload)
      });
      const stripeRouteUrl = result.client_secret ? buildStripeRouteUrl({
        orderId: result.order_id,
        clientSecret: result.client_secret,
        method: normalized === 'wxpay' ? 'wechat_pay' : 'alipay',
        resumeToken: result.resume_token
      }) : '';
      const decision = decidePaymentLaunch(result, {
        visibleMethod: normalized || context.paymentType,
        orderType: context.orderType,
        isMobile: false,
        isWechatBrowser: false,
        stripePopupUrl: stripeRouteUrl,
        stripeRouteUrl
      });
      if (decision.kind !== 'qr_waiting' || !decision.paymentState.qrCode) return false;
      setFormError('');
      writePaymentRecoverySnapshot(decision.paymentState);
      setPaying({
        state: decision.paymentState,
        phase: 'paying'
      });
      return true;
    } catch {
      return false;
    }
  }
  function resetPayment() {
    clearPaymentRecoverySnapshot();
    setPaying(null);
    setOrdersRefreshKey(k => k + 1);
    refreshBalance();
  }
  async function cancelPayingOrder() {
    const state = paying?.state;
    if (state?.orderId) {
      try {
        await request(`/payment/orders/${state.orderId}/cancel`, {
          method: 'POST'
        });
      } catch {/* already settled */}
    }
    resetPayment();
  }

  // ---- amount / method availability ----
  const activeMethodLimit = methods.find(([name]) => name === method)?.[1];
  const amountFits = useCallback(m => {
    const limit = methods.find(([name]) => name === m)?.[1];
    if (!limit) return true;
    const min = Number(limit.single_min || 0);
    const max = Number(limit.single_max || 0);
    if (min > 0 && amount < min) return false;
    if (max > 0 && amount > max) return false;
    if (Number(limit.daily_limit || 0) > 0 && Number(limit.daily_remaining ?? 1) > 0 && amount > Number(limit.daily_remaining)) return false;
    return limit.available !== false;
  }, [methods, amount]);
  useEffect(() => {
    // Keep the selected method compatible with the chosen amount.
    if (method && !amountFits(method)) {
      const next = methods.map(([name]) => name).find(amountFits);
      if (next) setMethod(next);
    }
  }, [amount, methods, method, amountFits]);
  const feeRate = activeMethodLimit?.fee_rate ?? info?.recharge_fee_rate ?? 0;
  const fee = feeAmountFor(amount, feeRate);
  const total = totalAmountFor(amount, feeRate);
  const multiplier = Number(info?.balance_recharge_multiplier || 1);
  const credited = Math.round((Number(amount) || 0) * multiplier);
  const {
    min: globalMin,
    max: globalMax
  } = getRechargeAmountRange(info || {});
  const amountPresets = buildRechargeAmountPresets(globalMin, globalMax);
  const amountOutOfRange = globalMin > 0 && amount < globalMin || globalMax > 0 && amount > globalMax;
  function submitRecharge() {
    setFormError('');
    if (!method) return setFormError(message("请选择支付方式"));
    if (!Number.isFinite(Number(amount)) || Number(amount) <= 0) return setFormError(message("请输入有效的充值金额"));
    if (amountOutOfRange) return setFormError(message("充值金额需在 {{value1}} 与 {{value2}} 之间", {
      value1: globalMin > 0 ? `${currencySymbol()}${globalMin}` : '0',
      value2: globalMax > 0 ? `${currencySymbol()}${globalMax}` : message("不限")
    }));
    if (activeMethodLimit) {
      const min = Number(activeMethodLimit.single_min || 0);
      const max = Number(activeMethodLimit.single_max || 0);
      if (min > 0 && amount < min) return setFormError(message("{{value1}}单笔最低 {{value2}}{{value3}}", {
        value1: METHOD_LABELS[method] || method,
        value2: currencySymbol(),
        value3: min
      }));
      if (max > 0 && amount > max) return setFormError(message("{{value1}}单笔最高 {{value2}}{{value3}}", {
        value1: METHOD_LABELS[method] || method,
        value2: currencySymbol(),
        value3: max
      }));
    }
    createOrder(Number(amount), 'balance');
  }
  function submitSubscribe() {
    if (!selectedPlan) return;
    createOrder(selectedPlan.price, 'subscription', selectedPlan.id);
  }
  const showBalanceTab = !info?.balance_disabled;
  const showSubscriptionTab = (info?.plans?.length || 0) > 0;
  const ordersPanel = <OrdersPanel refreshKey={ordersRefreshKey} />;
  if (paying) {
    return <div>
      <div className="page-head"><div><p className="kicker">YINGZO / WORKSPACE</p><h1>{tr("订单支付")}</h1><p className="muted">{tr("支付完成后页面会自动确认到账。")}</p></div></div>
      <PayPanel info={info} state={paying.state} phase={paying.phase} onPhase={handlePhase} onCancel={cancelPayingOrder} onDone={resetPayment} onRefreshBalance={refreshBalance} />
    </div>;
  }
  return <div>
    <div className="page-head">
      <div>
        <p className="kicker">YINGZO / WORKSPACE</p>
        <h1>{tr("充值")}</h1>
        <p className="muted">{tr("为你的创作余额充值或开通订阅，继续把想法变成作品。")}</p>
      </div>
    </div>
    {error && <div className="error">{tr(error)}</div>}
    {loading && <div className="empty">{tr("正在加载支付配置…")}</div>}
    {!loading && <>
      <div className="recharge-summary">
        <div className="panel balance-card">
          <small>{tr("当前余额")}</small>
          <div className="balance">{balance != null ? `${currencySymbol()}${formatNumber(Number(balance), {
              minimumFractionDigits: 2,
              maximumFractionDigits: 2
            })}` : '—'}</div>
          {multiplier > 0 && multiplier !== 1 && <span className="badge recharge-bonus"><Trans i18nKey="充值到账 ×<0/>" components={[<>{multiplier}</>]} /></span>}
        </div>
        {showBalanceTab && showSubscriptionTab && <div className="recharge-tabs">
          <button className={tab === 'recharge' ? 'active' : ''} onClick={() => {
            setTab('recharge');
            setSelectedPlan(null);
          }}>{tr("余额充值")}</button>
          <button className={tab === 'subscription' ? 'active' : ''} onClick={() => setTab('subscription')}>{tr("订阅计划")}</button>
        </div>}
      </div>

      {tab === 'recharge' && showBalanceTab && <div className="recharge-grid">
        <div className="panel form-card">
          <h2>{tr("选择金额")}</h2>
          <div className="amounts recharge-amounts">{amountPresets.map(x => <button className={Number(amount) === x ? 'selected' : ''} key={x} onClick={() => {
              setAmount(x);
              setFormError('');
            }}>{currencySymbol()}{x}</button>)}</div>
          <label>{tr("自定义金额")}<input type="number" min={globalMin || 0.01} max={globalMax || undefined} step="0.01" value={amount} onChange={e => {
              setAmount(e.target.value === '' ? '' : Number(e.target.value));
              setFormError('');
            }} placeholder={tr("输入金额")} />
          </label>
          {(globalMin > 0 || globalMax > 0) && <p className="muted small-note"><Trans i18nKey="充值金额范围：<0/><1/> 起<2/>" components={[<>{currencySymbol()}</>, <>{globalMin || 0.01}</>, <>{globalMax > 0 ? tr("，最高 {{value1}}{{value2}}", {
                value1: currencySymbol(),
                value2: globalMax
              }) : ''}</>]} /></p>}
          <h2>{tr("支付方式")}</h2>
          <div className="method-list">
            {methods.map(([name, limit]) => <button key={name} className={`method-item ${method === name ? 'selected' : ''} ${!amountFits(name) ? 'disabled' : ''}`} onClick={() => amountFits(name) && setMethod(name)}>
              <span className="method-name">{limit.display_name || METHOD_LABELS[name] || name}</span>
              <span className="method-meta">
                {Number(limit.fee_rate) > 0 ? tr("手续费 {{value1}}%", {
                  value1: limit.fee_rate
                }) : tr("免手续费")}
                {Number(limit.single_min) > 0 ? tr(" · 单笔 ≥ {{value1}}{{value2}}", {
                  value1: currencySymbol(),
                  value2: Number(limit.single_min)
                }) : ''}
                {Number(limit.single_max) > 0 ? tr(" · 单笔 ≤ {{value1}}{{value2}}", {
                  value1: currencySymbol(),
                  value2: Number(limit.single_max)
                }) : ''}
              </span>
              {Number(limit.daily_limit || 0) > 0 && Number(limit.daily_remaining ?? 1) <= 0 && <em className="method-unavailable">{tr("今日额度已用完")}</em>}
            </button>)}
            {methods.length === 0 && <p className="muted">{tr("当前没有可用的支付方式，请联系管理员。")}</p>}
          </div>
          <div className="fee-preview">
            <div><span>{tr("充值金额")}</span><strong>{currencySymbol()}{formatNumber(Number(amount || 0), {
                  minimumFractionDigits: 2,
                  maximumFractionDigits: 2
                })}</strong></div>
            {fee > 0 && <div><span>{tr("支付手续费")}</span><strong>{currencySymbol()}{formatNumber(fee, {
                  minimumFractionDigits: 2,
                  maximumFractionDigits: 2
                })}</strong></div>}
            <div><span>{tr("实付合计")}</span><strong>{currencySymbol()}{formatNumber(Number(total), {
                  minimumFractionDigits: 2,
                  maximumFractionDigits: 2
                })}</strong></div>
            {multiplier > 0 && multiplier !== 1 && <div><span>{tr("预计到账")}</span><strong className="credited">{currencySymbol()}{formatNumber(credited, {
                  minimumFractionDigits: 2,
                  maximumFractionDigits: 2
                })}</strong></div>}
          </div>
          {formError && <div className="error">{tr(formError)}</div>}
          <button className="dark full" onClick={submitRecharge} disabled={submitting || !methods.length}>{submitting ? tr("正在创建订单…") : tr("继续支付 →")}</button>
        </div>
        <div className="recharge-side">
          <RedeemCard onRedeemed={refreshBalance} />
          {(info?.help_text || info?.help_image_url) && <div className="panel help-card">
            <h2>{tr("支付说明")}</h2>
            {info.help_text && <div className="md-body muted" dangerouslySetInnerHTML={{
              __html: renderMarkdown(info.help_text)
            }} />}
            {info.help_image_url && <img className="help-image" src={info.help_image_url} alt={tr("支付说明")} />}
          </div>}
        </div>
        <div className="recharge-orders">{ordersPanel}</div>
      </div>}

      {tab === 'subscription' && showSubscriptionTab && <div>
        <div className="plans-grid">
          {info.plans.filter(p => p.for_sale !== false).map(plan => <article className={`panel plan-card ${selectedPlan?.id === plan.id ? 'selected' : ''}`} key={plan.id} onClick={() => setSelectedPlan(plan)}>
            <p className="kicker">{plan.group_name || tr("订阅计划")}</p>
            <h3>{plan.name}</h3>
            <div className="plan-price">
              <strong>{currencySymbol(plan.currency)}{formatNumber(Number(plan.price), {
                  minimumFractionDigits: 2,
                  maximumFractionDigits: 2
                })}</strong>
              {plan.original_price && Number(plan.original_price) > Number(plan.price) && <s className="muted">{currencySymbol(plan.currency)}{formatNumber(Number(plan.original_price), {
                  minimumFractionDigits: 2,
                  maximumFractionDigits: 2
                })}</s>}
              <span className="muted"> / {planValiditySuffix(plan)}</span>
            </div>
            {plan.description && <p className="muted plan-desc">{plan.description}</p>}
            {(plan.features || []).length > 0 && <ul className="plan-features">{plan.features.map((f, i) => <li key={i}>{f}</li>)}</ul>}
            <div className="plan-meta muted">
              {plan.rate_multiplier != null && Number(plan.rate_multiplier) !== 1 && <span><Trans i18nKey="费率 ×<0/>" components={[<>{plan.rate_multiplier}</>]} /></span>}
              {plan.peak_rate_enabled && <span><Trans i18nKey="高峰 ×<0/>" components={[<>{plan.peak_rate_multiplier}</>]} /></span>}
            </div>
          </article>)}
        </div>
        {formError && <div className="error plans-error">{tr(formError)}</div>}
        <div className="plans-submit">
          <button className="dark" onClick={submitSubscribe} disabled={submitting || !selectedPlan}>{submitting ? tr("正在创建订单…") : selectedPlan ? tr("订阅「{{value1}}」→", {
              value1: selectedPlan.name
            }) : tr("先选择一个订阅计划")}</button>
        </div>
        <div className="recharge-orders">{ordersPanel}</div>
      </div>}
    </>}
  </div>;
}
function parseWechatResume(params, plans) {
  const token = params.get('wechat_resume_token') || '';
  const openid = params.get('openid') || '';
  if (params.get('wechat_resume') !== '1' && !token && !openid) return null;
  const paymentType = normalizeVisibleMethod(params.get('payment_type')) || 'wxpay';
  const planIdRaw = Number.parseInt(params.get('plan_id') || '', 10);
  const planId = Number.isFinite(planIdRaw) && planIdRaw > 0 ? planIdRaw : undefined;
  const orderType = params.get('order_type') === 'subscription' || planId ? 'subscription' : 'balance';
  if (token) return {
    token,
    paymentType,
    orderType,
    planId,
    amount: 0,
    openid: ''
  };
  if (!openid) return null;
  const rawAmount = Number.parseFloat(params.get('amount') || '');
  const amount = Number.isFinite(rawAmount) && rawAmount > 0 ? rawAmount : orderType === 'subscription' ? plans.find(p => p.id === planId)?.price ?? 0 : 0;
  return {
    token: '',
    openid,
    paymentType,
    orderType,
    planId,
    amount
  };
}
function buildWechatOAuthAuthorizeUrl(authorizeUrl, {
  paymentType,
  orderType,
  planId,
  orderAmount
}) {
  try {
    const target = new URL(authorizeUrl, location.origin);
    const redirectPath = target.searchParams.get('redirect') || '/recharge';
    const redirectUrl = new URL(redirectPath, location.origin);
    redirectUrl.searchParams.set('payment_type', normalizeVisibleMethod(paymentType) || paymentType || 'wxpay');
    redirectUrl.searchParams.set('order_type', orderType);
    if (planId) redirectUrl.searchParams.set('plan_id', String(planId));else redirectUrl.searchParams.delete('plan_id');
    if (orderAmount > 0) redirectUrl.searchParams.set('amount', String(orderAmount));else redirectUrl.searchParams.delete('amount');
    redirectUrl.searchParams.set('wechat_resume', '1');
    target.searchParams.set('redirect', `${redirectUrl.pathname}${redirectUrl.search}`);
    return target.toString();
  } catch {
    return authorizeUrl;
  }
}
function waitForWeixinJSBridge(timeoutMs = 4000) {
  const getBridge = () => window.WeixinJSBridge || null;
  return new Promise(resolve => {
    const existing = getBridge();
    if (existing) return resolve(existing);
    const timer = setTimeout(() => finish(getBridge()), timeoutMs);
    function finish(bridge) {
      clearTimeout(timer);
      document.removeEventListener('WeixinJSBridgeReady', handleReady);
      document.removeEventListener('onWeixinJSBridgeReady', handleReady);
      resolve(bridge);
    }
    function handleReady() {
      finish(getBridge());
    }
    document.addEventListener('WeixinJSBridgeReady', handleReady, false);
    document.addEventListener('onWeixinJSBridgeReady', handleReady, false);
  });
}
async function invokeWechatJsapiPayment(payload) {
  const bridge = await waitForWeixinJSBridge();
  if (!bridge) throw new Error('WECHAT_JSAPI_UNAVAILABLE');
  return new Promise(resolve => {
    bridge.invoke('getBrandWCPayRequest', payload, result => resolve(result || {}));
  });
}

// ---------------------------------------------------------------------------
// /payment/result — canonical return_url page; /recharge/result remains an alias.
// ---------------------------------------------------------------------------
const RESULT_POLL_INTERVAL_MS = 2000;
const RESULT_POLL_MAX_ATTEMPTS = 15;
export function PaymentResult({
  setUser
}) {
  const [order, setOrder] = useState(null);
  const [attempts, setAttempts] = useState(0);
  const [error, setError] = useState('');
  const [returnInfo, setReturnInfo] = useState(null);
  const bootstrapped = useRef(false);
  useEffect(() => {
    if (bootstrapped.current) return;
    bootstrapped.current = true;
    (async () => {
      const resumeToken = queryParam('resume_token');
      const orderId = queryParam('order_id');
      const outTradeNo = queryParam('out_trade_no');
      const tradeStatus = queryParam('trade_status');
      try {
        if (resumeToken) {
          setOrder(await request('/payment/public/orders/resolve', {
            method: 'POST',
            body: JSON.stringify({
              resume_token: resumeToken
            })
          }));
          return;
        }
        if (orderId) {
          setOrder(await request(`/payment/orders/${orderId}`));
          return;
        }
        if (outTradeNo) {
          try {
            setOrder(await request('/payment/orders/verify', {
              method: 'POST',
              body: JSON.stringify({
                out_trade_no: outTradeNo
              })
            }));
            return;
          } catch {
            const pub = await request('/payment/public/orders/verify', {
              method: 'POST',
              body: JSON.stringify({
                out_trade_no: outTradeNo
              })
            });
            setOrder({
              out_trade_no: pub.out_trade_no,
              status: pub.status,
              created_at: pub.created_at,
              expires_at: pub.expires_at,
              publicOnly: true
            });
            return;
          }
        }
        if (tradeStatus) {
          setReturnInfo({
            out_trade_no: outTradeNo,
            money: queryParam('money'),
            type: queryParam('type'),
            trade_status: tradeStatus
          });
          return;
        }
        setError(message("缺少订单信息，无法查询支付结果。"));
      } catch (e) {
        setError(e.message);
      }
    })();
  }, []);
  function persistUser(u) {
    setUser(prev => {
      const next = {
        ...(prev || {}),
        ...u
      };
      try {
        localStorage.setItem('auth_user', JSON.stringify(next));
      } catch {/* ignore */}
      return next;
    });
  }
  // Pending orders refresh every 2s (max 15 attempts); balance refreshes on success.
  useEffect(() => {
    const id = order?.id;
    const status = String(order?.status || '').toUpperCase();
    if (id && status === 'PENDING' && attempts < RESULT_POLL_MAX_ATTEMPTS) {
      const t = setTimeout(async () => {
        try {
          const next = await request(`/payment/orders/${id}`);
          setOrder(prev => ({
            ...prev,
            ...next
          }));
        } catch {/* keep polling */}
        setAttempts(a => a + 1);
      }, RESULT_POLL_INTERVAL_MS);
      return () => clearTimeout(t);
    }
    if (id && isTerminalSuccess(status) && String(order?.order_type).toLowerCase() === 'balance') {
      request('/auth/me').then(x => persistUser(x?.user || x)).catch(() => {});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [order, attempts]);
  function goRecharge() {
    history.pushState({}, '', '/recharge');
    location.reload();
  }
  function goWorkspace() {
    history.pushState({}, '', '/keys');
    location.reload();
  }
  const status = String(order?.status || '').toUpperCase();
  const outcome = isTerminalSuccess(status) ? 'success' : status === 'CANCELLED' ? 'cancelled' : ['EXPIRED', 'FAILED'].includes(status) ? 'failed' : status === 'PENDING' ? 'pending' : '';
  const headline = {
    success: tr("支付成功"),
    pending: tr("支付确认中…"),
    cancelled: tr("订单已取消"),
    failed: tr("支付未完成")
  }[outcome] || (returnInfo ? tr("支付回执") : tr("支付结果"));
  const baseAmount = order && order.pay_amount != null ? order.pay_amount / (1 + Number(order.fee_rate || 0) / 100) : null;
  const feeAmount = order && order.pay_amount != null ? order.pay_amount - baseAmount : null;
  return <div className="auth-page result-page">
    <div className="result-card">
      <p className="kicker">YINGZO / WORKSPACE</p>
      <h1>{headline}</h1>
      {error && <div className="error">{tr(error)}</div>}
      {returnInfo && <p className="muted"><Trans i18nKey="渠道回执：单号 <0/> · 金额 <1/> · 状态 <2/>" components={[<>{returnInfo.out_trade_no}</>, <>{returnInfo.money || '—'}</>, <>{returnInfo.trade_status}</>]} /></p>}
      {order && <>
        <p className="muted"><Trans i18nKey="单号 <0/>" components={[<>{order.out_trade_no || `#${order.id}`}</>]} /></p>
        <div className="result-amounts">
          {baseAmount != null && Number.isFinite(baseAmount) && <div><span>{tr("充值金额")}</span><strong>{formatPaymentAmount(baseAmount, order.currency)}</strong></div>}
          {feeAmount != null && Number(feeAmount) > 0 && <div><span>{tr("手续费")}</span><strong>{formatPaymentAmount(feeAmount, order.currency)}</strong></div>}
          {order.pay_amount != null && <div><span>{tr("实付")}</span><strong>{formatPaymentAmount(order.pay_amount, order.currency)}</strong></div>}
          <div><span>{tr("状态")}</span><strong>{ORDER_STATUS_LABELS[status] || status}</strong></div>
        </div>
        {outcome === 'success' && <p className="muted">{tr("余额已到账，可以继续创作了。")}</p>}
        {outcome === 'pending' && <p className="muted">{tr("正在等待支付渠道确认，页面会自动刷新，也可以稍后在「充值」页查看记录。")}</p>}
      </>}
      <div className="result-actions">
        <button className="dark" onClick={goRecharge}>{tr("返回充值")}</button>
        <button className="outline" onClick={goWorkspace}>{tr("进入工作区")}</button>
      </div>
    </div>
  </div>;
}

// ---------------------------------------------------------------------------
// /payment/stripe — Stripe landing page (same query contract as the original)
// ---------------------------------------------------------------------------
export function StripePayment() {
  const [error, setError] = useState('');
  const [qrData, setQrData] = useState('');
  const [statusText, setStatusText] = useState(tr("正在连接支付网关…"));
  const bootstrapped = useRef(false);
  function scheduleClose(orderId) {
    setStatusText(tr("支付成功，正在关闭…"));
    setTimeout(() => {
      if (window.opener) window.close();else location.href = buildPaymentResultUrl({
        orderId,
        status: 'success'
      });
    }, 2000);
  }
  useEffect(() => {
    if (bootstrapped.current) return;
    bootstrapped.current = true;
    const orderId = queryParam('order_id');
    const clientSecret = queryParam('client_secret');
    const method = queryParam('method'); // '' = full Payment Element
    if (!orderId || !clientSecret) {
      setError(message("缺少支付参数，请回到充值页重新发起支付。"));
      return;
    }
    let alive = true;
    let pollTimer = null;
    (async () => {
      try {
        const [config, order] = await Promise.all([request('/payment/config'), request(`/payment/orders/${orderId}`)]);
        const currency = order?.currency || undefined;
        if (!config?.stripe_publishable_key) throw new Error(tr("支付网关未配置，请联系管理员。"));
        const {
          loadStripe
        } = await import('@stripe/stripe-js');
        const stripe = await loadStripe(config.stripe_publishable_key, {
          locale: getLocale() === 'zh-CN' ? 'zh' : getLocale()
        });
        if (!stripe) throw new Error(tr("Stripe 加载失败，请检查网络后重试。"));
        if (!alive) return;
        const returnUrl = buildPaymentResultUrl({
          origin: location.origin,
          orderId,
          status: 'success'
        });
        if (method === 'alipay') {
          setStatusText(tr("正在跳转支付宝…"));
          const {
            error: confirmError
          } = await stripe.confirmAlipayPayment(clientSecret, {
            return_url: returnUrl
          });
          if (confirmError && alive) {
            setError(confirmError.message || message("支付宝支付失败。"));
            setStatusText('');
          }
        } else if (method === 'wechat_pay') {
          setStatusText(tr("等待扫码支付…"));
          const {
            paymentIntent,
            error: confirmError
          } = await stripe.confirmWechatPayPayment(clientSecret, {
            payment_method_options: {
              wechat_pay: {
                client: isMobileDevice() ? 'mobile_web' : 'web'
              }
            }
          });
          if (confirmError) {
            if (alive) {
              setError(confirmError.message || message("微信支付失败。"));
              setStatusText('');
            }
            return;
          }
          const qrUrl = paymentIntent?.next_action?.wechat_pay_display_qr_code?.image_data_url || '';
          if (qrUrl && alive) setQrData(qrUrl);
          pollTimer = setInterval(async () => {
            try {
              const next = await request(`/payment/orders/${orderId}`);
              if (isTerminalSuccess(next?.status)) {
                clearInterval(pollTimer);
                scheduleClose(orderId);
              }
            } catch {/* keep polling */}
          }, POLL_INTERVAL_MS);
        } else {
          setStatusText(tr("请完成支付…"));
          const elements = stripe.elements({
            clientSecret,
            appearance: {
              theme: 'flat',
              variables: {
                colorPrimary: '#30241e',
                fontFamily: 'DM Sans, sans-serif'
              }
            }
          });
          const paymentElement = elements.create('payment', {
            layout: 'tabs',
            paymentMethodOrder: ['alipay', 'wechat_pay', 'card', 'link']
          });
          paymentElement.mount('#stripe-payment-element');
          paymentElement.on('change', e => {
            if (alive) setStatusText(e.complete ? tr("已就绪，点击下方按钮完成支付。") : tr("请填写支付信息。"));
          });
          const submitBtn = document.getElementById('stripe-submit');
          const onSubmit = async e => {
            e.preventDefault();
            if (submitBtn) submitBtn.disabled = true;
            setStatusText(tr("正在确认支付…"));
            const {
              error: confirmError
            } = await stripe.confirmPayment({
              elements,
              redirect: 'if_required',
              confirmParams: {
                return_url: returnUrl
              }
            });
            if (confirmError) {
              setError(confirmError.message || message("支付失败，请重试。"));
              setStatusText('');
              if (submitBtn) submitBtn.disabled = false;
              return;
            }
            scheduleClose(orderId);
          };
          if (submitBtn) submitBtn.addEventListener('click', onSubmit);
        }
      } catch (e) {
        if (alive) {
          setError(e.message);
          setStatusText('');
        }
      }
    })();
    return () => {
      alive = false;
      if (pollTimer) clearInterval(pollTimer);
    };
  }, []);
  return <div className="auth-page result-page stripe-page">
    <div className="result-card">
      <p className="kicker">YINGZO / SECURE CHECKOUT</p>
      <h1>{tr("完成支付")}</h1>
      {error && <div className="error">{tr(error)}</div>}
      {statusText && <p className="muted">{statusText}</p>}
      {qrData && <img className="stripe-wechat-qr" src={qrData} alt={tr("微信扫码支付二维码")} />}
      <div id="stripe-payment-element" />
      <div className="result-actions"><button className="dark" id="stripe-submit">{tr("确认支付")}</button></div>
    </div>
  </div>;
}

// ---------------------------------------------------------------------------
// /payment/airwallex — Airwallex hosted checkout hand-off
// ---------------------------------------------------------------------------
export function AirwallexPayment() {
  const [error, setError] = useState('');
  const [statusText, setStatusText] = useState(tr("正在跳转 Airwallex 安全收银台…"));
  const bootstrapped = useRef(false);
  useEffect(() => {
    if (bootstrapped.current) return;
    bootstrapped.current = true;
    const orderId = queryParam('order_id');
    const outTradeNo = queryParam('out_trade_no');
    const resumeToken = queryParam('resume_token');
    const snapshot = readPaymentRecoverySnapshot({
      resumeToken: resumeToken || undefined
    });
    if (!snapshot || snapshot.paymentType !== 'airwallex' || orderId && Number(orderId) !== snapshot.orderId) {
      setError(message("支付会话已失效，请回到充值页重新发起支付。"));
      return;
    }
    if (!snapshot.intentId || !snapshot.clientSecret) {
      setError(message("支付会话不完整，请重新发起支付。"));
      return;
    }
    ;
    (async () => {
      try {
        const sdk = await import('@airwallex/components-sdk');
        await sdk.init({
          env: snapshot.paymentEnv === 'prod' ? 'prod' : 'demo',
          enabledElements: ['payments'],
          locale: getLocale() === 'zh-CN' ? 'zh' : 'en'
        });
        await sdk.payments.redirectToCheckout({
          intent_id: snapshot.intentId,
          client_secret: snapshot.clientSecret,
          currency: snapshot.currency || undefined,
          country_code: snapshot.countryCode || undefined,
          successUrl: buildPaymentResultUrl({
            origin: location.origin,
            orderId: snapshot.orderId,
            outTradeNo,
            resumeToken
          })
        });
      } catch (e) {
        setError(e?.message || message("无法跳转 Airwallex 收银台，请稍后重试。"));
        setStatusText('');
      }
    })();
  }, []);
  return <div className="auth-page result-page">
    <div className="result-card">
      <p className="kicker">YINGZO / SECURE CHECKOUT</p>
      <h1>{tr("正在前往收银台")}</h1>
      {error && <div className="error">{tr(error)}</div>}
      {statusText && !error && <p className="muted">{statusText}</p>}
      {error && <div className="result-actions"><button className="dark" onClick={() => {
          location.href = '/recharge';
        }}>{tr("返回充值")}</button></div>}
    </div>
  </div>;
}
