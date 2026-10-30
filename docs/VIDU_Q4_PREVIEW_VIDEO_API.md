# Vidu Q4 Preview 下游对接文档

下游继续使用平台域名、平台 API Key 和统一视频异步接口。无需持有 Vidu Key，也无需调用 Vidu 原生路径。

## 接口与参数

| 项目 | 约定 |
| --- | --- |
| 创建 | `POST /v1/videos` |
| 查询 | `GET /v1/videos/{id}`，`id` 使用创建响应的平台任务 ID |
| 鉴权 | `Authorization: Bearer $API_KEY` |
| `model` | `vidu-q4-preview` |
| `duration` | 必填，3–16 的整数秒；不支持 `-1` 自动时长 |
| `resolution` | `540p`、`720p`、`1080p`、`2K`、`4K`，默认 `720p` |
| `prompt` | 最多 20000 字符；图生可省略，参考生必填；兼容 `content` 内的 `text` 项，顶层非空 `prompt` 优先 |
| `generate_audio` | 布尔值，默认 `true`；`false` 输出静音视频 |
| `ratio` | 仅参考生视频可用：`1:1`、`9:16`、`16:9`、`3:4`、`4:3`，默认 `16:9`；兼容 `aspect_ratio`、`aspectRatio` |
| `seed` | 可选整数；省略或 `0` 使用随机种子 |
| `watermark` | 可选布尔值，默认 `false`；网关显式向上游发送无水印，`true` 添加水印 |
| `is_rec` | 仅图生视频可用，是否扩写提示词，默认 `true` |
| `ability_code` | 通常省略，由素材角色推断；可显式指定 `video_image_to_video` 或 `video_reference_to_video` |

下游可省略 `watermark`、`generate_audio` 和 `is_rec`：网关默认无水印、音频直出，图生视频默认启用提示词扩写。参考生视频接口没有 `is_rec` 参数，不传该字段。Vidu 图生文档的水印说明与字段默认值存在矛盾，网关通过显式发送 `watermark: false` 保证默认无水印。

图生视频由首帧图片决定画幅，**必须省略 `ratio` 及其别名**，显式设置会返回参数错误。不支持纯文生视频、尾帧、首尾帧插值、参考视频和纯音频参考。

统一素材格式：

| 用途 | `type` | `role` | URL 字段 | 数量 |
| --- | --- | --- | --- | --- |
| 图生首帧 | `image_url` | `first_frame` | `image_url.url` | 恰好 1 张 |
| 参考图片 | `image_url` | `reference_image` | `image_url.url` | 1–15 张 |
| 参考音频 | `audio_url` | `reference_audio`（可省略） | `audio_url.url` | 0–3 段，必须搭配参考图片 |

首帧与参考素材互斥。素材顺序原样保留，可在提示词中使用“参考图1”“参考图2”“参考音频1”等编号。不要使用原生 `images`、`sounds`、`audio` 代替统一的 `content`、`generate_audio`。

推荐传入公网可访问的 HTTP(S) URL；也支持现有平台素材上传、`attachment://N` multipart 引用和内联 base64 data URI，复用统一素材处理流程。图片使用 PNG/JPEG/JPG/WEBP，Vidu 上游单张最大 50MB；参考音频使用 MP3，每段 3–12 秒、最大 50MB。平台内联转存/上传另受统一素材限制（图片 30MiB、音频 15MiB），采用两者中更严格的限制。公网图片与音频 URL 原样透传，媒体格式、大小和音频时长由上游最终校验；不要把下游 `duration_seconds` 当作已验证的音频时长。

## 图生视频示例

```bash
curl "$BASE_URL/v1/videos" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: vidu-image-unique-001' \
  -d '{
    "model": "vidu-q4-preview",
    "prompt": "宇航员挥手，镜头缓慢向上移动",
    "duration": 5,
    "resolution": "720p",
    "generate_audio": true,
    "content": [
      {"type":"image_url","role":"first_frame","image_url":{"url":"https://cdn.example.com/start.png"}}
    ]
  }'
```

## 参考生视频示例

```bash
curl "$BASE_URL/v1/videos" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: vidu-reference-unique-001' \
  -d '{
    "model": "vidu-q4-preview",
    "prompt": "在参考图1的场景中，参考图2的人物使用参考音频1的声音说：欢迎来到影造。",
    "duration": 8,
    "resolution": "1080p",
    "ratio": "16:9",
    "generate_audio": true,
    "content": [
      {"type":"image_url","role":"reference_image","image_url":{"url":"https://cdn.example.com/scene.png"}},
      {"type":"image_url","role":"reference_image","image_url":{"url":"https://cdn.example.com/person.png"}},
      {"type":"audio_url","role":"reference_audio","audio_url":{"url":"https://cdn.example.com/voice.mp3"}}
    ]
  }'
```

