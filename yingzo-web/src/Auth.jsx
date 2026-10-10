import { useEffect, useRef, useState } from 'react'
import { apiErrorCode, clearAuthKeys, request } from './lib.js'
import './auth.css'

const COPY = {
  login: ['欢迎回来。', '进入你的本地创作工作区。', '登录'],
  register: ['开始你的创作。', '让一个想法，长成完整作品。', '创建账户'],
  'forgot-password': ['找回你的密码。', '输入注册邮箱，我们会向你发送密码重设链接。', '发送重设链接'],
  'reset-password': ['设置新密码。', '设置一个新密码，重新进入你的创作工作区。', '重设密码'],
}

const ERROR_MESSAGES = {
  EMAIL_VERIFY_REQUIRED: '请先获取并填写邮箱验证码。',
  INVALID_VERIFY_CODE: '验证码无效或已过期，请检查验证码或重新获取。',
  VERIFY_CODE_TOO_FREQUENT: '验证码发送过于频繁，请稍后重试。',
  VERIFY_CODE_MAX_ATTEMPTS: '验证码错误次数过多，请重新获取验证码。',
  REGISTRATION_DISABLED: '当前暂未开放注册。',
  PASSWORD_RESET_DISABLED: '当前暂未开启邮箱找回密码，请联系管理员。',
  INVALID_RESET_TOKEN: '重设链接无效或已过期，请重新申请。',
  EMAIL_EXISTS: '该邮箱已注册，请登录或找回密码。',
}

function errorMessage(error) {
  return ERROR_MESSAGES[apiErrorCode(error)] || error.message || '操作失败，请稍后重试。'
}

