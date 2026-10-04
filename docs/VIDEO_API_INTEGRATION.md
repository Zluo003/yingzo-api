# yingzo-api 视频模型统一下游对接指南

本文面向 Yingzo 客户端和第三方调用方。所有接入 yingzo-api 统一视频链路的模型，均使用本文的请求字段、素材结构和异步任务流程。Seedance、Grok Imagine、Kling 等模型以及后续接入的模型，共用一套客户端实现。

**下游只指定公开模型 ID、生成模式和生成参数。上游模型名、端点、字段名、素材格式、鉴权、状态和成片获取方式，由 yingzo-api 的适配器自动转换。** 切换模型时修改 `model`，并选择该模型支持的参数值；切换同一模型背后的供应商不需要修改下游请求。

统一协议包含文生、图生、首尾帧、参考生成四种模式，但各模型和渠道支持的模式、时长、分辨率、画幅及素材数量可以不同。适配器只能转换已支持的能力；没有兼容渠道时会返回错误，不会把首尾帧等请求改成另一种生成模式。

## 1. 统一端点与鉴权

| 操作 | 方法与路径 | 返回内容 |
| --- | --- | --- |
| 创建视频 | `POST /v1/videos` | JSON 任务对象 |
| 查询任务 | `GET /v1/videos/{id}` | 同一格式的 JSON 任务对象 |
| 获取成片 | `GET /v1/videos/{id}/content` | 完成后重定向到视频文件 |
| 查询模型目录 | `GET /v1/models` | 当前凭证可访问的模型目录 |

- 所有请求使用 `Authorization: Bearer <API_KEY>`；查询、下载使用创建任务的同一个 API Key。
- 创建请求推荐使用 `Content-Type: application/json`。本地文件也可以通过 multipart 提交，JSON 内的字段与模式不变，见第 5 节。
- 每个新任务使用一个独立的 `Idempotency-Key`，网络重试保留原键和原请求体，见第 7 节。
- `{id}` 使用创建响应里的下游任务 `id`，不要使用上游任务 ID。
- 创建成功返回 HTTP 200，表示任务已受理；是否生成成功须以查询结果中的 `status` 为准。

下游统一调用 `/v1/videos`，不要按供应商改成 `/v1/videos/generations`、`/v1/video/generations` 等上游路径。

## 2. 统一请求字段

### 2.1 顶层字段

| 字段 | 类型 | 下游约定 |
| --- | --- | --- |
| `model` | string | 必填。当前凭证可访问的公开模型 ID，如 `seedance-2.0`、`seedance-2.5`、`grok-imagine-video-1.5`、`kling-v3-omni`。不填写供应商内部模型名。 |
| `prompt` | string | 提示词。文生模式必填且非空；其他模式建议填写动作、镜头及画面要求。统一放在顶层。 |
| `ability_code` | string | 建议显式填写。四种模式的固定值见第 3 节。 |
| `duration` | number | 必填，单位为秒。统一传模型支持的正整数，不传字符串；不使用上游的 `seconds` 等字段名。 |
| `resolution` | string | 建议显式填写，如 `480p`、`720p`、`1080p`、`4K`，具体取值以模型和可用渠道为准。 |
| `aspect_ratio` | string | 画幅，如 `16:9`、`9:16`、`1:1`。建议显式填写模型支持的值；首帧模式的实际画幅也可能由输入图片决定。 |
| `generate_audio` | boolean | 可选，期望是否生成音频。适配器在上游支持时转换；不支持该开关的上游可能忽略它，`false` 不是所有渠道都能保证的静音承诺。 |
| `content` | array | 文生模式省略或传 `[]`；其余模式统一在此填写素材，结构见下表。 |
| `safety_identifier` | string | 可选，安全审计标识。 |

服务端兼容 `ratio` 和 `aspectRatio`，新客户端统一使用 `aspect_ratio`，不要同时发送多个画幅字段。为便于跨模型切换，不依赖省略参数时的默认值，也不把个别模型的特殊值（如自动时长）作为通用参数。

