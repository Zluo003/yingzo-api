import { getLocale, getIntlLocale, t as tr } from "./i18n/index.js";
// Shared API client + payment-flow helpers.
// The payment logic mirrors frontend/src/components/payment/paymentFlow.ts so both
// frontends speak the exact same backend contract.
import { marked } from 'marked';
import DOMPurify from 'dompurify';
const API = import.meta.env?.VITE_API_BASE || '/api/v1';
export const UPDATE_API = 'https://updata.yingzo.art/v1/updates';

// ---- Auth session persistence (mirrors frontend/src/api/tokenRefresh.ts) ----
// The access token is short-lived; without a silent refresh the user is logged
// out whenever it expires. These helpers refresh on demand and coordinate with
// the legacy admin app, which shares the same localStorage keys and rotates the
// one-time refresh token on every use.
const AUTH_PATHS_NO_REFRESH = ['/auth/login', '/auth/register', '/auth/refresh', '/auth/send-verify-code', '/auth/forgot-password', '/auth/reset-password', '/auth/captcha'];
export const AUTH_EXPIRED_EVENT = 'yingzo:auth-expired';
export function clearAuthKeys() {
  localStorage.removeItem('auth_token');
  localStorage.removeItem('refresh_token');
  localStorage.removeItem('token_expires_at');
  localStorage.removeItem('auth_user');
}
function expireAuthSession() {
  clearAuthKeys();
  window.dispatchEvent(new CustomEvent(AUTH_EXPIRED_EVENT));
}
let refreshInFlight = null;
export function refreshAuthTokens() {
  if (refreshInFlight) return refreshInFlight;
  refreshInFlight = doRefreshAuthTokens().finally(() => {
    refreshInFlight = null;
  });
  return refreshInFlight;
}
async function doRefreshAuthTokens() {
  const refreshToken = localStorage.getItem('refresh_token');
  if (!refreshToken) throw noSessionError();
  const res = await fetch(`${API}/auth/refresh`, {
    method: 'POST',
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      'Accept-Language': getLocale()
    },
    body: JSON.stringify({
      refresh_token: refreshToken
    })
  });
  const raw = await res.json().catch(() => ({}));
  const data = raw?.code === 0 ? raw.data : null;
  if (!res.ok || !data?.access_token) {
    // Another tab (either frontend) may have rotated the one-time refresh token
    // while this request was in flight: adopt the stored pair instead of logging out.
    const storedRefresh = localStorage.getItem('refresh_token');
    const storedAccess = localStorage.getItem('auth_token');
    if (storedRefresh && storedRefresh !== refreshToken && storedAccess) {
      return {
        access_token: storedAccess,
        refresh_token: storedRefresh
      };
    }
    // A definitive rejection means the session is gone; transient failures keep
    // the tokens so a later retry can still recover.
    if ([400, 401, 403].includes(res.status)) {
      expireAuthSession();
    }
    const err = new Error(raw?.message || tr("登录已过期，请重新登录"));
    err.status = res.status;
    throw err;
  }
  // Logging out while refresh is in flight must not restore the old session.
  if (localStorage.getItem('refresh_token') !== refreshToken) {
    const access = localStorage.getItem('auth_token');
    const refresh = localStorage.getItem('refresh_token');
    if (access && refresh) return {
      access_token: access,
      refresh_token: refresh
    };
    throw noSessionError();
  }
  localStorage.setItem('auth_token', data.access_token);
  if (data.expires_in) localStorage.setItem('token_expires_at', String(Date.now() + data.expires_in * 1000));
  // The rotating refresh token is written last so peers can treat its change as a commit marker.
  localStorage.setItem('refresh_token', data.refresh_token || refreshToken);
  return data;
}
function noSessionError() {
  const err = new Error(tr("登录已过期，请重新登录"));
  err.status = 401;
  return err;
}
async function fetchAPI(path, options) {
  const token = localStorage.getItem('auth_token');
  const res = await fetch(`${API}${path}`, {
    credentials: 'include',
    ...options,
    headers: {
      'Content-Type': 'application/json',
      'Accept-Language': getLocale(),
      ...(token ? {
        Authorization: `Bearer ${token}`
      } : {}),
      ...(options.headers || {})
    }
  });
  const raw = await res.json().catch(() => ({}));
  return {
    res,
    raw,
    token
  };
}
export async function request(path, options = {}) {
  let attempt = await fetchAPI(path, options);
  const sessionRequest = !AUTH_PATHS_NO_REFRESH.some(p => path === p || path.startsWith(`${p}?`));
  let refreshUnavailable = false;
  // Access token expired: refresh silently once, then retry the original request.
  // 比较基准是本次请求实际携带的 token——401 返回时其它并发请求可能已完成续期，
  // 存储里已是新值，此时直接复用新 token 重试即可，不必再触发一次续期。
  if (attempt.res.status === 401 && sessionRequest && (attempt.token || localStorage.getItem('refresh_token'))) {
    if (!attempt.token || localStorage.getItem('auth_token') === attempt.token) {
      try {
        await refreshAuthTokens();
      } catch (error) {
        // Preserve a recoverable session after network errors, throttling or 5xx.
        refreshUnavailable = error.status !== 401 && ![400, 403].includes(error.status);
      }
    }
    const nextToken = localStorage.getItem('auth_token');
    if (nextToken && nextToken !== attempt.token) {
      attempt = await fetchAPI(path, options);
    }
  }
  if (attempt.res.status === 401 && sessionRequest && !refreshUnavailable && (!localStorage.getItem('auth_token') || localStorage.getItem('auth_token') === attempt.token)) {
    expireAuthSession();
  }
  const data = attempt.raw?.code !== undefined ? attempt.raw.code === 0 ? attempt.raw.data : attempt.raw : attempt.raw;
  if (!attempt.res.ok || attempt.raw?.code > 0) {
    const err = new Error(attempt.raw.message || attempt.raw.error || tr("请求失败"));
    err.reason = attempt.raw.reason || '';
    err.metadata = attempt.raw.metadata || null;
    err.status = attempt.res.status;
    if (refreshUnavailable) err.sessionUnavailable = true;
    throw err;
  }
  return data;
}
export function apiErrorCode(err) {
  return err && typeof err === 'object' ? String(err.reason || err.code || '') : '';
}

