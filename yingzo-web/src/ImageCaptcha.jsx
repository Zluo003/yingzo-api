import { useEffect, useRef, useState } from 'react'
import { apiErrorCode, request } from './lib.js'
import { CAPTCHA_CODE_MAX_LENGTH, captchaRefreshDelayMs } from './auth-captcha.js'

// 自研图形验证码：点击图片换一张；nonce 变化时（每次提交后）重新领取，
// 因为服务端校验一次就作废。value 为 { id, code }，由父组件持有。
export default function ImageCaptcha({ value, onChange, onUnavailable, nonce = 0, disabled = false, required = true }) {
  const [challenge, setChallenge] = useState(null)
  const [status, setStatus] = useState('loading')
  const [reload, setReload] = useState(0)
  const onChangeRef = useRef(onChange)
  const onUnavailableRef = useRef(onUnavailable)
  onChangeRef.current = onChange
  onUnavailableRef.current = onUnavailable

  useEffect(() => {
    let active = true
    setStatus('loading')
    onChangeRef.current({ id: '', code: '' })
    request('/auth/captcha').then(data => {
      if (!active) return
      setChallenge(data)
      setStatus('ready')
      onChangeRef.current({ id: data.captcha_id, code: '' })
    }).catch(error => {
      if (!active) return
      setChallenge(null)
      setStatus('error')
      if (apiErrorCode(error) === 'IMAGE_CAPTCHA_DISABLED') onUnavailableRef.current?.()
    })
    return () => { active = false }
  }, [nonce, reload])

  useEffect(() => {
    if (status !== 'ready' || !challenge) return
    const timer = setTimeout(() => setReload(count => count + 1), captchaRefreshDelayMs(challenge.expires_in))
    return () => clearTimeout(timer)
  }, [status, challenge])

  return <div className="auth-verification auth-captcha">
    <label htmlFor="auth-captcha-code">图形验证码</label>
    <div className="auth-code-row">
      <input id="auth-captcha-code" name="image_captcha_code" value={value?.code || ''} onChange={event => onChange({ id: value?.id || '', code: event.target.value })} placeholder="输入图中字符" autoComplete="off" autoCapitalize="characters" spellCheck={false} maxLength={CAPTCHA_CODE_MAX_LENGTH} disabled={disabled || status !== 'ready'} required={required} aria-describedby="auth-captcha-hint" />
      <button type="button" className="auth-captcha-image" onClick={() => setReload(count => count + 1)} disabled={disabled || status === 'loading'} aria-label="看不清？换一张图形验证码" title="看不清？点击换一张">
        {status === 'ready' && challenge ? <img src={challenge.image} alt="图形验证码" /> : <span>{status === 'loading' ? '加载中…' : '加载失败，点击重试'}</span>}
      </button>
    </div>
    <small id="auth-captcha-hint" className="auth-field-hint">不区分大小写；看不清可点击图片换一张。</small>
  </div>
}
