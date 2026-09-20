# yingzo-api

自建 AI 模型网关 + 管理面板：把 OpenAI、Anthropic、Gemini、Grok、Kimi、智谱 GLM、DeepSeek、MiniMax、Seedance 视频等上游账号统一收口成一套 OpenAI / Anthropic / Gemini 兼容接口，配套 API Key 分发、用量计费、渠道定价、视频生成与素材托管。

后端 Go（gin + ent + wire）单二进制内嵌前端产物，前端 Vue 3 + TypeScript，数据存 Postgres，缓存与调度状态存 Redis。

---

## 目录

- [核心能力](#核心能力)
- [技术栈与目录结构](#技术栈与目录结构)
- [部署](#部署)
- [关键配置](#关键配置)
- [升级](#升级)
- [本地开发](#本地开发)
- [开发约定](#开发约定)
- [内部技术文档](#内部技术文档)
- [许可证](#许可证)

---

## 核心能力

### 上游接入与调度

- **多平台账号池**：`openai` / `anthropic` / `gemini` / `antigravity` / `grok` / `kimi` / `zhipu` / `deepseek` / `minimax` / `video`，支持 API Key、OAuth、Setup Token 等账号类型。
- **原生协议入口**：`/v1/messages`、`/v1/responses`、`/v1/chat/completions`、`/v1/embeddings`、`/v1/images/*`、`/v1beta/*`（Gemini 原生）、`/v1/videos`。
- **调度**：按分组选号、粘性会话、并发槽位与排队、失败重试与换号、模型级账号路由（`model_routing`）、模型白名单、RPM 限制、账号级倍率与优先级。
- **分组**：普通分组按平台隔离；`composite` 分组可把公开模型名路由到不同平台的具体模型。

### 计费与风控

- **价格解析链**：分组模型定价 → 渠道定价 → 内置价格表兜底，支持 token 阶梯、按次、按图片档位（1K/2K/4K）、按视频分辨率×秒计价。
- **用量入账**：按 token / 按次 / 按秒分别计算，写用量日志与日汇总，支持缓存读写细分、长上下文阶梯价、高峰时段倍率。
- **上游成本与利润保护**：账号级倍率、渠道成本、`profit_control`（按毛利率 + 安全缓冲过滤账号）、账号统计定价对比。
- **用户与配额**：余额 / 订阅两种计费模式、日/周/月限额、用户×分组专属倍率与 RPM 覆盖、平台级配额、风控与内容审核、审计日志。
- **运营**：兑换码、优惠码、公告、邀请返利、支付渠道对接。

### Yingzo Agent（系统内置聚合分组）

一个凭证调用全部已配置的模型：把账号归属到该系统分组后，模型目录会从这些账号的 `model_mapping` 自动发现，管理员再逐个模型配置启用与价格。

- **唯一且不可删除**：数据库中最多一条存活 agent 分组（唯一索引兜底），管理端不能新建、复制或删除。
- **覆盖全部平台**：入口按请求模型解析所属平台（协议默认平台只作兜底），因此同一个 key 既能调 `deepseek-v4-pro`，也能调 `seedance-2.5`。
- **逐模型计价**：文本 = 源分组渠道价 × 该模型倍率；图片 = 每张单价（1K/2K/4K）；视频 = 每秒单价（按分辨率）。
- **缺价不放行**：源渠道价或倍率缺失时在转发上游之前失败，避免收不到钱还付成本。

### 视频与素材

- **异步视频生成**：Seedance 2.0 / 2.0-fast / 2.5（aigod、newtoken 上游），任务落库、轮询、失败退费。
- **产物本地落地**：上游返回的结果 URL 由网关回捞到自有存储后再返回下游地址，上游链接过期不影响交付。
- **参考素材托管**：下游用公网 URL、base64 data URI 或 multipart 上传的参考图/视频/音频，统一落到本地并换成平台公网地址；**参考视频时长由平台自己探测**（ffprobe），下游传的时长一律忽略。
- **素材存储管理**：本地目录（宿主机 bind mount）或 S3；参考素材与生成产物各自独立配额，支持保留时长、容量上限、冗余水位（超水位先驱逐最旧的未租用素材）、按凭证的日配额。

### 管理面板

- 仪表盘 / 用户 / 分组 / 账号 / 渠道与渠道定价 / 渠道监控 / 用量与运维 / 风控与提示词审计 / 公告 / 兑换码 / 优惠码 / 邀请 / 订阅 / 支付 / 备份 / 插件 / 文件服务 / **Yingzo Agent**。
- 中英双语（`zh` / `en`），支持简易模式（`RUN_MODE=simple`）与标准模式。

---

## 技术栈与目录结构

| 层 | 选型 |
| --- | --- |
| 后端 | Go + gin + ent + google/wire，单二进制内嵌前端产物 |
| 前端 | Vue 3 + TypeScript + Vite + Tailwind + Pinia + vue-i18n |
| 存储 | PostgreSQL（业务数据）+ Redis（缓存 / 调度 / 队列） |
| 发布 | goreleaser（多平台归档 + checksums）、Docker 镜像 |

```
backend/                 Go 服务端
  cmd/server/            入口（--setup / --migrate / --version）
  internal/handler/      HTTP 层（含 admin/ 管理端）
  internal/service/      领域逻辑（网关、计费、调度、Agent、视频、素材）
  internal/repository/   持久化（ent + SQL）
  migrations/            内嵌 SQL 迁移（见 CONVENTIONS.md）
  pkg/                   可复用包（插件协议见 PROTOCOL.md）
frontend/                Vue 3 管理面板与用户界面
deploy/                  Dockerfile、compose、安装脚本、配置模板
.github/workflows/       CI 与发布流水线
```

---

## 部署

### 方式一：Docker Compose（推荐）

```bash
cd deploy
cp .env.example .env          # 至少填写 POSTGRES_PASSWORD、JWT_SECRET
docker compose up -d          # 默认镜像 ghcr.io/zluo003/yingzo-api
docker compose logs -f        # 首次启动会打印自动生成的管理员密码
```

访问 `http://<主机>:8080`，用 `ADMIN_EMAIL` / `ADMIN_PASSWORD` 登录；`.env` 未设密码时以日志里自动生成的为准。

其他 compose 文件：

| 文件 | 用途 |
| --- | --- |
| `docker-compose.yml` | 拉取发布镜像运行（生产默认） |
| `docker-compose.standalone.yml` | 单容器 + 外部数据库 |
| `docker-compose.local.yml` | 本机快速试跑 |
| `docker-compose.dev.yml` | 从本地源码构建（开发用） |

### 方式二：一键脚本（Linux）

```bash
curl -sSL https://raw.githubusercontent.com/Zluo003/yingzo-api/main/deploy/install.sh | sudo bash
```

安装到 `/opt/yingzo-api`，注册 systemd 服务 `yingzo-api.service`（`systemctl status yingzo-api`）。脚本会拉取 GitHub Release 产物，可用 `UPDATE_GITHUB_TOKEN` 提高 API 限额。

### 方式三：手动运行二进制

从 [Releases](https://github.com/Zluo003/yingzo-api/releases) 下载 `yingzo-api_<版本>_<系统>_<架构>.tar.gz`，解压后：

```bash
./yingzo-api --migrate     # 应用数据库迁移后退出
./yingzo-api --setup       # 命令行初始化管理员
./yingzo-api               # 启动服务（默认 8080）
```

### 反向代理注意

前端是 SPA + SSE 流式响应，Nginx 侧需要关闭 `proxy_buffering`、放宽 `proxy_read_timeout`，并透传 `Upgrade` / `Connection` 头（WebSocket 实时接口）。示例配置见 `deploy/Caddyfile`。

---

## 关键配置

配置来源：环境变量（`deploy/.env`）+ 面板内的系统设置。常用项：

| 变量 | 说明 |
| --- | --- |
| `SERVER_HOST` / `SERVER_PORT` | 监听地址与端口（默认 `0.0.0.0:8080`） |
| `RUN_MODE` | `standard`（完整）或 `simple`（简易模式，隐藏高级功能） |
| `DATABASE_HOST` / `PORT` / `USER` / `PASSWORD` / `DBNAME` / `SSLMODE` | PostgreSQL 连接 |
| `REDIS_HOST` / `PORT` / `PASSWORD` / `DB` | Redis 连接 |
| `JWT_SECRET` | 签发凭证用密钥，**生产必须设置且不可随意更换** |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | 初始管理员账号（仅首次初始化使用） |
| `TOTP_ENCRYPTION_KEY` | 秘密加密密钥（TOTP、S3 备份等落库敏感信息），可选；留空时首次启动自动生成并持久化到数据库 |
| `AGENT_ASSETS_HOST_DIR` | 素材目录在宿主机上的绝对路径；容器按**同路径**挂载，面板里填容器内路径 |
| `TZ` | 时区（影响日汇总与高峰时段判定） |
| `UPDATE_GITHUB_TOKEN` | 面板内检查更新用的 GitHub Token（可选，避免限流） |
| `GATEWAY_*` | 网关超时、连接池、HTTP/2 回退、调度与粘性会话等细项 |

素材存储、S3、容量水位、保留时长、日配额等在面板「文件服务」页配置。

---

## 升级

- **面板内检查**：系统设置页调用 `GET /api/v1/admin/system/check-updates` 对比 GitHub Release；升级要求产物含 `linux_amd64` 归档与 `checksums.txt`，二进制名为 `yingzo-api`。预发布版本不会提示。
- **一键脚本**：重跑 `install.sh` 覆盖 `/opt/yingzo-api` 并重启 systemd 服务。
- **Docker**：`docker compose pull && docker compose up -d`。
- **迁移**：启动时自动应用内嵌迁移；也可在升级前手动执行 `./yingzo-api --migrate`。迁移是**前向且校验和锁定**的（见下）。

---

## 本地开发

环境要求：Go 1.27+、Node 24+（含 pnpm）、Docker（跑集成测试与开发栈）。

```bash
# 后端（在 backend/ 下）
make build              # 编译
make test-unit          # go test -tags=unit ./...        ← 主要测试套件
make test-integration   # go test -tags=integration ./... （需要 Docker，自动起 PG/Redis 容器）

go vet -tags=unit ./...
$(go env GOPATH)/bin/golangci-lint run ./...   # 静态检查

# 前端（在 frontend/ 下）
pnpm install
pnpm dev                # 开发服务器
pnpm typecheck          # vue-tsc
pnpm test:run           # vitest
pnpm check:i18n         # 中英键完整性
```

**开发栈**：

```bash
cd deploy
docker compose -f docker-compose.dev.yml build && docker compose -f docker-compose.dev.yml up -d
```

---

## 开发约定

- **迁移不可回改**：`backend/migrations/*.sql` 前向执行、按内容校验和锁定，已被应用的迁移文件**不能修改**，只能新增。并发建索引用 `_notx.sql` 后缀。详见 [CONVENTIONS.md](backend/migrations/CONVENTIONS.md)。
- **测试标签**：主要套件是 `-tags=unit`；`-tags=integration` 需要 Docker（testcontainers）。改动计费/调度/迁移时优先补 unit 与 integration 两层。
- **接口兼容**：原生 `/v1/messages`、`/v1/responses`、`/v1/chat/completions`、`/v1beta/*` 是外部契约，改动不得破坏既有行为。
- **前端**：新增文案必须同时补 `zh` / `en`（`pnpm check:i18n` 会拦），路由与状态管理遵循 `frontend/src` 现有分层。
- **新增平台**：账号平台、分组平台、网关分发、计费平台集合、前端平台下拉都要同步，参考 `internal/domain/constants.go` 与 `internal/service/domain_constants.go` 的单一来源注释。

---

## 内部技术文档

| 文档 | 内容 |
| --- | --- |
| [backend/migrations/CONVENTIONS.md](backend/migrations/CONVENTIONS.md) | 数据库迁移命名、不可变原则、`_notx.sql` 语义、误改修复流程 |
| [backend/pkg/pluginapi/PROTOCOL.md](backend/pkg/pluginapi/PROTOCOL.md) | 本地插件协议、包结构、兼容性与 UI 隔离 |
| [backend/resources/model-pricing/SOURCE.md](backend/resources/model-pricing/SOURCE.md) | 内置模型价格数据来源、镜像与手动更新方式 |

维护脚本：`go run ./cmd/cleanup-ingress-reject-logs --before <RFC3339>`（默认 dry-run，加 `--execute` 才落库）用于清理历史准入拒绝日志。

---

## 许可证

本项目基于开源项目二次开发，遵循仓库内 [LICENSE](LICENSE)（GNU LGPL v3）。