## 任务查询、失败与幂等

创建成功返回 HTTP 200 和统一任务对象，例如：

```json
{
  "video_channel_id": "",
  "id": "video_example",
  "object": "video",
  "model": "vidu-q4-preview",
  "status": "queued",
  "refund_status": "not-applicable",
  "created_at": 1793376000
}
```

```bash
curl "$BASE_URL/v1/videos/$VIDEO_ID" \
  -H "Authorization: Bearer $API_KEY"
```

建议每 5–10 秒查询一次。状态沿用 `queued`、`processing`、`completed`、`failed`、`cancelled`；完成后读取 `video_url` 和 `completed_at`，失败读取 `error.code`、`error.message` 和 `refund_status`。已扣费的失败任务沿用平台退款流程，`refund_status` 可为 `pending` 或 `refunded`。查询必须使用有权访问该任务的 Key。

链接有效期取决于平台交付配置，请及时保存。Vidu 原生结果 URL 有效期为 24 小时；平台继续复用现有成片发布流程，结果下载不会携带上游 Key。下游无需处理 `task_id`、`state`、`err_code`、`creations`。本次未开放 Vidu `callback_url`、`payload`、`wm_position`、`wm_url`、`meta_data`，请使用统一查询接口。

重试同一创建请求应复用 `Idempotency-Key` 和相同请求体；新生成任务使用新 Key。参数不合法返回 HTTP 400，包含 `invalid_video_model`、`invalid_video_duration`、`invalid_video_resolution`、`invalid_video_ability`、`invalid_video_content`、`invalid_video_prompt`、`invalid_video_ratio` 或 `invalid_video_options`。价格或渠道未配置时依平台统一错误处理，不会自动套用其他模型价格。

## 计费与上线配置

沿用现有视频按秒计费：生成时长 × 所选分辨率每秒单价；参考图片、参考音频不额外收费。普通视频分组再应用分组倍率；Agent 分组使用配置的最终单价。实际价格由运营配置，本次不预设 Vidu 价格。成本预估继续使用现有统一入口与同一请求参数。

1. 后台新增/编辑视频 API Key 账号，选择 **Vidu**，填写 Vidu Key。默认 Base URL 为 `https://api.vidu.cn`，API Path 为 `/ent/v2/tasks`，这是查询集合路径，不能填成创建路径。
2. 选择模型 `vidu-q4-preview`，默认映射上游 `viduq4-preview`。需要覆盖上游名时可设置 `credentials.model_mapping`；未改写的同名映射会自动使用默认上游名。
3. 关联目标分组，并按账号实际权限勾选可用分辨率和 3–16 秒时长，可继续收窄参考素材数量与能力。图生和参考生以模型实际能力为限，账号开关无法开启文生或首尾帧。
4. 普通视频分组按模型、分辨率配置 `video_pricing_rules` 的 `credits_per_second`；Agent 分组在模型表格按分辨率配置每秒价格。无需新增数据库迁移；模型目录沿用账号映射发现机制。
5. 本模型不使用 Seedance 专属通道，`video_channel_id` 省略或传空字符串。上游默认轮询间隔 5 秒、总等待 15 分钟，可沿用账号超时配置。
6. 上线使用真实 Vidu 账号分别验证图生、参考图加音频、静音输出及实际价格。开发测试采用模拟上游，不代表已验证真实账号权限、生成质量或实际扣费。

账号配置关键字段示例：

```json
{
  "platform": "video",
  "type": "apikey",
  "credentials": {
    "api_key": "YOUR_VIDU_KEY",
    "model_mapping": {"vidu-q4-preview":"viduq4-preview"}
  },
  "extra": {
    "video_provider": "vidu",
    "base_url": "https://api.vidu.cn",
    "api_path": "/ent/v2/tasks"
  }
}
```

网关内部将首帧请求转换为 `POST /ent/v2/img2video`，参考请求转换为 `POST /ent/v2/reference2video`，查询转换为 `GET /ent/v2/tasks/{id}/creations`，上游鉴权为 `Token`。

官方来源：[图生视频](https://platform.vidu.cn/docs/api-reference/video-models/vidu-q4-preview/image-to-video)、[参考生视频](https://platform.vidu.cn/docs/api-reference/video-models/vidu-q4-preview/reference-to-video)。核对日期：2026-10-31。
