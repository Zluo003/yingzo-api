# yingzo-api 视频接口对接指南

本文档描述 yingzo-api 当前的视频生成协议，适用于 Yingzo 客户端、第三方 OpenAI 兼容客户端和上游适配器。接口是异步任务模型：创建任务后轮询任务状态，成功后通过 content 地址下载视频。

## 1. 基本信息

- 创建：`POST /v1/videos`
- 查询：`GET /v1/videos/{id}`
- 下载：`GET /v1/videos/{id}/content`
- 认证：`Authorization: Bearer <API_KEY>`
- 建议：创建请求携带 `Idempotency-Key`，网络重试时避免重复任务和重复计费。
- 请求与响应均使用 JSON；服务端也支持 multipart，见第 7 节。

请求示例：

```bash
curl https://your-host/v1/videos \\
  -H 'Authorization: Bearer sk-xxx' \\
  -H 'Content-Type: application/json' \\
  -H 'Idempotency-Key: video-demo-0001' \\
  -d '{
    "model": "seedance-2.0",
    "prompt": "海边日落，镜头缓慢推进",
    "ability_code": "video_text_to_video",
    "duration": 8,
    "resolution": "720p",
    "aspect_ratio": "16:9",
    "generate_audio": true
  }'
```

## 2. 创建请求字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `model` | string | 已在渠道/分组中配置的视频模型，例如 `seedance-2.0`。 |
| `prompt` | string | 视频描述和导演要求。 |
| `content` | array | 文生可省略；图像、视频、音频参考素材放在这里。 |
| `ability_code` | string | 能力类型，见第 3 节。未传时服务端会根据素材推断。 |
| `duration` | number | 秒数。通常为整数；服务端还会按模型能力校验范围。 |
| `resolution` | string | `480p`、`720p`、`1080p`、`4K` 等，必须是模型支持的值。 |
| `aspect_ratio` | string | 推荐使用 `16:9`、`9:16`、`1:1` 等。也兼容 `ratio` 和 `aspectRatio`。 |
| `generate_audio` | boolean | 是否生成音频；不支持该开关的上游会由适配器忽略。 |
| `safety_identifier` | string | 可选的安全审计标识。 |

`duration`、`resolution` 和画幅最终以模型能力合同为准。以当前 Seedance 配置为例：2.0 支持 480p/720p/1080p/4K，2.0-fast 不支持 1080p/4K，2.5 支持 480p/720p/1080p；超出能力会在创建阶段返回错误，不会产生上游任务。

## 3. 四种视频能力

### 3.1 文生视频

不携带媒体内容，能力码为 `video_text_to_video`：

```json
{
  "model": "seedance-2.0",
  "prompt": "一只柴犬在草地上奔跑，电影级光影",
  "ability_code": "video_text_to_video",
  "duration": 8,
  "resolution": "720p",
  "aspect_ratio": "16:9"
}
```

### 3.2 图生视频

携带一张图片，使用 `image_url`，角色必须为 `first_frame`：

```json
{
  "model": "seedance-2.0",
  "prompt": "让人物自然转身并微笑",
  "ability_code": "video_image_to_video",
  "duration": 6,
  "resolution": "720p",
  "content": [
    {
      "type": "image_url",
      "image_url": {"url": "https://cdn.example.com/start.png"},
      "role": "first_frame"
    }
  ]
}
```

### 3.3 首尾帧生视频

携带两张图片，顺序和角色分别为 `first_frame`、`last_frame`：

```json
{
  "model": "seedance-2.0",
  "prompt": "从白天平滑过渡到夜晚",
  "ability_code": "video_start_end_to_video",
  "duration": 8,
  "resolution": "720p",
  "content": [
    {"type": "image_url", "image_url": {"url": "https://cdn.example.com/first.png"}, "role": "first_frame"},
    {"type": "image_url", "image_url": {"url": "https://cdn.example.com/last.png"}, "role": "last_frame"}
  ]
}
```

两张图必须都是图片；不能混入视频或音频。

### 3.4 参考生视频

用于多素材控制。支持图片、视频和音频：

```json
{
  "model": "seedance-2.0",
  "prompt": "保持人物外观，参考视频的动作节奏，并使用参考音乐",
  "ability_code": "video_reference_to_video",
  "duration": 8,
  "resolution": "720p",
  "content": [
    {"type": "image_url", "image_url": {"url": "https://cdn.example.com/person.png"}, "role": "reference_image"},
    {"type": "video_url", "video_url": {"url": "https://cdn.example.com/motion.mp4"}, "role": "reference_video"},
    {"type": "audio_url", "audio_url": {"url": "https://cdn.example.com/music.mp3"}, "role": "reference_audio"}
  ]
}
```

