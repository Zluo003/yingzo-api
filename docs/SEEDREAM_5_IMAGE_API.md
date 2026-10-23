# Seedream 5.0 Pro / Flash 下游对接指南

核对日期：2026-10-23。上游为 APIMart，下游使用 Yingzo 地址与 Yingzo API Key。

| 模型 | 对外模型 ID | 接口 |
| --- | --- | --- |
| Seedream 5.0 Pro | `seedream-5-0-pro` | `openai.images` |
| Seedream 5.0 Flash | `seedream-5-0-flash` | `openai.images` |

两个模型使用相同的参数协议。支持文生图、最多 10 张参考图融合、坐标/手绘标记编辑、透明背景编辑、底图加最多 16 个图层的拆分。每次提交只能 `n=1`；图层拆分是一次请求的多个输出，不是批量生成。

## 管理端启用

1. 部署后端及管理前端。复用已有异步图片任务和 Agent 输入图片计费字段，不增加数据库迁移。
2. 系统设置 → 素材存储 → 生成结果，配置本地或对象存储，并开启异步图片任务。关闭时新请求返回 `503 image_tasks_disabled`，已接受任务仍能轮询、结算和退款。
3. 账户管理 → 添加图片模型 → **Seedream 5.0 Pro / Flash · APIMart**。填写 APIMart API Key，Base URL 默认 `https://api.apimart.ai`，也支持末尾 `/v1`。可配置并发、优先级、代理及绑定分组。
4. 专用账户同时声明两个模型，不参与文本或普通同步 OpenAI 图片调度。编辑时 API Key 留空保留旧值；测试账户只请求 `/v1/models` 检查连接和鉴权，不创建付费图片。
5. Yingzo Agent → 图片计费，同步模型后，分别为两个模型配置并启用 **1K、2K 两个输出价格**，再启用模型。1.5K 共用 1K 价格，不填写独立 1.5K 或 4K 价格。
6. 同一表格可设置免费参考图数量和超额输入图片单价。默认首张参考图免费；免费数量 `0` 表示全部收费；超额单价留空或 `0` 表示下游免费。请根据实际采购成本设置，系统不会自动套用 APIMart 的标价。

账户保存为 `platform=openai`、`type=apikey`、`extra.image_account=true`、`extra.image_provider=apimart_seedream`，模型映射固定为上述两个 ID。

也支持普通 OpenAI 分组：配置分组或渠道的两个输出档位图片价格（或按次价格），遵循现有图片倍率。第二张起的参考图要求显式配置对应模型键 `seedream-5-0-pro:input` / `seedream-5-0-flash:input` 的 `per_request` 单价；每张超额参考图计一次。显式单价 `0` 可表示免费。普通分组默认仅第一张参考图免费。

## 模型发现与价格预估

Agent API Key 调用 `GET /v1/models`。已配置两个计费档位后，能力片段如下：

```json
{
  "id": "seedream-5-0-pro",
  "media_types": ["image"],
  "platforms": ["openai"],
  "interfaces": ["openai.images"],
  "capabilities": {
    "input_modalities": ["text", "image"],
    "output_modalities": ["image"],
    "operations": ["image.generate", "image.edit"],
    "streaming": false,
    "asynchronous": true,
    "supported_image_sizes": ["1K", "1.5K", "2K"],
    "supported_aspect_ratios": ["1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "2:1", "1:2", "21:9"],
    "max_input_images": 10,
    "max_output_images": 1,
    "supports_layer_decomposition": true,
    "max_layer_outputs": 17,
    "supported_output_formats": ["jpeg", "png"]
  }
}
```

`asynchronous` 同时受系统异步开关控制。缺少任一输出档位时 `supported_image_sizes` 为空，应提示管理员补齐价格。`max_output_images=1` 表示正常生图只能一张，拆图单独使用 `max_layer_outputs`。

