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
- **注册与访问风控**：自研图形验证码（登录 / 注册 / 找回密码 / 发送邮箱验证码，与 Turnstile、腾讯、阿里云验证码四选一）；按来源 IP 限制最近 24 小时与累计注册账号数（IPv6 按 /64 计）；记录注册 IP，管理员可在用户列表一键封禁 IP / 网段（拦截网站、面板 API 与网关的全部访问）。
- **用户与配额**：余额 / 订阅两种计费模式、日/周/月限额、用户×分组专属倍率与 RPM 覆盖、平台级配额、风控与内容审核、审计日志。
- **运营**：兑换码、优惠码、公告、邀请返利、支付渠道对接。

### Yingzo Agent（系统内置聚合分组）

一个凭证调用全部已配置的模型：把账号归属到该系统分组后，模型目录会从这些账号的 `model_mapping` 自动发现，管理员再逐个模型配置启用与价格。

- **唯一且不可删除**：数据库中最多一条存活 agent 分组（唯一索引兜底），管理端不能新建、复制或删除。
- **覆盖全部平台**：入口按请求模型解析所属平台（协议默认平台只作兜底），因此同一个 key 既能调 `deepseek-v4-pro`，也能调 `seedance-2.5`。
- **逐模型计价**：文本 = 源分组渠道价 × 该模型倍率；图片 = 每张单价（1K/2K/4K）；视频 = 每秒单价（按分辨率）；Suno 音乐 = 按操作与版本设置每次价格，Max 为对应价格的 2 倍。
- **音乐生成接口**：`POST /v1/music/generations` 受理 Suno V6 音乐；POST /v1/music/generations/{operation} 提供续写、翻唱、编辑、分轨、歌词、Persona、自定义模型等全部操作，`GET /v1/music/tasks/{task_id}` 查询，支持按幂等键恢复、生成文件转存及失败退款，见 [Suno 对接指南](docs/SUNO_AGENT_INTEGRATION.md)。
- **缺价不放行**：源渠道价或倍率缺失时在转发上游之前失败，避免收不到钱还付成本。

### 视频与素材

- **异步视频生成**：Seedance 2.0 / 2.0-fast / 2.5（aigod、newtoken 上游），任务落库、轮询、失败退费。
- **产物本地落地**：上游返回的结果 URL 由网关回捞到自有存储后再返回下游地址，上游链接过期不影响交付。
- **参考素材托管**：下游用公网 URL、base64 data URI 或 multipart 上传的参考图/视频/音频，统一落到本地并换成平台公网地址；**参考视频时长由平台自己探测**（ffprobe），下游传的时长一律忽略。
- **素材存储管理**：本地目录（宿主机 bind mount）或 S3；参考素材与生成产物各自独立配额，支持保留时长、容量上限、冗余水位（超水位先驱逐最旧的未租用素材）、按凭证的日配额。

### 管理面板

