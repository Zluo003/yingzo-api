# Video Reference Materials

Video generation requests may carry reference media (image / video / audio) that the upstream provider needs to fetch. Downstream clients submit those materials in three shapes, and the gateway normalises all of them into **platform-hosted public URLs** before the request reaches an upstream channel:

| Client submits | Gateway does |
| --- | --- |
| `https://…` public URL | Downloads it, probes it, stores it, and hands the upstream its own URL |
| `data:<mime>;base64,…` inline payload | Decodes it, probes it, stores it, and hands the upstream its own URL |
| `multipart/form-data` file part (JSON part uses `attachment://N`) | Already uploaded by the multipart reader — the upstream gets the platform URL directly |

The upstream therefore never depends on a third-party URL that may expire, require credentials, or be unreachable from the provider's network.

## Reference video duration is measured by the platform

`duration_seconds` on a reference video is **not** a client input:

- Any `duration_seconds` the client sends is discarded during material resolution; it is never forwarded upstream and never used for billing.
- The duration comes from the gateway's own trusted probe (`ffprobe`, or `image.DecodeConfig` for images) of the stored file, and is recorded in the asset's metadata row.
- When a request references an asset that is already in the platform store, the duration is read from that stored probe record instead of re-downloading the file.

The measured seconds are added to the generated seconds for billing:

```text
billable_seconds = generated_seconds + Σ(measured reference video seconds)
```

## Request examples

Public URLs:

```json
{"model":"seedance-2.0","prompt":"animate","duration":8,"resolution":"720p",
 "content":[{"type":"video_url","role":"reference_video","video_url":{"url":"https://cdn.example.com/ref.mp4"}}]}
```

Inline base64 (no upload step for the client):

```json
{"model":"seedance-2.0","prompt":"animate","duration":8,"resolution":"720p",
 "content":[{"type":"image_url","role":"reference_image","image_url":{"url":"data:image/png;base64,iVBORw0KGgo…"}}]}
```

Multipart (JSON part + one `file` part per referenced material, in order):

```text
curl -X POST https://api-key.cc/v1/videos \
  -H "Authorization: Bearer $KEY" \
  -F 'request={"model":"seedance-2.0","prompt":"animate","duration":8,"resolution":"720p",
       "content":[{"type":"video_url","role":"reference_video","video_url":{"url":"attachment://0"}}]}' \
  -F 'file=@ref.mp4;type=video/mp4'
```

## Limits

- Reference video, per clip: 2–15s (`seedance-2.5`: 2–30s); total reference video duration: ≤15s (`seedance-2.5`: ≤30s). These are now evaluated against the **measured** durations.
- Reference counts: `seedance-2.0` and `seedance-2.0-fast` accept 9 images + 3 videos + 3 audios; `seedance-2.5` accepts 30 images + 10 videos + 10 audios, ≤50 materials in total.
- Reference audio must be accompanied by at least one image or video. An audio-only reference set is rejected for **every** model (`seedance-2.0`, `seedance-2.0-fast`, `seedance-2.5`) with `invalid_video_content`. The model catalog declares this per model as `capabilities.supports_audio_only_reference` (`false` for all current models).
- Single material size: image 30 MiB, video 200 MiB, audio 15 MiB. The media type is decided by content sniffing, not by what the client declares, and must be one of the whitelisted types (`jpeg/png/webp/gif/bmp/tiff/heic/heif`, `mp4/quicktime`, `wav/mp3`).
- Only `http` / `https` URLs are fetched. Loopback, private-range and link-local addresses (including cloud metadata endpoints) are refused, redirects are re-validated hop by hop, and the download is capped by the size limit above with a 5-minute timeout.
- Identical materials referenced more than once in the same request are downloaded and stored once; each content item still counts its own measured duration.
- Resolution happens synchronously while the task is submitted: submission latency includes downloading and probing every material.

## Storage, capacity and cleanup

Files live in the platform temporary-asset store and are served from `/media/{asset_id}/asset.{ext}` (or `/temporary-assets/{token}`). Configuration lives in **Admin → Settings → Asset storage** (`/api/v1/admin/file-service/settings`).

The store holds two categories, each with **its own retention and capacity budget** (`purpose` column, migration 241):

| Category | Written by | Retention | Capacity |
| --- | --- | --- | --- |
| `reference` | Downstream uploads: inline base64, multipart parts, fetched public URLs, and the agent `/assets` endpoint | `retention_hours` (1–720) | `max_total_bytes` (0 = unlimited) |
| `generated` | Results rehosted from upstream (`/media/...` deliverables) | `result_retention_hours` (1–8760) | `result_max_total_bytes` (0 = unlimited) |

The split exists so that a flood of client uploads cannot evict files that were already delivered downstream. Eviction is always scoped to one category.

Shared settings:

| Setting | Meaning |
| --- | --- |
| `public_base_url` | Origin used to build the URLs handed to upstream. Must be HTTPS outside localhost. Empty means "derive from the request origin"; the resolved value is returned as `effective_public_base_url`. |
| `capacity_reserve_percent` | 0–50, default 10. Headroom kept below each category's cap: cleanup starts at `cap × (1 − reserve/100)`. See below. |
| `daily_max_count` / `daily_max_bytes` | Rolling 24-hour **upload** quota per credential (API key + user). Applies to reference materials. |
| `backend` / `s3.*` | Store files locally or in S3-compatible object storage. |
| `local_dir` | Absolute path of the local asset root, used when `backend` is `local`. Empty means the default (`<data_dir>/agent-assets`). See "Where local files live" below. |

Capacity is a **waterline, not a hard wall**: cleanup starts before the cap is reached, so writes never fail merely because storage is exactly full.