`GET /api/v1/agent/pricing` 的模型价格规则仍为 `unit_kind=image`，`resolution` 仅 `1K`、`2K`，并附带 `free_input_images` 与 `input_image_unit_price`。1.5K 映射到 1K。显示实际 `effective_unit_price`，不要把上游文档美元标价作为 Yingzo 单价。

Agent 可调用 `POST /api/v1/agent/generation/estimates` 获取提交时的预扣金额：

```json
{
  "kind": "image",
  "platform": "openai",
  "model": "seedream-5-0-pro",
  "count": 1,
  "request": {
    "prompt": "简洁的产品海报",
    "size": "16:9",
    "resolution": "1.5K",
    "output_format": "png"
  }
}
```

响应的 `actual_price` / `total_price` 表示保守预扣金额；`details.reserved_output_images` 为普通生成 `1`、拆图 `17`，`details.input_image_cost` 为本次参考图费用。最终按真实输出面积结算，可能低于预扣。估价本身不创建图片任务，实际提交时冻结当时价格。

## 提交接口与参数

```http
POST /v1/images/generations
Authorization: Bearer <YINGZO_API_KEY>
Content-Type: application/json
Prefer: respond-async
Idempotency-Key: <本次逻辑操作唯一 UUID>
```

Seedream 始终异步，成功提交返回 **HTTP 202**。客户端应发送 `Prefer: respond-async`，以便余额或额度被预扣耗尽时仍可通过同一幂等键重放已接受请求。也可使用 `/v1/images/generations/async`。

图生图仍可使用生成接口并传 `image_urls`；`/v1/images/edits` 和 `/v1/images/edits/async` 是 JSON 编辑入口，要求至少一张参考图。两个入口共用同一规范化请求幂等指纹。此适配器不接受 multipart 或 OpenAI `image` 文件字段。

| 参数 | 约束和默认值 |
| --- | --- |
| `model` | 必填，上述两个规范 ID；兼容 `seedream-5.0-pro` / `seedream-5.0-flash` 拼写，白名单建议始终使用规范 ID |
| `prompt` | 文本描述；拆图时可省略，其余必填。建议控制在 600 个英文单词以内 |
| `n` | 可省略或 `1`，不支持组图参数 |
| `resolution` | `1K`（默认）、`1.5K`、`2K`，不区分大小写 |
| `size` | `auto`（默认）、上述比例、`1K` / `1.5K` / `2K`，或精确像素 `2048x1024` |
| `image_urls` | 最多 10 个 HTTP(S) 图片 URL 或 `data:image/<格式>;base64,...`；Data URI 格式名须小写 |
| `output_format` | `jpeg`（默认）或 `png` |
| `response_format` | 仅作为 `output_format` 的兼容别名，同样仅接受 `jpeg` / `png`；结果始终为 URL |
| `background` | `opaque`（默认）或 `transparent`；透明背景要求恰好一张带透明通道的输入图，并指定 `output_format=png` |
| `layer_decomposition` | 默认 `false`；`true` 时恰好一张参考图，`size` 仅 `auto`、`1K`、`1.5K`、`2K` |
| `optimize_prompt_options` | 可选对象，仅 `{"mode":"standard"}`；兼容扁平字段 `optimize_prompt_options.mode` |
| `watermark` | 布尔值，默认 `false` |
| `nsfw_check` | 上游额外审核开关，默认 `false`；服务端已配置的审核策略独立执行 |

比例兼容小写 `x`，如 `16x9`；不接受带空格或大写 `16X9`。精确像素则兼容 `2048X1024`、`2048×1024`，总像素须在 **921600–4624220**，宽高比在 **1:16–16:1**。精确像素完全覆盖 `resolution`；`size` 直接写档位时也覆盖 `resolution`。

参考图的上游限制：jpeg/png/webp/bmp/tiff/gif/heic/heif，每张不超过 30 MB、36,000,000 像素，单边大于 14 px，宽高比在 1:16–16:1。拆图输入仅 PNG/JPEG，总像素 262144–36,000,000。实际可提交的 Base64 总量还受网关请求体大小限制；大图优先使用公网 URL。图像内容、透明通道及远程图片尺寸由上游进一步验证。

