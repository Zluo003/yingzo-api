# Yingzo Agent Suno v6 对接指南

Suno 音乐生成使用 Yingzo 地址和 **Yingzo API Key**。该 Key 必须绑定系统 Yingzo Agent 分组。APIMart API Key 仅由管理员保存到服务端，不能发给客户端。下游模型统一为 `suno-v6`，上游仍为 APIMart。已接入 Suno 目录全部操作，完整参数与结果见 [全功能接口参考](SUNO_FULL_API_REFERENCE.md)。

## 管理员配置

1. 部署包含 `262_suno_music_tasks.sql` 的版本，启动时执行新增迁移。原有模型、价格、历史用量不变。
2. 文件服务 → 生成产物：配置本地目录或 S3/R2，以及公网地址、保留时间、容量和配额。运行环境需有项目内置或系统 `ffprobe`，用于验证下载的音频。
3. 开启独立的「音乐生成」开关。默认关闭；配置账号和价格完成之前无法生成。
4. 账号管理 → 添加音乐模型 → **Suno v6 · APIMart**：填写名称、备注、API Key、Base URL、并发、优先级和代理。Base URL 默认 `https://api.apimart.ai`，兼容末尾 `/v1`，只允许 HTTPS origin 或 `/v1`。绑定系统 Yingzo Agent 分组。编辑时 API Key 留空保留原值。
5. 账号测试只发送只读连接与鉴权请求。提示连接正常不等于实际生成成功，也不会产生音乐生成费用。
6. Yingzo Agent → 音乐计费：发现 `suno-v6` 后，配置各操作价格与启用状态（基础生成保留 `instrumental` / `song`），再启用模型并保存。所有档位单位均为 **credit / 次**。新发现模型默认停用，不填充售价。除 `uploadTask`（免费内部步骤）外，缺失或停用价格拒绝创建；明确配置为 `0` 并启用表示免费。

一次受理任务收取该操作、版本的固定价格；支持的操作开启 `max_mode=true` 时为对应价格的 2 倍，多首音轨不增加收费。Agent 价格为最终单价，不叠加文本、图片或视频倍率。任务受理时在同一数据库事务中落库、预扣余额和相关额度，冻结当时价格；成功结算，失败或超过 30 分钟全额退款。查询免费。后续调价不影响已受理任务。

用量记录展示 `music_task_id`、`music_mode`、`music_task_status` 和 `funds_event`。预扣记录成功后更新任务状态，退款另记负数金额事件；免费任务仍有状态记录。上游美元 `cost` 与 APIMart `credits_cost` 分开保存用于成本核对，不决定 Yingzo 下游售价。

关闭音乐开关只阻止新任务。已受理任务继续执行、查询、结算或退款。

## 模型发现与价格

以下所有示例中的 `https://yingzo.example` 应替换为部署的 Yingzo 地址(`https://yingzo.art`)，`YINGZO_API_KEY` 应替换为 Yingzo 凭证。

```bash
export YINGZO_BASE_URL='https://yingzo.example'
export YINGZO_API_KEY='你的 Yingzo API Key'
curl "$YINGZO_BASE_URL/v1/models" -H "Authorization: Bearer $YINGZO_API_KEY"
curl "$YINGZO_BASE_URL/api/v1/agent/pricing" -H "Authorization: Bearer $YINGZO_API_KEY"
```

已配置、启用且有账号可用时，模型目录包含如下字段：

```json
{
  "id": "suno-v6",
  "media_types": ["audio"],
  "platforms": ["openai"],
  "interfaces": ["music.generations"],
  "capabilities": {
    "input_modalities": ["text", "audio"],
    "output_modalities": ["audio", "text", "video"],
    "operations": ["audio.generate"],
    "streaming": false,
    "asynchronous": true,
    "supported_modes": ["instrumental", "song"],
    "versions": ["v6"],
    "max_mode_multiplier": 2
  }
}
```

`capabilities.operations` 是与其它模型一致的字符串数组路由词表（`audio.generate`），不携带各操作的 API 字段规格；每个操作的参数校验由网关在请求时执行。按 `supported_modes` 控制对应价格档位入口，`versions` 给出公共版本，`max_mode_multiplier=2`。只有启用价格的模式会下发，目录不保证每次提交时账号仍有容量。

