import { useEffect, useRef, useState } from 'react'
import './landing.css'

const art = {
  hero: '/assets/landing/creative-studio.webp',
  cinema: '/assets/landing/rainy-reunion.webp',
  commerce: '/assets/landing/everyday-object.webp',
  workspace: '/assets/landing/yingzo-writing-workspace.webp',
}

function Arrow({ down = false, diagonal = false }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
      className={`lp-arrow${down ? ' is-down' : ''}${diagonal ? ' is-diagonal' : ''}`}
    >
      <path
        d="M4 12h15m-6-6 6 6-6 6"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

function Mark({ kind }) {
  return (
    <svg viewBox="0 0 40 40" fill="none" aria-hidden="true" className="lp-mark">
      {kind === 'spark' ? (
        <>
          <path d="M20 4v32M4 20h32M9 9l22 22M9 31 31 9" />
          <circle cx="20" cy="20" r="9" />
        </>
      ) : kind === 'layers' ? (
        <>
          <path d="m20 5 16 9-16 9L4 14 20 5ZM4 21l16 9 16-9M4 28l16 9 16-9" />
        </>
      ) : (
        <>
          <path d="M8 6h16l8 8v20H8V6Z" />
          <path d="M24 6v9h8M13 24l5 5 10-11" />
        </>
      )}
    </svg>
  )
}

function useLandingMotion(root) {
  useEffect(() => {
    const element = root.current
    const preference = matchMedia('(prefers-reduced-motion: reduce)')
    let frame = 0
    const update = () => {
      frame = 0
      const progress =
        scrollY /
        Math.max(1, document.documentElement.scrollHeight - innerHeight)
      element.style.setProperty('--reading-progress', Math.min(1, progress))
      element.style.setProperty(
        '--hero-shift',
        `${preference.matches ? 0 : Math.min(scrollY, innerHeight) * 0.16}px`
      )
    }
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(update)
    }
    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            entry.target.classList.add('is-visible')
            observer.unobserve(entry.target)
          }
        })
      },
      { threshold: 0.12 }
    )
    element
      .querySelectorAll('[data-reveal]')
      .forEach((node) => observer.observe(node))
    update()
    addEventListener('scroll', onScroll, { passive: true })
    addEventListener('resize', onScroll)
    preference.addEventListener('change', onScroll)
    return () => {
      observer.disconnect()
      cancelAnimationFrame(frame)
      removeEventListener('scroll', onScroll)
      removeEventListener('resize', onScroll)
      preference.removeEventListener('change', onScroll)
    }
  }, [root])
}

const steps = [
  {
    title: '带着目标和素材开始',
    text: '一个想法、一段文稿，或一张参考图。把你想做的事，在对话里说清楚。',
    label: '说出想法',
  },
  {
    title: '在项目里推进制作',
    text: '梳理内容，准备参考，再推进制作。每一步的进度与产物，都看得见。',
    label: '推进制作',
  },
  {
    title: '查看结果，由你决定',
    text: '保留满意的候选，也可以继续提出修改。选好要采用的版本，再导出作品。',
    label: '选择成果',
  },
]

function moveTab(event, index, length, setIndex) {
  let next
  if (event.key === 'ArrowRight' || event.key === 'ArrowDown')
    next = (index + 1) % length
  if (event.key === 'ArrowLeft' || event.key === 'ArrowUp')
    next = (index - 1 + length) % length
  if (event.key === 'Home') next = 0
  if (event.key === 'End') next = length - 1
  if (next === undefined) return
  event.preventDefault()
  setIndex(next)
  event.currentTarget.parentElement
    .querySelectorAll('[role="tab"]')
    [next]?.focus()
}