// ---- Markdown (announcements / payment help) ----
marked.setOptions({
  gfm: true,
  breaks: true
});
export function renderMarkdown(text) {
  return DOMPurify.sanitize(marked.parse(String(text || '')));
}

// ---- Device detection ----
export function isMobileDevice() {
  if (typeof navigator === 'undefined') return false;
  const ua = navigator.userAgent || '';
  return /android|iphone|ipad|ipod|opera mini|iemobile|mobile/i.test(ua) || navigator.maxTouchPoints > 1 && /Macintosh/.test(ua);
}
export function isWechatBrowser() {
  return typeof navigator !== 'undefined' && /MicroMessenger/i.test(navigator.userAgent || '');
}

// ---- Payment method normalization (frontend/src/components/payment/paymentFlow.ts) ----
const VISIBLE_METHOD_ALIASES = {
  alipay: 'alipay',
  alipay_direct: 'alipay',
  wxpay: 'wxpay',
  wxpay_direct: 'wxpay',
  stripe: 'stripe',
  airwallex: 'airwallex'
};
export const METHOD_ORDER = ['alipay', 'alipay_direct', 'wxpay', 'wxpay_direct', 'stripe', 'airwallex'];
export const METHOD_LABELS = {
  get alipay() {
    return tr("支付宝");
  },
  get wxpay() {
    return tr("微信支付");
  },
  get stripe() {
    return tr("Stripe（银行卡等）");
  },
  airwallex: 'Airwallex'
};
export function normalizeVisibleMethod(method) {
  const key = String(method || '').trim();
  return VISIBLE_METHOD_ALIASES[key] || '';
}
export function getVisibleMethods(methods) {
  const visible = {};
  Object.entries(methods || {}).forEach(([type, limit]) => {
    const normalized = normalizeVisibleMethod(type) || type.trim();
    if (!normalized) return;
    if (!visible[normalized] || type === normalized) visible[normalized] = {
      ...limit
    };
  });
  return visible;
}
export function sortMethodEntries(entries) {
  return entries.slice().sort((a, b) => {
    const ia = METHOD_ORDER.indexOf(a[0]);
    const ib = METHOD_ORDER.indexOf(b[0]);
    return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib);
  });
}

