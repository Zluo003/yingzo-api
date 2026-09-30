import { useEffect, useState } from 'react'
import { request } from './lib.js'
import { modelPriceRange } from './model-pricing.js'
import './model-plaza.css'

const categories = [
  { id: 'text', name: '文本', en: 'Text', mark: 'Aa', description: '让灵感成为文字。', unit: '每百万 tokens' },
  { id: 'image', name: '图片', en: 'Image', mark: '▧', description: '让想象有迹可循。', unit: '每张' },
  { id: 'video', name: '视频', en: 'Video', mark: '▷', description: '让画面开始叙事。', unit: '每秒' },
]
const platforms = { openai: 'OpenAI', anthropic: 'Anthropic', gemini: 'Google', grok: 'xAI', deepseek: 'DeepSeek', kimi: 'Kimi', zhipu: '智谱', minimax: 'MiniMax', video: '视频生成' }

function PriceLine({ label, prices, field, featured = false }) {
  return <div className={`mp-price-line${featured ? ' is-featured' : ''}`}><dt>{label}</dt><dd>{modelPriceRange(prices, field)}</dd></div>
}

function ModelCard({ model, category }) {
  const prices = model.prices || []
  const tokenPrices = prices.filter(price => price.unit === 'million_tokens')
  const requestPrices = prices.filter(price => price.unit === 'request')
  const operationLabels = model.model_code === 'midjourney-v8.2' ? { generation: '图片生成', upscale: '放大' } : {}
  return <article className="mp-card">
    <div className="mp-card-top"><span>{platforms[model.platform] || model.platform}</span><span className="mp-enabled"><i />已启用</span></div>
    <h2>{model.model_code}</h2>
    <div className="mp-price-caption"><span>{category.name}模型</span><span>人民币 · {tokenPrices.length ? '每百万 tokens' : requestPrices.length ? '每次' : category.unit}</span></div>
    {prices.length ? <>
      {tokenPrices.length > 0 && <dl className="mp-prices">
        <PriceLine label="输入" prices={tokenPrices} field="input_price" featured />
        <PriceLine label="输出" prices={tokenPrices} field="output_price" featured />
      </dl>}
      {model.media_type === 'text' && requestPrices.length > 0 && <dl className="mp-prices"><PriceLine label="调用价格" prices={requestPrices} field="unit_price" featured /></dl>}
      {model.media_type !== 'text' && <dl className="mp-prices">{prices.map((price, index) => <PriceLine key={`${price.resolution}-${index}`} label={operationLabels[price.resolution] || price.resolution || '标准'} prices={[price]} field="unit_price" featured />)}</dl>}
      {tokenPrices.some(price => price.cache_read_price != null || price.cache_write_price != null) && <details className="mp-cache"><summary>缓存价格<span aria-hidden="true">＋</span></summary><dl>
        <PriceLine label="缓存读取" prices={tokenPrices} field="cache_read_price" />
        <PriceLine label="缓存写入" prices={tokenPrices} field="cache_write_price" />
        {tokenPrices.some(price => price.cache_write_1h_price != null) && <PriceLine label="缓存写入 · 1 小时" prices={tokenPrices} field="cache_write_1h_price" />}
      </dl></details>}
    </> : <div className="mp-unpriced"><strong>价格暂不可用</strong><p>定价完善后将在这里显示。</p></div>}
  </article>
}

export default function ModelPlaza() {
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [revision, setRevision] = useState(0)
  const [type, setType] = useState('text')
  const [query, setQuery] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    request('/yingzo/models', { signal: controller.signal })
      .then(result => { if (!controller.signal.aborted) setData(result) })
      .catch(err => { if (!controller.signal.aborted) setError(err.message || '模型加载失败，请重试。') })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [revision])
  useEffect(() => {
    const previous = document.title
    document.title = '模型广场 · Yingzo'
    return () => { document.title = previous }
  }, [])

  const models = data?.models || []
  const category = categories.find(item => item.id === type)
  const search = query.trim().toLowerCase()
  const visible = models.filter(model => model.media_type === type && `${model.model_code} ${model.platform} ${platforms[model.platform] || ''}`.toLowerCase().includes(search))
  const counts = Object.fromEntries(categories.map(item => [item.id, models.filter(model => model.media_type === item.id).length]))
  return <div className="model-plaza-page">
    <header className="page-head"><div><p className="kicker">YINGZO / MODEL COLLECTION</p><h1>模型广场</h1><p className="muted">从文字到画面，为每一种创意找到合适的模型。</p></div><span className="mp-currency">¥ <span>人民币计价</span></span></header>
    <div className="mp-categories" role="group" aria-label="模型分类">{categories.map(item => <button key={item.id} aria-pressed={type === item.id} className={type === item.id ? 'is-selected' : ''} onClick={() => setType(item.id)}><span className="mp-category-mark" aria-hidden="true">{item.mark}</span><span className="mp-category-label">{item.name}<small>{item.en}</small></span><span className="mp-category-count">{loading || error ? '—' : counts[item.id]}</span></button>)}</div>
    <div className="mp-toolbar"><div><h2>{category.description}</h2><p aria-live="polite">{loading ? '正在读取最新模型与价格…' : error ? '暂时无法获取模型' : `${visible.length} 个${category.name}模型 · 仅展示已启用模型`}</p></div><label className="mp-search"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 5 5" /></svg><input type="search" aria-label="搜索模型或提供方" placeholder="搜索模型或提供方" value={query} onChange={event => setQuery(event.target.value)} /></label></div>
    {loading ? <div className="mp-loading" role="status"><span className="mp-loading-dot" />正在同步模型目录</div> : error ? <div className="mp-empty" role="alert"><h2>模型暂时未能加载</h2><p>{error}</p><button className="outline" onClick={() => setRevision(value => value + 1)}>重新加载 ↗</button></div> : visible.length ? <div className="mp-grid">{visible.map(model => <ModelCard key={`${model.platform}:${model.model_code}`} model={model} category={category} />)}</div> : <div className="mp-empty"><span aria-hidden="true">{category.mark}</span><h2>{search ? '没有找到匹配的模型' : `暂无已启用的${category.name}模型`}</h2><p>{search ? '试试其他名称，或切换模型分类。' : '模型启用后，会自动出现在这里。'}</p>{search && <button className="outline" onClick={() => setQuery('')}>清除搜索</button>}</div>}
    <footer className="mp-footnote"><p>展示 Yingzo Agent 的实际价格，文本价格已包含模型倍率。文本按百万 tokens、图片按张、视频按秒计价；按次模型单独标注。</p><p>文本展示当前时段、标准请求的基础价格。价格区间表示可用线路的价格差异；长上下文、服务档位及推理选项等以实际请求计费为准。</p>{!loading && !error && data?.as_of && <div><span>更新于 {new Date(data.as_of).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })}</span><button onClick={() => setRevision(value => value + 1)}>刷新价格 ↻</button></div>}</footer>
  </div>
}
