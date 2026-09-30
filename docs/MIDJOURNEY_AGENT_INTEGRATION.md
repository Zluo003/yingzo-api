# Yingzo Agent Midjourney v8.2 对接指南

本次接入使用 APIMart 上游，Yingzo 对外模型名为 `midjourney-v8.2`。客户端通过 Yingzo 专用异步接口完成文生图、普通垫图、Edits 图生图、风格参考和 U1–U4 放大选图，使用现有 Yingzo API Key；上游密钥只在服务端保存。

**所有操作固定 `fast`**；`speed` 可省略，显式传其他值返回 400。文生图、普通垫图和 Edits 图生图共用一次“图片生成”价格；选图使用独立“放大”价格。文生图通常返回四宫格，Edits 可返回 1–4 张结果，一次任务均只计一次生成费用。这里的 upscale 是从四宫格中选择单图，不是 HD 2x/4x 放大。

## 管理端准备

1. 部署包含本次改动的后端与管理前端。正常启动会执行迁移 `259_midjourney_operation_pricing.sql` 和 `260_midjourney_usage_without_image_size.sql`，分别允许媒体价格使用 `request` 计费单位，以及 Midjourney 按次用量不填写像素分辨率。迁移 `261_midjourney_fast_generation_pricing.sql` 会将原 `imagine_fast` 改为 `generation`、`upscale_fast` 改为 `upscale`，保留原单价和启用状态，移除 Relax/Turbo 价格；历史任务和用量不改写。普通分组和渠道的对应计费模型键也会迁移。
2. 在文件存储设置中配置生成结果存储并开启异步图片任务。可使用系统支持的本地或对象存储；未启用时提交接口返回 `503 image_tasks_disabled`。
3. 账户管理 → 添加图片模型 → **Midjourney v8.2 · APIMart**。填写账户名、API Key、Base URL（默认 `https://api.apimart.ai`，也兼容末尾 `/v1`），绑定 Yingzo Agent 分组。可设置并发、优先级和代理。
4. 编辑该账户时会打开同一专用面板。API Key 留空保留旧值。账户测试只检查 APIMart 连接与鉴权，不产生付费图片任务。
5. Yingzo Agent → 图片计费，找到 `midjourney-v8.2`，分别设置以下两项价格和启用开关，再启用模型并保存。

| 操作维度 | 管理端含义 | 计费单位 |
| --- | --- | --- |
| `generation` | 图片生成：文生图、垫图、Edits 图生图（含风格参考） | credit / 次 |
| `upscale` | 放大选图 | credit / 次 |

未配置或未勾选的操作不可调用。显式启用且单价为 `0` 表示免费。Midjourney 不使用 1K/2K/4K 图片价格，四宫格也不按四张累加。

账户内部沿用 `platform=openai`、`type=apikey`，并保存 `extra.image_account=true`、`extra.image_provider=apimart_midjourney`。它只声明 `midjourney-v8.2`，不参与普通 OpenAI 图片或文本接口的调度。

普通 OpenAI 分组也可调用。此时需要在分组模型价格或关联渠道中，分别为下面两个模型键配置 `per_request` 价格；未配置时不会回退到通用图片价格。普通分组按既有分组/用户图片倍率结算，Agent 两项价格则是最终单价。

```text
midjourney-v8.2:generation
midjourney-v8.2:upscale
```

## 客户端模型发现和价格

请求 `GET /v1/models`，找到 `id=midjourney-v8.2`。该模型的 `interfaces` 包含 `midjourney.generations`。不要按普通 `openai.images` 模型调用 `/v1/images/generations`，图生图使用下文专用 `/v1/midjourney/generations/edits`，不是 OpenAI `/v1/images/edits`。

`capabilities` **只包含生成速度档位**。列表项示例：

```json
{
  "id": "midjourney-v8.2",
  "display_name": "midjourney-v8.2",
  "media_types": ["image"],
  "platforms": ["openai"],
  "interfaces": ["midjourney.generations"],
  "capabilities": {
    "supported_speeds": ["fast"]
  }
}
```

配置并启用 `generation` 价格时，`supported_speeds` 为 `["fast"]`，否则为空。客户端可直接显示固定 Fast，无需速度选择器；数组为空时禁用文生图和图生图。图生图和风格参考按本指南固定协议实现，不扩展能力对象。不要展示其他版本、Relax/Turbo、角色权重或 HD 放大选项。