### 2.2 统一素材结构

每个素材只使用与 `type` 对应的一个 URL 对象，角色使用 `role` 显式声明：

| 素材用途 | `type` | URL 字段 | `role` |
| --- | --- | --- | --- |
| 首帧图片 | `image_url` | `image_url: {"url": "..."}` | `first_frame` |
| 尾帧图片 | `image_url` | `image_url: {"url": "..."}` | `last_frame` |
| 参考图片 | `image_url` | `image_url: {"url": "..."}` | `reference_image` |
| 参考视频 | `video_url` | `video_url: {"url": "..."}` | `reference_video` |
| 参考音频 | `audio_url` | `audio_url: {"url": "..."}` | `reference_audio` |

例如，一张参考图片始终写为：

```json
{
  "type": "image_url",
  "image_url": {"url": "https://cdn.example.com/person.png"},
  "role": "reference_image"
}
```

不要在顶层发送供应商的 `images`、`videos`、`audios`、`image`、`first_frame_url` 等字段；不要把 `image_url` 写成字符串。下游不需要发送 `subject_type`，也不需要声明参考视频的 `duration_seconds`。参考视频时长由平台探测，客户端声明不会用于计费。

## 3. 四种模式：同一请求，只改变能力码与素材

| 模式 | `ability_code` | `content` 要求 |
| --- | --- | --- |
| 文生视频 | `video_text_to_video` | 无图片、视频或音频，省略或传 `[]` |
| 图生视频（首帧） | `video_image_to_video` | 恰好一张 `first_frame` 图片 |
| 首尾帧生视频 | `video_start_end_to_video` | 恰好一张 `first_frame` 和一张 `last_frame` 图片 |
| 参考生成 | `video_reference_to_video` | 至少一种受支持的参考素材，使用 `reference_image`、`reference_video`、`reference_audio` |

以下示例使用同一公开模型 `seedance-2.0`、5 秒、720p、16:9 展示四种请求结构。它们是协议示例，并不表示每个 Seedance 渠道都开放四种模式。更换模型时保留相同字段和结构，按目标模型能力调整取值。

### 3.1 文生视频

```json
{
  "model": "seedance-2.0",
  "prompt": "清晨的海岸，海浪轻拍礁石，镜头缓慢推进，自然光，无字幕",
  "ability_code": "video_text_to_video",
  "duration": 5,
  "resolution": "720p",
  "aspect_ratio": "16:9",
  "content": []
}
```

### 3.2 图生视频（首帧）

```json
{
  "model": "seedance-2.0",
  "prompt": "以输入图片为首帧，让人物自然转身并微笑，保持外观一致",
  "ability_code": "video_image_to_video",
  "duration": 5,
  "resolution": "720p",
  "aspect_ratio": "16:9",
  "content": [
    {
      "type": "image_url",
      "image_url": {"url": "https://cdn.example.com/start.png"},
      "role": "first_frame"
    }
  ]
}
```

首帧模式不能再混入参考图、视频或音频。

### 3.3 首尾帧生视频

```json
{
  "model": "seedance-2.0",
  "prompt": "从首帧的白天平滑过渡到尾帧的夜晚，保持场景结构一致",
  "ability_code": "video_start_end_to_video",
  "duration": 5,
  "resolution": "720p",
  "aspect_ratio": "16:9",
  "content": [
    {
      "type": "image_url",
      "image_url": {"url": "https://cdn.example.com/first.png"},
      "role": "first_frame"
    },
    {
      "type": "image_url",
      "image_url": {"url": "https://cdn.example.com/last.png"},
      "role": "last_frame"
    }
  ]
}
```

按首帧、尾帧顺序填写，并分别标注角色；两张都必须是图片，不能混入其他素材。

### 3.4 参考生成

