# Seedance 视频通道

每个视频账号通过 `extra.video_model_channels` 将 Seedance 模型绑定到一个通道 ID。未配置的模型使用空字符串 ID 的默认通道。默认通道可以修改名称、启停和定价；将账号改绑到新通道不会自动复制默认价格。

## 管理

- `GET /api/v1/admin/video-channels`：列出通道。
- `POST /api/v1/admin/video-channels`：提交 `{ "name": "官转", "enabled": true }`，创建稳定 UUID。
- `PUT /api/v1/admin/video-channels`：提交 `{ "id": "…", "name": "…", "enabled": true }` 修改通道；默认通道的 `id` 为 `""`。
- 视频账号绑定示例：`extra.video_model_channels = { "seedance-2.0": "通道 UUID" }`。
- Agent 模型价格增加 `video_channel_id`，唯一键为模型、通道、分辨率。Seedance 支持 `billing_unit: "second" | "request"`，同模型同通道的各档位使用同一计费单位。
- 普通视频分组价格同样增加 `video_channel_id`、`billing_unit` 和 `credits_per_request`。

## 客户端契约

Agent 模型目录的 `capabilities.video_channels` 返回已启用、有账号且有可用价格的通道，包括稳定 ID、名称、价格、分辨率及 `durations_by_resolution`。切换通道后应重新验证参数和获取报价。

估价请求的 `request` 和正式 `POST /v1/videos` 请求均传 `video_channel_id`。不传或传空字符串只选择默认通道，不自动寻找其他通道。

正式请求可携带 `expected_unit_price` 和 `expected_billing_unit`。应使用刚确认报价的单价和单位；若提交时价格已变化，API 在扣款和提交上游前返回 `video_price_changed`，客户端应重新报价并让用户确认。

`GET /api/v1/agent/pricing?video_channels=1` 返回带通道 ID 的全部可用价格。不带该参数时只返回默认通道的视频价格，兼容旧版按模型查价的客户端。

## 收费与调度约束

- 按秒：Agent Seedance 沿用只计生成时长的规则；普通视频分组保留原有生成与参考时长计费行为。
- 按次：每个生成任务一次费用，参考视频不增加费用，仍执行时长与素材能力校验。
- 缺价、通道停用或没有匹配账号均拒绝请求。价格不能回退到其他通道。
- 首次选号与重试均按请求固定的通道 ID 过滤，再按能力和账号优先级选取；同优先级轮转。沿用最多三个账号的重试预算。
- 自动换号不重复扣费；最终失败按任务快照退款。任务保存通道 ID、计费单位、单价和原扣款金额。
- 旧任务和旧价格迁入默认通道；迁移不会改动历史扣款或将旧价格传播给新通道。

部署顺序：先发布 API 与管理端（执行 266 迁移），配置账号通道与价格，再发布支持通道选择的 Yingzo 客户端。