- 仪表盘 / 用户 / 分组 / 账号 / 渠道与渠道定价 / 渠道监控 / 用量与运维 / 风控与提示词审计 / 公告 / 兑换码 / 优惠码 / 邀请 / 订阅 / 支付 / 备份 / 插件 / 文件服务 / **Yingzo Agent**。
- 新版用户前端支持中文、英文、越南文，通过地球图标切换并记住语言偏好；语言包可继续扩展，见 [多语言接入说明](yingzo-web/README.md#多语言)。原管理后台支持中英双语。
- 支持简易模式（`RUN_MODE=simple`）与标准模式。

---

## 技术栈与目录结构

| 层 | 选型 |
| --- | --- |
| 后端 | Go + gin + ent + google/wire，单二进制内嵌前端产物 |
| 前端 | Vue 3 + TypeScript + Vite + Tailwind + Pinia + vue-i18n |
| 存储 | PostgreSQL（业务数据）+ Redis（缓存 / 调度 / 队列） |
| 发布 | goreleaser（Linux amd64/arm64 归档 + checksums）、Linux Docker 镜像 |

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

发布产物支持 **Linux amd64 / arm64**，主程序已内嵌用户前端和管理后台，部署时无需编译 Go 或前端。

| 方式 | 适用场景 | 数据库与进程管理 |
| --- | --- | --- |
| [一键脚本](#方式一linux-一键脚本与-systemd) | 在服务器上直接安装，由 systemd 管理 | 自行准备 PostgreSQL、Redis；脚本安装网关服务 |
| [Docker Compose](#方式二docker-compose) | 希望一并启动网关、数据库和缓存 | 标准 Compose 包含 PostgreSQL、Redis，使用持久化卷 |
| [手动运行二进制](#方式三手动运行二进制) | 已有自己的进程管理或部署工具 | 自行准备依赖、配置和进程管理 |

### 方式一：Linux 一键脚本与 systemd

#### 1. 准备服务器

需要 root / sudo 权限、Bash 4+ 和 systemd。服务器应能访问 GitHub Release；启用 MJ 时还需要访问 Node.js 和 npm 下载源，以及 Discord。

**安装脚本不安装 PostgreSQL 和 Redis。** 请提前准备好这两个服务，为网关创建独立的 PostgreSQL 用户、数据库和密码，并确认服务器可以连接。可使用本机数据库，也可连接已有数据库服务器。

例如，Debian / Ubuntu 可先安装依赖及本机数据库：

```bash
sudo apt-get update
sudo apt-get install -y curl ca-certificates tar coreutils openssl xz-utils util-linux postgresql redis-server
sudo systemctl enable --now postgresql redis-server

# 仅在首次创建时执行，按提示设置数据库密码
sudo -u postgres createuser --pwprompt yingzo
sudo -u postgres createdb --owner=yingzo yingzo
```

#### 2. 安装网关并完成初始化

```bash
curl -fsSL https://raw.githubusercontent.com/Zluo003/yingzo-api/main/deploy/install.sh | sudo bash -s -- install
```

按提示选择语言、监听地址和端口。默认端口为 `8080`；配置了本机反向代理时可选择仅监听 `127.0.0.1`。脚本将程序安装到 `/opt/yingzo-api`，创建 `yingzo-api` 服务用户，并配置 systemd 服务及开机自启。

首次访问 `http://服务器地址:8080/admin`，按安装向导填写 PostgreSQL、Redis 和管理员账号。上例数据库名、用户名均为 `yingzo`，密码使用创建时设置的值；本机 Redis 默认地址为 `127.0.0.1:6379`。安装完成后进入管理后台。

如果安装时选择了其他端口，请替换地址中的 `8080`；若仅监听 `127.0.0.1`，通过已配置的反向代理域名或 SSH 隧道打开安装向导。

| 地址 | 用途 |
| --- | --- |
| `/` | 新版用户前端 |
| `/admin` | 管理后台 |
| `/admin/setup` | 首次安装向导；尚未初始化时访问 `/`、`/admin` 或 `/setup` 也会进入向导 |

默认服务使用 HTTP。域名的 HTTPS 需要另外配置反向代理，见下方[反向代理与端口](#反向代理与端口)。

#### 3. 需要 Midjourney 时启用配套服务

在已经装好网关的同一台服务器执行：

```bash
curl -fsSL https://raw.githubusercontent.com/Zluo003/yingzo-api/main/deploy/install.sh | sudo bash -s -- install-mj
```

**执行此命令就会自动配置 systemd 和共享密钥，无需手工编辑服务文件。** 脚本会下载与当前网关版本对应的 MJ 源码、准备 Node.js 24、安装依赖，创建 `yingzo-midjourney-discord.service`，并让网关与 MJ 服务读取同一份密钥。MJ 健康检查通过后，才重启原本正在运行的网关。

MJ 默认监听 `127.0.0.1:18081`；被占用时从 `18082–18100` 选择空闲端口。重复执行会保留已配置的端口、密钥和任务数据。**`8081` 留给客户端升级服务，MJ 不探测、不使用该端口。**

完成后，在管理后台「账号管理 → 添加 MJ 账号」填写 Discord Token、Server ID、Channel ID、分组及并发，再执行连接测试。生成图片还需配置异步图片存储和 MJ 操作价格，详见 [Midjourney 部署与对接指南](docs/MIDJOURNEY_AGENT_INTEGRATION.md)。

以后在其他服务器全新部署，也按上述步骤安装网关、完成初始化，再按需执行一次 `install-mj`；原安装脚本继续使用。

#### 4. 服务管理与文件位置

```bash
# 网关状态、日志与重启
sudo systemctl status yingzo-api
sudo journalctl -u yingzo-api -n 80 --no-pager
sudo systemctl restart yingzo-api

# 已启用 MJ 时，检查配套服务
sudo systemctl status yingzo-midjourney-discord
sudo journalctl -u yingzo-midjourney-discord -n 80 --no-pager
```

| 文件 / 目录 | 用途 |
| --- | --- |
| `/opt/yingzo-api/yingzo-api` | 网关主程序 |
| `/opt/yingzo-api/config.yaml` | 默认脚本部署下，安装向导生成的配置；自定义 `DATA_DIR` 时以实际目录为准 |
| `/etc/systemd/system/yingzo-api.service` | 网关服务 |
| `/etc/systemd/system/yingzo-midjourney-discord.service` | MJ 配套服务 |
| `/etc/systemd/system/yingzo-api.service.d/midjourney.conf` | 为网关注入 MJ 地址、共享密钥及服务依赖 |
| `/etc/yingzo-api/midjourney.env` | 自动生成的 MJ 共享配置，仅 root 可读；无需在后台再次填写 |
| `/opt/yingzo-api/data/midjourney-discord/` | MJ 任务状态数据库，与可替换的服务代码分开保存 |

发行程序内嵌对应平台的 `ffprobe`，首次启动自动释放并校验，用于参考视频和音频探测；无需额外安装系统包。Docker 镜像也包含 `ffmpeg`。

### 方式二：Docker Compose

需要 Git、Docker Engine 和 Docker Compose 插件。从准备存放项目的目录执行；已经下载仓库时直接进入其中的 `deploy/` 目录：

```bash
git clone https://github.com/Zluo003/yingzo-api.git
cd yingzo-api/deploy
cp .env.example .env
chmod 600 .env
```

编辑 `.env`，至少设置：

| 配置项 | 填写方式 |
| --- | --- |
| `POSTGRES_PASSWORD` | 独立的强密码，替换模板占位值 |
| `JWT_SECRET` | 固定随机密钥，可用 `openssl rand -hex 32` 生成并保存 |
| `ADMIN_EMAIL` | 首次创建的管理员邮箱 |
| `ADMIN_PASSWORD` | 首次管理员密码；留空则自动生成并打印到首次启动日志 |
| `BIND_HOST` / `SERVER_PORT` | 宿主机监听地址与端口；默认 `0.0.0.0:8080` |

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs -f sub2api
```

标准 Compose 会启动网关、PostgreSQL 和 Redis，首次运行按 `.env` 自动初始化，无需再走安装向导。访问 `http://服务器地址:8080/admin` 登录后台，根路径 `/` 为用户前端。

PostgreSQL、Redis 和网关配置保存在命名卷中，素材保存在配置的宿主机目录。日常更新保留这些数据卷；迁移服务器时需要备份、恢复数据库及对应的配置和文件。Compose 里的服务名仍为 `sub2api`，日志命令使用该名称。

其他 Compose 文件按需选择：

| 文件 | 用途 |
| --- | --- |
| [docker-compose.yml](deploy/docker-compose.yml) | 标准部署，包含网关、PostgreSQL、Redis |
| [docker-compose.standalone.yml](deploy/docker-compose.standalone.yml) | 只运行网关，连接已有 PostgreSQL、Redis；需另外填写 `DATABASE_*`、`REDIS_HOST` 等连接参数 |
| [docker-compose.local.yml](deploy/docker-compose.local.yml) | 将网关、数据库、缓存数据放到本地目录，便于统一备份和迁移 |
| [docker-compose.dev.yml](deploy/docker-compose.dev.yml) | 从本地源码构建的开发环境 |

**Compose 启用 MJ：** 在 `deploy/.env` 中额外设置 `MIDJOURNEY_BRIDGE_SECRET`（至少 32 字符的随机密钥）和 `MIDJOURNEY_BRIDGE_VERSION=0.3.71`。然后从 `deploy/` 目录执行：

```bash
docker compose -f docker-compose.yml -f docker-compose.midjourney.yml pull
docker compose -f docker-compose.yml -f docker-compose.midjourney.yml up -d --no-build
```

该配置同时向网关和 MJ 服务注入相同密钥；MJ 在容器网络内使用 `18081`，不发布到宿主机公网。后续查看状态、升级或重建时继续带上这两个 `-f` 参数。Compose 部署无需执行裸机的 `install-mj` 脚本。

### 方式三：手动运行二进制

准备 PostgreSQL 和 Redis，从 [Releases](https://github.com/Zluo003/yingzo-api/releases) 下载 `yingzo-api_<版本>_linux_<架构>.tar.gz`（`amd64` 或 `arm64`），对照同一 Release 的 `checksums.txt` 校验 SHA-256 后解压。在准备长期保存配置的目录中执行：

```bash
./yingzo-api --setup       # 首次交互配置数据库、Redis 和管理员
./yingzo-api               # 启动网关；默认端口 8080
```

配置保存在数据目录；默认使用当前工作目录，设置 `DATA_DIR` 后使用指定目录。由自己的 systemd、进程管理器或容器负责常驻运行及重启。已有配置时可用 `./yingzo-api --migrate` 单独执行数据库迁移。

### 反向代理与端口

| 默认端口 | 用途 | 访问范围 |
| --- | --- | --- |
| `8080` | 网关网站、管理后台与 API | 可直接访问，也可仅供反向代理转发 |
| `8081` | 独立部署的客户端升级服务 | 按客户端升级服务配置；MJ 不占用 |
| `18081` | MJ 配套服务 | 裸机仅监听 `127.0.0.1`，Compose 仅供容器网络访问 |
| `5432` / `6379` | PostgreSQL / Redis | 供网关连接；标准 Compose 不向宿主机发布 |

生产域名配置 HTTPS 后，将网站和 API 转发到网关端口。前端使用 SPA，网关包含 SSE 流式响应；Nginx 需要关闭 `proxy_buffering`、放宽 `proxy_read_timeout`，并正确处理 WebSocket 的 `Upgrade` / `Connection` 头。Caddy 示例见 [deploy/Caddyfile](deploy/Caddyfile)。

使用本地素材存储时，在后台「文件服务」配置可被下游和上游访问的公网地址。来源 IP 用于注册限制和封禁，CDN / 多层代理需要正确配置可信代理与转发头，见 [边缘代理安全说明](deploy/EDGE_SECURITY.md)。

---

## 关键配置

基础连接、监听和密钥配置来自 `config.yaml` 与环境变量；业务配置在管理后台设置。Compose 通过 `deploy/.env` 注入环境变量；systemd 部署使用安装向导生成的配置，需要额外环境变量时通过 systemd 服务配置。裸机程序不会自动读取仓库里的 `deploy/.env`。

| 变量 | 说明 |
| --- | --- |
| `SERVER_HOST` / `SERVER_PORT` | 监听地址与端口（默认 `0.0.0.0:8080`） |
| `RUN_MODE` | `standard`（完整）或 `simple`（简易模式，隐藏高级功能） |
| `DATABASE_HOST` / `PORT` / `USER` / `PASSWORD` / `DBNAME` / `SSLMODE` | PostgreSQL 连接 |
| `REDIS_HOST` / `PORT` / `PASSWORD` / `DB` | Redis 连接 |
| `JWT_SECRET` | 签发凭证用的固定密钥；脚本安装向导会生成并保存到配置，Compose 建议显式设置，不可随意更换 |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | 初始管理员账号（仅首次初始化使用） |
| `TOTP_ENCRYPTION_KEY` | 秘密加密密钥（TOTP、S3 备份等落库敏感信息），可选；留空时首次启动自动生成并持久化到数据库 |
| `AGENT_ASSETS_HOST_DIR` | 素材目录在宿主机上的绝对路径；容器按**同路径**挂载，面板里填容器内路径 |
| `TZ` | 时区（影响日汇总与高峰时段判定） |
| `UPDATE_GITHUB_TOKEN` | GitHub 更新检查 / 安装脚本查询 Release 时可选，减少 API 限流 |
| `MIDJOURNEY_BRIDGE_URL` / `MIDJOURNEY_BRIDGE_SECRET` | 网关连接 MJ 服务的地址和共享密钥；裸机由 `install-mj` 自动配置 |
| `DATA_DIR` | 配置与安装标记所在的数据目录；Compose 默认 `/app/data`，普通裸机默认当前工作目录 |
| `GATEWAY_*` | 网关超时、连接池、HTTP/2 回退、调度与粘性会话等细项 |

素材存储、S3、容量水位、保留时长、日配额等在面板「文件服务」页配置。

---

## 升级

升级前备份 PostgreSQL、配置和素材；已启用 MJ 时一并备份共享配置及任务数据库。数据库迁移是前向执行的，回退程序版本不会自动还原数据库。

### 脚本部署升级

已有安装使用 **`upgrade` 命令**，无需重新走初始化向导：

```bash
curl -fsSL https://raw.githubusercontent.com/Zluo003/yingzo-api/main/deploy/install.sh | sudo bash -s -- upgrade
```

脚本下载最新 Release、备份并替换主程序，再启动网关。已经启用的 MJ 服务会同步更新，保留其端口、共享密钥和任务数据；未启用 MJ 的服务器不会因此安装 Node.js。

指定版本安装或回退，例如切换到 `v0.3.71`：

```bash
curl -fsSL https://raw.githubusercontent.com/Zluo003/yingzo-api/main/deploy/install.sh | sudo bash -s -- upgrade -v v0.3.71
```

只需补装或更新当前网关对应的 MJ 服务时，执行上文的 `install-mj`；无需重装网关。

### 后台在线升级

在管理后台检查新版本、执行升级，并按提示重启服务。更新器会下载当前 Linux 架构对应的 Release 归档、校验随包的 `checksums.txt`，再替换主程序；预发布版本不会提示。脚本安装的 systemd 服务已配置自动重启，支持后台发起重启。

**在线升级只替换 Go 网关主程序，不安装 Node.js、不创建 MJ 的 systemd 服务，也不更新已有 MJ 配套服务。** 首次启用 MJ 或某次版本需要更新 MJ 服务时，仍需在服务器执行一次 `install-mj`。普通网关升级可以继续使用后台在线升级。

### Docker Compose 升级

在原来的 `deploy/` 目录执行，沿用同一个 `.env` 和 Compose 项目：

```bash
docker compose pull
docker compose up -d
```

启用了 MJ 的部署使用：

```bash
docker compose -f docker-compose.yml -f docker-compose.midjourney.yml pull
docker compose -f docker-compose.yml -f docker-compose.midjourney.yml up -d --no-build
```

若固定了镜像版本，先更新对应的镜像标签及 `.env` 中的 `MIDJOURNEY_BRIDGE_VERSION`。容器部署通过拉取镜像并重建来升级，确保重建后运行的仍是选定版本。

### 迁移与媒体运行时

网关启动时自动应用内嵌数据库迁移；也可在已有配置的目录执行 `./yingzo-api --migrate` 后退出。迁移是**前向且校验和锁定**的，详见 [数据库迁移约定](backend/migrations/CONVENTIONS.md)。

在线升级即使只替换主程序，新程序启动后也会自动补齐内嵌的 `ffprobe`，无需 root 或再次联网下载。运行时保存在程序目录的 `.runtime/ffprobe-<SHA256>/`；程序目录不可写时使用当前服务用户的缓存目录。新版本与旧版本使用各自的内容哈希目录，回滚不覆盖正在使用的探测程序。可执行 `./yingzo-api --prepare-runtime` 单独验证自动安装。

---

## 本地开发

环境要求：Go 1.27.3+、Node 24+（含 pnpm）、Docker（跑集成测试与开发栈）。

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
| [DeepSeek 统一搜索与网页读取](docs/DEEPSEEK_SEARCH_API.md) | 所有 LLM 共用、上游账号、按次计费与搜索失败跳过 |
| [MiMo V2.5 语音合成对接](docs/MIMO_SPEECH_INTEGRATION.md) | 语音账号、预置音色、音色设计、音频目录、token 计费与流式播放 |
| [Seedream 5.0 Pro / Flash 图片对接](docs/SEEDREAM_5_IMAGE_API.md) | 专用账户、分辨率与参考图计费、异步生成、透明编辑和完整图层拆分 |
| [Midjourney v8.2 客户端对接](docs/MIDJOURNEY_AGENT_INTEGRATION.md) | Discord 账号池、部署、三项计费、参考图、编辑、选图、变体与高清放大 |
| [Suno 全功能接口参考](docs/SUNO_FULL_API_REFERENCE.md) | 全部端点参数、源任务引用、Max、自定义模型、结果结构 |
| [Suno v6 音乐对接](docs/SUNO_AGENT_INTEGRATION.md) | 专用账号、操作与版本计费、异步任务、幂等恢复、结果下载与退款 |
| [backend/migrations/CONVENTIONS.md](backend/migrations/CONVENTIONS.md) | 数据库迁移命名、不可变原则、`_notx.sql` 语义、误改修复流程 |
| [backend/pkg/pluginapi/PROTOCOL.md](backend/pkg/pluginapi/PROTOCOL.md) | 本地插件协议、包结构、兼容性与 UI 隔离 |
| [backend/resources/model-pricing/SOURCE.md](backend/resources/model-pricing/SOURCE.md) | 内置模型价格数据来源、镜像与手动更新方式 |

维护脚本：`go run ./cmd/cleanup-ingress-reject-logs --before <RFC3339>`（默认 dry-run，加 `--execute` 才落库）用于清理历史准入拒绝日志。

---

## 许可证

本项目基于开源项目二次开发，遵循仓库内 [LICENSE](LICENSE)（GNU LGPL v3）。