export default function Auth({ mode, setPage, setUser }) {
  const login = mode === 'login'
  const register = mode === 'register'
  const forgot = mode === 'forgot-password'
  const reset = mode === 'reset-password'
  const [title, description, submitLabel] = COPY[mode]
  const [form, setForm] = useState({ name: '', email: '', password: '', confirmPassword: '', verifyCode: '', invitationCode: '' })
  const [resetLink] = useState(() => {
    const query = new URLSearchParams(window.location.search)
    return { email: (query.get('email') || '').trim(), token: (query.get('token') || '').trim() }
  })
  const [settings, setSettings] = useState(null)
  const [settingsError, setSettingsError] = useState('')
  const [settingsAttempt, setSettingsAttempt] = useState(0)
  const [error, setError] = useState('')
  const [resetExpired, setResetExpired] = useState(false)
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [sending, setSending] = useState(false)
  const [success, setSuccess] = useState(false)
  const [resendAt, setResendAt] = useState(0)
  const [now, setNow] = useState(Date.now)
  const emailRef = useRef(null)
  const countdown = Math.max(0, Math.ceil((resendAt - now) / 1000))
  const emailVerification = register && settings?.email_verify_enabled === true
  const passwordResetEnabled = settings?.email_verify_enabled === true && settings?.password_reset_enabled === true
  const invalidResetLink = reset && (!resetLink.email || !resetLink.token)
  const settingsBlocked = (register || forgot) && !settings
  const featureDisabled = (register && settings?.registration_enabled === false) || (forgot && settings && !passwordResetEnabled)

  useEffect(() => {
    if (reset) return
    let active = true
    setSettings(null)
    setSettingsError('')
    request('/settings/public').then(data => {
      if (active) setSettings(data)
    }).catch(() => {
      if (active) setSettingsError('无法加载账户设置，请重试。')
    })
    return () => { active = false }
  }, [reset, settingsAttempt])

  useEffect(() => {
    if (!resendAt) return
    const timer = setInterval(() => {
      const time = Date.now()
      setNow(time)
      if (time >= resendAt) setResendAt(0)
    }, 1000)
    return () => clearInterval(timer)
  }, [resendAt])

  function startCountdown(seconds = 60) {
    const duration = Number.isFinite(Number(seconds)) ? Math.max(0, Number(seconds)) : 60
    const time = Date.now()
    setNow(time)
    setResendAt(time + duration * 1000)
  }

  function navigate(event, page) {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    history.pushState({}, '', page === 'home' ? '/' : `/${page}`)
    setPage(page)
  }

  function authLink(page, label, className = 'text-link') {
    return <a className={className} href={`/${page}`} onClick={event => navigate(event, page)}>{label}</a>
  }

  function changeEmail(value) {
    setForm(previous => ({ ...previous, email: value, verifyCode: '' }))
    setNotice('')
    setError('')
  }

  async function sendCode() {
    if (sending || busy || countdown || settingsBlocked || featureDisabled) return
    if (!emailRef.current.reportValidity()) return
    setSending(true)
    setError('')
    setNotice('')
    try {
      const data = await request('/auth/send-verify-code', {
        method: 'POST', body: JSON.stringify({ email: form.email.trim() }),
      })
      startCountdown(data.countdown ?? 60)
      setNotice('验证码已发送，请查收邮箱；如未收到，请检查垃圾邮件。')
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSending(false)
    }
  }

  async function submit(event) {
    event.preventDefault()
    if (busy || sending || settingsBlocked || featureDisabled || invalidResetLink) return
    setError('')
    setResetExpired(false)
    if (reset && form.password !== form.confirmPassword) {
      setError('两次输入的密码不一致。')
      return
    }
    setBusy(true)
    try {
      if (forgot) {
        if (countdown) return
        await request('/auth/forgot-password', {
          method: 'POST', body: JSON.stringify({ email: form.email.trim() }),
        })
        startCountdown()
        setSuccess(true)
        return
      }
      if (reset) {
        await request('/auth/reset-password', {
          method: 'POST', body: JSON.stringify({ ...resetLink, new_password: form.password }),
        })
        clearAuthKeys()
        setUser(null)
        setForm(previous => ({ ...previous, password: '', confirmPassword: '' }))
        history.replaceState({}, '', '/reset-password')
        setSuccess(true)
        return
      }
      const payload = { email: form.email.trim(), password: form.password }
      if (register) {
        payload.username = form.name.trim()
        if (emailVerification) payload.verify_code = form.verifyCode.trim()
        if (settings.invitation_code_enabled) payload.invitation_code = form.invitationCode.trim()
      }
      const data = await request(login ? '/auth/login' : '/auth/register', {
        method: 'POST', body: JSON.stringify(payload),
      })
      if (data.access_token) localStorage.setItem('auth_token', data.access_token)
      if (data.refresh_token) localStorage.setItem('refresh_token', data.refresh_token)
      if (data.expires_in) localStorage.setItem('token_expires_at', String(Date.now() + data.expires_in * 1000))
      const user = data.user || data
      localStorage.setItem('auth_user', JSON.stringify(user))
      setUser(user)
      if (user.role === 'admin') {
        window.location.href = import.meta.env.VITE_ADMIN_URL || (import.meta.env.DEV ? 'http://127.0.0.1:5174/admin/index.html' : '/admin/')
      } else {
        history.pushState({}, '', '/keys')
        setPage('keys')
      }
    } catch (err) {
      setError(errorMessage(err))
      if (reset) setResetExpired(apiErrorCode(err) === 'INVALID_RESET_TOKEN')
    } finally {
      setBusy(false)
    }
  }

  return <main className="auth-page">
    <a href="/" aria-label="返回 Yingzo 首页" onClick={event => navigate(event, 'home')}>
      <img className="logo" src="/assets/yingzo-logo.png" alt="Yingzo" />
    </a>
    <section className="auth-card" aria-labelledby="auth-title">
      <p className="kicker">YINGZO / CREATIVE AGENT</p>
      <h1 id="auth-title">{title}</h1>
      <p className="muted">{description}</p>
      {success ? <div className="auth-result">
        <div className="auth-notice" role="status">
          {forgot ? <>如果 <strong>{form.email.trim()}</strong> 已注册，你将收到密码重设链接。请查收邮箱或垃圾邮件，并点击邮件中的链接继续。</> : '密码已重设成功，请使用新密码登录。'}
        </div>
        {error && <div className="error" role="alert">{error}</div>}
        {forgot && <>
          <button className="outline full" onClick={submit} disabled={busy || countdown > 0}>{busy ? '发送中…' : countdown > 0 ? `${countdown} 秒后可重新发送` : '重新发送链接'}</button>
          <button className="text-link" onClick={() => { setSuccess(false); setError('') }}>换一个邮箱</button>
        </>}
        {authLink('login', '返回登录', 'dark full auth-primary-link')}
      </div> : <>
        {settingsError && <div className="error auth-settings-error" role="alert">
          <span>{settingsError}</span><button type="button" onClick={() => setSettingsAttempt(attempt => attempt + 1)}>重试</button>
        </div>}
        {settingsBlocked && !settingsError && <p className="muted" role="status">正在加载账户设置…</p>}
        {featureDisabled && <div className="auth-notice" role="status">{register ? '当前暂未开放注册，请联系管理员。' : '当前暂未开启邮箱找回密码，请联系管理员。'}</div>}
        {invalidResetLink ? <div className="auth-result">
          <div className="error" role="alert">重设链接不完整或无效，请重新申请密码重设邮件。</div>
          {authLink('forgot-password', '重新申请重设链接', 'dark full auth-primary-link')}
        </div> : !featureDisabled && <form onSubmit={submit} aria-busy={busy || sending}>
          {register && <label>昵称<input name="username" autoComplete="nickname" value={form.name} onChange={event => setForm({ ...form, name: event.target.value })} disabled={busy || sending} required /></label>}
          {reset ? <p className="auth-reset-email">重设账户：<strong>{resetLink.email}</strong></p> : <label>邮箱<input ref={emailRef} type="email" name="email" autoComplete="email" value={form.email} onChange={event => changeEmail(event.target.value)} disabled={busy || sending} required /></label>}
          {!forgot && <label>{reset ? '新密码' : '密码'}<input type="password" name="password" autoComplete={login ? 'current-password' : 'new-password'} minLength={login ? undefined : 6} value={form.password} onChange={event => setForm({ ...form, password: event.target.value })} disabled={busy || sending} aria-describedby={login ? undefined : 'auth-password-hint'} required />{!login && <small id="auth-password-hint" className="auth-field-hint">密码至少 6 位。</small>}</label>}
          {reset && <label>确认新密码<input type="password" name="confirm-password" autoComplete="new-password" minLength={6} value={form.confirmPassword} onChange={event => setForm({ ...form, confirmPassword: event.target.value })} disabled={busy} required /></label>}
          {emailVerification && <div className="auth-verification">
            <label htmlFor="auth-verify-code">邮箱验证码</label>
            <div className="auth-code-row">
              <input id="auth-verify-code" name="verify_code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} placeholder="6 位验证码" value={form.verifyCode} onChange={event => setForm({ ...form, verifyCode: event.target.value })} disabled={busy || sending} aria-describedby="auth-code-hint" required />
              <button type="button" className="outline" onClick={sendCode} disabled={busy || sending || countdown > 0 || !form.email.trim()}>{sending ? '发送中…' : countdown > 0 ? `${countdown} 秒后重发` : '获取验证码'}</button>
            </div>
            <small id="auth-code-hint" className="auth-field-hint">验证码将发送到上方邮箱。</small>
          </div>}
          {register && settings?.invitation_code_enabled && <label>邀请码<input name="invitation_code" value={form.invitationCode} onChange={event => setForm({ ...form, invitationCode: event.target.value })} disabled={busy || sending} required /></label>}
          {login && passwordResetEnabled && authLink('forgot-password', '忘记密码？', 'auth-forgot-link')}
          {notice && <div className="auth-notice" role="status">{notice}</div>}
          {error && <div className="error" role="alert">{error}</div>}
          {resetExpired && authLink('forgot-password', '重新申请重设链接', 'auth-forgot-link')}
          <button className="dark full" type="submit" disabled={busy || sending || settingsBlocked || (forgot && countdown > 0)}>{busy ? '处理中…' : forgot && countdown > 0 ? `${countdown} 秒后可重新发送` : `${submitLabel} →`}</button>
        </form>}
        {(!login || settings?.registration_enabled !== false) && authLink(login ? 'register' : 'login', login ? '还没有账户？创建账户' : '已有账户？返回登录')}
      </>}
    </section>
  </main>
}