列表顶层的模型标识、媒体类型、平台和接口标识仍用于识别和路由。版本固定 v8.2、专用提交路径、异步轮询和 upscale 固定 fast 是本文约定，不放入能力对象。选图是否已定价，通过下面的价格快照判断。

请求 `GET /api/v1/agent/pricing` 获取价格快照。筛选 `model=midjourney-v8.2`、`platform=openai`、`media_type=image` 的规则。为兼容现有价格结构，操作维度存放在 `resolution` 字段中，但它不是像素分辨率：

```json
{
  "model": "midjourney-v8.2",
  "platform": "openai",
  "media_type": "image",
  "resolution": "generation",
  "unit_kind": "request",
  "unit_price": 0.2,
  "effective_unit_price": 0.2,
  "billing_multiplier": 1
}
```

示例数值不代表默认价格。显示时使用实际 `effective_unit_price`，单位为 credit/次，遵循响应中的价格版本和有效期。

## 文生图提交

```http
POST /v1/midjourney/generations
Authorization: Bearer <YINGZO_API_KEY>
Content-Type: application/json
Idempotency-Key: <本次操作的唯一 UUID>
```

`POST /v1/midjourney/generations/imagine` 是同一操作的别名。两种路径的幂等指纹相同。

```json
{
  "model": "midjourney-v8.2",
  "prompt": "A quiet mountain lake at sunrise, watercolor illustration",
  "version": "8.2",
  "speed": "fast",
  "size": "16:9",
  "stylize": 250
}
```

| 字段 | 约束 |
| --- | --- |
| `model` | 可省略；提供时只能为 `midjourney-v8.2` |
| `version` | 可省略；提供时只能为字符串 `8.2` |
| `prompt` | 必填，去除首尾空格后 1–16000 字节 |
| `speed` | 可省略，仅 `fast` |
| `size` | 可选宽高比，如 `1:1`、`16:9`、`9:16`；不是 `1024x1024` |
| `quality` | 可选字符串 `0.25`、`0.5`、`1`、`2`，默认 `2`；放大选图不接受此字段 |
| `style` | 可选，仅 `raw` |
| `seed` | 可选整数，0–4294967295 |
| `negative_prompt` | 可选纯文本负面提示词 |
| `stylize` | 可选整数，0–1000 |
| `chaos` | 可选整数，0–100 |
| `weird` | 可选整数，0–3000 |
| `tile`、`raw` | 可选布尔值；Edits 不支持 `tile=true` |
| `image_urls` | 可选 HTTPS 图片 URL 数组，最多 4 张；Edits 必填至少 1 张 |
| `iw` | Imagine 垫图权重，0–3；需同时提供 `image_urls`，不可用于 Edits |
| `sref` | 可选单张风格参考图 HTTPS URL；Imagine 与 Edits 均支持 |
| `sw` | 可选整数 0–1000，上游默认 100；必须同时提供 `sref` |

这些结构化参数经过本地格式验证，具体组合是否被 v8.2 上游接受以任务结果为准。提示词必须为纯文本，不接受图片 URL 或原生 `--参数`；请使用对应结构化字段。服务端始终指定上游 `version=8.2`、`speed=fast`，不提供 `extra`、`repeat`、`niji`、`cref/cw`、`oref/ow`、`hd`、`draft` 或任意按钮 `custom_id` 的透传入口，也不支持 `n` 批量生成。

## 图生图与风格参考

### Edits：编辑原图、保持人物或物体

```http
POST /v1/midjourney/generations/edits
Authorization: Bearer <YINGZO_API_KEY>
Content-Type: application/json
Idempotency-Key: <新的 UUID>
```

```json
{
  "model": "midjourney-v8.2",
  "prompt": "保留图中人物的面部特征和服装，将背景改成雪山湖泊",
  "image_urls": ["https://assets.example/person.png"],
  "speed": "fast",
  "size": "16:9"
}
```

`image_urls` 提供 1–4 张参考图，通过 `prompt` 明确每张图的用途、保留的内容和修改目标。v8.2 使用 Edit Model 承接角色与物体参考，不使用旧版 `cref/cw` 或 `oref/ow`。Edits 不接受 `iw` 或 `tile=true`。也可同时传 `sref` 和 `sw`，分别指定人物/物体来源与目标画风。

### Imagine：普通垫图

向 `/v1/midjourney/generations` 提交 `prompt` + `image_urls`，可选 `iw`。此模式参考原图的内容、构图和色彩，适合基于图片重新创作；需要明确修改原图或保留角色时用 Edits。

