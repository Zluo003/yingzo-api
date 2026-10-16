# 教程板块

新前端入口：`/tutorials`。管理后台菜单：**教程管理**（路由 `/admin/tutorials`，沿用后台认证、限流和审计）。教程公开可读，无需 Yingzo 登录。

在后台启用教程板块，创建一级分类，再在分类下添加二级教程标题和飞书正文地址。上下箭头调整顺序，显示开关控制发布，点击“保存配置”后生效。目录配置存于现有 settings 表的 `tutorial_config`，无需数据库迁移。默认关闭、空目录，不自动发布示例文档。

总开关为“开启教程板块”。关闭并保存后，首页、桌面侧栏和手机导航均隐藏教程入口；直接访问 `/tutorials` 返回首页，不显示目录、占位教程或加载飞书正文。已有目录与地址保留。前端进入页面、切回窗口及每分钟检查最新开关；配置加载失败时也隐藏教程入口和内容。

## 流量

- 本站 API 只返回目录、文档地址和（组件模式下）短期签名。
- 读者浏览器直接请求飞书页面、SDK、图片、视频等资源；本站不抓取、缓存、代理或托管文档正文。
- “不走服务器带宽”指正文及媒体流量。站点页面、目录 API 和鉴权仍有少量本站流量。

## 直接嵌入

选择“直接嵌入”，填写 HTTPS 的飞书 `docx`、`docs` 或 `wiki` 地址。应允许外部读者阅读，并在未登录飞书的浏览器中验证。飞书可保留自己的导航、目录、登录入口和样式；跨域隔离使本站无法修改这些界面，也无法可靠检测所有 iframe 权限错误。始终提供“打开原文”和“重新加载”。

用户提供的 `https://my.feishu.cn/docx/B8PVdk08UoGft2x1sR6cfBMLnve` 已在本地浏览器验证可作为 iframe 阅读；实际访问仍取决于飞书权限、浏览器及飞书后续策略。

## 云文档组件（融合展示）

选择“飞书云文档组件”，配置：

1. 飞书企业自建应用 App ID、App Secret。建议为教程创建独立、仅授权教程文档的应用。
2. 按飞书官方文档申请云文档访问权限，并为应用授予所需文档的阅读权限。公开分享权限不代替应用身份授权。
3. 填写实际教程网站的 origin，例如 `https://yingzo.art`，不含路径。多个站点每行一个。本地调试允许 `http://127.0.0.1:5173`。

密钥保存后不会回传给管理端或公开接口，后续留空保留已有密钥。组件模式隐藏飞书顶部栏、文档自带目录、评论和页脚，使用站点阅读背景；只读展示。组件内部字体、布局及移动端支持仍以飞书能力为准，不能保证所有元素完全自定义。

后端缓存飞书 jsapi_ticket，使用新随机数和毫秒时间戳生成一次性 SHA-1 签名；签名限定管理员批准的阅读站点及 `/tutorials` 路径。浏览器不接收应用密钥、access token 或 ticket。鉴权响应禁止缓存。

飞书官方注明应用身份访问限制为 **100 次/分钟**；大量访问前需要评估额度。未配置真实应用凭证时，只能验证 SDK 加载、参数、错误处理与页面集成，不能完成真实组件鉴权验收。

若部署环境自定义了反向代理/CDN 的 CSP，需要允许飞书文档域名的 frame-src 以及 `https://sf1-scmcdn-cn.feishucdn.com` 的 script-src。Go 服务已补齐这些来源。文档自身的媒体资源在飞书 iframe 内加载。

参考：

- [云文档组件开始使用](https://open.feishu.cn/document/common-capabilities/web-components/uYDO3YjL2gzN24iN3cjN/introduction)
- [组件 SDK 鉴权流程](https://open.feishu.cn/document/common-capabilities/web-components/component-sdk-authentication-process)
- [组件功能配置](https://open.feishu.cn/document/common-capabilities/web-components/uYDO3YjL2gzN24iN3cjN/feature-config)
