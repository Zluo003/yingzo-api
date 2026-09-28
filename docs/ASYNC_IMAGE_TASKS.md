# Unified asynchronous image tasks

## Configuration and upgrade

Use **System settings → Asset storage → Generated outputs** to configure the image async switch and generated-output local directory or S3/R2 bucket, prefix, endpoint, region, credentials, public domain and signed-link lifetime. Images and videos use this generated-output configuration; reference uploads retain their separate settings. Saving applies immediately and requires the existing step-up verification when enabled. Blank secret fields retain the stored secret.

New installations default to synchronous images. Turning off asynchronous images stops new async admissions; it does not disable synchronous generation, video generation, polling, settlement, or refunds for accepted jobs.

Before the first save of the unified configuration, upgraded installations retain legacy async image S3 settings and the legacy local video location. The first save changes subsequent writes only. Existing files are not moved. Each new generated asset records an immutable encrypted storage profile. Historical objects continue to use their original bucket and credentials for download and deletion. Retired profiles are removed after their final asset is deleted and the upload grace period passes.

The Backup page links to the new location. The old image-storage GET endpoint remains readable; its PUT responds with `409 IMAGE_STORAGE_SETTINGS_MOVED`. Legacy configuration/environment variables remain the upgrade fallback until the unified configuration is saved.

Generated outputs reuse their existing retention, capacity and daily quotas. Default retention is 24 hours; configured values are preserved. Local files use `/media/{asset_id}/asset.ext`. For S3/R2, task queries resolve the stored asset ID and object location to a current public or signed direct-download URL. Do not persist a signed URL as the permanent asset identity. S3/R2 transfer is performed by the gateway after it receives the upstream image/video.

## Submission and compatibility

Send `Prefer: respond-async` with the existing non-streaming request body:

- `POST /v1/images/generations` — OpenAI or Grok images.
- `POST /v1/images/edits` — JSON or multipart reference images.
- `POST /v1beta/models/{model}:generateContent` — Gemini image models.

Agent and composite dispatch use the existing platform resolution and routing. Without the preference, with async disabled, or for an unsupported/streaming request, the same request follows the existing synchronous handler. Clients accept either the original synchronous `200` result or the following `202` receipt, **without submitting again**.

```http
Prefer: respond-async
Idempotency-Key: unique-logical-image-attempt
Authorization: Bearer YOUR_KEY
```

```json
{
  "id": "imgtask_example",
  "task_id": "imgtask_example",
  "object": "image.generation.task",
  "status": "processing",
  "phase": "queued",
  "billing_status": "precharged",
  "refund_status": "none",
  "created_at": 1790593200,
  "deadline_at": 1790595000,
  "expires_at": 1790679600,
  "poll_url": "/v1/images/tasks/imgtask_example"
}
```

The response includes `Location`, `Retry-After: 3` and `Preference-Applied: respond-async`. The deadline is 30 minutes after admission. The existing `/v1/images/generations/async` and `/v1/images/edits/async` routes and no-prefix aliases remain available. Explicit `/async` routes reject admission when the switch is off.

`Idempotency-Key` is scoped to the API key. Repeating the same parsed request returns its original task and never charges twice. Reusing a key with different parameters/content returns `409`. JSON member order and multipart boundary/filename changes do not create different fingerprints; attachment content and order do.

## Polling and uncertain submissions

```text
GET /v1/images/tasks/{task_id}
GET /v1/images/tasks/by-idempotency/{idempotency_key}
```

Use the original API key. Both user and key ownership are checked, including for another key owned by the same user. These reads do not require remaining balance or quota after precharge. Redis remains a read fallback for legacy task IDs.

A task retains `processing`, `completed` or `failed`. `phase` reports queued/executing/saving/refunding/completed/failed; `billing_status` reports precharged/settled/refunding/refunded; `refund_status` is none/pending/refunded. A failed generation with a pending refund stays recoverable by the background compensator. “Refunded” is only returned after the credit transaction commits.

Successful results normalize OpenAI/Grok Base64 or URLs and Gemini `inlineData` to `result.data[].url`, with asset IDs, model, usage and available text metadata. Success is published only after image validation, storage and settlement. Poll again to refresh expiring object URLs.

If the submit response is lost, query by the original idempotency key. Never create another generation to recover an accepted or uncertain request. Poll URLs must resolve to the original gateway origin before attaching credentials.

## Durable execution and billing

New tasks live in PostgreSQL with `SKIP LOCKED` worker claims and renewable execution leases. Request/response snapshots are encrypted database payloads, accessible only to the service, and erased at terminal state; caller API keys are not included. Queued work resumes after restart. An expired lease on an upstream execution whose result was not checkpointed fails and refunds; it is never regenerated. Checkpointed responses resume validation/storage/settlement. Lease fencing prevents late success from replacing a terminal result.

Before `202`, one transaction commits the task, idempotency registration, balance/subscription debit, API-key/platform quota updates and precharge usage row. Insufficient funds or any transaction failure rejects admission before an upstream call. Pricing is snapshotted. Per-image requests use size/count quotes; token-priced images reserve the existing image quote and settle actual usage against frozen prices.

Success appends only a nonzero settlement difference. Failure appends a negative `failure_refund` event and preserves the original debit. `settlement` and `settlement_refund` identify positive/negative success adjustments. The original row receives measured image/token usage and the actual upstream account. Adjustments contribute to net customer cost but do not increment request/image/token counts. Upstream statistical cost remains separate from customer refunds. Every funds event has a unique task business key and is transactionally idempotent.

## YingZo desktop

Both image adapters send the async preference and handle `200`/`202`. A submission journal and its idempotency key are committed before network submission. Accepted IDs are bound to `generation_attempt.remote_job_id`; recovery only queries, downloads and registers existing results. Polling honors `Retry-After`, backs off on network errors and keeps the original connection. Async jobs use the gateway deadline; synchronous fallback retains the five-minute budget.

Images and videos keep the shared eight media slots. A task ID does not release a slot; a remote terminal state does, before local downloading. Task cards show generating/saving/downloading/refund progress. File validation and SQLite recovery retain the original batch, reference images, prompt snapshots and merchant workflows.

## Verification

- Backend: `go test -tags unit ./internal/service ./internal/repository ./internal/handler/... ./internal/server/...`.
- PostgreSQL ledger: `go test -tags integration ./internal/repository -run TestDurableImageLedger` (disposable Docker PostgreSQL/Redis).
- Storage/worker fixtures: set `YINGZO_TEST_POSTGRES_DSN` to a disposable PostgreSQL and run `go test ./internal/service -run 'GeneratedImageStorage|DurableImageWorkerMockUpstream'` (isolated temporary schemas, mock upstream/object store).
- Frontend: `pnpm typecheck`, plus AssetStorageView, BackupView and UsageTable Vitest suites.
- Desktop: `tests/python/test_async_images.py` plus image/video/concurrency/batch/merchant regressions, `pnpm schema:check`, `pnpm schema:compat`, `pnpm typecheck`.

Deploy the server/migration/settings before the desktop update. Rollback of new async admission uses the switch; leave the worker running to finish accepted tasks and refunds.
