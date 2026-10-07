import test from 'node:test'
import assert from 'node:assert/strict'
import { buildCreateOrderPayload, buildPaymentResultUrl, decidePaymentLaunch } from './lib.js'

test('all payment methods submit the canonical internal result URL', () => {
  for (const paymentType of ['alipay', 'alipay_direct', 'wxpay', 'wxpay_direct', 'stripe', 'airwallex']) {
    const payload = buildCreateOrderPayload({
      origin: 'https://app.example.com/', amount: 20, paymentType,
      orderType: 'balance', isMobile: false, isWechatBrowser: false,
    })
    assert.equal(payload.return_url, 'https://app.example.com/payment/result', paymentType)
    assert.equal(payload.amount, 20)
    assert.equal(payload.order_type, 'balance')
  }
})

test('Alipay QR fallback keeps the canonical URL and desktop request mode', () => {
  const payload = buildCreateOrderPayload({
    origin: 'http://127.0.0.1:8080', amount: 50, paymentType: 'alipay_direct',
    orderType: 'subscription', planId: 7, isMobile: true, forceQRCode: true,
  })
  assert.equal(payload.return_url, 'http://127.0.0.1:8080/payment/result')
  assert.equal(payload.payment_type, 'alipay')
  assert.equal(payload.is_mobile, false)
  assert.equal(payload.plan_id, 7)
  assert.equal(payload.payment_source, 'hosted_redirect')

  const decision = decidePaymentLaunch({ order_id: 42, amount: 50, qr_code: 'https://qr.alipay.com/example' }, {
    visibleMethod: 'alipay', orderType: 'subscription', isMobile: false,
  })
  assert.equal(decision.kind, 'qr_waiting')
  assert.equal(decision.paymentState.qrCode, 'https://qr.alipay.com/example')
})

test('payment return URLs preserve and encode order recovery parameters', () => {
  const params = { orderId: 42, outTradeNo: 'order & 42', resumeToken: 'resume+a/b=c', status: 'success' }
  const relative = buildPaymentResultUrl(params)
  assert.ok(relative.startsWith('/payment/result?'))
  const url = new URL(relative, 'https://app.example.com')
  assert.equal(url.pathname, '/payment/result')
  assert.deepEqual(Object.fromEntries(url.searchParams), {
    order_id: '42', out_trade_no: 'order & 42', resume_token: 'resume+a/b=c', status: 'success',
  })
  assert.equal(buildPaymentResultUrl({ origin: 'https://app.example.com/', ...params }), url.href)
  assert.equal(buildPaymentResultUrl(), '/payment/result')
})