一张或多张参考图片都使用参考模式。下面是一张参考图的最小示例：

```json
{
  "model": "seedance-2.0",
  "prompt": "保持参考图中的人物外观，让人物在海边自然行走，镜头缓慢跟随",
  "ability_code": "video_reference_to_video",
  "duration": 5,
  "resolution": "720p",
  "aspect_ratio": "16:9",
  "content": [
    {
      "type": "image_url",
      "image_url": {"url": "https://cdn.example.com/person.png"},
      "role": "reference_image"
    }
  ]
}
```

若目标模型和渠道支持视频、音频参考，可将上述 `content` 替换为：

```json
[
  {
    "type": "image_url",
    "image_url": {"url": "https://cdn.example.com/person.png"},
    "role": "reference_image"
  },
  {
    "type": "video_url",
    "video_url": {"url": "https://cdn.example.com/motion.mp4"},
    "role": "reference_video"
  },
  {
    "type": "audio_url",
    "audio_url": {"url": "https://cdn.example.com/music.mp3"},
    "role": "reference_audio"
  }
]
```

对应提示词可以描述“保持人物外观，参考视频中的动作节奏，并使用参考音乐”。素材类型、数量和参考视频时长受模型及渠道限制；当前内置模型的参考音频需要伴随至少一张图片或一段视频，不能单独使用音频。

**一张图不一定是首帧，两张图也不一定是首尾帧。** 作为构图、人物、风格等参考时，即使只有一张或两张图，也应使用 `video_reference_to_video` 和 `reference_image`。

### 3.5 能力推断与客户端选择

未传 `ability_code` 时，服务端会根据素材角色推断模式：参考素材优先；一首帧加一尾帧推断为首尾帧；单张首帧推断为图生；无素材推断为文生。新客户端应显式提交能力码和角色，避免省略角色产生歧义。

客户端先确定用户选择的模式，再组装 `content`。不要仅凭图片数量选模式，也不要根据供应商名称改变请求结构。

## 4. 模型能力与适配器分工

### 4.1 获取可用模型与参数范围

```bash
curl -sS "$BASE_URL/v1/models" \
  -H "Authorization: Bearer $API_KEY"
```

其中 `BASE_URL` 是网关域名，如 `https://your-host`，不含 `/v1`；`API_KEY` 是下游凭证。Agent 分组的模型目录提供 `capabilities`，可读取 `supported_video_resolutions`、`supported_video_durations_sec`、`supports_video_audio`、`supports_audio_only_reference` 等字段。目录是模型级信息，当前不提供每个渠道四种模式的完整可用矩阵；实际请求仍须通过账号与适配器的能力检查。

当前内置模型的规格如下。此表是模型级范围，可用渠道、账号白名单和定价配置可能进一步收窄它：

| 公开模型 ID | 时长（整数秒） | 模型级分辨率范围 |
| --- | --- | --- |
| `seedance-2.0` | 4–15 | `480p` / `720p` / `1080p` / `4K` |
| `seedance-2.0-fast` | 4–15 | `480p` / `720p` |
| `seedance-2.5` | 4–30 | `480p` / `720p` / `1080p` |
| `grok-imagine-video-1.5` | 1–15 | `480p` / `720p` / `1080p` |
| `kling-v3-omni` | 3–15 | `720p` / `1080p` / `4K` |

Grok Imagine 和 Kling Omni 当前接入的参考素材为图片（至多 7 张），未开放首尾帧模式；Kling Omni 画幅为 `16:9`、`9:16`、`1:1`。这些是能力差异，客户端仍使用第 2、3 节的同一套参数。

### 4.2 网关负责的转换

