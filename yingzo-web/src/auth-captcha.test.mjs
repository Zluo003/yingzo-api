import test from 'node:test'
import assert from 'node:assert/strict'
import { captchaPayload, captchaRefreshDelayMs, hasCaptchaCode } from './auth-captcha.js'

test('captcha fields are only sent when the image captcha is enabled', () => {
  assert.deepEqual(captchaPayload(false, { id: 'abc', code: 'K7MP' }), {})
  assert.deepEqual(captchaPayload(true, { id: 'abc', code: ' k7 mp ' }), { image_captcha_id: 'abc', image_captcha_code: 'k7mp' })
  assert.deepEqual(captchaPayload(true, null), { image_captcha_id: '', image_captcha_code: '' })
})

test('captchas refresh shortly before they expire', () => {
  assert.equal(captchaRefreshDelayMs(300), 280_000)
  assert.equal(captchaRefreshDelayMs(15), 10_000)
  assert.equal(captchaRefreshDelayMs(undefined), 240_000)
  assert.equal(captchaRefreshDelayMs('oops'), 240_000)
})

test('a captcha needs both an id and a typed answer', () => {
  assert.equal(hasCaptchaCode({ id: 'abc', code: 'K7MP' }), true)
  assert.equal(hasCaptchaCode({ id: 'abc', code: '   ' }), false)
  assert.equal(hasCaptchaCode({ id: '', code: 'K7MP' }), false)
  assert.equal(hasCaptchaCode(undefined), false)
})
