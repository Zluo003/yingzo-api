import { useEffect, useRef, useState } from 'react'
import { UPDATE_API } from './lib.js'
import './download.css'

const targets = [
  { id: 'mac', name: 'macOS', platform: 'darwin', arch: 'arm64', architecture: 'Apple Silicon', suffix: '适用于 M 系列芯片' },
  { id: 'windows', name: 'Windows', platform: 'win32', arch: 'x64', architecture: 'x64', suffix: '适用于 Intel 与 AMD 64 位处理器' },
]

function PlatformIcon({ platform }) {
  return <svg className="yd-platform-icon" viewBox="0 0 24 24" aria-hidden="true">{platform === 'mac'
    ? <path d="M17.05 12.54c-.02-2.25 1.84-3.35 1.93-3.4a4.16 4.16 0 0 0-3.27-1.77c-1.38-.14-2.72.82-3.43.82-.73 0-1.87-.8-3.07-.78a4.54 4.54 0 0 0-3.8 2.32c-1.64 2.84-.42 7.02 1.16 9.32.79 1.13 1.7 2.39 2.93 2.35 1.18-.05 1.62-.76 3.05-.76 1.4 0 1.81.76 3.04.73 1.27-.02 2.05-1.13 2.81-2.27a9.35 9.35 0 0 0 1.28-2.62 4.05 4.05 0 0 1-2.63-3.94ZM14.79 5.9c.63-.77 1.05-1.84.93-2.9-.91.04-2.01.61-2.66 1.36-.58.67-1.09 1.75-.95 2.78 1.02.08 2.06-.52 2.68-1.24Z" />
    : <path d="M2 4.2 10.5 3v8.4H2V4.2ZM12 2.8 22 1.4v10H12V2.8ZM2 12.8h8.5v8.4L2 20v-7.2Zm10 0h10v10l-10-1.4v-8.6Z" />}</svg>
}