| 下游统一约定 | 网关与适配器负责 |
| --- | --- |
| 公开 `model` | 根据账号映射转换为上游模型名 |
| `POST /v1/videos` | 选择兼容渠道，调用其创建端点并附带上游鉴权 |
| `ability_code` + `content[].role` | 选择上游对应模式，转换首帧、尾帧或参考素材表达 |
| `duration`、`resolution`、`aspect_ratio` | 转换上游字段名和格式，并校验可用取值 |
| `content` 中的图片、视频、音频 | 转换为上游要求的内容数组、URL 数组或其他素材字段 |
| `generate_audio` | 在渠道支持时转换为上游音频开关 |
| 下游任务 `id` 和统一状态 | 保存上游任务标识、轮询上游、统一状态值 |
| `GET /v1/videos/{id}/content` | 回收成片，向下游提供可下载结果 |

例如，同一个下游 `aspect_ratio` 可以被适配器转换成上游的 `ratio`，`duration` 可以被转换成 `seconds`，`content` 中的参考图可以被转换成上游的 `images` 数组。这些转换不需要客户端感知。

Xingguang 适配器目前接入 Seedance 2.0、2.5 的文生与参考生成，两者的 5 秒、720p 文生请求均已实测成功；Seedance 2.0 的 `21:9` 文生请求也已实测完成。画幅按通用枚举 `16:9` / `9:16` / `21:9` / `1:1` / `4:3` / `3:4` 透传，文档中的“常用 16:9、9:16”不作为完整白名单；其余比例仍应以对应上游模型的实际结果为准。首帧、首尾帧尚未在该适配器开放，客户端不能因为文档列出四种统一模式就假定这个渠道支持全部模式。

## 5. 公网素材与本地文件

### 5.1 已有公网 URL：直接填写

将公网地址直接放入 `content` 对应的 `url`。地址必须无需下游 API Key 即可供上游访问，并在生成期间持续有效。

- 公网图片、音频 URL 经地址校验后原样透传，不下载转存，不改写路径或签名参数，不占用素材上传配额。
- 公网参考视频 URL 也保持原样。网关仅临时下载以探测实际时长，供校验和计费使用，随后删除临时文件。
- 不接受本机文件路径、`localhost` 或私网 URL 作为公网素材。
- 网关自身的临时素材 URL 会检查归属与有效期。

### 5.2 本地文件：先上传，再使用 URL

Agent 分组凭证可调用 `POST /v1/files`（也提供 `POST /api/v1/agent/assets`），用 `multipart/form-data` 和 `file` 字段上传：

```bash
curl -sS "$BASE_URL/v1/files" \
  -H "Authorization: Bearer $API_KEY" \
  -F 'file=@./start.png'
```

成功响应包含 `id`、`url`、`contentType`、`size`、`sha256`、`expiresAt`、`leaseUntil`。读取 `url` 后，按第 3 节放入对应素材项即可。首尾帧上传两次，分别使用两个 URL；参考素材按其类型和用途声明角色。

上传后尽快创建任务，勿将临时 URL 作为长期素材地址。已有公网 URL 的素材无需再上传。

### 5.3 本地文件：一次 multipart 创建

创建地址仍是 `POST /v1/videos`，模式与参数仍使用同一个 JSON：

- `request` 表单字段保存完整请求 JSON（兼容 `json`、`body` 字段名，新客户端统一用 `request`）。
- 文件字段重复使用 `file`。
- JSON 内的 URL 写为 `attachment://0`、`attachment://1`，对应 `file` 字段的零基顺序。
- 网关上传本地素材并替换附件 URL，再交给适配器；客户端不要手动设置 multipart boundary。

```bash
curl -sS "$BASE_URL/v1/videos" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Idempotency-Key: $REQUEST_KEY" \
  -F 'request={"model":"seedance-2.0","prompt":"人物自然转身并微笑","ability_code":"video_image_to_video","duration":5,"resolution":"720p","aspect_ratio":"16:9","content":[{"type":"image_url","image_url":{"url":"attachment://0"},"role":"first_frame"}]}' \
  -F 'file=@./start.png'
```

