# Yingzo Midjourney v8.2 下游对接指南（Discord）

网关通过专用 Node.js 服务与 Discord 中的 Midjourney 交互。下游只使用 Yingzo API Key，不接触 Discord Token、频道、消息 ID 或按钮 custom_id。

这是替换 APIMart 的新接口契约，不提供 APIMart Midjourney 兼容。其他 APIMart 模型（如 Seedream、Suno）不受影响。

## 1. 部署和账号池

需要 Node.js 24 或对应容器。主网关仍是 Go；Discord 服务常驻以保持 WebSocket 连接。

### 一键脚本 / systemd 部署（Linux）

使用 `deploy/install.sh` 安装网关后，用新版安装脚本单独启用 MJ 伴随服务：

```bash
curl -fsSL https://raw.githubusercontent.com/Zluo003/yingzo-api/main/deploy/install.sh | sudo bash -s -- install-mj
```

也可以从新版源码仓库或新版安装包中取得配套脚本。对于已经通过旧安装脚本部署的 `v0.3.68`，无需重装 Go 网关：

```bash
sudo bash deploy/install-midjourney.sh --version v0.3.68
```

以上路径是源码仓库中的路径；从 Release 解压安装包后也有 `deploy/` 目录。只复制了配套脚本时，运行 `sudo bash ./install-midjourney.sh --version v0.3.68` 即可。脚本未指定 `--version` 时会读取当前网关版本，下载并校验同一 Release 中的 MJ 源码。

脚本自动安装所需 Node.js 24 运行时，以 `yingzo-api` 服务用户安装锁定依赖，创建并启用 `yingzo-midjourney-discord.service`，仅监听本机回环地址。首次默认使用 `18081`，被占用时从 `18082–18100` 中选择可用端口；重复安装和升级保留已配置的端口。`8081` 专用于客户端升级服务，MJ 不探测、不使用该端口；旧 MJ 配置若使用 `8081`，重跑新版脚本会迁移到新端口并同步网关地址。共享密钥自动生成到 root 专用文件 `/etc/yingzo-api/midjourney.env`，网关的 systemd drop-in 与 MJ 服务引用同一个文件；重复安装保留密钥与 `/opt/yingzo-api/data/midjourney-discord/` 中的任务数据。健康检查通过后，才重启原本正在运行的网关。

也可显式指定空闲端口，地址会自动同步到网关，无需手动编辑 systemd 或密钥文件：

```bash
curl -fsSL https://raw.githubusercontent.com/Zluo003/yingzo-api/main/deploy/install.sh | sudo env MJ_BRIDGE_PORT=18081 bash -s -- install-mj
```

```bash
sudo systemctl status yingzo-midjourney-discord
sudo journalctl -u yingzo-midjourney-discord -n 50
```

随后在管理后台对 MJ 账号执行连接测试。旧版 `v0.3.68` 主安装脚本仅安装 Go 网关，不会自动安装 MJ 服务；后台填写 Discord Token 无法替代这一步。新版主安装脚本会在升级时同步更新已经启用的 MJ 服务，普通网关安装无需 Node.js。

### Docker Compose 部署

在部署使用的 `.env` 中添加随机共享密钥（至少 32 字符）：

```dotenv
MIDJOURNEY_BRIDGE_SECRET=替换为至少32字符的随机密钥
MIDJOURNEY_BRIDGE_VERSION=0.3.69
```

从仓库根目录启动标准 Compose 和 Discord 服务叠加配置：

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yml \
  -f deploy/docker-compose.midjourney.yml up -d --build
