# Agent 素材复用与服务器租约（0.1.190）

`POST /api/v1/agent/assets/resolve` 使用与现有素材上传相同的 Agent API Key 鉴权。单次 1–50 个文件，按 API Key、用户、分组及 SHA-256、字节数、MIME 查找，不能跨凭据复用。

```json
{"assets":[{"sha256":"<64位十六进制>","size":12345,"content_type":"image/png"}],"lease_seconds":600}
```

响应顺序与请求一致；未命中项 `hit=false`，命中项返回 `id`、`url`、`expires_at`、`lease_until`、`metadata`。租约默认 600 秒，接受 60–3600 秒；原保存期限 `expires_at` 不变。新上传也返回服务器创建的十分钟租约。客户端应先批量解析，只上传缺失项，并在提交任务前重新解析整个素材集合。租约到期后，未到原保存期限的文件仍可读取。

```json
{"assets":[{"sha256":"<64位十六进制>","size":12345,"content_type":"image/png","hit":true,"id":"<uuid>","url":"https://example.test/...","expires_at":"2026-09-09T00:00:00Z","lease_until":"2026-09-08T08:10:00Z"}],"lease_seconds":600}
```

解析和清理锁定同一数据库行。公网读取和清理共同使用 `max(expires_at, lease_until)`；清理跳过正在锁定的行。每次解析检查本地文件或 S3 对象元数据，实际文件缺失时返回未命中。URL 是临时读取能力，不应写入公开日志。

上传通过 MultipartReader 边接收边计算哈希，写入一次临时文件后校验并存储。文件 MIME、大小、媒体探测仍校验。S3 上传使用可定位文件流和明确长度；HEAD 使用 HeadObject；单段 Range 使用 GetObject 的 Range，仅传输指定范围。多段 Range 忽略并返回完整内容，非法单段范围返回 416。Server-Timing 提供 receive、validate、store、total 或 resolve 时长。

## 兼容部署

先部署服务端 0.1.190，再更新 Yingzo alpha.5。197 迁移为新增列和索引，启动自动应用；原上传接口保留。旧客户端继续上传。新客户端遇到旧服务器的 404/405/501 时，只复用真实剩余保存期限超过十分钟且 HEAD 成功的缓存，否则重传；本地时间不冒充服务器租约。回滚服务端前应等待活跃租约结束，旧服务端不识别 lease_until。

公网测试环境由用户更新部署至 https://api-key.cc 后再联调。本次本地验收没有提交收费生成任务。

## 验证与测量

- 完整 Go unit suite、golangci-lint；CI 使用 Docker 运行 integration suite。
- `YINGZO_TEST_POSTGRES_DSN=... go test ./internal/handler -run TestTemporaryAssetPostgresLeaseCleanupRace -count=1 -v`：真实 PostgreSQL 197 迁移、32 组租约/清理竞争、原期限到期后租约继续读取、重启、三层凭据隔离与最终删除。CI 专项 job 提供 PostgreSQL 17。
- `go test ./internal/handler -run '^$' -bench BenchmarkTemporaryAssetReceive -benchmem`：8 MiB 上传接收路径比较；不包含媒体解码校验成本。
- Yingzo `scripts/reference_transfer_acceptance.py`：真实两仓代码、临时 PostgreSQL、localhost HTTP，三张合计 9,451,974 字节的 PNG。冷缓存 30.167 ms / 9,452,502 字节 multipart 正文；热缓存 3.615 ms / 0 字节；重启客户端 11.990 ms / 0 字节。HEAD 正文 0 字节，Range 正文 10 字节。
- 8 MiB 接收旧路径约 10.45 ms、33,610,760 B/op，新路径约 12.91 ms、47,326 B/op；独立进程最大 RSS 108,986,368 → 71,221,248 字节。流式路径显著减少分配，并未改善本机冷接收速度。localhost 结果不能推算公网带宽和真实生成耗时。

## 发布门禁补丁

GitHub Security Scan 发现既有依赖问题后，构建工具链由 Go 1.26.5 升到同系列补丁 1.26.8，golang.org/x/image 升到 0.45.0 并更新其必要依赖；nanoid 锁定已修复版本。SheetJS 从 npm 的 0.18.5 改为官方 CDN 0.20.3，并删除已过期的两项 xlsx 豁免。报表导出的 11 项回归通过，生产依赖 audit 门禁通过，govulncheck 未发现可达漏洞。

依据：[Go 发布记录](https://go.dev/doc/devel/release#go1.26.8)、[图片解码修复](https://pkg.go.dev/vuln/GO-2026-6222)、[SheetJS 官方安装说明](https://docs.sheetjs.com/docs/getting-started/installation/nodejs/)、[nanoid 修复公告](https://github.com/advisories/GHSA-2v37-7h3g-55p8)。