素材上传、内联素材和存储限制详见 [视频参考素材说明](VIDEO_REFERENCE_MATERIALS.md)。

## 6. 创建、查询和获取成片

### 6.1 创建

将第 3 节任一完整请求保存为 `request.json`。每个新任务生成并保存一个 `REQUEST_KEY`，例如使用 UUID；下面的重试必须复用该值：

```bash
curl -sS "$BASE_URL/v1/videos" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $REQUEST_KEY" \
  --data-binary @request.json
```

创建成功的响应示例：

```json
{
  "id": "video_example123",
  "object": "video",
  "model": "seedance-2.0",
  "status": "queued",
  "refund_status": "not-applicable",
  "created_at": 1791098331
}
```

保存 `id` 作为 `VIDEO_ID`。`model` 始终表示下游公开模型，响应不要求客户端解析上游任务结构。

### 6.2 查询

```bash
curl -sS "$BASE_URL/v1/videos/$VIDEO_ID" \
  -H "Authorization: Bearer $API_KEY"
```

| `status` | 含义 | 客户端处理 |
| --- | --- | --- |
| `queued` | 排队中 | 继续查询 |
| `processing` | 生成或成片准备中 | 继续查询 |
| `completed` | 已完成且成片已可供下游获取 | 获取成片 |
| `failed` | 任务失败 | 读取 `error` 与 `refund_status`，结束生成轮询 |
| `cancelled` | 已取消 | 结束生成轮询 |

建议从 5 秒间隔开始，逐渐退避至 10–30 秒；若响应带 `Retry-After`，按其等待。网络异常时继续查询原任务。上游已完成但网关尚在回收成片时，下游仍可能为 `processing`，不要因此重复创建任务。

完成响应示例：

```json
{
  "id": "video_example123",
  "object": "video",
  "model": "seedance-2.0",
  "status": "completed",
  "video_url": "https://your-host/media/result-asset/asset.mp4",
  "refund_status": "not-applicable",
  "created_at": 1791098331,
  "completed_at": 1791098851
}
```

`created_at`、`completed_at` 是 Unix 秒级时间戳。`video_url` 是网关交付的成片地址，可能有有效期；建议以统一 `/content` 接口作为获取入口，不依赖具体存储域名或上游地址。

### 6.3 获取成片

```bash
curl -sS -L "$BASE_URL/v1/videos/$VIDEO_ID/content" \
  -H "Authorization: Bearer $API_KEY" \
  -o output.mp4
```

当前统一视频链路在完成后返回 HTTP 307，客户端需跟随 `Location` 获取视频。重定向到其他域名时不要转发下游 API Key。任务未完成时返回 HTTP 409，错误码为 `video_content_not_ready`，应继续查询原任务。

## 7. 幂等、失败与重试

### 7.1 创建重试

- 同一任务的网络超时或断线重试，保留同一个 API Key、`Idempotency-Key` 和原请求体。
- 命中幂等重放时返回原创建结果，并带 `X-Idempotency-Replayed: true`；用返回的 `id` 查询最新状态。
- 同一个键不能用于不同参数的请求，否则返回 `IDEMPOTENCY_KEY_CONFLICT`。
- 收到 `IDEMPOTENCY_IN_PROGRESS` 或 `IDEMPOTENCY_RETRY_BACKOFF` 时等待再重试；存在 `Retry-After` 时遵守它。
- 已拿到任务 ID 后，轮询或下载失败只重试 GET，不重新 POST。已明确失败后若要发起一次新的生成，才使用新的幂等键。
- multipart 网络重试建议先确认是否已经创建成功；需要稳定重放时，优先采用“上传一次取得 URL，再提交固定 JSON”的两步方式。

### 7.2 HTTP 请求错误

```json
{
  "error": {
    "type": "invalid_request_error",
    "code": "invalid_video_content",
    "message": "Image-to-video requires exactly one first frame image"
  }
}
```

