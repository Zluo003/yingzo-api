
# Yingzo 用户前端

仓库根目录的默认用户前端入口：

```bash
make dev
```

旧管理端仍可通过 `make dev-admin` 单独启动，管理页面源码未改动。

## 多语言

内置简体中文（`zh-CN`）、英语（`en`）和越南语（`vi`）。点击页面上的地球图标打开语言菜单；选择后立即切换，保留路由、表单、登录和支付状态，并将偏好存入 `localStorage.yingzo_locale`。首次访问优先采用浏览器支持的语言，均不匹配时回退到中文；同源标签页之间同步偏好。

`src/i18n/index.js` 统一管理语言注册、回退、翻译和格式化，`LocaleProvider.jsx` 提供 React 更新及自定义语言菜单。页面不依赖在线翻译服务。用户数据、管理员发布的公告/教程、模型编号和 API 请求值保留原文，切换语言不会改变人民币计价或换算货币。

### 添加文案

所有内置语言词典位于 `src/i18n/locales/`，使用中文原文作为稳定的键。同一条文案应在三个词典中同时补齐。

```jsx
import { t, message, formatNumber } from './i18n/index.js'
import { Trans } from 'react-i18next'

// 普通文案和插值；动态值必须作为参数传入。
t('登录')
t('{{value1}} 秒', { value1: 10 })

// 提示保存在状态中时保存键和值，渲染时翻译，让已显示的提示也能切换。
setError(message('请输入 Key 名称'))
// JSX: {error && <p role="alert">{t(error)}</p>}

// 富文本中通过 React 组件槽保留实时值，避免拼接或注入 HTML。
<Trans i18nKey="有效期至 <0/>" components={[<>{expiry}</>]}/>

// 日期与数字使用当前界面语言；费用仍然以人民币显示。
formatNumber(1234.5, { minimumFractionDigits: 2 })
```

词典中的 `{{value1}}` 等变量，以及 `<0/>`、`<1>…</1>` 等组件槽必须保持不变。不要翻译 CSS 类名、路由、请求字段、模型 ID 或管理员配置的数据。模块级文案需在渲染/调用时翻译，避免初始化时固定为某种语言。

### 接入其他语言

新增完整词典后，在 `src/i18n/index.js` 的注册表中加入语言代码、显示名、Intl 区域、方向和资源；菜单、偏好保存和格式化会自动支持，无需修改各页面。

可选语言包也可以在应用启动时注册：

```js
import { registerLocale } from './i18n/index.js'
import messages from './i18n/locales/fr.json'

registerLocale({
  code: 'fr',
  name: 'Français',
  intlLocale: 'fr-FR',
  direction: 'ltr',
  aliases: ['fr-FR'],
  messages,
})
```

缺失文案自动回退到中文。内置语言支持区域代码匹配，例如 `en-US` → `en`、`vi-VN` → `vi`。第三方支付组件遵循各自支持的语言；Stripe 使用所选语言，Airwallex 在非中文界面使用其支持的英文。

## 验证

在 `yingzo-web/` 目录执行：

```bash
npm run check:i18n   # 词典完整性、插值/富文本槽、语言匹配、格式化、扩展接口
npm run build
npm test
```

词典完整性检查自动覆盖注册表中的全部内置语言。部署构建继续输出 `dist/client/`、`dist/server/` 和 `dist/.openai/hosting.json`。