价格快照读取 `rules` 中 `model=suno-v6`、`platform=openai`、`media_type=audio` 的规则。模式沿用字段名 `resolution`，界面应显示「生成模式」，不是音频分辨率。示意规则如下，数值不代表默认售价：

```json
{
  "model": "suno-v6",
  "platform": "openai",
  "media_type": "audio",
  "resolution": "song",
  "unit_kind": "request",
  "unit_price": 2,
  "effective_unit_price": 2,
  "billing_multiplier": 1
}
```

使用 `effective_unit_price` 展示 credit/次，遵循顶层 `pricing_version`、`fetched_at` 和 `valid_until`。模型广场暴露已启用的配置价格。快照是展示依据，最终按任务受理时服务端价格预扣。

## 纯音乐 cURL

先为这次用户操作生成并持久化一个 UUID，重试时复用同一值，不要每次请求重新生成。

```bash
MUSIC_KEY='77089fa5-5c23-4e8c-8363-7a0db7fb8dd1'
curl -i "$YINGZO_BASE_URL/v1/music/generations" \
  -H "Authorization: Bearer $YINGZO_API_KEY" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $MUSIC_KEY" \
  --data '{
    "model":"suno-v6",
    "custom":false,
    "instrumental":true,
    "prompt":"宁静温暖的钢琴与弦乐纯音乐，适合清晨阅读，轻柔节奏，无人声",
    "style_weight":0.7,
    "weirdness_constraint":0.3,
    "audio_format":"mp3"
  }'
```

## 歌词歌曲 cURL

```bash
SONG_KEY='f2f50431-f373-49af-939b-8d573b264b67'
curl -i "$YINGZO_BASE_URL/v1/music/generations" \
  -H "Authorization: Bearer $YINGZO_API_KEY" \
  -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $SONG_KEY" \
  --data '{
    "model":"suno-v6",
    "custom":true,
    "instrumental":false,
    "auto_lyrics":false,
    "title":"晨光",
    "prompt":"[Verse]\n微风轻轻推开窗\n晨光落在你肩上\n[Chorus]\n一起走向新的远方\n把心愿慢慢唱响",
    "style":"Mandarin indie pop, warm acoustic guitar, gentle female vocal, 90 BPM",
    "vocal_gender":"Female",
    "negative_tags":"heavy metal, harsh distortion",
    "duration":120,
    "style_weight":0.8,
    "weirdness_constraint":0,
    "audio_format":"m4a"
  }'
```

## JavaScript 提交与恢复

示例用于可信服务器或客户端安全凭据环境，不要将共享管理员密钥打包进网页。每次用户发起新创作时只生成一次 UUID，将 `{key, body}` 持久化后再提交。

```javascript
const yingzoBase = 'https://yingzo.example';
const yingzoKey = process.env.YINGZO_API_KEY;
const headers = { Authorization: `Bearer ${yingzoKey}` };

const instrumental = {
  model: 'suno-v6', custom: false, instrumental: true,
  prompt: '舒缓的钢琴纯音乐，温暖、平静，无人声', audio_format: 'mp3'
};
const song = {
  model: 'suno-v6', custom: true, instrumental: false, auto_lyrics: false,
  prompt: '[Verse]\n晨光落在你肩上\n[Chorus]\n一起走向新的远方',
  style: 'Mandarin indie pop, acoustic guitar, gentle female vocal',
  title: '晨光', vocal_gender: 'Female', duration: 120, audio_format: 'wav'
};

async function createMusic(body, persistedIdempotencyKey) {
  const response = await fetch(`${yingzoBase}/v1/music/generations`, {
    method: 'POST',
    headers: { ...headers, 'Content-Type': 'application/json',
      'Idempotency-Key': persistedIdempotencyKey },
    body: JSON.stringify(body)
  });
  const payload = await response.json();
  if (response.status !== 202) throw Object.assign(new Error('Music submission failed'),
    { status: response.status, payload });
  return payload;
}

async function recoverMusic(persistedIdempotencyKey) {
  return fetch(`${yingzoBase}/v1/music/tasks/by-idempotency/${encodeURIComponent(persistedIdempotencyKey)}`,
    { headers });
}

async function readMusic(taskId) {
  const response = await fetch(`${yingzoBase}/v1/music/tasks/${encodeURIComponent(taskId)}`, { headers });
  const payload = await response.json();
  if (!response.ok) throw Object.assign(new Error('Music query failed'),
    { status: response.status, payload });
  return payload;
}

// 每个新操作各有自己的 UUID，并先存储到你的任务记录。
// const receipt = await createMusic(instrumental, savedInstrumentalKey);
// const receipt = await createMusic(song, savedSongKey);
// 首次约 3 秒后调用 readMusic(receipt.task_id)，之后按 Retry-After 或 5–10 秒轮询。
```

