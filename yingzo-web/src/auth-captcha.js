// 自研图形验证码的前端约定：每张验证码只能校验一次（答对答错都会作废），
// 所以每次提交后都要换一张；验证码在 expires_in 秒后过期，提前一点刷新，
// 避免用户刚好在过期边缘提交。
export const CAPTCHA_CODE_MAX_LENGTH = 8

const DEFAULT_REFRESH_MS = 4 * 60 * 1000
const REFRESH_LEAD_SECONDS = 20
const MIN_REFRESH_SECONDS = 10

// 只在启用图形验证码时附带凭据字段；去掉输入里的所有空白，服务端不区分大小写。
export function captchaPayload(enabled, captcha) {
  if (!enabled) return {}
  return {
    image_captcha_id: captcha?.id || '',
    image_captcha_code: String(captcha?.code || '').replace(/\s+/g, ''),
  }
}

export function captchaRefreshDelayMs(expiresIn) {
  const seconds = Number(expiresIn)
  if (!Number.isFinite(seconds) || seconds <= 0) return DEFAULT_REFRESH_MS
  return Math.max(MIN_REFRESH_SECONDS, seconds - REFRESH_LEAD_SECONDS) * 1000
}

export function hasCaptchaCode(captcha) {
  return Boolean(captcha?.id) && String(captcha?.code || '').trim() !== ''
}