服务端会校验参考素材数量、类型和视频时长。参考视频的时长由服务端探测并用于计费，不采信客户端伪造的时长。

## 4. 素材 URL 与 multipart

JSON 请求中的 URL 必须是上游可访问的公网 URL。若客户端只有本地文件，可使用 multipart：

- JSON 字段名使用 `request`、`json` 或 `body`，内容为完整 JSON。
- 文件字段重复使用 `file`。
- JSON 中对应 URL 写成 `attachment://0`、`attachment://1`，数字是 `file` 的零基顺序。

服务端会先上传并转存素材，再替换 URL 后调用上游。上传失败、类型不支持或视频时长超限会在创建阶段返回明确错误。

## 5. 异步查询与下载

创建成功后保存响应中的任务 ID。使用同一个 API Key 查询：

```bash
curl https://your-host/v1/videos/<VIDEO_ID> \\
  -H 'Authorization: Bearer sk-xxx'
```

状态通常包括 `queued`、`processing`、`completed`、`failed`。客户端建议使用指数退避轮询，并遵守响应中的 `Retry-After`。完成后可访问：

```bash
curl -L https://your-host/v1/videos/<VIDEO_ID>/content \\
  -H 'Authorization: Bearer sk-xxx' \\
  -o output.mp4
```

不要把上游下载地址当作永久地址；应通过 yingzo-api 的 content 接口获取并保存结果。

## 6. 错误处理

错误统一为：

```json
{
  "error": {
    "type": "invalid_request_error",
    "code": "invalid_video_request",
    "message": "..."
  }
}
```

常见代码：

- `invalid_api_key`：API Key 缺失或无效。
- `invalid_video_request`：JSON、字段或能力组合无效。
- `video_model_not_found`：模型未配置或未绑定到当前渠道/分组。
- `video_ability_unsupported`：模型不支持请求的能力。
- `video_reference_unsupported`：参考素材类型、数量或时长不符合模型限制。
- `video_reference_mode_requires_media`：全能参考模式没有素材。
- `reference_material_failed`：素材转存或探测失败。
- `upstream_timeout`、`upstream_unavailable`：上游暂时不可用，可按幂等键重试。

## 7. 渠道、分组与平台配置

视频请求必须能通过 `model` 找到可用渠道。Yingzo Agent 是多平台聚合分组，平台类型应保持为多平台路由；它不能被定义为 OpenAI 单平台，否则其它平台渠道会被错误附加 OpenAI 标识，导致渠道定价分组无法正确保存。

配置检查顺序：

1. 模型目录中存在该视频模型及其能力合同。
2. 渠道已启用视频能力并绑定正确上游平台。
3. 渠道已加入对应分组，且分组平台与渠道平台匹配。
4. 该分组有可用账户和余额/配额。
5. 对 Seedance 参考模式，确认模型允许相应的图片、视频、音频数量和时长。

Agent 分组的 `/v1/videos` 请求必须进入原生视频处理链路；如果被路由到 Grok 专用处理器，会返回 `Videos API is not supported for this platform` 或 HTTP 404。

## 8. Yingzo 客户端字段映射

Yingzo 客户端内部字段到 yingzo-api 字段的映射如下：

| 客户端字段 | API 字段 |
| --- | --- |
| `modelId` | `model` |
| `prompt` / `promptChinese` | `prompt` |
| `durationSeconds` | `duration` |
| `videoResolution` | `resolution` |
| `aspectRatio` | `aspect_ratio` |
| `generateAudio` | `generate_audio` |
| `referenceAssetVersionIds` | 上传后写入 `content` |
| 单张图片 | `video_image_to_video` + `first_frame` |
| 两张图片 | `video_start_end_to_video` + `first_frame/last_frame` |
| 多媒体或强制参考模式 | `video_reference_to_video` |

客户端不直接信任用户输入的能力码，而是根据素材数量和 `forceReferenceMode` 编译能力；这样可以避免把首尾帧请求误发成普通参考请求。

## 9. 最小接入检查清单

- [ ] API Key 可访问 `/v1/videos`。
- [ ] 模型已配置到正确渠道和多平台分组。
- [ ] 文生请求可创建并轮询完成。
- [ ] 单图请求生成 `first_frame`。
- [ ] 双图请求生成 `first_frame` 与 `last_frame`。
- [ ] 参考模式能上传并识别图片、视频、音频。
- [ ] 客户端使用 `Idempotency-Key`，并处理 `Retry-After`。
- [ ] 成功任务通过 `/content` 下载，而不是直接依赖上游 URL。