```

Release 同时提供 `ghcr.io/zluo003/yingzo-midjourney-discord:0.3.69` 镜像（Linux amd64 / arm64）。使用已发布镜像时，将上面的 `up -d --build` 改为 `up -d --no-build`，可先运行相同 Compose 配置的 `pull`；只编译源码时保留 `--build`。安装包包含 Discord 服务源码、锁文件和本指南。

桥接端口不对公网发布。其 SQLite 卷需要持久化；当前桥接服务部署一个实例，维护多个 Discord 用户会话。不要启动多个实例共享同一 SQLite 文件。网关可多实例部署，账号并发由现有 Redis 分布式并发锁约束。

### 手动启动（开发或自定义进程管理）

```bash
cd midjourney-discord
npm ci
MIDJOURNEY_BRIDGE_SECRET='至少32字符的随机密钥' \
MIDJOURNEY_DATA_FILE='/var/lib/yingzo-mj/tasks.sqlite' \
HOST=127.0.0.1 PORT=18081 npm start
```

Go 网关进程配置相同密钥，并指定服务地址：

```dotenv
MIDJOURNEY_BRIDGE_URL=http://127.0.0.1:18081
MIDJOURNEY_BRIDGE_SECRET=与桥接服务一致的密钥
```

跨主机连接需使用受保护的内网或 HTTPS，桥接请求携带 Discord 用户令牌。

### 账号池

管理后台 → 账号管理 → **添加 MJ 账号**：

- 名称：用于运营区分每个 Discord 用户。
- Discord User Token：用户会话令牌，不是 Bot Token。账号需要有效 Midjourney 订阅且能在目标频道正常操作。
- Discord Server ID / Channel ID：已加入 Midjourney Bot 的服务器与频道。
- 并发：1–100；应以该订阅允许的并发为准，建议初始设 1。
- 优先级：数值小的优先，同优先级优先分配更久未使用的账号。
- 代理：作用于该账号的 Discord HTTP 与 WebSocket 连接。
- 绑定分组：OpenAI 分组或 Yingzo Agent 分组。

每个 Discord 用户添加一条独立账号即可形成账号池。新任务分配可用账号并持有并发槽；选图、变体、高清放大固定使用父任务的账号、服务器、频道。父账号停用、凭据失效或频道变更时，不会跨账号重试后续操作。

Token 保存后不回显；编辑时留空保留原 Token。连接测试只验证会话和频道，不生成付费图片。

迁移 `271_midjourney_discord.sql` 会禁用旧 `apimart_midjourney` 账号。请在专用面板中录入真实 Discord 配置、恢复账号状态和调度开关，或新建账号。历史任务与用量保留，但 APIMart 历史任务不接受 Discord 后续操作。

## 2. 开启异步存储与计费

在文件存储设置中启用异步生成图片，并配置生成结果存储。所有 MJ 请求走持久化图片任务：接收时预扣费、成功时结算、失败时退款；余额不足不会透支。

Yingzo Agent → 图片计费 → `midjourney-v8.2`，配置：

| 操作价格键 | 含义 |
| --- | --- |
| `generation` | 文生图、普通参考图、Edit Model、风格参考、变体，每次任务一个价格 |
| `select` | 从四宫格 U1–U4 选出一张原尺寸单图 |
| `upscale` | 对已选单图执行 Subtle / Creative 高清放大 |

四宫格生成按一次计费，不按四张累加。未配置或未启用的操作不可调用；启用且单价 0 表示免费。选图通常可以设置为 0，但必须显式启用。

普通分组 / 渠道使用对应模型键：

```text
midjourney-v8.2:generation
midjourney-v8.2:select
midjourney-v8.2:upscale
```

升级前的 `upscale` 价格原本代表 U1–U4，因此迁移会将其改名为 `select` 并保留价格及启用状态。新的高清 `upscale` 不自动定价，需管理员配置。不会改写历史任务冻结价格或历史用量。

当前固定 V8.2、Fast、标准尺寸生成。HD 由独立的高清放大操作提供。不开放 Relax、Turbo、批量 repeat、原生自定义参数、任意 Discord 按钮或版本切换。

## 3. 模型发现和报价

`GET /v1/models` 中模型 ID 为 `midjourney-v8.2`，接口标识为 `midjourney.generations`。它不接收普通 OpenAI `/v1/images/generations` / `/v1/images/edits` 协议。

`GET /api/v1/agent/pricing` 中筛选 `model=midjourney-v8.2`、`media_type=image`、`platform=openai`。操作键存储在 `resolution` 字段，计费单位为 `request`，此处的 resolution 不是像素分辨率。

调用 `POST /api/v1/agent/generation/estimates` 前请按网关现有报价接口确认实际路由；报价请求的 `request` 与后续提交的 JSON 保持一致。MJ 的后续操作报价必须在 `request` 中包含 `operation`，取值 `select`、`upscale`、`variations`，以消除同一个父任务的操作歧义。

## 4. 所有请求共有规则

请求头：

```http
Authorization: Bearer <YINGZO_API_KEY>
Content-Type: application/json
Idempotency-Key: <为本次操作生成并持久保存的唯一标识>
```

每次用户主动操作使用一个新幂等键；网络重试重复使用同一键和相同 JSON。同一幂等键改请求内容返回 409。不要因为超时就生成新键重复付费提交。

可选 `model` 固定 `midjourney-v8.2`，可选 `version` 固定 `8.2`，可选 `speed` 固定 `fast`。若提供 `operation`，必须与端点对应。

生成类参数：

| 参数 | 约束 |
| --- | --- |
| `prompt` | 必填，去首尾空白后最多 1023 个 Unicode 字符，不包含 URL 或原生 `--参数` |
| `size` | 可选宽高比，如 `16:9`；不是 1K/2K/4K |
| `image_urls` | 最多四张公共 HTTPS 图片；Edit Model 必须提供 1–4 张 |
| `iw` | 普通参考图权重 0–3；需要 image_urls，不用于 edits |
| `sref` | 公共 HTTPS 风格参考图 URL |
| `sw` | 风格权重 0–1000，需要 sref |
| `seed` | 0–4294967295 整数 |
| `negative_prompt` | 负向提示词纯文本 |
| `stylize` / `chaos` / `weird` | 整数，分别 0–1000 / 0–100 / 0–3000 |
| `raw` / `style` | raw 布尔值，或 style=`raw` |
| `tile` | 平铺布尔值，edits 不支持 |
| `quality` | 仅接受省略或 `"1"`；省略时使用本接入的标准生成设置 |

图片必须能被 Discord/Midjourney 服务器访问，不能要求 Yingzo 登录 Cookie 或 Authorization。推荐带正确扩展名的 PNG/JPEG/WebP 图片，避免即将过期的签名 URL。最终编译后的 Discord 指令（包括图片 URL）不得超过 4000 字符。

## 5. 生成、参考图和编辑

文生图：

```http
POST /v1/midjourney/generations
```

```json
{"prompt":"一只坐在窗边的橘猫","size":"3:2","raw":true}
```

`/v1/midjourney/generations/imagine` 为同操作别名。

普通参考图（参考内容、构图和色彩，不承诺精确修改）：

```json
{"prompt":"保留构图，改成水彩画","image_urls":["https://example.com/cat.png"],"iw":1.5}
```

风格参考：

```json
{"prompt":"森林里的木屋","sref":"https://example.com/style.png","sw":200}
```

指令编辑 / 多图组合：

```http
POST /v1/midjourney/generations/edits
```

```json
{"prompt":"保持人物和服装，换成雪山背景","image_urls":["https://example.com/person.png"],"size":"2:3"}
```

网关将编辑映射为 Discord V8.2 `--edit`。这不是 Blend，也不是蒙版局部编辑。当前不接收 mask、坐标、画布或分层参数。

## 6. 接收与轮询

所有提交成功均返回 `202 Accepted`：

```json
{
  "id":"imgtask_example",
  "task_id":"imgtask_example",
  "status":"processing",
  "phase":"queued",
  "billing_status":"precharged",
  "refund_status":"none",
  "poll_url":"/v1/images/tasks/imgtask_example",
  "expires_at":1800000000,
  "deadline_at":1799900000
}
```

时间字段以实际响应为准。按 `Retry-After` 或约 3 秒间隔请求 `GET /v1/images/tasks/{task_id}`。任务只允许创建它的同一用户、同一 API Key 读取和引用。

完成时查看任务 `result`：

```json
{
  "model":"midjourney-v8.2",
  "version":"8.2",
  "action":"imagine",
  "speed":"fast",
  "available_actions":[{"operation":"select","index":1},{"operation":"select","index":2}],
  "data":[{"url":"https://your-gateway.example/media/...","kind":"grid","width":2048,"height":2048}]
}
```

此为简化例子；四宫格通常提供四个 select 动作。`available_actions` 只包含 Discord 实际返回的受支持按钮，客户端应据此显示操作，同时检查该操作是否已配置价格。不要固定假设所有任务都有四张图或都可放大。

结果图片会由网关下载并保存，返回网关存储地址；Discord 原始 ID、Token 和 custom_id 不对下游暴露。图片地址、父任务和后续操作均受任务过期时间约束。

## 7. 选出单图（对应 npm Upscale / Discord U1–U4）

```http
POST /v1/midjourney/generations/select
```

```json
{"operation":"select","task_id":"imgtask_grid","index":2}
```

父任务必须已完成、未过期、属于同一用户和 Key，且提供相应 U 按钮。此操作只分离单图，不提高分辨率，计费键 `select`。

## 8. 高清放大

先完成选图，再引用选图任务：

```http
POST /v1/midjourney/generations/upscale
```

```json
{"operation":"upscale","task_id":"imgtask_selected","mode":"subtle"}
```

`mode` 为 `subtle`（默认）或 `creative`，分别对应返回按钮 Upscale (Subtle) / Upscale (Creative)。不能传 index；若父任务仍为四宫格，先调用 select。

父任务必须提供相应按钮；HD 结果不保证支持再次放大。此操作计费键 `upscale`，不是原 APIMart 的 U1–U4 选图接口。

## 9. 变体

```http
POST /v1/midjourney/generations/variations
```

```json
{"operation":"variations","task_id":"imgtask_selected","mode":"strong"}
```

`mode` 为 `subtle`（默认）或 `strong`。使用父任务实际返回的 Vary 按钮，沿用原提示词，不支持自定义 Remix 提示词。结果可能重新成为四宫格，后续操作以 available_actions 为准。计费键 `generation`。

## 10. 失败、退款与运行限制

- 400：参数、父任务形态、按钮不可用或父账号频道发生变化；不会自动改用其它操作。
- 401 / 403：Key、模型权限、账户或分组不可用。
- 404：任务不存在、无权限读取，或未找到父任务。
- 409：幂等键冲突。
- 503：异步存储、操作价格、账号池或 Discord 桥接服务不可用。
- 已接收任务失败时由任务系统退款；客户端以 billing_status / refund_status 和终态为准。

桥接任务 ID、摘要、结果和消息引用持久化到 SQLite，不存 Discord Token。任务遇到未知提交结果、桥接重启或超时时不自动重复提交付费指令；未知结果可能已消耗上游 GPU 时间，但未交付任务仍按网关失败退款策略处理。Go 网关工作进程中断时亦沿用现有异步任务补偿流程，当前不承诺恢复尚未保存的 Discord 运行中任务。

账号执行失败后暂时停止调度 5 分钟，检查会话、订阅、频道和人工验证状态。SDK 的自动人机挑战处理和自动确认审核消息已禁用，需要人工在 Discord 处理。

订阅允许并发、消息格式、模型按钮和用户会话可能变化。本接入没有官方可用性保证。Discord 与 Midjourney 对用户账号自动化均有使用限制，部署者需确认自己的授权范围。

## 11. 对接改动清单

1. 模型和任务查询地址不变；彻底停止向 APIMart 提交 MJ 任务。
2. 原 `/upscale + index` 改为 `/select + index`。
3. 真正高清放大改为 `/upscale + mode`，父任务为单图任务。
4. 新增 `/variations + mode`。
5. 根据 `available_actions` 展示可用操作，不能自行拼接 Discord custom_id。
6. 报价请求显式提供 operation，并分别读取 generation / select / upscale 的价格。
7. 原 APIMart task_id 不可用于新 Discord 账号的后续操作。

官方能力参考：[Edit Model](https://docs.midjourney.com/hc/en-us/articles/48495453462797-Edit-Model)、[Upscalers](https://docs.midjourney.com/hc/en-us/articles/32804058614669-Upscalers)、[Image Prompts](https://docs.midjourney.com/hc/en-us/articles/32040250122381-Image-Prompts)。
