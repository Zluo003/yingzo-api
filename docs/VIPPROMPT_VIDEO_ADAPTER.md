# vipprompt 视频上游配置

下游继续使用现有视频接口、请求字段、响应结构及模型名：`kling-v4`、`seedance-2.0`、`seedance-2.5`。不需要更换接入方式。

在管理端新增或编辑视频账号，选择上游平台 `vipprompt`，填写 API Key，并为选中的模型填写 vipprompt 目录里的完整上游模型名。支持中文、空格、括号和斜杠；适配器不会添加分辨率后缀或改写名称。配置存入 `credentials.model_mapping`，例如：

```json
{
  "seedance-2.0": "sd-2.0-S2 支持真人933",
  "seedance-2.5": "SD-2.5-M支持真人满血稳定版",
  "kling-v4": "kling-video-v4"
}
```

Kling v4 的默认上游模型名为 `kling-video-v4`，留空或配置为下游同名 `kling-v4` 时使用该默认值；填写自定义上游模型名后优先使用自定义值。Seedance 未填写映射时仍发送下游模型名。

默认 Base URL 为 `https://www.vipprompt.cn`，API Path 为 `/v1/videos`。适配器创建任务使用 `POST /v1/videos`，查询使用 `GET /v1/videos/tasks/{id}`，默认每 10 秒轮询，超时沿用视频账号的 15 分钟默认值，可在账号中调整。

适配器将原有 `content` 中的图片、视频、音频转换成上游 `media` 的 `reference_image`、`reference_video`、`reference_audio`，将 `resolution` 转为大写，发送整数 `duration` 与 `ratio`。仅接入文生、参考生视频；统一接口没有定义可移植的首尾帧角色，首帧/尾帧请求由其他支持该能力的上游处理，避免降级成普通参考图。

不同上游模型变体的分辨率、时长和素材限制不同，请根据 vipprompt 的 `/v1/models`、`/api/relay/groups` 及账号授权，收敛本账号的分辨率、时长、能力和参考素材上限。网关已有模型的下游规格保持原样，vipprompt 不接 Seedance 2.0 的 4K 档位。

创建时自动附带唯一 `X-LF-Task-ID`。网络异常、无法解析的成功响应、409、429 或 5xx 会改为查询原追踪 ID，不重发创建请求。兼容 `taskId/task_id`、`status/task_status`、四种结果 URL 字段及同源相对路径。创建时直接返回成片会立即进入现有产物保存流程，不再查询任务。下载结果不携带上游 API Key；保存方式沿用现有账号与系统产物存储设置。