| 代码 | 处理方式 |
| --- | --- |
| `invalid_api_key` | 检查凭证与鉴权头 |
| `invalid_video_request` | 检查 JSON 和请求结构 |
| `invalid_video_model` / `invalid_video_resolution` / `invalid_video_duration` / `invalid_video_ratio` | 检查公开模型 ID 和参数取值 |
| `invalid_video_ability` / `invalid_video_prompt` / `invalid_video_content` | 检查模式、提示词、素材角色、数量和组合 |
| `invalid_reference_video_duration` | 检查参考视频实际时长是否符合模型限制 |
| `video_pricing_rule_not_found` | 联系管理员配置该模型与分辨率的价格 |
| `video_service_unavailable` | 可能没有满足模式和参数的可用渠道，也可能是生成服务暂时不可用；不要改用供应商私有协议绕过 |
| `reference_material_download_failed` / `reference_material_unavailable` / `reference_material_failed` | 检查素材公网可达性、有效期及处理错误 |
| `video_content_not_ready` | 继续查询原任务，等待完成后再下载 |
| `VIDEO_TASK_NOT_FOUND` | 检查下游任务 ID，并使用创建任务的同一凭证 |

具体以返回的 HTTP 状态、`error.code` 和 `error.message` 为准。JSON 有效不代表当前渠道支持该模式；HTTP 200 的状态查询也不代表生成成功。

### 7.3 任务失败响应

```json
{
  "id": "video_example123",
  "object": "video",
  "model": "seedance-2.0",
  "status": "failed",
  "error": {
    "code": "video_generation_failed",
    "message": "视频生成失败，请稍后再试"
  },
  "refund_status": "refunded",
  "created_at": 1791098331
}
```

任务错误位于 `error.code` / `error.message`。`refund_status` 可为 `not-applicable`、`pending`、`refunded`；退款状态与生成状态分别处理，`pending` 时可稍后再次查询。

## 8. Yingzo 客户端字段映射

| 客户端概念或字段 | 统一 API 字段 |
| --- | --- |
| `modelId` | `model`（公开模型 ID） |
| `prompt` / `promptChinese` | `prompt` |
| `durationSeconds` | `duration` |
| `videoResolution` | `resolution` |
| `aspectRatio` | `aspect_ratio` |
| `generateAudio` | `generate_audio` |
| 生成模式 | 第 3 节对应的 `ability_code` |
| 本地或已上传素材 | 解析为 URL 后写入 `content`，并按用途设置 `role` |
| `forceReferenceMode` 或明确选用参考生成 | `video_reference_to_video`，图片使用 `reference_image` |

客户端维护一套视频请求构造、任务查询、错误处理和下载逻辑。模型切换只影响 `model`、参数候选值以及可选择的模式，不引入按供应商分支的请求体。

## 9. 服务端配置与接入验收

统一链路要求使用视频分组，或已接入视频渠道的 Agent 多平台分组。本文中的 Grok Imagine 指统一视频目录里的模型，不要求下游切换到独立 Grok 专用分组的历史接口。

管理员负责配置公开模型到上游模型的映射、视频适配器、账号能力范围、分组与价格。Agent 多平台分组应保持多平台路由，不能配置成仅 OpenAI 平台。需要网关托管本地素材或成片时，在 **系统设置 → 素材存储** 配置存储与公网访问地址。

接入验收应按当前模型实际开放的模式进行：

- 模型目录可见，参数取值与当前可用渠道匹配。
- 四种模式均使用 `/v1/videos` 和同一套字段，素材角色表达正确。
- 支持的模式能够创建并查询任务；未支持的模式能够显示明确错误，不擅自切换语义。
- 公网素材直接使用原 URL，本地文件上传后按同一素材结构提交。
- 保存下游任务 ID 与幂等键，正确处理等待、失败、退款状态和网络重试。
- 成功任务通过统一 `/content` 入口获取，客户端不持有上游密钥、不依赖上游任务 ID。
