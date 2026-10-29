# DeepSeek shared Search / Fetch

The gateway provides model-independent `POST /v1/tools/search` and `POST /v1/tools/fetch`. Both authenticate with the user's existing Yingzo API key, require an active assigned group and enforce normal user concurrency, balance/subscription and key quotas. They run before model routing, so all platform and Agent groups share the same service without a `model` field.

## Administrator configuration

Under Settings → Web Search, enable **Shared DeepSeek Search / Fetch**, select an existing active DeepSeek API-key account, and enter the USD price per 1,000 Search and Fetch calls. Enter `0` explicitly for free service. This setting is independent of Brave/Tavily Anthropic search emulation. The account's stored key, upstream base URL and proxy are reused; client-supplied DeepSeek keys and account IDs are not accepted.

The `deepseek` member of `/api/v1/admin/settings/web-search-emulation` is:

```json
{
  "enabled": true,
  "account_id": 123,
  "search_price_per_1k": 0.7,
  "fetch_price_per_1k": 0.7,
  "timeout_seconds": 60
}
```

These prices are illustrative USD amounts, not a currency conversion or provider price recommendation. Explicit finite, nonnegative prices and a timeout of 1–300 seconds are required when enabled. Older admin clients omitting `deepseek` preserve its stored settings; disable it explicitly to turn off the service. Account validity is checked when resolving the upstream request. Deployments enforcing an upstream URL allowlist must permit the configured DeepSeek API host.

## Requests and responses

Search accepts `query` (1–128 Unicode characters), optional `count` (1–50), `ori_question` (1–128 characters), `localization` (an IP address), and `need_content`. It preserves DeepSeek's result fields, including title, URL, snippet, longer summary and publication date.

```http
POST /v1/tools/search
Authorization: Bearer <Yingzo key>
Content-Type: application/json

{"query":"Japan facial cleanser consumer preferences","count":8,"need_content":false}
```

Fetch accepts an HTTP(S) `url` without credentials, up to 8192 characters, and returns `{ "title": "…", "content": "Markdown…" }`. The gateway sends this URL as JSON to DeepSeek; it does not retrieve arbitrary client URLs itself. Unknown request fields are rejected. Requests are capped at 64 KiB and upstream responses at 8 MiB. Credential-bearing upstream redirects are rejected under the existing upstream URL policy.

Only valid, nonempty HTTP 200 search results / page content are charged, once per request regardless of result count or token count. Empty successful responses are logged at zero cost; errors, malformed responses and timeouts are not charged. Global configured tool prices apply without model, group, user or peak multipliers. Usage labels `deepseek-search` and `deepseek-fetch` are accounting labels, not LLM catalog entries. Normal balance/subscription, key usage and upstream account quota settlement is reused. Settlement errors return a sanitized error and request ID; successful upstream work settles even if the client disconnects.

There is one upstream request per operation, without model calls or automatic retry. Upstream 429 / 408 and `Retry-After` are preserved; credential or balance failures return sanitized 503 responses. Search / Fetch does not change chat cooldowns. Upstream limits apply per DeepSeek account, separately from model traffic. Operators can use request IDs and gateway usage/error logs to investigate failures without exposing credentials or upstream bodies to users.

Yingzo clients wait at most 15 seconds for web operations. If search fails or returns no relevant sources, they continue with supplied material, mark market assumptions as unverified and never request account troubleshooting. Failed page reads retain existing search snippets. Explicit user cancellation still stops the task. Native model search fallback is not currently implemented.

Official protocol references: [Search guide](https://api-docs.deepseek.com/zh-cn/guides/search/), [Search API](https://api-docs.deepseek.com/zh-cn/api/search/), [Fetch API](https://api-docs.deepseek.com/zh-cn/api/fetch/).