// Yingzo monetary amounts are displayed in RMB. Gateway currency fields remain
// untouched in order payloads and SDK calls; this is a presentation helper.
export function currencySymbol() {
  return '¥';
}
export function formatPaymentAmount(amount, currency) {
  const n = Number(amount);
  if (!Number.isFinite(n)) return `${currencySymbol(currency)}0.00`;
  return `${currencySymbol(currency)}${n.toLocaleString(getIntlLocale(), {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  })}`;
}
export function feeAmountFor(amount, feeRate) {
  return Math.ceil(Number(amount || 0) * Number(feeRate || 0) / 100);
}
export function totalAmountFor(amount, feeRate) {
  return Math.round(Number(amount || 0) + feeAmountFor(amount, feeRate));
}

// ---- Order creation payload / launch decision (paymentFlow.ts port) ----
// Keep this path aligned with the backend's CanonicalizeReturnURL allowlist.
export function buildPaymentResultUrl({
  origin = '',
  orderId,
  outTradeNo,
  resumeToken,
  status
} = {}) {
  const params = new URLSearchParams();
  if (orderId) params.set('order_id', String(orderId));
  if (outTradeNo) params.set('out_trade_no', outTradeNo);
  if (resumeToken) params.set('resume_token', resumeToken);
  if (status) params.set('status', status);
  const query = params.toString();
  return `${String(origin).trim().replace(/\/+$/, '')}/payment/result${query ? `?${query}` : ''}`;
}
export function buildCreateOrderPayload(input) {
  const visibleMethod = normalizeVisibleMethod(input.paymentType) || String(input.paymentType || '').trim();
  const origin = String(input.origin || location.origin).replace(/\/+$/, '');
  const forceQRCode = !!input.forceQRCode;
  const mobilePrecreateDeepLink = !!input.mobilePrecreateDeepLink;
  const effectiveMobile = forceQRCode && !mobilePrecreateDeepLink && visibleMethod === 'alipay' ? false : input.isMobile;
  const payload = {
    amount: input.amount,
    payment_type: visibleMethod,
    order_type: input.orderType,
    is_mobile: effectiveMobile,
    payment_source: visibleMethod === 'wxpay' && input.isWechatBrowser ? 'wechat_in_app_resume' : 'hosted_redirect'
  };
  if (input.planId) payload.plan_id = input.planId;
  payload.return_url = buildPaymentResultUrl({
    origin
  });
  return payload;
}
export const PAYMENT_RECOVERY_STORAGE_KEY = 'payment.recovery.current';
export function createPaymentRecoverySnapshot(state, now = Date.now()) {
  return {
    ...state,
    createdAt: now
  };
}
export function writePaymentRecoverySnapshot(snapshot) {
  try {
    localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify(snapshot));
  } catch {/* storage unavailable */}
}
export function clearPaymentRecoverySnapshot() {
  try {
    localStorage.removeItem(PAYMENT_RECOVERY_STORAGE_KEY);
  } catch {/* storage unavailable */}
}
export function readPaymentRecoverySnapshot(options = {}) {
  let raw = null;
  try {
    raw = localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY);
  } catch {
    return null;
  }
  if (!raw) return null;
  try {
    const p = JSON.parse(raw);
    if (typeof p.orderId !== 'number' || typeof p.amount !== 'number' || typeof p.paymentType !== 'string' || typeof p.createdAt !== 'number') return null;
    const expiresAt = Date.parse(p.expiresAt);
    if (Number.isFinite(expiresAt) && expiresAt <= (options.now || Date.now())) return null;
    if (options.resumeToken && p.resumeToken !== options.resumeToken) return null;
    return {
      orderId: p.orderId,
      amount: p.amount,
      qrCode: p.qrCode || '',
      expiresAt: p.expiresAt || '',
      paymentType: p.paymentType,
      payUrl: p.payUrl || '',
      outTradeNo: p.outTradeNo || '',
      clientSecret: p.clientSecret || '',
      intentId: p.intentId || '',
      currency: p.currency || '',
      countryCode: p.countryCode || '',
      paymentEnv: p.paymentEnv || '',
      payAmount: p.payAmount ?? 0,
      orderType: p.orderType === 'subscription' ? 'subscription' : 'balance',
      paymentMode: p.paymentMode || '',
      resumeToken: p.resumeToken || '',
      alipayMobilePrecreateDeepLink: p.alipayMobilePrecreateDeepLink === true,
      createdAt: p.createdAt
    };
  } catch {
    return null;
  }
}

