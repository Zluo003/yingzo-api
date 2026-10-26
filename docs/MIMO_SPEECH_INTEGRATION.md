# MiMo V2.5 语音合成 · Yingzo 客户端对接

核对日期：2026-10-27。下游使用 **Yingzo 服务地址与 Yingzo API Key**。上游 MiMo API Key 只保存在网关账号中。

上游依据：[使用指南](https://mimo.mi.com/docs/zh-CN/quick-start/usage-guide/audio/speech-synthesis-v2.5)、[API 参数原文](https://mimo.mi.com/static/docs/api/audio/tts.md)。

| 模型 ID | 功能 | 音色来源 | 流式行为 |
| --- | --- | --- | --- |
| `mimo-v2.5-tts` | 预置音色合成，可用风格标签、唱歌 | `audio.voice` | 支持实时返回音频块 |
| `mimo-v2.5-tts-voicedesign` | 用文字描述设计音色并合成 | `user` 消息 | 支持 SSE，但上游目前在全部推理完成后一次返回结果 |

本次接入以上两个模型。音色设计返回本次合成音频，不创建可持久复用的 voice ID。

## 管理端配置

1. 账号管理 → **添加语音账号**，填写名称、MiMo API Key、代理、并发数、优先级及分组。Base URL 默认 `https://api.xiaomimimo.com/v1`，可填写 HTTPS 域名或带 `/v1` 的地址。
2. 绑定普通 OpenAI 分组或系统 Yingzo Agent 分组。账号固定声明以上两个模型；编辑时 API Key 留空保留原密钥。账号列表“类型”可筛选“语音账号 · MiMo”，列表也显示语音标签。
3. 对于 Yingzo Agent，在渠道中为 `openai` 平台的两个模型配置输入/输出 token 价格，并将渠道关联至 Agent 分组，或配置在该账号的同平台源分组。然后到 **Yingzo Agent → 音频计费** 同步模型，为每个模型设置倍率并启用。缺少渠道价格或模型倍率时，请求在转发前失败。
4. 普通 OpenAI 分组使用现有分组模型定价或渠道定价；请显式配置这两个模型的 token 价格。本次不预设 MiMo 商业价格，也不套用 Grok 的按字符 TTS 价格。
5. 账号“测试”执行 `GET /v1/models` 检查连接和鉴权，不产生收费语音。这不代表已验证合成结果；合成验收使用下文示例。

音频目录类型为 `audio`，但这两个模型按上游 `usage.prompt_tokens` / `usage.completion_tokens` 进入 token 计费链路。Agent 售价是账号对应渠道价乘模型倍率；Suno 继续按其操作和版本逐次计费。

账号在管理 API 中保存为 `platform=openai`、`type=apikey`、`extra.speech_account=true`、`extra.speech_provider=mimo`。仅允许 Chat Completions 调度，不参与 Responses、图片、Embedding 或 Live 请求。无需新增数据库迁移。

## 模型发现

Yingzo Agent API Key 调用 `GET /v1/models`。账号已绑定、模型已启用且可用时，目录含：

```json
{
  "id": "mimo-v2.5-tts",
  "media_types": ["audio"],
  "platforms": ["openai"],
  "interfaces": ["openai.chat_completions"],
  "capabilities": {
    "media_types": ["audio"],
    "platforms": ["openai"],
    "interfaces": ["openai.chat_completions"],
    "input_modalities": ["text"],
    "output_modalities": ["audio"],
    "operations": ["audio.generate"],
    "streaming": true,
    "realtime_streaming": true,
    "asynchronous": false,
    "supported_output_formats": ["wav", "mp3", "pcm16"],
    "streaming_output_formats": ["pcm16"],
    "audio_sample_rate_hz": 24000,
    "audio_channels": 1,
    "supports_voice_design": false,
    "supports_text_optimization": false,
    "voices": ["mimo_default", "冰糖", "茉莉", "苏打", "白桦", "Mia", "Chloe", "Milo", "Dean"]
  }
}
```

`mimo-v2.5-tts-voicedesign` 同样使用 `openai.chat_completions`，其 `realtime_streaming=false`、`supports_voice_design=true`、`supports_text_optimization=true`，且不提供预置 `voices`。采样率与声道数描述流式 PCM 音频。

客户端按 `audio.generate` 选择音频生成能力，按 `interfaces` 发送 Chat Completions 请求。`streaming=true` 表示支持 SSE 协议；是否可以边生成边播放看 `realtime_streaming`。这两个模型采用单次 HTTP 请求，不创建异步任务，无轮询接口。

Agent 的 `GET /api/v1/agent/pricing` 中，这两个模型的价格规则为 `media_type=audio`、`unit_kind=channel_price_multiplier`。`billing_multiplier` / `effective_unit_price` 表示模型倍率，不是每条音频或每百万 token 的美元单价，客户端不要把倍率展示成固定合成费用。最终金额由网关按真实用量结算。

## 请求约定

```http
POST /v1/chat/completions
Authorization: Bearer <YINGZO_API_KEY>
Content-Type: application/json
```

| 字段 | 规则 |
| --- | --- |
| `model` | 上述两个规范 ID |
| `messages` | 非空数组，每条消息为 `role=user` 或 `role=assistant`、非空字符串 `content` |
| `messages[].role=assistant` | **待朗读文本**，支持在文本内嵌风格、情绪和声音标签 |
| `messages[].role=user` | 风格指令或对话上下文；音色设计模型必填，作为音色描述，不直接朗读 |
| `audio` | 可省略；如提供，须为对象 |
| `audio.format` | 非流式默认 `wav`；支持 `wav`、`mp3`、`pcm16`，`pcm` 是 `pcm16` 别名。流式默认 PCM，仅允许 `pcm` / `pcm16` |
| `audio.voice` | 仅预置音色模型支持；默认 `mimo_default`，可用值见上方目录 |
| `audio.optimize_text_preview` | 仅音色设计支持，布尔值，默认 `false`；`true` 时允许省略 `assistant`，由上游生成合适文本，也可能润色传入文本 |
| `stream` | 布尔值，默认 `false`；`true` 返回 `text/event-stream` |

待朗读文本必须放到 `assistant`，只发 `user` 不会按普通聊天自动转为朗读。希望逐字播报时，保留 `assistant` 并保持 `optimize_text_preview=false`。

预置音色：冰糖、茉莉为中文女声，苏打、白桦为中文男声；Mia、Chloe 为英文女声，Milo、Dean 为英文男声。`mimo_default` 随上游集群而定，中国集群默认为冰糖，其他集群默认为 Mia。客户端需要固定声音时，应显式选择 voice ID。

`tools`、`tool_choice`、Responses `input`、`response_format` 不适用于此接口。网关不会把普通聊天、Responses 或 `/v1/audio/speech` 参数自动转换成 MiMo 请求。

## 非流式示例

预置音色，生成 MP3：

```bash
export YINGZO_BASE_URL='https://yingzo.example'
export YINGZO_API_KEY='你的 Yingzo API Key'

curl --fail-with-body "$YINGZO_BASE_URL/v1/chat/completions" \
  -H "Authorization: Bearer $YINGZO_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{
    "model":"mimo-v2.5-tts",
    "messages":[
      {"role":"user","content":"用温柔、放松的语气，语速稍慢。"},
      {"role":"assistant","content":"你好，欢迎使用 Yingzo 语音合成。"}
    ],
    "audio":{"format":"mp3","voice":"茉莉"}
  }' > speech.json
```

返回 **JSON**，音频是 `choices[0].message.audio.data` 内的 Base64；不是文件 URL 或原始音频响应：

```json
{
  "id": "chatcmpl_example",
  "object": "chat.completion",
  "model": "mimo-v2.5-tts",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": null,
      "audio": {"id": "audio_example", "data": "<Base64 音频>", "expires_at": null, "transcript": null}
    },
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 20, "completion_tokens": 100, "total_tokens": 120}
}
```

Python 解码：

```python
import base64
import json
from pathlib import Path

response = json.loads(Path("speech.json").read_text())
Path("speech.mp3").write_bytes(base64.b64decode(response["choices"][0]["message"]["audio"]["data"]))
```

音色设计请求体：

```json
{
  "model": "mimo-v2.5-tts-voicedesign",
  "messages": [
    {"role": "user", "content": "一位温暖沉稳的中年男声，音色醇厚，普通话清晰，像深夜电台主持人，语速舒缓。"},
    {"role": "assistant", "content": "夜深了，放下今天的疲惫，愿你拥有一个好梦。"}
  ],
  "audio": {"format": "wav", "optimize_text_preview": false}
}
```

自动生成试听文案时可省略 `assistant`，并设置 `audio.optimize_text_preview=true`；最终文案读取 `choices[0].message.final_text_preview`。此字段与 `audio` 同级，`audio.transcript` 当前可能为 `null`，不要据此判断生成失败。

## 流式：Python OpenAI SDK

```python
import base64
import os
import wave
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["YINGZO_API_KEY"],
    base_url=os.environ["YINGZO_BASE_URL"].rstrip("/") + "/v1",
    timeout=300.0,
    max_retries=0,
)

stream = client.chat.completions.create(
    model="mimo-v2.5-tts",
    messages=[{"role": "assistant", "content": "你好，这是流式语音。"}],
    audio={"format": "pcm16", "voice": "冰糖"},
    stream=True,
)

with wave.open("speech.wav", "wb") as output:
    output.setnchannels(1)
    output.setsampwidth(2)
    output.setframerate(24000)
    for chunk in stream:
        if not chunk.choices:  # 最后一帧可能只包含 usage
            continue
        delta = chunk.choices[0].delta
        # 不同 SDK 版本可能将额外 audio 字段解析成 dict 或对象。
        audio = getattr(delta, "audio", None)
        encoded = audio.get("data") if isinstance(audio, dict) else getattr(audio, "data", None)
        if encoded:
            output.writeframesraw(base64.b64decode(encoded))
```

音色设计流式使用相同循环，改 `model`，添加描述音色的 `user` 消息，并移除 `audio.voice`。需要润色或自动生成文案时通过 SDK 的 `extra_body={"audio":{"format":"pcm16","optimize_text_preview":True}}` 传递扩展参数，不要同时再传 `audio=`。

## Yingzo TypeScript 客户端

非流式可直接用 `fetch`：

```ts
async function synthesize(baseUrl: string, apiKey: string, text: string): Promise<Blob> {
  const response = await fetch(`${baseUrl.replace(/\/$/, '')}/v1/chat/completions`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${apiKey}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      model: 'mimo-v2.5-tts',
      messages: [{ role: 'assistant', content: text }],
      audio: { format: 'mp3', voice: '茉莉' },
    }),
  })
  if (!response.ok) throw new Error(await response.text())
  const data = await response.json()
  const encoded = data.choices?.[0]?.message?.audio?.data
  if (!encoded) throw new Error('语音响应缺少 audio.data')
  const bytes = Uint8Array.from(atob(encoded), char => char.charCodeAt(0))
  return new Blob([bytes], { type: 'audio/mpeg' })
}
```

播放时 `URL.createObjectURL(blob)` 赋给音频播放器，完成后 `URL.revokeObjectURL(url)`。桌面客户端可以使用自己的安全凭证存储；公开网页应通过自己的服务端调用，避免把长期 API Key 打包进静态资源。

流式客户端使用 POST + `fetch` 或支持自定义 POST 请求的 SSE 库，处理规则如下：

1. 用流式 `TextDecoder` 解码网络字节，保留跨网络块的不完整 SSE 帧；以空行切分事件，合并帧内的 `data:` 行。不能把一次网络 `read()` 当成一次 SSE 事件。
2. `data: [DONE]` 表示流结束。其他 `data:` 内容解析为 JSON，先检查 `error`；`choices=[]` 的帧可能只有 `usage`。初始化帧的 `delta.audio` 可能为 `null`，跳过没有音频数据的帧即可。
3. 音频位于 `choices[0].delta.audio.data`。**每个音频字段独立 Base64 解码，再按顺序拼接字节**，不要先拼接 Base64 字符串。文本润色结果可能位于 `delta.final_text_preview`，分别保存。
4. 音频格式是 **24 kHz、单声道、PCM16LE**。每个采样点占两字节；若跨块出现单字节尾部，将它留到下一块再组成采样。实时播放可用 AudioWorklet 队列，将有符号 16 位整数除以 `32768` 转为浮点采样，再按输出设备采样率重采样。
5. 原始 PCM 没有 WAV 文件头，不能直接作为 WAV/MP3 Blob 交给 `<audio>`。下载时添加 WAV 文件头，或使用音频库封装；上面的 Python 例子展示了标准 WAV 写入方式。
6. 区分“正在生成”和“正在播放”。音色设计可能长时间没有音频块，不能用预置音色的首包延迟标准判断失败。可采用 300 秒的总请求超时，并由用户取消；实际超时仍受服务端及反向代理配置限制。
7. 已收到音频后发生网络中断，标记音频不完整。不要自动重放整条生成请求，以免产生重复语音与费用。此接口没有持久幂等任务和断点续传保证。

## 错误与验收

错误沿用 OpenAI 兼容结构：`{"error":{"type":"invalid_request_error","message":"..."}}`。流开始前看 HTTP 状态与错误体，流开始后继续检查 SSE 中的 `error`。常见状态包括参数错误 `400`、无效 Key `401`、权限或模型限制 `403`、限流 `429`、路由不可用 `503`、上游故障 `502`；缺少 Agent 定价以返回的定价错误为准，不应当作网络错误反复重试。

建议客户端验收：

- 两个模型各生成一个非流式 WAV/MP3，检查 Base64 解码与播放。
- 预置音色流式检查首段可播放、24 kHz 单声道 PCM 拼接及正常结束；忽略无 `choices` 的用量帧。
- 音色设计检查用户描述必填、禁止预置 voice、`optimize_text_preview=true` 自动生成文案及 `final_text_preview` 展示。
- 含 `assistant` 的风格标签、中文 voice ID、长文本与网络断流，均不丢失或重复音频块。
- 查询网关用量，核对返回 token 用量与已配置价格；模型目录缺失时检查账号绑定、目录同步、模型启用及定价。

遇到 `503 Service temporarily unavailable` 时，先检查错误记录是否包含选中账号和上游状态码。两者都为空且错误阶段为 `routing`，表示请求在本地调度阶段失败，上游尚未收到请求。旧版日志会把 OpenAI 兼容请求的默认推断端点显示为 `/v1/responses`，这不代表实际调用了 Responses；无上游调用的调度失败应显示空端点，成功合成的实际端点应为 `/v1/chat/completions`。

2026-10-27 已在本地重建的 Docker 容器中使用临时 MiMo Key 验证两个模型的 JSON 与 SSE 调用均返回 200，音频为 24 kHz、单声道 PCM16；模型发现、真实 token 用量、渠道价格乘模型倍率结算及实际端点记录均已核对。