- `capacity_reserve_percent` (0–50, default **10**) is applied to each category's own cap. Eviction starts once usage exceeds `cap × (1 − reserve/100)`: with a 100 GiB cap and 10% reserve, cleanup begins at 90 GiB.
- Past the waterline the gateway evicts in two tiers, until the incoming write fits under the waterline again:
  1. Files **not under a read lease**, closest to expiry first. This never disturbs an in-flight read.
  2. Only if that is not enough, files **still under a lease**, oldest lease first. Every upload carries a short lease, so a category whose budget is entirely filled by very recent uploads would otherwise be unable to free anything — which is exactly the "reject when full" behaviour this design removes. Treat this tier as the price of never refusing a write: a file can be evicted while a provider is still fetching it.
- There is deliberately **no "reject when full" policy**: reference materials and delivered results must both keep accepting writes. `507 temporary_asset_capacity_exceeded` is only the last-resort guard for when there is literally nothing left to delete — in practice, when the waterline is smaller than a single material (or than the write being attempted).
- Setting the reserve to `0` is allowed and means cleanup starts exactly at the cap (no headroom).
- Keep the waterline (`cap × (1 − reserve/100)`) comfortably above the largest single material (video: 200 MiB), otherwise one oversized write can never fit and returns `507` no matter how much is evicted. The `507` is also the transient double-usage guard: a write briefly occupies its own space before older files are removed.

`GET .../settings` reports `usage.reference_files` / `reference_bytes` and `usage.generated_files` / `generated_bytes` so each category can be compared against its own budget.

## Where local files live

Assets are written as `<root>/<asset_id>/object` and served back through
`/media/<asset_id>/asset.<ext>`. The root is:

1. `local_dir` when set in **Admin → Settings → Asset storage**,
2. otherwise the deployment default — `AGENT_ASSETS_HOST_DIR` when the deployment provides it,
3. otherwise `<data_dir>/agent-assets`.

Rules that apply everywhere:

- `local_dir` must be an **absolute path** and must **already exist**. The service never creates a
  configured directory: auto-creating one could silently put assets somewhere the operator did not
  intend (inside a container's writable layer, for instance). A missing directory, a path that is
  not a directory, or a non-writable directory fails the save with a clear error instead of
  breaking uploads later.
- Changing the root affects **new writes only**. Every asset row stores the absolute path it was
  written to, so earlier files stay readable while that path is still reachable; move them along
  (or let them expire) if you relocate the root.

### Shell (systemd) deployment — the simple case

`install.sh` creates `/opt/sub2api/data`, chowns it to the `sub2api` service user, and the unit runs
with `WorkingDirectory=/opt/sub2api`. So the default root
`/opt/sub2api/data/agent-assets` is already a real host directory with the right ownership:
**nothing to configure**.

To keep assets on another disk instead:

```bash
sudo mkdir -p /data/yingzo-assets
sudo chown -R sub2api:sub2api /data/yingzo-assets
```

then enter `/data/yingzo-assets` once in **Admin → Settings → Asset storage** (or set
`FILE_SERVICE_LOCAL_DIR=/data/yingzo-assets` for the service, e.g. as an `Environment=` line in the
unit). That is a one-time step — online upgrades only swap the binary (the previous one is kept as
`sub2api.backup` for rollback) and never touch the data directory.

### Docker deployment

A container cannot see host paths, which is why Docker needs extra plumbing: the host directory is
bind-mounted in, and the panel field must hold a path that exists *inside* the container. The
compose files mount it **at the same path** so there is no translation to get wrong:

```bash
AGENT_ASSETS_HOST_DIR=/data/yingzo-assets   # host directory, create it first
docker compose up -d
```

- The mount is `${AGENT_ASSETS_HOST_DIR:-./data/agent-assets}:${AGENT_ASSETS_HOST_DIR:-/app/data/agent-assets}`,
  so a configured directory maps to the identical path on both sides and `AGENT_ASSETS_HOST_DIR`
  also becomes the default `local_dir`.
- The entrypoint runs as root and chowns `/app/data` plus the mounted `AGENT_ASSETS_HOST_DIR` root,
  so uid 1000 can write without manual work.
- Leave the panel field empty to follow `AGENT_ASSETS_HOST_DIR`; unset, the historical default
  `/app/data/agent-assets` ← host `./data/agent-assets` still applies.

## Error codes

| Code | HTTP | Meaning |
| --- | --- | --- |
| `invalid_reference_material` | 400 | Malformed inline payload (not base64, missing comma, empty) |
| `unsupported_reference_material` | 400 | Sniffed type is not a whitelisted reference media type |
| `reference_material_download_failed` | 400 | Public URL refused by the SSRF policy, unreachable, non-2xx, or over the size limit |
| `reference_material_unavailable` | 400 | A platform asset URL that is missing, expired, or owned by another credential |
| `reference_video_duration_unavailable` | 400 | A reference video without a usable measured duration |
| `reference_material_lookup_failed` | 503 | Asset metadata could not be read |
| `media_too_large` | 413 | Material exceeds the per-type size limit |
| `temporary_asset_quota_exceeded` | 429 | The credential exceeded its rolling 24-hour upload quota |
| `temporary_asset_capacity_exceeded` | 507 | Last-resort guard: the write cannot fit even after evicting everything evictable past the waterline (all candidates leased, or the cap is below current usage) |
| `media_upload_unavailable` | 503 | The asset store is not configured or its spool is unavailable |
| `FILE_STORAGE_LOCAL_DIR_UNAVAILABLE` | 400 | Saving storage settings: the configured `local_dir` cannot be created or written |

Reference-material URLs are bearer capabilities for temporary reads: do not write them into public logs.
