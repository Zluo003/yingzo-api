import { useEffect, useMemo, useRef, useState } from 'react'
import { request } from './lib.js'
import { loadTutorialSDK, selectedTutorial, tutorialArticles, tutorialFeatureConfig } from './tutorials.js'
import './tutorials.css'

function DocumentReader({ article, mode }) {
  const mount = useRef(null)
  const [status, setStatus] = useState(mode === 'sdk' ? 'loading' : 'embedded')
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let disposed = false
    let component
    setStatus(mode === 'sdk' ? 'loading' : 'embedded')
    setError('')
    // A cross-origin iframe's load event can wait on Feishu's nested frames and
    // cannot prove that the document is readable. Only time the official SDK.
    const timer = mode === 'sdk' ? setTimeout(() => { if (!disposed) setStatus(s => s === 'loading' ? 'slow' : s) }, 15000) : null
    if (mode === 'sdk') {
      Promise.all([
        loadTutorialSDK(),
        request(`/settings/tutorials/auth?article_id=${encodeURIComponent(article.id)}&url=${encodeURIComponent(location.origin + location.pathname)}`),
      ]).then(async ([SDK, auth]) => {
        if (disposed) return
        component = new SDK({
          src: article.url, mount: mount.current, auth,
          size: { width: '100%', height: '100%' },
          config: tutorialFeatureConfig,
          onAuthError: () => { if (!disposed) { setError('飞书文档鉴权失败，请重试或打开原文。'); setStatus('error') } },
          onError: () => { if (!disposed) { setError('暂时无法显示这篇教程，请检查文档权限或打开原文。'); setStatus('error') } },
        })
        await component.start()
        if (disposed) return
        component.setFeatureConfig(tutorialFeatureConfig)
        setStatus(s => s === 'error' ? s : 'ready')
      }).catch(err => { if (!disposed) { setError(err.message); setStatus('error') } })
    }
    return () => { disposed = true; clearTimeout(timer); component?.destroy() }
  }, [article.id, article.url, mode, attempt])

  return <section className="tutorial-reader" aria-label={article.title}>
    <div className="tutorial-reader-toolbar">
      <span>{article.category.title}<span aria-hidden="true"> / </span><strong>{article.title}</strong></span>
      <div><button onClick={() => setAttempt(a => a + 1)}>重新加载</button><a href={article.url} target="_blank" rel="noopener noreferrer">打开原文 ↗</a></div>
    </div>
    {status === 'loading' && <div className="tutorial-loading" role="status">正在加载教程…</div>}
    {(status === 'error' || status === 'slow') && <div className="tutorial-reader-notice" role="status">
      <span>{error || '加载时间较长。如未显示正文，请重新加载或打开原文。'}</span>
      <button onClick={() => setAttempt(a => a + 1)}>重试</button>
    </div>}
    <div className="tutorial-document" ref={mount} aria-busy={status === 'loading'}>
      {mode !== 'sdk' && <iframe key={`${article.id}-${attempt}`} title={article.title} src={article.url}
        referrerPolicy="strict-origin-when-cross-origin" allow="fullscreen" allowFullScreen
        onLoad={() => setStatus('ready')} onError={() => { setError('文档暂时无法加载，请重试或打开原文。'); setStatus('error') }} />}
    </div>
  </section>
}