`Idempotency-Key` 服务端可选，下游应始终提供。建议使用 UUID，仅用 URL 安全字符，最长 200 字节；作用域为同一 API Key。相同 Key 与规范化后相同请求返回原任务，不重复预扣；同 Key 不同请求返回 409。显式默认值与省略默认值等价，歌词内容和换行变化视为不同请求。

提交网络超时或响应丢失时，先按幂等键查询。若暂时 404，可稍后再查或用**同一 Key、同一请求**重新提交以恢复受理结果；服务端唯一约束保证至多受理一次。不要用新 Key 自动重提。已进入 `submission_unknown` 时，服务端不重新提交或换账号，等待期限届满退款。退款后的再次创作应是用户明确发起的新操作。

## 任务响应

创建返回 **HTTP 202**，以及 `Location: /v1/music/tasks/{task_id}`、`Retry-After: 3`、`Cache-Control: no-store`。时间戳单位为秒，以下时间仅作示意。

```json
{
  "id":"musictask_0ba284c30e4249e4a621061005e64c92",
  "task_id":"musictask_0ba284c30e4249e4a621061005e64c92",
  "object":"music.generation.task",
  "status":"processing",
  "phase":"queued",
  "mode":"song",
  "progress":0,
  "poll_url":"/v1/music/tasks/musictask_0ba284c30e4249e4a621061005e64c92",
  "billing_status":"precharged",
  "refund_status":"none",
  "created_at":1790812800,
  "deadline_at":1790814600,
  "expires_at":1790899200
}
```

查询返回 HTTP 200；处理中仍是 `status=processing`，`phase` 可为 `queued`、`preparing`、`submitting`、`submission_unknown`、`polling`、`saving`。按顶层 `status` 判断成功或失败，不依赖某个中间阶段。`progress` 是上游进度提示，不能作为完成依据。

完成响应保留上述任务标识与时间字段，并返回如下字段：

```json
{
  "status":"completed",
  "phase":"completed",
  "mode":"song",
  "progress":100,
  "billing_status":"settled",
  "refund_status":"none",
  "http_status":200,
  "completed_at":1790812950,
  "result":{"music":[
    {"audio_id":"track-a","status":"complete","title":"晨光","lyrics":"[Verse]\n晨光落在你肩上","tags":"indie pop, acoustic","duration":118.4,"audio_url":"https://yingzo.example/media/9b381044-1d32-4a01-96d3-13aef9178e35/asset.m4a","image_url":"https://yingzo.example/media/7cdd7be4-2a31-43e2-9aaa-0663f7a1c227/asset.jpg"},
    {"audio_id":"track-b","status":"complete","title":"晨光","lyrics":"[Verse]\n晨光落在你肩上","tags":"indie pop, acoustic","duration":124.1,"audio_url":"https://yingzo.example/media/826826ca-c801-4dd3-965b-a7acbf5f2d27/asset.m4a"}
  ]}
}
```

音轨数量不固定，遍历 `result.music[]` 展示标题、歌词、风格（`tags` / `display_tags`）、封面和实际时长。音乐生成类任务至少有一首有效音频才会成功；上传、歌词、MIDI 等工具任务按各自结果结构判定。音频转存失败只重试转存，直到完成或任务超时，不重新生成；封面可用时转存，否则可省略。

失败响应保留任务标识，并包含如下字段。查询 HTTP 状态仍为 200，`http_status` 是任务错误信息：

```json
{
  "status":"failed",
  "phase":"failed",
  "billing_status":"refunded",
  "refund_status":"refunded",
  "http_status":502,
  "error":{"code":"music_upstream_failed","message":"Suno generation failed"}
}
```

如果退款正在重试，会先返回 `status=failed`、`billing_status=refunding`、`refund_status=pending`；继续查询直至 `refunded`。退款幂等，不会因多次轮询或服务重启多退金额。

## 播放、下载及链接刷新