function Workflow() {
  const [step, setStep] = useState(0)
  return (
    <section
      className="lp-workflow"
      id="workflow"
      aria-labelledby="workflow-title"
    >
      <div className="lp-wrap">
        <div className="lp-section-heading" data-reveal>
          <div>
            <p className="lp-eyebrow">THE CREATIVE PROCESS / 工作方式</p>
            <h2 id="workflow-title">
              开始很简单，
              <br />
              <em>过程看得见。</em>
            </h2>
          </div>
          <p>
            把注意力放在想表达什么，
            <br />
            以及哪一版，更接近你的想法。
          </p>
        </div>
        <div className="lp-workflow-layout" data-reveal>
          <div
            className="lp-steps"
            role="tablist"
            aria-label="创作流程"
            aria-orientation="vertical"
          >
            {steps.map((item, index) => (
              <button
                key={item.title}
                id={`workflow-tab-${index}`}
                role="tab"
                type="button"
                aria-selected={step === index}
                aria-controls="workflow-preview"
                tabIndex={step === index ? 0 : -1}
                className={step === index ? 'is-active' : ''}
                onClick={() => setStep(index)}
                onKeyDown={(event) =>
                  moveTab(event, index, steps.length, setStep)
                }
              >
                <span className="lp-step-number">0{index + 1}</span>
                <span>
                  <strong>{item.title}</strong>
                  <span className="lp-step-description">{item.text}</span>
                </span>
                <Arrow />
              </button>
            ))}
          </div>
          <div
            className="lp-workflow-preview"
            role="tabpanel"
            id="workflow-preview"
            aria-labelledby={`workflow-tab-${step}`}
            tabIndex={0}
          >
            <a
              className="lp-desktop-capture"
              href={art.workspace}
              target="_blank"
              rel="noreferrer"
              aria-label="查看 Yingzo 写作工作区完整截图（新标签页）"
            >
              <img
                src={art.workspace}
                alt="Yingzo 桌面端写作工作区：左侧按项目管理创作对话，右侧通过对话推进小说写作并查看阶段成果。"
                width="2760"
                height="1440"
                loading="lazy"
                decoding="async"
              />
            </a>
            <div className="lp-workflow-context" key={step}>
              <span>
                0{step + 1} / {steps[step].label}
              </span>
              <p>{steps[step].text}</p>
            </div>
          </div>
        </div>
        <p className="lp-workflow-caption">
          <span>Yingzo 桌面端 · 写作工作区</span>
          <a href={art.workspace} target="_blank" rel="noreferrer">
            查看完整界面 <Arrow diagonal />
          </a>
        </p>
      </div>
    </section>
  )
}

function CreativeControl() {
  const [mode, setMode] = useState(0)
  return (
    <section
      className="lp-control lp-wrap"
      id="creative-control"
      aria-labelledby="control-title"
    >
      <div className="lp-control-copy" data-reveal>
        <p className="lp-eyebrow">YOUR WAY, YOUR WORK / 创作掌控</p>
        <h2 id="control-title">
          有协作，
          <br />
          也有你的坚持。
        </h2>
        <p className="lp-body-copy">
          交给 Yingzo 推进，也可以亲自控制细节。
          <br />
          创作的方法、素材和最终决定，都有自己的位置。
        </p>
        <a className="lp-text-link" href="#download">
          找到你的创作方式 <Arrow />
        </a>
      </div>
      <div className="lp-control-details" data-reveal>
        <div className="lp-mode-tabs" role="tablist" aria-label="创作模式">
          {['Agent 协作', '直接生成'].map((label, index) => (
            <button
              type="button"
              key={label}
              id={`mode-tab-${index}`}
              role="tab"
              aria-selected={mode === index}
              aria-controls="mode-description"
              tabIndex={mode === index ? 0 : -1}
              onKeyDown={(event) => moveTab(event, index, 2, setMode)}
              onClick={() => setMode(index)}
            >
              {label}
              <Arrow diagonal />
            </button>
          ))}
        </div>
        <div
          className="lp-mode-panel"
          role="tabpanel"
          id="mode-description"
          aria-labelledby={`mode-tab-${mode}`}
          tabIndex={0}
        >
          <div key={mode} className="lp-mode-inner">
            <span className="lp-mode-kicker">
              {mode === 0 ? '从目标出发' : '从明确的提示词出发'}
            </span>
            <h3>
              {mode === 0 ? '你说想法，它协助推进。' : '每个细节，按你的设置。'}
            </h3>
            <p>
              {mode === 0
                ? '结合项目资料组织内容、准备提示词，并调用相应能力。你可以随时查看产物，给出下一步意见。'
                : '在影视工作区，选择图片或视频模型，设置提示词与参数，直接提交你的创作想法。'}
            </p>
            <span className="lp-mode-footnote">
              {mode === 0
                ? '影视与商业内容，均可从对话开始。'
                : '可用模型、参考类型与输出规格以当前连接为准。'}
            </span>
          </div>
        </div>
        <div className="lp-ownership-row">
          <Mark kind="layers" />
          <div>
            <h3>每次尝试，都有来处。</h3>
            <p>复用参考素材，保留媒体候选与修改版本，决定哪一版进入作品。</p>
          </div>
        </div>
        <div className="lp-ownership-row">
          <Mark kind="file" />
          <div>
            <h3>项目在本地，作品在手里。</h3>
            <p>文稿和已下载媒体留在电脑里，按任务导出成果或素材包。</p>
            <small>调用云端模型时，会发送本次任务所需的内容。</small>
          </div>
        </div>
        <div className="lp-ownership-row">
          <Mark kind="spark" />
          <div>
            <h3>把你的方法，留给下一次。</h3>
            <p>
              通过对话整理常用的写作方式与制作要求，形成可以查看、修改和采用的创作技能。
            </p>
          </div>
        </div>
      </div>
    </section>
  )
}

