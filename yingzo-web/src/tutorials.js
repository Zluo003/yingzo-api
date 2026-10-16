export function isTutorialURL(raw) {
  try {
    const url = new URL(raw)
    return url.protocol === 'https:' && !url.username && !url.password && !url.port &&
      /\.(feishu\.cn|larksuite\.com)$/.test(url.hostname) &&
      /^\/(docx|docs|wiki)\/[a-zA-Z0-9_-]{1,80}\/?$/.test(url.pathname)
  } catch { return false }
}

export function tutorialArticles(config) {
  if (!config?.enabled) return []
  return (config.categories || []).filter(c => c.enabled).flatMap(category =>
    (category.articles || []).filter(a => a.enabled && isTutorialURL(a.url)).map(article => ({ ...article, category }))
  )
}

export function selectedTutorial(articles, search) {
  const id = new URLSearchParams(search).get('article')
  return articles.find(article => article.id === id) || articles[0] || null
}

let sdkPromise
export function loadTutorialSDK() {
  if (window.DocComponentSdk) return Promise.resolve(window.DocComponentSdk)
  if (!sdkPromise) sdkPromise = new Promise((resolve, reject) => {
    const script = document.createElement('script')
    script.src = 'https://sf1-scmcdn-cn.feishucdn.com/obj/feishu-static/docComponentSdk/lib/1.0.13.js'
    script.async = true
    const timer = setTimeout(() => fail(), 15000)
    function fail() {
      clearTimeout(timer)
      script.remove()
      sdkPromise = null
      reject(new Error('飞书组件暂时无法加载，请重试或打开原文。'))
    }
    script.onload = () => {
      clearTimeout(timer)
      if (window.DocComponentSdk) resolve(window.DocComponentSdk)
      else fail()
    }
    script.onerror = fail
    document.head.appendChild(script)
  })
  return sdkPromise
}

export const tutorialFeatureConfig = {
  extensions: {
    suiteNavBar: { disable: true },
    directory: { disable: true },
    content: { readonly: true, titleVisible: true, mode: 'wide', maxWidth: 900, padding: [32, 32, 48, 32], background: '#faf7f2' },
    comment: { partial: { disable: true }, global: { disable: true } },
    like: { disable: true },
    footer: { enable: false },
  },
}