```json
{
  "model": "midjourney-v8.2",
  "prompt": "A mountain cabin reflected in a quiet lake",
  "image_urls": ["https://assets.example/landscape.png"],
  "iw": 1.5
}
```

### 风格参考与权重

Imagine 与 Edits 均接受以下字段：

```json
{
  "model": "midjourney-v8.2",
  "prompt": "A mountain cabin reflected in a quiet lake",
  "sref": "https://assets.example/watercolor.png",
  "sw": 200
}
```

此例发送到 Imagine。`sref` 参考画风，不作为人物身份参考；`sw` 调节风格参考影响，区别于模型自身的 `stylize`。第一版下游仅支持单张 `sref` 图片，不接受风格数字码或多个 URL 拼接。

**图片输入要求：** 本接口接收 HTTPS 图片 URL，不接收本地路径、Base64 或 multipart。每个 URL 最长 8192 字节，不得包含登录信息、空白或原生参数。图片须能被 APIMart 无鉴权访问，Edits 单图上游限制为 12 MiB；客户端应在上传时检查大小和图片格式。先通过素材上传流程取得公网 URL，再提交任务。`localhost`、容器内网地址，以及需要 Yingzo Authorization 的私有资源地址不可直接作为上游参考图。签名 URL 的有效期应覆盖排队与生成时间。

客户端建议提供“文生图 / 图生图”切换、最多 4 张图生图参考图，以及独立的“风格参考图 + 权重”控件。全部显示 Fast，文生图和图生图均从 `generation` 读取价格。

## 异步受理

提交成功返回 **HTTP 202**，表示已受理并按当前操作价格预扣费，不表示图片已经生成：

```json
{
  "id": "imgtask_example",
  "task_id": "imgtask_example",
  "object": "image.generation.task",
  "status": "processing",
  "phase": "queued",
  "billing_status": "precharged",
  "refund_status": "none",
  "created_at": 1790780400,
  "expires_at": 1790866800,
  "deadline_at": 1790782200,
  "poll_url": "/v1/images/tasks/imgtask_example"
}
```

以实际响应字段为准，尤其是 `expires_at` 和 `deadline_at`。客户端无需发送 `Prefer: respond-async`；Midjourney 始终走异步链路。响应提供 `Location` 和 `Retry-After: 3`。

## 轮询与结果展示

使用相同 Yingzo API Key 请求响应给出的 `poll_url`。建议每 3–5 秒查询一次，429/5xx 时退避；不要因为任务等待较久就重新提交生成。

```http
GET /v1/images/tasks/imgtask_example
Authorization: Bearer <YINGZO_API_KEY>
```

| `status` | 客户端处理 |
| --- | --- |
| `processing` | 显示排队或处理中并继续查询 |
| `completed` | 读取 `result`，展示并按需下载图片 |
| `failed` | 显示 `error`，查看 `billing_status` / `refund_status`；退款为 pending 时仍可继续查询状态 |

正常完成的文生图结果：

```json
{
  "id": "imgtask_example",
  "task_id": "imgtask_example",
  "status": "completed",
  "billing_status": "settled",
  "refund_status": "none",
  "result": {
    "model": "midjourney-v8.2",
    "version": "8.2",
    "action": "imagine",
    "speed": "fast",
    "data": [{ "url": "https://yingzo.example/media/asset-id/asset.png", "asset_id": "asset-id", "kind": "grid" }]
  }
}
```

当 `data[0].kind=grid` 时，展示一张四宫格预览，按钮顺序为 **U1 左上、U2 右上、U3 左下、U4 右下**。若上游只提供拆分结果，`data` 包含四张 `kind=image`、`index=1..4` 的图片，按 `index` 展示。两种形态都只收取一次图片生成费用。

Edits 结果的 `action=edits`，优先返回 1–4 张 `kind=image`、`index=1..N` 的图片；只提供网格时返回 `kind=grid`。客户端按实际 `data` 展示，不应假设图生图总是四宫格。只有已有的 index 可供放大选图；超出父任务图片数量返回 400。

结果会经 Yingzo 生成文件存储发布。不要缓存 APIMart URL；保存本地任务 ID、用户选中的 index、结果 URL 与过期时间。结果已过期时查询可能返回 410。恢复已有任务仍可在异步开关关闭或余额用尽后查询，但 API Key 必须仍满足身份认证要求。

服务端不返回上游按钮和上游任务 ID。只展示本次范围内的 U1–U4 操作。

## 放大选图