function Arrow({ down = false }) {
  return <svg className={`yd-arrow${down ? ' is-down' : ''}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><path d="M4 12h15M13 5l7 7-7 7" /></svg>
}

function formatSize(value) {
  const bytes = Number(value)
  if (!Number.isFinite(bytes) || bytes <= 0) return ''
  return bytes < 1024 * 1024 ? `${Math.round(bytes / 1024)} KB` : `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function releaseLink(value) {
  try {
    const url = new URL(value)
    return ['https:', 'http:'].includes(url.protocol) ? url.href : ''
  } catch { return '' }
}

export default function DownloadPage({ user, setPage }) {
  const [releases, setReleases] = useState(() => targets.map(target => ({ ...target, status: 'loading' })))
  const [revision, setRevision] = useState(0)
  const pageRef = useRef(null)
  const go = (event, id) => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    history.pushState({}, '', id === 'home' ? '/' : `/${id}`)
    setPage(id)
    window.scrollTo(0, 0)
  }
  const route = id => ({ href: id === 'home' ? '/' : `/${id}`, onClick: event => go(event, id) })

  useEffect(() => {
    let active = true
    const controllers = []
    setReleases(targets.map(target => ({ ...target, status: 'loading' })))
    targets.forEach(async target => {
      const controller = new AbortController()
      controllers.push(controller)
      const timeout = setTimeout(() => controller.abort(), 12000)
      let release = { ...target, status: 'error' }
      try {
        const response = await fetch(`${UPDATE_API}/check?version=0.0.0&platform=${target.platform}&arch=${target.arch}`, { headers: { Accept: 'application/json' }, signal: controller.signal })
        if (!response.ok) throw new Error('Update service unavailable')
        const data = await response.json()
        const url = releaseLink(data.installer_url) || releaseLink(data.download_url)
        release = data.update && url
          ? { ...target, status: 'available', version: data.version, notes: typeof data.notes === 'string' ? data.notes : '', url, size: data.installer_size_bytes || data.size_bytes }
          : { ...target, status: 'unavailable' }
      } catch { /* Render a retry action for connectivity failures. */ }
      finally { clearTimeout(timeout) }
      if (active) setReleases(current => current.map(item => item.id === target.id ? release : item))
    })
    return () => { active = false; controllers.forEach(controller => controller.abort()) }
  }, [revision])

  useEffect(() => {
    const previousTitle = document.title
    document.title = '下载 Yingzo · 你的桌面创作室'
    const root = pageRef.current
    const observer = new IntersectionObserver(entries => entries.forEach(entry => {
      if (entry.isIntersecting) { entry.target.classList.add('is-visible'); observer.unobserve(entry.target) }
    }), { threshold: 0.12 })
    root?.querySelectorAll('[data-yd-reveal]').forEach(element => observer.observe(element))
    return () => {
      document.title = previousTitle
      observer.disconnect()
    }
  }, [])

  const hasError = releases.some(release => release.status === 'error')
  return <div className="yd" ref={pageRef}>
    <a className="yd-skip" href="#download-main">跳到下载</a>
    <header className="yd-header">
      <a className="yd-brand" aria-label="Yingzo 首页" {...route('home')}><img src="/assets/yingzo-logo.png" alt="影作" /><span>Yingzo</span></a>
      <nav className="yd-nav" aria-label="下载页导航"><a {...route('home')}>返回首页</a><a href="#getting-started">安装指引</a><a className="yd-account" {...route(user ? 'keys' : 'login')}>{user ? '我的工作区' : '登录'}<Arrow /></a></nav>
    </header>
    <main id="download-main">
      <section className="yd-hero" aria-labelledby="download-title">
        <div className="yd-hero-copy">
          <p className="yd-eyebrow yd-enter">A HOME FOR YOUR IDEAS / 桌面端下载</p>
          <h1 className="yd-enter" id="download-title"><span>Yingzo</span><span>你的桌面创作室。</span></h1>
          <p className="yd-intro yd-enter">从一个想法，到一部作品。<br className="yd-mobile-break" />在你的电脑上，自然发生。</p>
          <div className="yd-downloads yd-enter" aria-label="选择操作系统" aria-live="polite">
            {releases.map(release => <div className="yd-platform" key={release.id}>
              {release.status === 'available'
                ? <a className={`yd-download-button${release.id === 'windows' ? ' is-secondary' : ''}`} href={release.url} target="_blank" rel="noopener noreferrer"><PlatformIcon platform={release.id} /><span>下载 {release.name}</span><Arrow down /></a>
                : <button className={`yd-download-button${release.id === 'windows' ? ' is-secondary' : ''}`} disabled aria-label={`${release.name}：${release.status === 'loading' ? '检查中' : '暂不可用'}`}><PlatformIcon platform={release.id} /><span>{release.status === 'loading' ? '正在获取版本' : `${release.name} 暂不可用`}</span><Arrow down /></button>}
              <p className="yd-platform-caption">{release.architecture}<span>·</span>{release.status === 'available' ? `${release.version ? `v${String(release.version).replace(/^v/, '')}` : '最新版本'}${formatSize(release.size) ? ` · ${formatSize(release.size)}` : ''}` : release.status === 'loading' ? '正在检查…' : release.status === 'error' ? '连接失败' : '尚未发布安装包'}</p>
            </div>)}
          </div>
          {hasError ? <button className="yd-retry" onClick={() => setRevision(value => value + 1)}>暂时未能获取完整版本，重新检查 ↻</button> : <a className="yd-release-link" href="#release-details">版本信息与更新说明<Arrow down /></a>}
        </div>
      </section>
      <section className="yd-start yd-wrap" id="getting-started" data-yd-reveal>
        <div className="yd-section-heading"><p className="yd-eyebrow">READY WHEN YOU ARE</p><h2>三步，开始创作。</h2></div>
        <ol className="yd-steps">
          <li><span className="yd-step-number">01</span><h3>安装到你的电脑</h3><p>选择对应系统的安装包，<br />安装并打开 Yingzo。</p><a href="#download-main">选择安装包<Arrow /></a></li>
          <li><span className="yd-step-number">02</span><h3>连接模型服务</h3><p>登录账户，创建 API Key，<br />在桌面端完成模型连接。</p><a {...route(user ? 'keys' : 'login')}>获取 API Key<Arrow /></a></li>
          <li><span className="yd-step-number">03</span><h3>带上你的第一个想法</h3><p>新建项目，放入素材，<br />从一句话开始你的创作。</p><span className="yd-step-note">故事、图片与视频，都在这里。</span></li>
        </ol>
      </section>
      <section className="yd-release yd-wrap" id="release-details" data-yd-reveal>
        <div className="yd-section-heading"><p className="yd-eyebrow">ALWAYS MOVING FORWARD</p><h2>一次安装，持续更新。</h2><p>后续版本可在桌面端内获取，<br />让创作始终接得上。</p></div>
        <div className="yd-release-list">{releases.map(release => <details className="yd-release-item" key={release.id}>
          <summary><PlatformIcon platform={release.id} /><span>{release.name}<small>{release.architecture} · {release.suffix}</small></span><span className="yd-version">{release.status === 'available' && release.version ? `v${String(release.version).replace(/^v/, '')}` : release.status === 'loading' ? '检查中' : '暂不可用'}</span><span className="yd-expand" aria-hidden="true">＋</span></summary>
          <div className="yd-release-content">{release.status === 'available' ? <><p className="yd-release-notes">{release.notes || '当前提供最新稳定版本，安装后可在桌面端内检查后续更新。'}</p><a href={release.url} target="_blank" rel="noopener noreferrer">下载 {release.name} 安装包{formatSize(release.size) ? ` · ${formatSize(release.size)}` : ''}<Arrow down /></a></> : <p>{release.status === 'error' ? '暂时无法连接更新服务，请重新检查。' : release.status === 'loading' ? '正在获取最新版本信息…' : '该系统当前暂无可用安装包。'}</p>}</div>
        </details>)}<button className="yd-check" onClick={() => setRevision(value => value + 1)} disabled={releases.some(release => release.status === 'loading')}>重新检查版本 ↻</button></div>
      </section>
    </main>
    <footer className="yd-footer yd-wrap"><span>Yingzo 影作<small>让想法自然生长。</small></span><a {...route('home')}>探索 Yingzo<Arrow /></a><small>© {new Date().getFullYear()} Yingzo</small></footer>
  </div>
}