export default function TutorialsPage({ config, setPage, user }) {
  const [search, setSearch] = useState(location.search)
  const [filter, setFilter] = useState('')
  const [menuOpen, setMenuOpen] = useState(false)
  const [collapsed, setCollapsed] = useState({})
  useEffect(() => {
    const onPop = () => setSearch(location.search)
    addEventListener('popstate', onPop)
    return () => removeEventListener('popstate', onPop)
  }, [])
  const articles = useMemo(() => tutorialArticles(config), [config])
  const selected = selectedTutorial(articles, search)
  useEffect(() => {
    const previousTitle = document.title
    document.title = selected ? `${selected.title} · 使用教程 | Yingzo` : '使用教程 | Yingzo'
    return () => { document.title = previousTitle }
  }, [selected?.id, selected?.title])
  const categories = (config?.categories || []).filter(c => c.enabled).map(category => ({
    ...category, articles: articles.filter(a => a.category.id === category.id &&
      `${category.title} ${a.title}`.toLocaleLowerCase().includes(filter.trim().toLocaleLowerCase())),
  })).filter(c => c.articles.length)

  const route = page => ({ href: page === 'home' ? '/' : `/${page}`, onClick: event => {
    if (event.button || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault(); history.pushState({}, '', page === 'home' ? '/' : `/${page}`); setPage(page)
  } })
  function selectArticle(event, article) {
    if (event.button || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    const next = `?article=${encodeURIComponent(article.id)}`
    history.pushState({}, '', `/tutorials${next}`); setSearch(next); setMenuOpen(false)
  }
  const index = articles.findIndex(a => a.id === selected?.id)
  return <div className="tutorial-page">
    <a className="tutorial-skip" href="#tutorial-content">跳到教程正文</a>
    <header className="tutorial-header">
      <a className="tutorial-brand" {...route('home')}><img src="/assets/yingzo-logo.png" alt="影作"/><span>Yingzo</span></a>
      <nav aria-label="教程页导航"><a {...route('home')}>首页</a><a {...route('download')}>软件下载</a><a aria-current="page" href="/tutorials">教程</a></nav>
      <a className="tutorial-account" {...route(user ? 'keys' : 'login')}>{user ? '我的工作区' : '登录'} <span>↗</span></a>
    </header>
    <div className="tutorial-workspace">
      <aside className={`tutorial-sidebar${menuOpen ? ' is-open' : ''}`}>
        <div className="tutorial-sidebar-head"><p>YINGZO / LEARN</p><h1>使用教程</h1><span>从第一步，到每一次创作。</span></div>
        <label className="tutorial-search"><span className="sr-only">搜索教程标题</span><input type="search" placeholder="搜索教程" value={filter} onChange={e => setFilter(e.target.value)}/><span aria-hidden="true">⌕</span></label>
        <nav className="tutorial-directory" aria-label="教程目录">
          {categories.map(category => <section key={category.id}>
            <button className="tutorial-category" aria-expanded={filter ? true : !collapsed[category.id]} onClick={() => setCollapsed(c => ({ ...c, [category.id]: !c[category.id] }))}>
              <span>{category.title}</span><span aria-hidden="true">{!filter && collapsed[category.id] ? '+' : '−'}</span>
            </button>
            {(filter || !collapsed[category.id]) && <ul>{category.articles.map(article => <li key={article.id}><a
              href={`/tutorials?article=${encodeURIComponent(article.id)}`} aria-current={selected?.id === article.id ? 'page' : undefined}
              onClick={event => selectArticle(event, article)}>{article.title}<span aria-hidden="true">↗</span></a></li>)}</ul>}
          </section>)}
          {categories.length === 0 && <p className="tutorial-directory-empty">{filter ? '未找到相关教程' : '暂无教程'}</p>}
        </nav>
        <a className="tutorial-sidebar-footer" {...route('download')}>下载 Yingzo <span>↗</span></a>
      </aside>
      <main id="tutorial-content" className="tutorial-main">
        <button className="tutorial-mobile-toggle" aria-expanded={menuOpen} onClick={() => setMenuOpen(v => !v)}>{menuOpen ? '收起目录 −' : '浏览教程目录 +'}</button>
        {selected ? <>
          <DocumentReader key={selected.id} article={selected} mode={config.mode}/>
          <div className="tutorial-pagination">{index > 0 ? <a href={`/tutorials?article=${encodeURIComponent(articles[index - 1].id)}`} onClick={e => selectArticle(e, articles[index - 1])}><small>← 上一篇</small>{articles[index - 1].title}</a> : <span/>}{index < articles.length - 1 && <a href={`/tutorials?article=${encodeURIComponent(articles[index + 1].id)}`} onClick={e => selectArticle(e, articles[index + 1])}><small>下一篇 →</small>{articles[index + 1].title}</a>}</div>
        </> : <div className="tutorial-empty"><p>YINGZO / LEARN</p><h2>教程准备中</h2><p>新的使用指南将在这里发布。</p><a {...route('home')}>返回首页 →</a></div>}
      </main>
    </div>
  </div>
}
