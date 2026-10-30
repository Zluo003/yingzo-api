import { message, getIntlLocale, getLocale, t as tr } from "./i18n/index.js";
import { Trans } from "react-i18next";
import { useEffect, useState } from 'react';
import { request } from './lib.js';
import { modelPriceRange, modelPriceUnit, seedancePriceChannels } from './model-pricing.js';
import { musicPriceLabel } from './music-pricing.js';
import './model-plaza.css';
function getCategories() {
  return [{
    id: 'text',
    get name() {
      return tr("文本");
    },
    en: 'Text',
    mark: 'Aa',
    get description() {
      return tr("让灵感成为文字。");
    },
    get unit() {
      return tr("每百万 tokens");
    }
  }, {
    id: 'image',
    get name() {
      return tr("图片");
    },
    en: 'Image',
    mark: '▧',
    get description() {
      return tr("让想象有迹可循。");
    },
    get unit() {
      return tr("每张");
    }
  }, {
    id: 'audio',
    get name() {
      return tr("音乐");
    },
    en: 'Music',
    mark: '♪',
    get description() {
      return tr("让灵感成为旋律。");
    },
    get unit() {
      return tr("每次");
    }
  }, {
    id: 'video',
    get name() {
      return tr("视频");
    },
    en: 'Video',
    mark: '▷',
    get description() {
      return tr("让画面开始叙事。");
    },
    get unit() {
      return tr("每秒");
    }
  }];
}
const platforms = {
  openai: 'OpenAI',
  anthropic: 'Anthropic',
  gemini: 'Google',
  grok: 'xAI',
  deepseek: 'DeepSeek',
  kimi: 'Kimi',
  get zhipu() {
    return tr("智谱");
  },
  minimax: 'MiniMax',
  get video() {
    return tr("视频生成");
  }
};
function PriceLine({
  label,
  prices,
  field,
  unit,
  featured = false
}) {
  return <div className={`mp-price-line${featured ? ' is-featured' : ''}`}><dt>{label}</dt><dd>{modelPriceRange(prices, field)}{unit && <small className="mp-price-unit">/{modelPriceUnit(unit) || tr("计费单位待确认")}</small>}</dd></div>;
}
function SeedanceChannelPrices({
  prices
}) {
  return <div className="mp-video-channels">{seedancePriceChannels(prices).map(channel => {
      const units = [...new Set(channel.prices.map(price => price.unit))];
      const billing = units.length === 1 ? {
        second: tr("按秒计费"),
        request: tr("按次计费")
      }[units[0]] : '';
      return <section className="mp-video-channel" key={channel.id} aria-label={tr("{{value1}}价格", {
        value1: channel.name
      })}>
      <div className="mp-channel-heading"><h3>{channel.name}</h3><span>{billing || tr("按档位计费")}</span></div>
      <dl className="mp-prices">{channel.prices.map((price, index) => <PriceLine key={`${price.resolution}-${index}`} label={price.resolution || tr("标准")} prices={[price]} field="unit_price" unit={price.unit} featured />)}</dl>
      {units.includes('request') && <p className="mp-channel-note">{tr("按次档位每个生成任务计费一次，参考视频不另收费。")}</p>}
      {units.includes('second') && <p className="mp-channel-note">{tr("按秒档位按生成视频时长计费。")}</p>}
    </section>;
    })}</div>;
}
function ModelCard({
  model,
  category
}) {
  const prices = (model.prices || []).filter(price => price.enabled !== false);
  const tokenPrices = prices.filter(price => price.unit === 'million_tokens');
  const requestPrices = prices.filter(price => price.unit === 'request');
  const hasVideoChannels = model.media_type === 'video' && model.model_code.startsWith('seedance-');
  const operationLabels = model.model_code === 'midjourney-v8.2' ? {
    generation: tr("图片生成 / 变体"),
    select: tr("选出单图"),
    upscale: tr("高清放大")
  } : {};
  return <article className="mp-card">
    <div className="mp-card-top"><span>{model.model_code === 'suno-v6' ? 'Suno' : platforms[model.platform] || model.platform}</span><span className="mp-enabled"><Trans i18nKey="<0/>已启用" components={[<i />]} /></span></div>
    <h2>{model.model_code}</h2>
    <div className="mp-price-caption"><span><Trans i18nKey="<0/>模型" components={[<>{category.name}</>]} /></span><span><Trans i18nKey="人民币 · <0/>" components={[<>{hasVideoChannels ? tr("按通道计价") : tokenPrices.length ? tr("每百万 tokens") : requestPrices.length ? tr("每次") : category.unit}</>]} /></span></div>
    {prices.length ? <>
      {tokenPrices.length > 0 && <dl className="mp-prices">
        <PriceLine label={tr("输入")} prices={tokenPrices} field="input_price" featured />
        <PriceLine label={tr("输出")} prices={tokenPrices} field="output_price" featured />
      </dl>}
      {model.media_type === 'text' && requestPrices.length > 0 && <dl className="mp-prices"><PriceLine label={tr("调用价格")} prices={requestPrices} field="unit_price" featured /></dl>}
      {hasVideoChannels ? <SeedanceChannelPrices prices={prices} /> : model.media_type !== 'text' && <dl className="mp-prices">{prices.map((price, index) => <PriceLine key={`${price.resolution}-${index}`} label={model.media_type === 'audio' ? musicPriceLabel(price.resolution) : operationLabels[price.resolution] || price.resolution || tr("标准")} prices={[price]} field="unit_price" unit={price.unit} featured />)}</dl>}
      {tokenPrices.some(price => price.cache_read_price != null || price.cache_write_price != null) && <details className="mp-cache"><summary><Trans i18nKey="缓存价格<0>＋</0>" components={[<span aria-hidden="true" />]} /></summary><dl>
        <PriceLine label={tr("缓存读取")} prices={tokenPrices} field="cache_read_price" />
        <PriceLine label={tr("缓存写入")} prices={tokenPrices} field="cache_write_price" />
        {tokenPrices.some(price => price.cache_write_1h_price != null) && <PriceLine label={tr("缓存写入 · 1 小时")} prices={tokenPrices} field="cache_write_1h_price" />}
      </dl></details>}
    </> : <div className="mp-unpriced"><strong>{tr("价格暂不可用")}</strong><p>{tr("定价完善后将在这里显示。")}</p></div>}
  </article>;
}
export default function ModelPlaza() {
  const [data, setData] = useState(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [revision, setRevision] = useState(0);
  const [type, setType] = useState('text');
  const [query, setQuery] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    request('/yingzo/models', {
      signal: controller.signal
    }).then(result => {
      if (!controller.signal.aborted) setData(result);
    }).catch(err => {
      if (!controller.signal.aborted) setError(err.message || message("模型加载失败，请重试。"));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [revision]);
  useEffect(() => {
    const previous = document.title;
    document.title = tr("模型广场 · Yingzo");
    return () => {
      document.title = previous;
    };
  }, [getLocale()]);
  const models = data?.models || [];
  const category = getCategories().find(item => item.id === type);
  const search = query.trim().toLowerCase();
  const visible = models.filter(model => model.media_type === type && `${model.model_code} ${model.platform} ${platforms[model.platform] || ''}`.toLowerCase().includes(search));
  const counts = Object.fromEntries(getCategories().map(item => [item.id, models.filter(model => model.media_type === item.id).length]));
  return <div className="model-plaza-page">
    <header className="page-head"><div><p className="kicker">YINGZO / MODEL COLLECTION</p><h1>{tr("模型广场")}</h1><p className="muted">{tr("从文字、画面到音乐，为每一种创意找到合适的模型。")}</p></div><span className="mp-currency">¥ <span>{tr("人民币计价")}</span></span></header>
    <div className="mp-categories" role="group" aria-label={tr("模型分类")}>{getCategories().map(item => <button key={item.id} aria-pressed={type === item.id} className={type === item.id ? 'is-selected' : ''} onClick={() => setType(item.id)}><span className="mp-category-mark" aria-hidden="true">{item.mark}</span><span className="mp-category-label">{item.name}<small>{item.en}</small></span><span className="mp-category-count">{loading || error ? '—' : counts[item.id]}</span></button>)}</div>
    <div className="mp-toolbar"><div><h2>{category.description}</h2><p aria-live="polite">{loading ? tr("正在读取最新模型与价格…") : error ? tr("暂时无法获取模型") : tr("{{value1}} 个{{value2}}模型 · 仅展示已启用模型", {
            value1: visible.length,
            value2: category.name
          })}</p></div><label className="mp-search"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 5 5" /></svg><input type="search" aria-label={tr("搜索模型或提供方")} placeholder={tr("搜索模型或提供方")} value={query} onChange={event => setQuery(event.target.value)} /></label></div>
    {loading ? <div className="mp-loading" role="status"><Trans i18nKey="<0/>正在同步模型目录" components={[<span className="mp-loading-dot" />]} /></div> : error ? <div className="mp-empty" role="alert"><h2>{tr("模型暂时未能加载")}</h2><p>{tr(error)}</p><button className="outline" onClick={() => setRevision(value => value + 1)}>{tr("重新加载 ↗")}</button></div> : visible.length ? <div className="mp-grid">{visible.map(model => <ModelCard key={`${model.platform}:${model.model_code}`} model={model} category={category} />)}</div> : <div className="mp-empty"><span aria-hidden="true">{category.mark}</span><h2>{search ? tr("没有找到匹配的模型") : tr("暂无已启用的{{value1}}模型", {
          value1: category.name
        })}</h2><p>{search ? tr("试试其他名称，或切换模型分类。") : tr("模型启用后，会自动出现在这里。")}</p>{search && <button className="outline" onClick={() => setQuery('')}>{tr("清除搜索")}</button>}</div>}
    <footer className="mp-footnote">{type === 'audio' && <p>{tr("音乐按操作与版本分别计价，价格来自 Yingzo Agent 分组配置。支持 Max 的操作开启 Max 后按对应单次价格的 2 倍计费。")}</p>}{type === 'video' && <p>{tr("Seedance 按通道和分辨率独立定价，计费单位以各档位标注为准。按次计费包含参考视频；按秒计费按生成时长计算。")}</p>}<p>{tr("展示 Yingzo Agent 的实际价格，文本价格已包含模型倍率。文本按百万 tokens、图片按张或按次、视频按秒或按次、音乐按次计价；仅展示已启用的价格档位。")}</p>{type === 'text' && <p>{tr("文本展示当前时段、标准请求的基础价格。价格区间表示可用线路的价格差异；长上下文、服务档位及推理选项等以实际请求计费为准。")}</p>}{!loading && !error && data?.as_of && <div><span><Trans i18nKey="更新于 <0/>" components={[<>{new Date(data.as_of).toLocaleString(getIntlLocale(), {
              month: '2-digit',
              day: '2-digit',
              hour: '2-digit',
              minute: '2-digit'
            })}</>]} /></span><button onClick={() => setRevision(value => value + 1)}>{tr("刷新价格 ↻")}</button></div>}</footer>
  </div>;
}