const faqs = [
  [
    '不会写提示词，也能使用吗？',
    '可以从目标、素材和修改意见开始。Agent 模式会协助组织内容与提示词。需要精细控制时，也可以在影视工作区的直接生成入口选择模型、填写提示词和参数。',
  ],
  [
    '可以只写故事，或只生成一张图吗？',
    '可以。小说、短篇和故事创作可以独立进行，不必继续制作视频。你也可以直接生成一张图片、一段视频，或在对话中提出单张商品图、海报的需求，从当前最需要的任务开始。',
  ],
  [
    '能用自己的素材吗？角色和商品能保持一致吗？',
    '可以添加自己的图片、视频和文稿，也可以引用已有项目素材。Yingzo 支持复用角色、场景、商品资料和参考图片，帮助后续制作沿用已有设定。实际生成结果仍需检查；参考类型与输出规格取决于所选模型。',
  ],
  [
    '我的文件存在哪里？',
    '项目文稿和已下载媒体保存在本地，可以继续整理与导出。调用云端模型时，本次任务所需的提示词和参考素材会发送至所连接的服务；是否公开分享作品由你决定。',
  ],
  [
    '如何收费？支持哪些模型？',
    '模型调用费用按当前服务的计费规则计算，具体价格与活动以网站公布的信息为准。可用模型和输出规格以当前连接的模型目录为准。你可以在网站账户中查看使用记录和充值信息。',
    'usage',
  ],
  [
    '支持哪些电脑？如何开始？',
    '支持 Windows 和 macOS。下载对应系统的安装包，安装桌面端并配置模型连接后，即可开始创作。',
    'download',
  ],
]