父任务必须属于**同一用户且同一 API Key**，是已完成、未过期的 Midjourney v8.2 Imagine 或 Edits 生成任务（包括升级前的 Imagine 历史任务）。不同 Key 即使属于同一个用户，也不能互相选图。

```http
POST /v1/midjourney/generations/upscale
Authorization: Bearer <YINGZO_API_KEY>
Content-Type: application/json
Idempotency-Key: <本次选图操作的唯一 UUID>
```

```json
{
  "model": "midjourney-v8.2",
  "task_id": "imgtask_example",
  "index": 2,
  "speed": "fast"
}
```

`index` 必须是整数 1–4。`speed` 可省略，服务端设为 `fast`；显式传 `relax` 或 `turbo` 会返回 400。不能传生成参数、参考图片或 `custom_id`。

接口返回一个**新的** Yingzo 任务 ID，继续通过 `poll_url` 查询。完成后 `result.action=upscale`、`result.parent_task_id` 为原本的 Yingzo 生成任务 ID、`result.index` 为所选编号、`result.data` 中仅有一张图片。服务端沿用父任务的上游账户，不会换另一个账户尝试选图；父账户不可用时返回错误。

## 价格预估

在用户确认生成或选图前，可调用现有估价接口：

```http
POST /api/v1/agent/generation/estimates
Authorization: Bearer <YINGZO_API_KEY>
Content-Type: application/json
```

```json
{
  "kind": "image",
  "platform": "openai",
  "model": "midjourney-v8.2",
  "count": 1,
  "request": {
    "prompt": "A quiet mountain lake at sunrise",
    "speed": "fast"
  }
}
```

文生图、垫图、Edits 和风格参考均按 `generation` 估价，`request` 传相同生成字段；估价不会区分 Imagine/Edits 路径，因为二者单价相同。估价选图时，将 `request` 换成包含 `task_id`、`index`、`speed=fast` 的选图请求。响应中 `unit_kind=request`，`details.operation` 为 `generation` 或 `upscale`。`count` 表示计划发起的请求次数，不是候选图数；单次生成或选图均传 1。

估价不提交任务、不扣费，也不验证父任务归属；实际提交时重新校验并冻结价格。模型价格更新不会改变已经受理任务的预扣价格。

## 幂等和异常恢复

每次用户明确发起的新操作生成一个 `Idempotency-Key`，在提交前持久化它与请求体。网络断开、客户端重启或提交响应丢失时，使用**同一个 key 和同一个请求**重试，或者查询：

```http
GET /v1/images/tasks/by-idempotency/<URL编码后的key>
Authorization: Bearer <YINGZO_API_KEY>
```

相同 key、相同规范化请求返回原任务，不重复扣费。改变提示词、图片、风格权重、操作路径或选图编号后复用旧 key 会返回 409。想主动再生成一次，必须创建新 key。`Idempotency-Key` 最大 200 字节，建议使用 UUID。

任务失败按既有图片任务账本退款。`billing_status=refunded`、`refund_status=refunded` 才表示退款完成；若为 `refunding` / `pending`，保留任务并继续查询。执行中服务中断且无法确认上游结果时，会按既有机制失败退款，不自动重发付费 POST。

| HTTP 状态 | 常见原因 |
| --- | --- |
| 400 | 请求参数无效、非 v8.2、非 fast 速度、父任务未完成或操作未配置价格 |
| 401 / 403 | 鉴权、分组或图片生成权限不满足 |
| 404 | 任务不存在或不属于当前 Key，或模型被分组白名单禁用 |
| 409 | 幂等 key 已用于不同请求 |
| 410 | 父任务或生成文件已过期 |
| 429 | 频率或额度限制 |
| 503 | 异步存储未开启，或没有可用的 Midjourney 账户 |

错误使用 `error` 对象返回，客户端应显示服务端提供的 message。受理前失败不产生新的预扣；受理后的失败从任务响应判断退款状态。

## TypeScript 调用示例

以下示例假定 `baseURL` 是 Yingzo 服务根地址，不带 `/v1`。实际客户端需将 `idempotencyKey` 与 receipt 持久化，且只接受同一 Yingzo 服务返回的相对轮询路径。