// Launch kinds: qr_waiting / alipay_deep_link / redirect_waiting / stripe_popup /
// stripe_route / airwallex_route / wechat_oauth / wechat_jsapi / unhandled
export function decidePaymentLaunch(result, context) {
  const visibleMethod = normalizeVisibleMethod(context.visibleMethod) || context.visibleMethod;
  const baseState = createPaymentRecoverySnapshot({
    orderId: result.order_id,
    amount: result.amount,
    qrCode: result.qr_code || '',
    expiresAt: result.expires_at || '',
    paymentType: visibleMethod,
    payUrl: result.pay_url || '',
    outTradeNo: result.out_trade_no || '',
    clientSecret: result.client_secret || '',
    intentId: result.intent_id || '',
    currency: result.currency || '',
    countryCode: result.country_code || '',
    paymentEnv: result.payment_env || '',
    payAmount: result.pay_amount ?? 0,
    orderType: context.orderType,
    paymentMode: String(result.payment_mode || '').trim(),
    resumeToken: result.resume_token || '',
    alipayMobilePrecreateDeepLink: result.alipay_mobile_precreate_deep_link === true
  });
  if (visibleMethod === 'airwallex' && baseState.clientSecret && baseState.intentId) {
    if (!context.airwallexRouteUrl) return {
      kind: 'unhandled',
      paymentState: baseState
    };
    return {
      kind: 'airwallex_route',
      paymentState: {
        ...baseState,
        payUrl: context.airwallexRouteUrl
      }
    };
  }
  if (baseState.clientSecret) {
    const isStripeButton = visibleMethod === 'stripe';
    const stripeMethod = isStripeButton ? undefined : visibleMethod === 'wxpay' ? 'wechat_pay' : 'alipay';
    const kind = stripeMethod === 'alipay' && !context.isMobile ? 'stripe_popup' : 'stripe_route';
    return {
      kind,
      paymentState: {
        ...baseState,
        payUrl: kind === 'stripe_popup' ? context.stripePopupUrl || '' : context.stripeRouteUrl || ''
      },
      stripeMethod
    };
  }
  if (result.result_type === 'oauth_required' && result.oauth?.authorize_url) {
    return {
      kind: 'wechat_oauth',
      paymentState: baseState,
      oauth: result.oauth
    };
  }
  const jsapiPayload = result.jsapi || result.jsapi_payload;
  if (result.result_type === 'jsapi_ready' && jsapiPayload) {
    return {
      kind: 'wechat_jsapi',
      paymentState: baseState,
      jsapi: jsapiPayload
    };
  }
  if (visibleMethod === 'alipay' && context.isMobile && baseState.alipayMobilePrecreateDeepLink && baseState.qrCode) {
    return {
      kind: 'alipay_deep_link',
      paymentState: baseState
    };
  }
  const forceQRCode = !!context.forceQRCode && !context.mobilePrecreateDeepLink && visibleMethod === 'alipay';
  const effectiveMobile = forceQRCode ? false : context.isMobile;
  const mode = baseState.paymentMode.toLowerCase();
  const prefersRedirect = mode === 'redirect' || mode === 'popup' || effectiveMobile && !!baseState.payUrl;
  const prefersQr = mode === 'qrcode' || mode === 'native' || !prefersRedirect && !!baseState.qrCode;
  if (visibleMethod === 'wxpay' && context.isWechatBrowser && baseState.payUrl && !baseState.qrCode) {
    return {
      kind: 'redirect_waiting',
      paymentState: baseState
    };
  }
  if (prefersRedirect && baseState.payUrl) return {
    kind: 'redirect_waiting',
    paymentState: baseState
  };
  if (prefersQr && baseState.qrCode) return {
    kind: 'qr_waiting',
    paymentState: baseState
  };
  if (baseState.payUrl) return {
    kind: 'redirect_waiting',
    paymentState: baseState
  };
  return {
    kind: 'unhandled',
    paymentState: baseState
  };
}