不支持 `stream`、`tools`、任意未知字段；非法参数在本地返回 400。与上游宽松兼容行为相比，Yingzo 对非法 `output_format` / `response_format` 直接拒绝，避免静默更换格式。

## 场景示例

文生图，两种模型仅需替换 `model`：

```json
{"model":"seedream-5-0-pro","prompt":"极简风格橘猫海报，暖色背景","size":"16:9","resolution":"1.5K","output_format":"png"}
```

精确像素：

```json
{"model":"seedream-5-0-flash","prompt":"白底产品主图","size":"1600x1600"}
```

多参考图融合：

```json
{"model":"seedream-5-0-pro","prompt":"把图1的人物放入图2的场景，统一黄昏光线","image_urls":["https://assets.example/person.png","https://assets.example/scene.jpg"],"resolution":"2K","size":"16:9"}
```

交互编辑，坐标归一化到 0–1000，也可在 prompt 描述输入图中的手绘标记：

```json
{"model":"seedream-5-0-flash","prompt":"将图1 <bbox>179 283 796 986</bbox> 的主体放到图2 <bbox>118 331 933 871</bbox> 位置","image_urls":["https://assets.example/a.png","https://assets.example/b.png"]}
```

透明图层编辑：

```json
{"model":"seedream-5-0-pro","prompt":"将鹦鹉改成孔雀，保留透明背景","image_urls":["https://assets.example/transparent.png"],"background":"transparent","output_format":"png","size":"2K"}
```

拆图，可省略 prompt：

```json
{"model":"seedream-5-0-flash","image_urls":["https://assets.example/poster.png"],"layer_decomposition":true,"size":"2K"}
```

## 任务查询、结果与退款

提交回执：

```json
{
  "id": "imgtask_example",
  "task_id": "imgtask_example",
  "object": "image.generation.task",
  "status": "processing",
  "phase": "queued",
  "billing_status": "precharged",
  "refund_status": "none",
  "poll_url": "/v1/images/tasks/imgtask_example"
}
```

用原 Yingzo API Key 请求 `GET /v1/images/tasks/{task_id}`，建议间隔 5 秒。`processing` 继续等待，`completed` 读取 `result.data`，`failed` 读取 `error` 和退款状态。任务截止时间以回执 `deadline_at` 为准，当前为提交后 30 分钟。上游约需 90–160 秒，客户端不要设置几十秒的整任务超时。

```json
{
  "id": "imgtask_example",
  "status": "completed",
  "billing_status": "settled",
  "result": {
    "model": "seedream-5-0-flash",
    "layer_decomposition": true,
    "data": [
      {"index":0,"asset_id":"asset-base","url":"https://yingzo.example/media/asset-base/asset.jpg","size":"2048x2048","output_format":"jpeg","layer":{"z_index":0,"size":"2048x2048","output_format":"jpeg"}},
      {"index":1,"asset_id":"asset-layer","url":"https://yingzo.example/media/asset-layer/asset.png","size":"492x98","output_format":"png","layer":{"z_index":1,"name":"标题","bounding_box":{"absolute":[140,451,631,548],"normalized":[68,220,308,268]}}}
    ]
  }
}
```

普通生图仅一项，省略 `layer`。拆图完整保留所有底图/图层、名称、描述、坐标和 `z_index`，不会只选最大图。按 `z_index` 升序叠放，绝对坐标为 `[left,top,right,bottom]`；恢复到任意 `W×H` 画布可用 `normalized/1000` 缩放。拆出的图层恒为 PNG，`output_format` 只控制底图。

结果经过网关验证并转存，URL 为 Yingzo 素材地址或对象存储 URL。不要调用 APIMart 的 `/v1/tasks`，下游不持有上游任务 ID 或密钥。对象存储签名链接过期可重新查询刷新；素材有保留期限，应及时下载持久化。