```ts
type Speed = 'fast'
type Receipt = { task_id: string; poll_url: string }
type ImageItem = { url: string; asset_id: string; kind: 'grid' | 'image'; index?: number }
type Task = {
  task_id: string
  status: 'processing' | 'completed' | 'failed'
  billing_status: string
  refund_status: string
  result?: { action: 'imagine' | 'edits' | 'upscale'; speed: Speed; data: ImageItem[] }
  error?: { message?: string }
  expires_at: number
}

async function submitMJ(
  baseURL: string,
  apiKey: string,
  action: 'imagine' | 'edits' | 'upscale',
  request: Record<string, unknown>,
  idempotencyKey: string,
): Promise<Receipt> {
  const path = action === 'imagine'
    ? '/v1/midjourney/generations'
    : `/v1/midjourney/generations/${action}`
  const response = await fetch(baseURL + path, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${apiKey}`,
      'Content-Type': 'application/json',
      'Idempotency-Key': idempotencyKey,
    },
    body: JSON.stringify({ ...request, model: 'midjourney-v8.2', speed: 'fast' }),
  })
  const body = await response.json()
  if (response.status !== 202) throw new Error(body.error?.message ?? `HTTP ${response.status}`)
  return body
}

async function readMJ(baseURL: string, apiKey: string, taskID: string): Promise<Task> {
  const response = await fetch(`${baseURL}/v1/images/tasks/${encodeURIComponent(taskID)}`, {
    headers: { Authorization: `Bearer ${apiKey}` },
  })
  const body = await response.json()
  if (!response.ok) throw new Error(body.error?.message ?? `HTTP ${response.status}`)
  return body
}

// idempotencyKey 先存盘，再提交；发生网络错误时保留它。
const imagineKey = crypto.randomUUID()
const receipt = await submitMJ(baseURL, apiKey, 'imagine', {
  prompt: 'A quiet mountain lake at sunrise', speed: 'fast', size: '16:9',
}, imagineKey)
// 将 receipt.task_id 存盘；应用退出后可以按此 ID 恢复查询。
let parent: Task
while (true) {
  await new Promise(resolve => setTimeout(resolve, 3000))
  parent = await readMJ(baseURL, apiKey, receipt.task_id)
  if (parent.status !== 'processing') break
}
if (parent.status !== 'completed') {
  throw new Error(parent.error?.message ?? `生成失败，退款状态：${parent.refund_status}`)
}
// 生产客户端还需支持暂停轮询、429/5xx 退避和失败后的退款状态查询。
// 以下选图应在用户查看结果并点击 U2 后执行：
const upscaleKey = crypto.randomUUID()
const selection = await submitMJ(baseURL, apiKey, 'upscale', {
  task_id: receipt.task_id, index: 2, speed: 'fast',
}, upscaleKey)
```

## 联调验收

- 仅有图片生成和放大两项价格；Imagine、普通垫图、风格参考和 Edits 都命中图片生成价格。
- 所有接口省略速度均为 Fast；Relax/Turbo 在受理前被拒绝。一次生成即使返回 4 张也只计一次。
- Edits 1–4 张结果正确展示；不存在的图片编号不能放大；参考图和风格权重变化会触发幂等冲突。
- 选图 U1–U4 对应正确位置，并返回独立任务；其他速度、版本或接口参数被拒绝。
- 中断提交连接后用相同 key 恢复原任务；改参数复用 key 返回 409。
- 其他用户或同一用户的其他 Key 无法查询或选取父任务。
- 生成失败、图片保存失败及无法恢复的执行中断能进入退款流程；客户端能区分退款等待与完成。
- 模型列表的能力对象仅包含 `supported_speeds`；启用生成价格为 `["fast"]`，禁用后为 `[]`。
- 价格迁移保留已有 Fast 单价、免费配置和启用状态，历史任务仍可查询和放大。

## 上游参考

- [APIMart Midjourney 接口概览](https://docs.apimart.ai/cn/api-reference/images/midjourney/generation)
- [APIMart Imagine](https://docs.apimart.ai/cn/api-reference/images/midjourney/imagine)
- [APIMart Edits](https://docs.apimart.ai/cn/api-reference/images/midjourney/edits)
- [Midjourney Edit Model](https://docs.midjourney.com/hc/en-us/articles/48495453462797-Edit-Model)
- [Midjourney Style Reference](https://docs.midjourney.com/hc/en-us/articles/32180011136653-Style-Reference)
- [APIMart Upscale](https://docs.apimart.ai/cn/api-reference/images/midjourney/upscale)
- [APIMart 任务查询](https://docs.apimart.ai/cn/api-reference/images/midjourney/query)

以上说明描述 Yingzo 本次实现的客户端协议；上游文档包含的其他 MJ 功能不代表 Yingzo 已开放。异步存储与通用任务行为另见 [异步图片任务](ASYNC_IMAGE_TASKS.md)。
