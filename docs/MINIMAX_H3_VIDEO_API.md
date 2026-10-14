# MiniMax H3 / H3 Max 统一视频接口

使用本平台 API Key 和域名，下游无需接触 APIMart 密钥或任务格式。

## 模型参数

| model | resolution（区分大小写） | duration（必填，整数秒） |
| --- | --- | --- |
| minimax-h3 | 768p、2K（默认） | 4–15 |
| minimax-h3-max | 480p、768p（默认）、1080p | 5–15 |

- prompt：所有模式必填，1–7000 字符；也可使用 content 中的 text 项，顶层 prompt 优先。
- ratio：可选 21:9、16:9、4:3、1:1、3:4、9:16、adaptive，兼容 aspect_ratio。文生默认 16:9；首尾帧由图片决定；参考模式不传时上游自适应。
- generate_audio：省略或 true，输出自带音轨，不支持 false。
- watermark：可选布尔值。H3 Max 1080p 必须省略，包括 false。
- nsfw_check：可选布尔值。

## 创建与查询

创建使用 POST /v1/videos，查询使用 GET /v1/videos/{id}。

```bash
curl "$BASE_URL/v1/videos" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: your-unique-request-key' \
  -d '{"model":"minimax-h3","prompt":"海边日落，镜头缓慢推进","duration":5,"resolution":"2K","ratio":"16:9"}'
```

HTTP 200 返回本平台任务，示意：

```json
{
  "id": "video_example",
  "object": "video",
  "model": "minimax-h3",
  "status": "queued",
  "refund_status": "not-applicable",
  "created_at": 1791900000
}
```

```bash
curl "$BASE_URL/v1/videos/$VIDEO_ID" -H "Authorization: Bearer $API_KEY"
```

每 5–10 秒轮询，建议最多等待 15 分钟。状态为 queued、processing、completed、failed、cancelled。completed 时读取 video_url；失败时读取 error.code、error.message、refund_status（pending / refunded）。必须使用有权访问该任务的 Key。链接有效期由平台交付配置决定，请及时保存成片。

## 统一素材结构

统一使用 content 数组，不传上游 image_urls、image_with_roles、first_frame_image 等字段。

| 类型 | type | URL 对象 | role |
| --- | --- | --- | --- |
| 首帧 | image_url | image_url.url | first_frame |
| 尾帧 | image_url | image_url.url | last_frame |
| 参考图 | image_url | image_url.url | reference_image |
| 参考视频 | video_url | video_url.url | reference_video |
| 参考音频 | audio_url | audio_url.url | reference_audio |

首尾帧各最多 1 张，支持仅尾帧。参考模式最多 9 张图片、3 段视频、3 段音频。首尾帧与参考素材互斥，音频不能单独使用。图片必须明确 role；通常无需传 ability_code。

首尾帧示例（仅首帧或尾帧时删除另一项）：

```json
{
  "model": "minimax-h3-max",
  "prompt": "从清晨平滑过渡到日落",
  "duration": 8,
  "resolution": "768p",
  "content": [
    {"type":"image_url","role":"first_frame","image_url":{"url":"https://cdn.example.com/start.png"}},
    {"type":"image_url","role":"last_frame","image_url":{"url":"https://cdn.example.com/end.png"}}
  ]
}
```

多模态示例：

```json
{
  "model": "minimax-h3-max",
  "prompt": "参考图中的人物按照参考视频的动作奔跑，配合参考音频节奏",
  "duration": 6,
  "resolution": "1080p",
  "content": [
    {"type":"image_url","role":"reference_image","image_url":{"url":"https://cdn.example.com/person.png"}},
    {"type":"video_url","role":"reference_video","video_url":{"url":"https://cdn.example.com/motion.mp4"}},
    {"type":"audio_url","role":"reference_audio","audio_url":{"url":"https://cdn.example.com/voice.mp3"}}
  ]
}
```

使用公网 HTTP(S) 素材地址。图片支持 JPG/JPEG/PNG/WEBP/HEIC/HEIF，单张 ≤30MB；视频 MP4/MOV、H.264/H.265，单段 ≤50MB；音频 WAV/MP3，单段 ≤15MB。视频与音频单段 2–15 秒，各自总时长 ≤15 秒。图片/视频宽高 256–5760px，宽高比 0.4–2.5；视频帧率 23.976–60fps。素材格式及音频时长由上游最终校验。不支持中间帧、Regeneration、webhook，请使用统一任务查询。

## 计费

每条输出基础费用：

```text
（生成秒数 + 参考视频计费秒数）× 当前分辨率每秒单价
+ max(0, 输入图片数 − 免费图片数) × 当前分辨率每秒单价
```

参考视频按平台探测时长，沿用每段向上取整后求和规则，不采信下游 duration_seconds。图片包含首帧、尾帧、参考图；H3 默认前 5 张免费，H3 Max 默认前 2 张免费，可由分组覆盖。参考音频免费。普通视频分组再乘分组倍率；Agent 分组使用最终单价，不重复加倍率。

例如 H3 Max：输出 6 秒、参考视频计费 4 秒、5 张参考图，免费 2 张，当前分辨率单价 0.20 元，基础费用为 (6+4)×0.20+(5−2)×0.20=2.60 元。

图片费用纳入预估和任务总金额，随任务扣费；失败按保存的任务金额退款，不因后续改价重新计算。

## 运营配置

1. 发布前执行迁移 263_video_input_image_pricing.sql 和 264_agent_video_input_image_pricing.sql。
2. 视频账号选择 APIMart，填写 Key。默认 Base URL：https://api.apimart.ai；API Path：/v1/tasks。此路径是查询集合，适配器将创建转换到 /v1/videos/generations。
3. 勾选 minimax-h3 / minimax-h3-max，关联分组，配置实际可用分辨率和时长。默认上游名分别是 MiniMax-H3 / MiniMax-H3-Max。
4. 后台“分组 → 新增/编辑 → 视频计费规则”按模型、分辨率配置每秒价格；H3/H3 Max 只需在同一模型的一行设置一次免费输入图片数，其他分辨率行自动沿用。超额图片直接使用当前分辨率的每秒单价，不配置独立图片单价。免费数留空按模型默认，0 表示所有图片收费。
5. Yingzo Agent 页面的视频模型表格中，H3/H3 Max 各有一个“免费参考图片数”配置，分辨率列仍填写输出每秒单价。Agent 请求会直接读取所选分辨率单价计算超额图片费用，无需额外的 video_pricing_rules。

规则示例，价格仅示意：

```json
{"model_code":"minimax-h3-max","resolution":"768p","credits_per_second":0.2,"free_input_images":2,"reference_video_multiplier":1,"enabled":true}
```

上线后应使用实际账号验证额度、模型权限和公网素材。开发验证使用模拟上游，未消耗真实生成额度。

来源：[H3 文档](https://docs.apimart.ai/cn/api-reference/videos/minimax-h3/generation)、[H3 Max 文档](https://docs.apimart.ai/cn/api-reference/videos/minimax-h3/max)，核对日期 2026-10-14。