// ---- Stripe route URL (same query contract as the original frontend) ----
export function buildStripeRouteUrl({
  orderId,
  clientSecret,
  method,
  resumeToken
}) {
  const params = new URLSearchParams({
    order_id: String(orderId),
    client_secret: clientSecret
  });
  if (method !== undefined) params.set('method', method);
  if (resumeToken) params.set('resume_token', resumeToken);
  return `/payment/stripe?${params.toString()}`;
}
export function buildAirwallexRouteUrl({
  orderId,
  outTradeNo,
  resumeToken
}) {
  const params = new URLSearchParams({
    order_id: String(orderId)
  });
  if (outTradeNo) params.set('out_trade_no', outTradeNo);
  if (resumeToken) params.set('resume_token', resumeToken);
  return `/payment/airwallex?${params.toString()}`;
}

// ---- Alipay mobile deep link (frontend/src/components/payment/alipayDeepLink.ts) ----
export function buildAlipayDeepLink(qrCode) {
  const qrcode = String(qrCode || '').trim();
  return qrcode ? `alipays://platformapi/startapp?saId=10000007&qrcode=${encodeURIComponent(qrcode)}` : '';
}

// ---- Misc ----
export function formatPaymentDate(value) {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString(getIntlLocale(), {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false
  });
}
export function relativeTime(value) {
  const t = new Date(value).getTime();
  if (Number.isNaN(t)) return '';
  const diff = Date.now() - t;
  if (diff < 60000) return tr("刚刚");
  if (diff < 3600000) return tr("{{value1}} 分钟前", {
    value1: Math.floor(diff / 60000)
  });
  if (diff < 86400000) return tr("{{value1}} 小时前", {
    value1: Math.floor(diff / 3600000)
  });
  if (diff < 2592000000) return tr("{{value1}} 天前", {
    value1: Math.floor(diff / 86400000)
  });
  return new Date(t).toLocaleDateString(getIntlLocale());
}
export const ORDER_STATUS_LABELS = {
  get PENDING() {
    return tr("待支付");
  },
  get PAID() {
    return tr("已支付");
  },
  get RECHARGING() {
    return tr("到账中");
  },
  get COMPLETED() {
    return tr("已完成");
  },
  get EXPIRED() {
    return tr("已过期");
  },
  get CANCELLED() {
    return tr("已取消");
  },
  get FAILED() {
    return tr("失败");
  },
  get REFUND_REQUESTED() {
    return tr("退款申请中");
  },
  get REFUNDING() {
    return tr("退款中");
  },
  get REFUND_PENDING() {
    return tr("退款待处理");
  },
  get PARTIALLY_REFUNDED() {
    return tr("部分退款");
  },
  get REFUNDED() {
    return tr("已退款");
  },
  get REFUND_FAILED() {
    return tr("退款失败");
  }
};
export const SUCCESS_ORDER_STATUSES = ['COMPLETED', 'PAID', 'RECHARGING'];
export function isTerminalSuccess(status) {
  return SUCCESS_ORDER_STATUSES.includes(String(status || '').toUpperCase());
}
