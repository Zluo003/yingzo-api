# Suno V6 全功能下游接口参考

配合 [接入指南](SUNO_AGENT_INTEGRATION.md) 使用。基于 2026-10-14 核对的 [APIMart Suno 目录](https://docs.apimart.ai/cn/api-reference/audios/suno/overview)。调用 Yingzo 地址（例如 `https://yingzo.art`），使用 Yingzo API Key；不要把 APIMart 凭证发给客户端。

## 通用协议与计费

所有操作均为 JSON POST，返回 Yingzo `202` 任务对象，统一轮询 `GET /v1/music/tasks/{task_id}`。返回的是 `musictask_…`，不能传上游 APIMart 任务 ID。所有 POST 支持 `Idempotency-Key`，包括免费工具；同一个幂等键不能跨操作复用。查询免费。完整受理、查询、失败退款与断线恢复示例见接入指南。

基础生成按 `instrumental=true` 使用 `instrumental` 价格档，否则使用 `song`，与 `custom` 独立。其它操作使用端点末段或下表的指定映射作为价格档，例如 `extend`、`create_model`、`download`。`v6` 使用基础档；`v6-wild` / `v6-mini` 加后缀，例如 `extend:v6-mini`；自定义模型加 `:custom`，例如 `song:custom`。Max 不新增档位，按对应价格乘 2。`variety="max"` 不会开启 Max 计费。没有版本参数的工具只有操作档位。每档必须明确配置并启用，不回退其它档位。`concat` 上游免费，下游免费需显式配置 `concat=0` 并启用。多格式下载按一次任务收费。

`custom` 表示歌词/灵感模式，`instrumental` 表示无人声。为兼容旧客户端，主生成省略 `instrumental` 时仍默认 `!custom`，省略 `audio_format` 时仍为 `wav`；灵感生成歌曲须显式发送 `instrumental:false`。`custom:true,instrumental:true` 支持自定义纯音乐，可省略歌词。`version` 默认 `v6`，与 `custom_model_id` 互斥；后者也与 `persona_id` 互斥。`model` 只能是 `suno-v6` 或省略。

未知字段、null、错误类型和越界参数返回 400。字段是否生效以端点说明为准，灵感模式下自定义文本字段由上游忽略。权重 0 有效；文本按 Unicode 字符计数。主生成用 `style` / `weirdness_constraint`，编辑用 `tags` / `weirdness`；支持旧别名的操作不能同时发送两个权重名字。歌词换行和段落标记保持原样。

## 源任务与资源归属

- `task_id` 必须属于同一 Yingzo API Key 且已完成。`audio_index` 从 1 开始，默认 1，按原始 `result.music[]` 顺序引用，不能用 audio_id、music_id 或音频 URL 替代。
- `mashup` 接收恰好两个 task_ids，audio_indexes 省略时都取 1。所有依赖必须在同一上游账号，服务端沿用该账号；不可用时不切到其它账号。
- addVocals、addInstrumental、sample 的源必须来自 uploadTask；concat 的源必须来自 extend / uploadExtend。
- 时间范围不得超过已知源时长；续写起点至少 1 秒，严格小于源时长。
- custom_model_id / persona_id 必须来自同一 Key 已完成的 createModel / persona 结果。服务端校验归属并保持账号一致，不能直接导入外部 APIMart 资源 ID。
- 引用受本地结果保留时间限制。uploadTask 可只返回 audio_id，仍可用于后续操作。上传只接受公网 HTTP(S) 音频直链，不接收 multipart、本地文件路径或 blob URL。

## 全部端点与参数

路径相对 Yingzo 基础地址。参数表省略通用可选 `model:"suno-v6"`；uploadTask 也接受该下游字段，但不会发给上游。表内 task_id 均指 Yingzo 任务；约束可能由本地或上游校验。上游拒绝会体现在已受理任务的失败状态，并退款。

### 添加伴奏 — `addInstrumental`

`POST /v1/music/generations/addInstrumental`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | `POST /v1/music/generations/uploadTask` 返回的上传任务 ID。普通生成任务不能使用；缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`；省略时默认 `v6`。使用 `custom_model_id` 时省略本字段。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID，与 `version` 互斥。 |
| `custom` | boolean | 否 | `true`=自定义模式（用 `prompt` 作歌词）；`false`=灵感模式（用 `gpt_description`）；不传时按内容推断（见上方 Warning）。 |
| `prompt` | string | 否 | 歌词，`custom=true` 时生效（灵感模式下会被忽略）。 |
| `gpt_description` | string | 否 | 灵感提示词，`custom=false` 时**必填**（缺了提交期直接 400、不扣费）。 |
| `title` | string | 否 | 标题。**仅 `custom=true` 时生效**。 |
| `tags` | string | 否 | 风格标签。**仅 `custom=true` 时生效**。 |
| `negative_tags` | string | 否 | 要排除的风格标签。**仅 `custom=true` 时生效**。 |
| `style_weight` | number | 否 | 风格权重，`0.00`–`1.00`（越界提交期直接返回 `400`）。**仅 `custom=true` 时生效**。 |
| `weirdness` | number | 否 | 创意权重，`0.00`–`1.00`。兼容旧名 `weirdness_constraint`；新请求请使用 `weirdness`。 |
| `audio_weight` | number | 否 | 音频权重，`0.00`–`1.00`。**仅 `custom=true` 时生效**。 |
| `vocal_gender` | string | 否 | 人声性别：`Male` / `Female`。**两种模式都生效**。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式。启用时要求自定义模式，并按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。本接口不支持目标时长字段。 |
| `weirdness_constraint` | number | 否 | 兼容 weirdness 的旧字段名，不能与 weirdness 同时提供。 |

### 添加音轨 (add stem) — `addStem`

`POST /v1/music/generations/addStem`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`；省略时默认 `v6`。使用 `custom_model_id` 时省略本字段。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID，与 `version` 互斥。 |
| `custom` | boolean | 否 | `true`=自定义模式（用 `prompt` 作歌词）；`false`=灵感模式（用 `gpt_description`）；不传时按内容推断（见上方 Warning）。 |
| `prompt` | string | 否 | 歌词，`custom=true` 时生效（灵感模式下会被忽略）。 |
| `gpt_description` | string | 否 | 灵感提示词，`custom=false` 时**必填**（缺了提交期直接 400、不扣费）。 |
| `title` | string | 否 | 标题。**仅 `custom=true` 时生效**。 |
| `tags` | string | 否 | 风格标签。**仅 `custom=true` 时生效**。 |
| `negative_tags` | string | 否 | 要排除的风格标签。**仅 `custom=true` 时生效**。 |
| `style_weight` | number | 否 | 风格权重，`0.00`–`1.00`（越界提交期直接返回 `400`）。**仅 `custom=true` 时生效**。 |
| `weirdness` | number | 否 | 创意权重，`0.00`–`1.00`。兼容旧名 `weirdness_constraint`；新请求请使用 `weirdness`。 |
| `audio_weight` | number | 否 | 音频权重，`0.00`–`1.00`。**仅 `custom=true` 时生效**。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式。启用时要求自定义模式，并按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。本接口不支持目标时长字段。 |
| `weirdness_constraint` | number | 否 | 兼容 weirdness 的旧字段名，不能与 weirdness 同时提供。 |

### 添加人声 — `addVocals`

`POST /v1/music/generations/addVocals`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | `POST /v1/music/generations/uploadTask` 返回的上传任务 ID。普通生成任务不能使用；缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`；省略时默认 `v6`。使用 `custom_model_id` 时省略本字段。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID，与 `version` 互斥。 |
| `custom` | boolean | 否 | `true`=自定义模式（用 `prompt` 作歌词）；`false`=灵感模式（用 `gpt_description`）；不传时按内容推断（见上方 Warning）。 |
| `prompt` | string | 否 | 歌词，`custom=true` 时生效（灵感模式下会被忽略）。 |
| `gpt_description` | string | 否 | 灵感提示词，`custom=false` 时**必填**（缺了提交期直接 400、不扣费）。 |
| `title` | string | 否 | 标题。**仅 `custom=true` 时生效**。 |
| `tags` | string | 否 | 风格标签。**仅 `custom=true` 时生效**。 |
| `negative_tags` | string | 否 | 排除的风格标签。**仅 `custom=true` 时生效**。 |
| `style_weight` | number | 否 | 风格权重，`0.00`–`1.00`（超出范围提交期直接返回 `400`）。**仅 `custom=true` 时生效**。 |
| `weirdness` | number | 否 | 创意度权重，`0.00`–`1.00`。兼容旧名 `weirdness_constraint`；新请求请使用 `weirdness`。 |
| `audio_weight` | number | 否 | 音频权重，`0.00`–`1.00`。**仅 `custom=true` 时生效**。 |
| `vocal_gender` | string | 否 | 人声性别：`Male` / `Female`。**两种模式均生效**。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式。启用时要求自定义模式，并按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。本接口不支持目标时长字段。 |
| `weirdness_constraint` | number | 否 | 兼容 weirdness 的旧字段名，不能与 weirdness 同时提供。 |

### 调整速度 — `adjustSpeed`

`POST /v1/music/generations/adjustSpeed`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `speed` | number | 是 | 倍速，范围 `0.25`～`4`，如 `1.25`。缺失或越界提交期直接返回 `400`。 |
| `keep_pitch` | boolean | 否 | 变速时是否保持原音高（默认 `true`）。 |
| `title` | string | 否 | 标题（默认 `Untitled`）。 |

### 歌词时间轴 — `alignedLyrics`

`POST /v1/music/generations/alignedLyrics`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |

### BPM 分析 — `bpm`

`POST /v1/music/generations/bpm`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |

### 完整歌曲合成 / 拼接 — `concat`

`POST /v1/music/generations/concat`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 源任务的 `task_id`——**必须是 extend（续写）产生的分段**（见上方 Warning）。缺失、非 extend 产物或无法解析源时，提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。 |

### 风格翻唱 — `coverSong`

`POST /v1/music/generations/coverSong`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`；缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based：1=第 1 首；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`；省略时默认 `v6`。使用 `custom_model_id` 时省略本字段。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID。与 `version`、`persona_id` 互斥。 |
| `custom` | boolean | 否 | `true`=自定义模式（用 `prompt` 作歌词）；`false`=灵感模式（用 `gpt_description`）；不传时按内容推断（见上方 Warning）。 |
| `prompt` | string | 否 | 歌词，`custom=true` 时生效（灵感模式下会被忽略）。 |
| `gpt_description` | string | 否 | 灵感提示词，`custom=false` 时**必填**（缺了提交期直接 400、不扣费）。 |
| `title` | string | 否 | 标题。**仅 `custom=true` 时生效**。 |
| `tags` | string | 否 | 目标风格标签。**仅 `custom=true` 时生效**。 |
| `negative_tags` | string | 否 | 排除的风格标签。**仅 `custom=true` 时生效**。 |
| `style_weight` | number | 否 | 风格权重，`0.00`–`1.00`（超出范围提交期直接返回 `400`）。**仅 `custom=true` 时生效**。 |
| `weirdness` | number | 否 | 创意度权重，`0.00`–`1.00`。兼容旧名 `weirdness_constraint`；新请求请使用 `weirdness`。 |
| `audio_weight` | number | 否 | 音频权重，`0.00`–`1.00`。**仅 `custom=true` 时生效**。 |
| `vocal_gender` | string | 否 | 人声性别：`Male` / `Female`。**两种模式均生效**。 |
| `persona_id` | string | 否 | Persona 风格 ID。**仅 `custom=true` 时生效**，与 `custom_model_id` 互斥。 |
| `duration_s` | integer | 否 | 目标生成时长，范围 10–360 秒，仅 `custom=true` 时可用。实际时长以结果为准。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。可省略。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式。启用时要求自定义模式，并按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。 |
| `weirdness_constraint` | number | 否 | 兼容 weirdness 的旧字段名，不能与 weirdness 同时提供。 |

### 创建自定义模型 — `createModel`

`POST /v1/music/generations/createModel`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是 | 自定义模型名称。去掉首尾空白后不能为空。 |
| `audio_urls` | string[] | 是 | 6–24 条公网可访问的 HTTP(S) 音频直链，建议使用 MP3、WAV 或 M4A。每条 URL 都应直接返回音频文件，不能是需要登录的网页。 |

### 创建语音 — `createVoice`

`POST /v1/music/generations/createVoice`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `audio_url` | string | 是 | 源音轨的公网可访问 URL，系统据此提取音色。**仅接受 MP3 / WAV。** 缺失返回 400 `audio_url cannot be empty`。 |

### 裁剪音频 — `crop`

`POST /v1/music/generations/crop`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `start_s` | number | 是 | 裁剪起点（秒），必须大于等于 0；支持小数秒。 |
| `end_s` | number | 是 | 裁剪终点（秒）。必须大于 `start_s`，且不能超过已知源时长；支持小数秒。 |

### 续写延长 — `extend`

`POST /v1/music/generations/extend`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`；缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `continue_at` | number | 是 | 从第几秒开始续写，必须大于等于 1 且小于源音轨实际时长。支持小数秒。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`；省略时默认 `v6`。使用 `custom_model_id` 时省略本字段。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID。与 `version`、`persona_id` 互斥。 |
| `custom` | boolean | 否 | `true`=按 `prompt` 歌词续写；`false`=灵感续写。省略时默认 `true`。 |
| `prompt` | string | 否 | 续写歌词，`custom=true` 时生效。 |
| `gpt_description` | string | 否 | 灵感提示词，仅在显式传入 `custom=false` 时生效，用于引导续写方向。 |
| `title` | string | 否 | 标题。**仅 `custom=true` 时生效**。 |
| `tags` | string | 否 | 风格标签。**仅 `custom=true` 时生效**。 |
| `negative_tags` | string | 否 | 排除的风格标签。**仅 `custom=true` 时生效**。 |
| `vocal_gender` | string | 否 | 人声性别：`Male` / `Female`。**两种模式均生效**。 |
| `style_weight` | number | 否 | 风格权重，`0.00`–`1.00`（超出范围提交期直接返回 `400`）。**仅 `custom=true` 时生效**。 |
| `weirdness` | number | 否 | 创意度权重，`0.00`–`1.00`。兼容旧名 `weirdness_constraint`；新请求请使用 `weirdness`。 |
| `audio_weight` | number | 否 | 音频权重，`0.00`–`1.00`。**仅 `custom=true` 时生效**。 |
| `auto_lyrics` | boolean | 否 | `true`=对输入歌词进行二次创作。**仅 `custom=true` 时生效**。 |
| `persona_id` | string | 否 | Persona 风格 ID。**仅 `custom=true` 时生效**，与 `custom_model_id` 互斥。 |
| `duration_s` | integer | 否 | 目标生成时长，范围 10–360 秒，仅 `custom=true` 时可用。实际时长以结果为准。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。可省略。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式。启用时要求 `custom=true`，并按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。 |
| `weirdness_constraint` | number | 否 | 兼容 weirdness 的旧字段名，不能与 weirdness 同时提供。 |

### 淡入 — `fadeIn`

`POST /v1/music/generations/fadeIn`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `duration_s` | number | 是 | 淡入时长（秒）。必须大于 0，且不能超过已知源时长；支持小数秒。 |
| `title` | string | 否 | 标题（默认 `Untitled`）。 |

### 淡出 — `fadeOut`

`POST /v1/music/generations/fadeOut`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `duration_s` | number | 是 | 淡出时长（秒）。必须大于 0，且不能超过已知源时长；支持小数秒。 |
| `title` | string | 否 | 标题（默认 `Untitled`）。 |

### 生成音乐视频 (MV) — `generateMp4`

`POST /v1/music/generations/generateMp4`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |

### 生成音乐 — `generation`

`POST /v1/music/generations`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `custom` | boolean | 否 | `false`=灵感模式；`true`=自定义模式（`prompt` 作歌词）。默认 `false`。 |
| `instrumental` | boolean | 否 | `true`=纯音乐、无人声。默认 `false`。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`。与 `custom_model_id` 至少提供一个；使用自定义模型时省略本字段。 |
| `custom_model_id` | string | 否 | 创建自定义模型任务返回的完整 UUID。与 `version`、`persona_id` 互斥；提供本字段后按自定义模型价格档计费。 |
| `prompt` | string | 否 | 灵感提示词 / 歌词。**`custom=false` 时必填**且不超过 3000 个字符；`custom=true` 且 `instrumental=false` 时**必填**且不超过 5000 个字符；自定义纯音乐可省略。 |
| `title` | string | 否 | 标题（自定义模式），不超过 80 个字符。**`custom=false`（灵感模式）时忽略**。 |
| `style` | string | 否 | 风格标签（自定义模式），不超过 1000 个字符。**`custom=false`（灵感模式）时忽略**。 |
| `negative_tags` | string | 否 | 负向风格标签（不希望出现的风格）。**仅 `custom=true` 时生效**。 |
| `auto_lyrics` | boolean | 否 | `true`=对输入歌词进行二次创作。**仅 `custom=true` 时生效**。 |
| `persona_id` | string | 否 | Persona 风格 ID。**仅 `custom=true` 时生效**，与 `custom_model_id` 互斥。 |
| `vocal_gender` | string | 否 | 人声性别：`Male` / `Female`（也接受 `m` / `f` / `male` / `female`，后端自动归一）。**两种模式均生效**。 |
| `style_weight` | number | 否 | 风格权重，`0.00`–`1.00`。两种模式下都会校验并生效。 |
| `weirdness_constraint` | number | 否 | 创意度，`0.00`–`1.00`。两种模式下都会校验并生效。 |
| `audio_weight` | number | 否 | 音频权重，`0.00`–`1.00`。两种模式下都会校验并生效。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。可省略，没有固定默认值。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式。启用时要求 `custom=true`，并按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。省略时由服务自动选择。 |
| `duration` | integer | 否 | 目标生成时长，范围 10–360 秒，仅 `custom=true` 时可用。实际成品时长以任务结果为准。 |

### 灵感生成 (inspo) — `inspo`

`POST /v1/music/generations/inspo`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `audio_urls` | array | 是 | 1–4 个可公开访问的音频 URL 数组。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`；省略时默认 `v6`。使用 `custom_model_id` 时省略本字段。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID，与 `version` 互斥。 |
| `prompt` | string | 否 | 歌词 / 内容。 |
| `title` | string | 否 | 标题。 |
| `tags` | string | 否 | 风格标签。 |
| `negative_tags` | string | 否 | 排除的风格标签。 |
| `style_weight` | number | 否 | 风格权重，`0.00`–`1.00`。 |
| `weirdness` | number | 否 | 创意度权重，`0.00`–`1.00`。兼容旧名 `weirdness_constraint`；新请求请使用 `weirdness`。 |
| `audio_weight` | number | 否 | 音频权重，`0.00`–`1.00`。 |
| `vocal_gender` | string | 否 | 人声性别：`Male` / `Female`。 |
| `auto_lyrics` | boolean | 否 | `true`=对输入歌词进行二次创作。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。可省略。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式；启用后按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。 |
| `weirdness_constraint` | number | 否 | 兼容 weirdness 的旧字段名，不能与 weirdness 同时提供。 |

### 生成歌词 — `lyrics`

`POST /v1/music/generations/lyrics`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `prompt` | string | 是 | 想生成什么歌词的描述，去掉首尾空白后不能为空。 |
| `lyrics_model` | string | 否 | 歌词模型：`classic` / `remi`。不传时使用服务默认设置。 |

### 生成混搭 (mashup) — `mashup`

`POST /v1/music/generations/mashup`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_ids` | string[] | 是 | 2 个源音轨所属任务的 `task_id` 数组（须**恰好 2 个**；数量不对提交期直接返回 `400`）。 |
| `audio_indexes` | integer[] | 否 | 与 `task_ids` 平行的数组，指定各任务取结果 `result.music[]` 第几首（1-based，默认都取 `1`）。 |
| `instrumental` | boolean | 否 | 是否纯器乐（`true`=无人声）；不传默认 `false`（要人声）。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`；省略时默认 `v6`。使用 `custom_model_id` 时省略本字段。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID。与 `version`、`persona_id` 互斥。 |
| `custom` | boolean | 否 | `true`=自定义模式（用 `prompt` 作歌词）；`false`=灵感模式（用 `gpt_description`）；不传时按内容推断（见上方 Warning）。 |
| `prompt` | string | 否 | **条件必填**：`custom=true && instrumental=false` 时必须提供歌词；灵感模式下不生效，但已提交值仍须满足长度要求。 |
| `gpt_description` | string | 否 | 灵感提示词，`custom=false` 时**必填**（缺了提交期直接 400、不扣费）。 |
| `title` | string | 否 | 标题。**仅 `custom=true` 时生效**。 |
| `tags` | string | 否 | 风格标签。**仅 `custom=true` 时生效**。 |
| `negative_tags` | string | 否 | 要排除的风格标签。**仅 `custom=true` 时生效**。 |
| `auto_lyrics` | boolean | 否 | `true`=对输入歌词进行二次创作。**仅 `custom=true` 时生效**。 |
| `style_weight` | number | 否 | 风格权重，`0.00`–`1.00`（越界提交期直接返回 `400`）。**仅 `custom=true` 时生效**。 |
| `weirdness` | number | 否 | 创意权重，`0.00`–`1.00`。兼容旧名 `weirdness_constraint`；新请求请使用 `weirdness`。 |
| `audio_weight` | number | 否 | 音频权重，`0.00`–`1.00`。**仅 `custom=true` 时生效**。 |
| `vocal_gender` | string | 否 | 人声性别：`Male` / `Female`。**两种模式都生效**。 |
| `persona_id` | string | 否 | Persona 风格 ID。**仅 `custom=true` 时生效**，与 `custom_model_id` 互斥。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。可省略。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式。启用时要求自定义模式，并按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。本接口不支持目标时长字段。 |
| `weirdness_constraint` | number | 否 | 兼容 weirdness 的旧字段名，不能与 weirdness 同时提供。 |

### 生成 MIDI — `midi`

`POST /v1/music/generations/midi`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |

### Persona — `persona`

`POST /v1/music/generations/persona`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `name` | string | 是 | Persona 名称；缺失直接返回 `400`。 |
| `describe` | string | 否 | 描述。 |
| `styles` | string | 否 | 风格。 |
| `vocal_start_s` | number | 否 | 人声片段起点（秒），必须大于等于 0。建议与 `vocal_end_s` 成对提供。 |
| `vocal_end_s` | number | 否 | 人声片段终点（秒），必须大于起点且不能超过已知源时长。 |

### 母带优化 — `remaster`

`POST /v1/music/generations/remaster`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`；缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `variation_category` | string | 否 | 改编强度：`subtle` / `normal` / `high`。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。 |

### 删除片段 — `removeSection`

`POST /v1/music/generations/removeSection`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `start_s` | number | 是 | 删除起点（秒），必须大于等于 0；支持小数秒。 |
| `end_s` | number | 是 | 删除终点（秒）。必须大于 `start_s`，且不能超过已知源时长；支持小数秒。 |

### 段落替换 — `replaceMusic`

`POST /v1/music/generations/replaceMusic`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `infill_lyrics` | string | 否 | 替换段歌词。 |
| `start_s` | number | 是 | 替换起点（秒）。缺失直接返回 `400`。 |
| `end_s` | number | 是 | 替换终点（秒）。必须满足 `0 ≤ start_s < end_s`；源时长已知时不能越界。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`；省略时默认 `v6`。使用 `custom_model_id` 时省略本字段。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID，与 `version` 互斥。 |
| `prompt` | string | 否 | 上下文歌词。 |
| `title` | string | 否 | 标题。 |
| `tags` | string | 否 | 风格标签。 |
| `negative_tags` | string | 否 | 要排除的风格标签。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。可省略。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式；启用后按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。本接口不支持目标时长字段。 |

### 样本转歌曲 (sample) — `sample`

`POST /v1/music/generations/sample`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`，必须为 `uploadTask` 上传任务。缺失、不是上传任务或无法解析源时，提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based：1=第 1 首；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `start_s` | number | 是 | 采样起点（秒）。缺失直接返回 `400`。 |
| `end_s` | number | 是 | 采样终点（秒）。必须满足 `0 ≤ start_s < end_s`；源时长已知时不能越界。 |
| `instrumental` | boolean | 否 | 是否纯器乐（`true`=无人声）；不传默认 `false`（要人声）。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`；省略时默认 `v6`。使用 `custom_model_id` 时省略本字段。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID，与 `version` 互斥。 |
| `custom` | boolean | 否 | `true`=自定义模式（用 `prompt` 作歌词）；`false`=灵感模式（用 `gpt_description`）；不传时按内容推断（见上方 Warning）。 |
| `prompt` | string | 否 | **条件必填**：`custom=true && instrumental=false` 时必须提供歌词；灵感模式下不生效，但已提交值仍须满足长度要求。 |
| `gpt_description` | string | 否 | 灵感提示词，`custom=false` 时**必填**（缺了提交期直接 400、不扣费）。 |
| `title` | string | 否 | 标题。**仅 `custom=true` 时生效**。 |
| `tags` | string | 否 | 风格标签。**仅 `custom=true` 时生效**。 |
| `negative_tags` | string | 否 | 要排除的风格标签。**仅 `custom=true` 时生效**。 |
| `auto_lyrics` | boolean | 否 | `true`=对输入歌词进行二次创作。**仅 `custom=true` 时生效**。 |
| `style_weight` | number | 否 | 风格权重，`0.00`–`1.00`（越界提交期直接返回 `400`）。**仅 `custom=true` 时生效**。 |
| `weirdness` | number | 否 | 创意权重，`0.00`–`1.00`。兼容旧名 `weirdness_constraint`；新请求请使用 `weirdness`。 |
| `audio_weight` | number | 否 | 音频权重，`0.00`–`1.00`。**仅 `custom=true` 时生效**。 |
| `vocal_gender` | string | 否 | 人声性别：`Male` / `Female`。**两种模式都生效**。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。可省略。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式。启用时要求自定义模式，并按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。本接口不支持目标时长字段。 |
| `weirdness_constraint` | number | 否 | 兼容 weirdness 的旧字段名，不能与 weirdness 同时提供。 |

### 音效生成 — `sounds`

`POST /v1/music/generations/sounds`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`；省略时默认 `v6`。 |
| `prompt` | string | 是 | 音效文本描述；缺失时提交期直接返回 `400`（不扣费）。建议尽量使用英文提示词，效果更佳。 |
| `type` | string | 否 | 音效类型：`one-shot`（默认，单次）/ `loop`（可循环）。 |
| `bpm` | integer | 否 | 速度，`1`–`300`；超出范围提交期直接返回 `400`。 |
| `key` | string | 否 | 调性枚举：大调 `C` / `C#` / `D` / `D#` / `E` / `F` / `F#` / `G` / `G#` / `A` / `A#` / `B`；小调在后面加 `m`（`Cm` / `C#m` / … / `Bm`）。仅支持升号（`#`）写法，降号（`Db` / `Eb` 等）与 `B#` 会返回 `400`（key param error）。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。 |

### 全量分轨 — `stemsAll`

`POST /v1/music/generations/stemsAll`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`；缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。本接口不发送 `stem_type`、`version` 或 Max 字段。 |

### 分轨提取 — `stems`

`POST /v1/music/generations/stems`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`；缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `stem_type` | string | 否 | 要提取的分轨类型。省略时默认使用 `lead_vocal`。常用值包括 `lead_vocal`、`backing_vocals`、`drum_kit`、`bass`、`piano` 和 `electric_guitar`。 |
| `audio_format` | string | 否 | 输出音频格式：`mp3` / `m4a` / `wav`。 |

### 上传音频并翻唱 — `uploadCover`

`POST /v1/music/generations/uploadCover`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `audio_url` | string | 是 | 公网可访问的 HTTP(S) 音频直链，建议原音频不超过 8 分钟。本地路径、`blob:` URL 和需要登录的页面链接不可用。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`。使用 `custom_model_id` 时省略。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID。与 `version`、`persona_id` 互斥。 |
| `custom` | boolean | 否 | 建议显式发送。`false` 使用 `gpt_description`；`true` 使用歌词和风格字段。 |
| `instrumental` | boolean | 否 | 是否生成纯音乐。`custom=true` 且本字段为 `false` 时，`prompt` 必填。 |
| `gpt_description` | string | 否 | 灵感描述，`custom=false` 时必填，不超过 3000 个字符。 |
| `prompt` | string | 否 | 自定义模式歌词，不超过 5000 个字符。 |
| `tags` | string | 否 | 风格描述，不超过 1000 个字符。 |
| `title` | string | 否 | 标题，不超过 80 个字符。 |
| `negative_tags` | string | 否 | 不希望出现的风格。 |
| `style_weight` | number | 否 | 风格权重，范围 0–1。 |
| `weirdness` | number | 否 | 创意度权重，范围 0–1。 |
| `audio_weight` | number | 否 | 音频权重，范围 0–1。 |
| `auto_lyrics` | boolean | 否 | 是否对输入歌词进行二次创作。 |
| `vocal_gender` | string | 否 | 人声性别：`Male` / `Female`。 |
| `persona_id` | string | 否 | Persona ID，与 `custom_model_id` 互斥。 |
| `duration_s` | integer | 否 | 目标生成时长，范围 10–360 秒，仅自定义模式可用。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式。启用时要求自定义模式，并按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出格式：`mp3` / `m4a` / `wav`。 |
| `weirdness_constraint` | number | 否 | 兼容 weirdness 的旧字段名，不能与 weirdness 同时提供。 |

### 上传音频并延伸 — `uploadExtend`

`POST /v1/music/generations/uploadExtend`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `audio_url` | string | 是 | 公网可访问的 HTTP(S) 音频直链，建议不超过 8 分钟。本地路径与 `blob:` URL 不可用。 |
| `continue_at` | number | 是 | 延伸起点，必须大于等于 1 秒，并严格小于源音频实际时长。 |
| `version` | string | 否 | 公共版本：`v6` / `v6-wild` / `v6-mini`。使用 `custom_model_id` 时省略。 |
| `custom_model_id` | string | 否 | 创建模型任务返回的完整 UUID。与 `version`、`persona_id` 互斥。 |
| `prompt` | string | 否 | 延伸段歌词，不超过 5000 个字符；省略时按纯音乐延伸语义使用。 |
| `tags` | string | 否 | 风格描述，不超过 1000 个字符。 |
| `title` | string | 否 | 标题，不超过 80 个字符。 |
| `negative_tags` | string | 否 | 不希望出现的风格。 |
| `style_weight` | number | 否 | 风格权重，范围 0–1。 |
| `weirdness` | number | 否 | 创意度权重，范围 0–1。 |
| `audio_weight` | number | 否 | 音频权重，范围 0–1。 |
| `auto_lyrics` | boolean | 否 | 是否对输入歌词进行二次创作。 |
| `vocal_gender` | string | 否 | 人声性别：`Male` / `Female`。 |
| `persona_id` | string | 否 | Persona ID，与 `custom_model_id` 互斥。 |
| `duration_s` | integer | 否 | 目标生成时长，范围 10–360 秒。实际成品时长以任务结果为准。 |
| `variety` | string | 否 | 风格变化程度：`off` / `normal` / `high` / `extra` / `max`。 |
| `max_mode` | boolean | 否 | 是否启用 Max 模式；启用后按普通售价的 2 倍计费。 |
| `audio_format` | string | 否 | 输出格式：`mp3` / `m4a` / `wav`。 |
| `weirdness_constraint` | number | 否 | 兼容 weirdness 的旧字段名，不能与 weirdness 同时提供。 |

### 上传音频 — `uploadTask`

`POST /v1/music/generations/uploadTask`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `audioFilePath` | string | 是 | 已上传到对象存储、可公开读取的音频 HTTP(S) 直链。本机路径与 `blob:` URL 不可用。 |

### 标签增强 — `upsampleTags`

`POST /v1/music/generations/upsampleTags`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `tags` | string | 是 | 待增强的风格标签；缺失时提交期直接返回 `400`（不扣费）。 |

### 提取 Vox（已弃用） — `vox`

`POST /v1/music/generations/vox`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 产出源音轨那次任务的 `task_id`。缺失或无法解析源时提交期直接返回 `400`。 |
| `audio_index` | integer | 否 | 引用源任务结果 `result.music[]` 中的第几首（1-based；默认 `1`；一次生成通常 2 首：索引 1 与 2）。 |
| `vocal_start_s` | number | 否 | 截取起点（秒）。 |
| `vocal_end_s` | number | 否 | 截取终点（秒）。 |

### 下载音频文件 — `download`

`POST /v1/music/generations/download`

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `task_id` | string | 是 | 生成源歌曲时返回的任务 ID。 源任务必须： * 属于当前账户 * 已完成 * 包含可下载的音轨 音乐生成、延长、翻唱、分轨等音频任务均可作为来源；歌词、BPM 等仅返回文本的任务无法下载。 |
| `audio_index` | integer | 否 | 要下载源任务结果 `result.music[]` 中的第几首歌曲。 * 从 `1` 开始计数 * 默认：`1` * 不得超过源任务实际包含的歌曲数量 |
| `formats` | string[] | 否 | 要下载的文件格式数组，至少包含一项。 可选值： * `mp3` * `m4a` * `wav` 支持同时请求多个格式；格式名大小写不敏感，重复值会自动去重。结果顺序与请求顺序一致。 |
| `format` | string | 否 | 只下载一种格式时，可使用此字段代替 `formats`。 示例：`"format": "mp3"` |

兼容 `POST /v1/music/generations/wav`，等价 download 固定 formats:["wav"]。新客户端使用 download。vox 已被上游弃用，仍提供兼容入口，新 Persona 流程直接调用 persona。

## 操作链示例

```bash
curl "$YINGZO_BASE_URL/v1/music/generations/extend" \
  -H "Authorization: Bearer $YINGZO_API_KEY" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: extension-unique-001' \
  -d '{"model":"suno-v6","task_id":"musictask_源任务","audio_index":1,"custom":true,"prompt":"[Verse]\n继续向前","tags":"acoustic pop","continue_at":30,"version":"v6","max_mode":true,"audio_format":"mp3"}'
```

保存返回的新 task_id，使用 poll_url 轮询至 completed，再用新任务 ID 调用 concat；不要继续使用原任务 ID。

## 结果结构

顶层保留 `object:"music.generation.task"`。新增操作携带 `operation`；基础生成可以省略。实际产物位于 `result`，没有上游 data 包装。

| 操作 | 结果读取 |
| --- | --- |
| 生成、续写、翻唱、音频编辑、分轨、音效 | result.music[] 及实际分轨结构；保留顺序和元数据，不能假设固定两首 |
| uploadTask | result.music[]，可能只有 audio_id，无需可播放 URL 即可引用 |
| download | result.files[] 内 format、url；兼容 result.wavUrl |
| generateMp4 | result.videoUrl，兼容 videos[] / music[].video_url |
| lyrics | result.lyrics[] 的 text/title/tags，兼容 result.text、字符串 lyrics、历史结构 |
| alignedLyrics | alignment[]、waveform_data，兼容 music 内 aligned_words / aligned_lyrics |
| bpm | avg_bpm、min_bpm、max_bpm，可能为数字或字符串 |
| midi | state、instruments[]、各乐器 notes[]，不保证提供 .mid 文件 |
| upsampleTags | result.upsampled_tags |
| persona / createModel | result.persona_id / result.model_id，保存用于后续请求 |
| createVoice / vox | 按实际对象读取；不保证固定 voice_id，不能当 persona_id |

已识别的音频、封面和视频 URL 保存到 Yingzo 产物存储，其余结构保留。权重 0、小数时长和音轨索引不会丢失。URL 受保留期限制，请及时保存。unknown 会继续轮询到期限，不自动重发收费 POST。没有取消端点，停止客户端轮询不会取消任务。创建模型可能耗时约 20 分钟，统一任务期限为 30 分钟。

额外错误：400 invalid_source 表示源类型、时间范围或账号不兼容；404 music_task_not_found 表示源任务/资源不存在或不属于当前 Key；410 music_result_expired 表示源已过期；503 music_account_unavailable 表示源账号不可用。其他错误及退款状态见接入指南。


## 指定音乐计费维度

以下维度均按次计费（billing_unit=request），在 Yingzo Agent 分组的音乐计费中独立配置价格和启用状态。计费标识与上游端点名称分开，接口路径保持不变。

| 功能 | 计费标识（resolution） | 操作端点末段 |
| --- | --- | --- |
| 上传音频翻唱 | upload_cover | uploadCover |
| 创建自定义音色模型 | create_model | createModel |
| 创建歌手 | persona | persona |
| 音效生成 | sounds | sounds |
| 上传音频 | upload | uploadTask |
| 样本转歌曲 | sample | sample |

支持版本选择的操作继续按版本后缀区分，例如 upload_cover:v6-mini；支持 Max 的操作仍按对应单次价格乘 2。创建自定义音色模型与后续使用模型生成分别计费。