export default function LandingPage({ user, setPage }) {
  const root = useRef(null)
  const menuButton = useRef(null)
  const [menuOpen, setMenuOpen] = useState(false)
  useLandingMotion(root)
  useEffect(() => {
    const title = document.title
    const language = document.documentElement.lang
    const description = document.createElement('meta')
    description.name = 'description'
    description.content =
      'Yingzo 是面向影视创作者与商家的 AI 创作桌面应用。通过对话推进小说、剧本、分镜、图片和视频制作，复用参考素材，管理本地项目与修改版本。'
    document.title = 'Yingzo 影作｜用对话创作故事、图片与视频'
    document.documentElement.lang = 'zh-CN'
    document.head.append(description)
    return () => {
      document.title = title
      document.documentElement.lang = language
      description.remove()
    }
  }, [])
  useEffect(() => {
    if (!menuOpen) return
    const escape = (event) => {
      if (event.key === 'Escape') {
        setMenuOpen(false)
        menuButton.current?.focus()
      }
    }
    addEventListener('keydown', escape)
    return () => removeEventListener('keydown', escape)
  }, [menuOpen])
  function go(event, page) {
    if (
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey ||
      event.button !== 0
    )
      return
    event.preventDefault()
    history.pushState({}, '', `/${page}`)
    setPage(page)
    scrollTo({ top: 0, behavior: 'instant' })
  }
  const routeLink = (page) => ({
    href: `/${page}`,
    onClick: (event) => go(event, page),
  })
  const navigation = [
    ['use-cases', '创作场景'],
    ['workflow', '工作方式'],
    ['creative-control', '创作掌控'],
    ['faq', '常见问题'],
  ]
  return (
    <div className="lp" ref={root}>
      <a href="#main-content" className="lp-skip">
        跳到主要内容
      </a>
      <header className="lp-header">
        <a href="#" className="lp-brand" aria-label="Yingzo 影作首页">
          <img
            src="/assets/yingzo-logo.png"
            alt="影作"
            width="64"
            height="48"
          />
          <span>Yingzo</span>
        </a>
        <nav
          className={`lp-nav${menuOpen ? ' is-open' : ''}`}
          id="landing-navigation"
          aria-label="首页导航"
        >
          {navigation.map(([id, label]) => (
            <a key={id} href={`#${id}`} onClick={() => setMenuOpen(false)}>
              {label}
            </a>
          ))}
        </nav>
        <div className="lp-header-actions">
          <a
            className="lp-account-link"
            {...routeLink(user ? 'keys' : 'login')}
          >
            {user ? '账户控制台' : '登录'}
          </a>
          <a className="lp-button lp-button-small" {...routeLink('download')}>
            下载 Yingzo <Arrow down />
          </a>
          <button
            ref={menuButton}
            type="button"
            className="lp-menu-toggle"
            aria-label={menuOpen ? '关闭导航' : '打开导航'}
            aria-expanded={menuOpen}
            aria-controls="landing-navigation"
            onClick={() => setMenuOpen((value) => !value)}
          >
            <span />
            <span />
          </button>
        </div>
        <div className="lp-reading-progress" />
      </header>
      <main id="main-content">
        <section className="lp-hero" aria-labelledby="hero-title">
          <div className="lp-hero-art" aria-hidden="true">
            <img
              src={art.hero}
              alt=""
              width="1536"
              height="1024"
              fetchPriority="high"
            />
            <div className="lp-hero-wash" />
          </div>
          <div className="lp-hero-copy">
            <p className="lp-eyebrow lp-enter" style={{ '--delay': '0ms' }}>
              给创作，一个新的开始 / YOUR CREATIVE COMPANION
            </p>
            <div
              className="lp-wordmark lp-enter"
              style={{ '--delay': '100ms' }}
            >
              Yingzo<span>影作</span>
            </div>
            <h1
              className="lp-enter"
              id="hero-title"
              style={{ '--delay': '200ms' }}
            >
              用对话，
              <br />
              把想法<em>做成作品。</em>
            </h1>
            <p
              className="lp-hero-description lp-enter"
              style={{ '--delay': '300ms' }}
            >
              你的 AI 创作桌面应用。
              <br />
              从故事到镜头，从商品到内容，
              <br className="lp-mobile-break" />
              让每个想法都有下一步。
            </p>
            <div
              className="lp-hero-actions lp-enter"
              style={{ '--delay': '400ms' }}
            >
              <a className="lp-button" {...routeLink('download')}>
                下载 Yingzo <Arrow down />
              </a>
              <a className="lp-text-link" href="#use-cases">
                看看能做什么 <Arrow />
              </a>
            </div>
            <div
              className="lp-hero-meta lp-enter"
              style={{ '--delay': '500ms' }}
            >
              <span>影视创作</span>
              <i />
              <span>商业内容</span>
              <i />
              <span>本地项目</span>
            </div>
          </div>
          <div className="lp-hero-bottom">
            <a href="#use-cases">
              <span className="lp-scroll-mark">
                <Arrow down />
              </span>
              向下探索
            </a>
            <span>SMALL IDEAS. BIGGER WORLDS.</span>
            <span>STORIES · IMAGES · VIDEO</span>
          </div>
        </section>
        <section className="lp-values lp-wrap" aria-label="Yingzo 的创作方式">
          {[
            ['spark', '从目标开始', '说出你想完成的事，让创作从对话发生。'],
            [
              'layers',
              '让素材接得上',
              '沿用角色、场景与商品资料，接着上次继续。',
            ],
            [
              'file',
              '由你决定版本',
              '查看候选，提出修改，把满意的那一版留下。',
            ],
          ].map(([kind, title, text], index) => (
            <article
              key={kind}
              data-reveal
              style={{ '--delay': `${index * 90}ms` }}
            >
              <Mark kind={kind} />
              <div>
                <h2>{title}</h2>
                <p>{text}</p>
              </div>
            </article>
          ))}
        </section>
        <section
          className="lp-scenes lp-wrap"
          id="use-cases"
          aria-labelledby="scenes-title"
        >
          <div className="lp-section-heading" data-reveal>
            <div>
              <p className="lp-eyebrow">MADE FOR YOUR NEXT IDEA / 创作场景</p>
              <h2 id="scenes-title">
                故事有镜头，
                <br />
                <em>商品有表达。</em>
              </h2>
            </div>
            <p>
              不同的创作，同一个起点。
              <br />
              把你脑海中的画面，说给 Yingzo。
            </p>
          </div>
          <article className="lp-scene lp-film-scene">
            <figure className="lp-scene-visual" data-reveal>
              <div className="lp-image-clip">
                <img
                  src={art.cinema}
                  alt="雨夜车站里，两位人物隔着站台重逢的电影感画面"
                  width="1536"
                  height="1024"
                  loading="lazy"
                  decoding="async"
                />
                <span className="lp-image-corner">01 / STORY & CINEMA</span>
              </div>
              <figcaption>
                <span>《雨夜重逢》 · 创作方向示意</span>
                <span>AI 生成画面</span>
              </figcaption>
              <div className="lp-film-note" aria-hidden="true">
                <span>SCENE 01</span>
                <p>
                  雨声里，
                  <br />
                  故事又开始。
                </p>
                <span>EXT. TRAIN STATION — NIGHT</span>
              </div>
            </figure>
            <div className="lp-scene-copy" data-reveal>
              <p className="lp-eyebrow">
                <span>01</span> 影视与故事创作
              </p>
              <h3>
                从故事，
                <br />
                走到具体的镜头。
              </h3>
              <p>
                带着一个想法、一段原稿或一份剧本开始。梳理人物，建立视觉参考，再把情绪写进分镜与画面。
              </p>
              <ol className="lp-capabilities">
                <li>
                  <span>01</span>
                  <div>
                    <strong>写作与改编</strong>
                    <p>构思、续写、修订，让故事继续往前走。</p>
                  </div>
                </li>
                <li>
                  <span>02</span>
                  <div>
                    <strong>角色与视觉资产</strong>
                    <p>整理角色、场景与道具，让后续创作有据可循。</p>
                  </div>
                </li>
                <li>
                  <span>03</span>
                  <div>
                    <strong>分镜与视频</strong>
                    <p>从景别、机位到动作，把镜头想清楚，再做出来。</p>
                  </div>
                </li>
              </ol>
              <details className="lp-prompt">
                <summary>
                  试着这样开始 <span>+</span>
                </summary>
                <blockquote>
                  “把这段小说改成两个人在雨夜车站重逢的短剧。先给我剧本和分镜，保留克制的对白。”
                </blockquote>
              </details>
              <p className="lp-scene-footnote">
                也支持独立小说、短篇与故事创作，从你需要的一步开始。
              </p>
            </div>
          </article>
          <article className="lp-scene lp-commerce-scene">
            <div className="lp-scene-copy" data-reveal>
              <p className="lp-eyebrow">
                <span>02</span> 商家与品牌内容
              </p>
              <h3>
                一份商品资料，
                <br />
                长出更多好内容。
              </h3>
              <p>
                上传商品图片，讲清卖点与受众。从主图、场景图到海报和宣传视频，围绕同一份资料，继续尝试新的表达。
              </p>
              <ol className="lp-capabilities">
                <li>
                  <span>01</span>
                  <div>
                    <strong>资料复用</strong>
                    <p>保存商品资料与模特参考，减少反复说明。</p>
                  </div>
                </li>
                <li>
                  <span>02</span>
                  <div>
                    <strong>多种内容</strong>
                    <p>图片、页面版式、文案与视频，按目标展开。</p>
                  </div>
                </li>
                <li>
                  <span>03</span>
                  <div>
                    <strong>成套交付</strong>
                    <p>选择采用版本，导出单个文件或整套素材包。</p>
                  </div>
                </li>
              </ol>
              <details className="lp-prompt">
                <summary>
                  试着这样开始 <span>+</span>
                </summary>
                <blockquote>
                  “根据这款保温杯的资料，制作三张通勤场景图和一张卖点海报。使用现有商品图，保持杯型和已确认的产品信息。”
                </blockquote>
              </details>
            </div>
            <figure className="lp-scene-visual" data-reveal>
              <div className="lp-image-clip">
                <img
                  src={art.commerce}
                  alt="暖色日光中，置于石材展台上的米白色保温杯商品摄影示意"
                  width="1536"
                  height="1024"
                  loading="lazy"
                  decoding="async"
                />
                <span className="lp-image-corner">02 / BRANDS & COMMERCE</span>
                <div className="lp-commerce-type" aria-hidden="true">
                  Everyday,
                  <br />
                  <em>reimagined.</em>
                </div>
              </div>
              <figcaption>
                <span>一只杯子的日常 · 创作方向示意</span>
                <span>AI 生成画面</span>
              </figcaption>
            </figure>
          </article>
          <p className="lp-art-disclosure">
            画面为 AI 生成的创意示意，用于展示创作方向。
          </p>
        </section>
        <Workflow />
        <CreativeControl />
        <section
          className="lp-faq lp-wrap"
          id="faq"
          aria-labelledby="faq-title"
        >
          <div data-reveal>
            <p className="lp-eyebrow">A LITTLE MORE TO KNOW / 常见问题</p>
            <h2 id="faq-title">
              开始之前，
              <br />
              你可能想知道。
            </h2>
            <p className="lp-body-copy">
              关于创作、素材，
              <br />
              以及你的第一步。
            </p>
          </div>
          <div className="lp-faq-list" data-reveal>
            {faqs.map(([question, answer, action], index) => (
              <details key={question} name="yingzo-faq">
                <summary>
                  <span className="lp-faq-index">0{index + 1}</span>
                  <span>{question}</span>
                  <span className="lp-faq-plus">+</span>
                </summary>
                <div className="lp-faq-answer">
                  <p>{answer}</p>
                  {action === 'usage' && (
                    <a
                      className="lp-text-link"
                      {...routeLink(user ? 'usage' : 'login')}
                    >
                      {user ? '查看使用记录' : '登录账户查看'} <Arrow />
                    </a>
                  )}
                  {action === 'download' && (
                    <a className="lp-text-link" {...routeLink('download')}>
                      查看安装包 <Arrow />
                    </a>
                  )}
                </div>
              </details>
            ))}
          </div>
        </section>
        <section
          className="lp-final"
          id="download"
          aria-labelledby="download-title"
        >
          <div className="lp-final-inner" data-reveal>
            <p className="lp-eyebrow">EVERY CREATION STARTS WITH AN IDEA</p>
            <h2 id="download-title">
              带上一个想法，
              <br />
              开始你的<em>下一件作品。</em>
            </h2>
            <p>
              一段故事、一张商品图，或一份还没完成的稿子。
              <br />
              都可以成为起点。
            </p>
            <a className="lp-button" {...routeLink('download')}>
              下载 Yingzo <Arrow down />
            </a>
            <span className="lp-final-note">
              安装桌面端并配置模型连接后，即可开始创作。
            </span>
            <a
              className="lp-text-link lp-service-link"
              {...routeLink(user ? 'keys' : 'register')}
            >
              {user ? '管理 API Key' : '获取模型服务 API Key'} <Arrow />
            </a>
          </div>
          <div className="lp-final-wordmark" aria-hidden="true">
            Yingzo
          </div>
        </section>
      </main>
      <footer className="lp-footer">
        <div>
          <span className="lp-footer-brand">
            Yingzo<span>影作</span>
          </span>
          <p>让想法自然生长。</p>
        </div>
        <div className="lp-footer-links">
          <a {...routeLink('download')}>客户端下载</a>
          <a {...routeLink(user ? 'keys' : 'login')}>账户控制台</a>
          <a href="#faq">常见问题</a>
          <a href="#">回到顶部 ↑</a>
        </div>
        <span className="lp-copyright">
          © {new Date().getFullYear()} Yingzo
        </span>
      </footer>
    </div>
  )
}