计费规则：每张实际输出图片总像素 **≤ 2,601,124 用 1K 单价，否则用 2K 单价**；不是按最长边分档，1.5K 的所有内置比例都归 1K。参考图加价单独计一次：

```text
最终费用 = 各输出图片按实际面积分档的单价之和
         + max(0, 输入参考图数量 - 配置免费数量) × 超额参考图单价
```

提交时普通生图按 **max(1K 单价, 2K 单价)** 预扣一张；拆图按该单价预扣 **17 张**，再加参考图费用。成功后退还预扣差额，失败全额退款；`refund_status=refunded` 表示退款事务已提交。价格在接受任务时冻结，排队期间改价不会追溯影响已接受任务。退款明细遵循现有异步图片账本。

提交连接中断时，先用同一 API Key 查询 `GET /v1/images/tasks/by-idempotency/{原幂等键}`，或用原幂等键和相同请求重放。不要生成新幂等键重复购买。不同参数复用同一键返回 `409 idempotency_conflict`。同一用户的另一把 Key 不能读取本任务。

## Python 完整示例

依赖 `requests`。设置 `YINGZO_API_KEY`，按部署地址修改 `BASE`。幂等键应在实际业务中持久化到本地提交日志，恢复时继续使用原值。

```python
import os
import time
import uuid
import requests

BASE = "https://yingzo.art"
IDEMPOTENCY_KEY = os.environ.get("IMAGE_IDEMPOTENCY_KEY") or str(uuid.uuid4())
print("请保存本次幂等键：", IDEMPOTENCY_KEY)
headers = {"Authorization": "Bearer " + os.environ["YINGZO_API_KEY"]}
payload = {
    "model": "seedream-5-0-pro",
    "prompt": "水墨风格江南小镇，清晨薄雾",
    "size": "16:9", "resolution": "1.5K", "output_format": "png",
}

try:
    response = requests.post(
        BASE + "/v1/images/generations", json=payload,
        headers={**headers, "Prefer": "respond-async", "Idempotency-Key": IDEMPOTENCY_KEY},
        timeout=60,
    )
    response.raise_for_status()
    receipt = response.json()
except requests.RequestException:
    # 原请求可能已被接受；仅按原键恢复，不再次创建新任务。
    response = requests.get(
        BASE + "/v1/images/tasks/by-idempotency/" + IDEMPOTENCY_KEY,
        headers=headers, timeout=30,
    )
    response.raise_for_status()
    receipt = response.json()

deadline = time.monotonic() + 1800
while time.monotonic() < deadline:
    response = requests.get(BASE + "/v1/images/tasks/" + receipt["id"], headers=headers, timeout=30)
    response.raise_for_status()
    task = response.json()
    if task["status"] == "completed":
        for image in task["result"]["data"]:
            print(image["url"], image.get("layer"))
        break
    if task["status"] == "failed":
        raise RuntimeError({"error": task.get("error"), "refund_status": task.get("refund_status")})
    time.sleep(5)
else:
    raise TimeoutError("本地等待已结束；请继续按原任务 ID 查询，不要重新提交")
```

## 错误及关联文档

| 状态 | 处理 |
| --- | --- |
| 400 `invalid_request_error` | 修正参数，未创建付费任务 |
| 400 `pricing_not_configured` | 管理员补齐两个输出档位及所需参考图价格 |
| 401 / 403 / 404 | 检查 API Key、图片权限、绑定分组、模型白名单或任务归属 |
| 409 `idempotency_conflict` | 同一逻辑操作保持参数一致；全新操作使用新键 |
| 503 `image_tasks_disabled` | 开启异步生成结果存储与任务开关 |
| 任务 `failed` | 查看错误与退款状态，按原 ID 查询确认退款完成 |

- [统一异步图片任务协议](ASYNC_IMAGE_TASKS.md)
- [APIMart Pro 原始参数](https://docs.apimart.ai/cn/api-reference/images/seedream-5-0-pro/generation)
- [APIMart Flash 原始参数](https://docs.apimart.ai/cn/api-reference/images/seedream-5-0-flash/generation)
