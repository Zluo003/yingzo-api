# yingzo-web

独立的 Yingzo 用户前端，服务于 `/Users/zluo/Dev/YingZo` 创作应用的账号、凭证和余额能力。后端与管理端保持不变。

## Development

```bash
npm install
npm run dev
```

默认通过相对路径调用 `/api/v1`；部署时可用 `VITE_API_BASE` 指定后端 API 前缀。

## Pages

首页、API Key 管理、使用记录、个人资料、充值、登录、注册。

管理员本地登录跳转到 `http://127.0.0.1:5174/admin`；开发时同时运行 `make dev-admin` 即可保留原管理端页面。