保存本地任务 ID、幂等键、所选音轨及 `expires_at`。使用结果中的 Yingzo 本地媒体链接或生成存储签名 URL 播放下载，不使用 APIMart 原始地址。签名 URL 可能早于文件保留时间过期；重新查询任务获取新链接。任务查询不会延长文件保留时间。超出保留期或文件已被容量策略清理时返回 410，不能恢复已清理文件。

查询按用户和 API Key 双重隔离，即使同一用户的另一把 Key 也不能读取。余额或额度用尽、音乐开关关闭不阻止原 Key 查询结果；Key、用户及分组仍须满足身份认证要求。关闭客户端轮询不会取消上游任务，也不会自动退款。

## 错误处理

错误采用 `{"error":{"type":"…","code":"…","message":"…"}}`；认证中间件沿用现有错误协议。


| HTTP / code                                                                                            | 含义与处理                            |
| ------------------------------------------------------------------------------------------------------ | -------------------------------- |
| 400 `invalid_request_error`                                                                            | 参数组合、长度、字段不正确，修正输入               |
| 400 `pricing_not_configured`                                                                           | 模型或该模式未启用、缺价或价格停用，刷新价格并提示管理员     |
| 401                                                                                                    | Key 无效、停用或用户不可用，恢复认证             |
| 403 `music_group_required` / `model_not_allowed`                                                       | 分组或模型权限不符                        |
| 400 `INSUFFICIENT_BALANCE`                                                                             | 可用余额不足，不创建任务、不调用上游               |
| 403 `music_key_quota_exceeded` / `music_subscription_quota_exceeded` / `music_platform_quota_exceeded` | 对应额度不足                           |
| 429 `music_rate_quota_exceeded`                                                                        | Key 时间窗口额度不足，待额度恢复               |
| 404 `music_task_not_found`                                                                             | 任务不存在或不属于当前 Key，不泄露他人任务          |
| 409 `idempotency_conflict`                                                                             | 同 Key 已用于另一请求，停止自动重试             |
| 410 `music_result_expired`                                                                             | 结果已过期或清理                         |
| 413                                                                                                    | 请求体超限                            |
| 503 `music_tasks_disabled`                                                                             | 管理员尚未开启音乐生成                      |
| 503 `music_account_unavailable`                                                                        | 无可用专用账号，稍后使用同一幂等键重试              |
| 503 `music_tasks_unavailable`                                                                          | 任务或结果存储暂不可用，稍后重查                 |
| 200 且任务 `status=failed`                                                                                | 上游失败、空结果或超时，按退款状态展示，不用新 Key 自动重提 |


## 上线验收

配置完成后，分别发起一笔纯音乐和一笔歌词歌曲，核对：预扣价格、全部音轨播放下载、实际时长、最终结算、成本记录。模拟客户端丢失提交响应后按幂等键恢复，确认不重复扣费；关闭开关后验证旧任务仍能查询。真实验收会消耗 APIMart 生成费用，连接测试不能代替此步骤。

上游协议依据：[APIMart Suno 生成](https://docs.apimart.ai/cn/api-reference/audios/suno/generation)、[任务查询](https://docs.apimart.ai/cn/api-reference/audios/suno/overview)。各操作的开放状态由对应价格档位决定，完整端点见 [全功能接口参考](SUNO_FULL_API_REFERENCE.md)。


## 指定音乐计费维度

以下维度均按次计费（billing_unit=request），在 Yingzo Agent 分组的音乐计费中独立配置价格和启用状态。计费标识与上游端点名称分开，接口路径保持不变。

| 功能 | 计费标识（resolution） | 操作端点末段 |
| --- | --- | --- |
| 上传音频翻唱 | upload_cover | uploadCover |
| 创建自定义音色模型 | create_model | createModel |
| 创建歌手 | persona | persona |
| 音效生成 | sounds | sounds |
| 样本转歌曲 | sample | sample |

`uploadTask`（上传音频）不单独计费、也不设价格档位：它是 `sample` / `uploadCover` / `createModel` 等流程的内部步骤，费用已含在对应操作价格中；网关按 0 元受理，价格快照中不会出现 `upload` 规则。

公共版本仅 `v6`，无版本后缀档位；使用自定义模型生成的操作按 `:custom` 后缀区分（如 `upload_cover:custom`），支持 Max 的操作仍按对应单次价格乘 2。创建自定义音色模型与后续使用模型生成分别计费。
