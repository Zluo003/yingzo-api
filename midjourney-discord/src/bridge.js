import { createHash, timingSafeEqual } from "node:crypto";
import { DatabaseSync } from "node:sqlite";
import { mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { createServer } from "node:http";
const digest = (value) =>
  createHash("sha256").update(JSON.stringify(value)).digest("hex");
const actions = new Set([
  "imagine",
  "edits",
  "select",
  "upscale",
  "variations",
]);
const labelFor = (j) =>
  j.action === "select"
    ? `U${j.index}`
    : j.action === "upscale"
      ? `Upscale (${j.mode === "creative" ? "Creative" : "Subtle"})`
      : `Vary (${j.mode === "strong" ? "Strong" : "Subtle"})`;
function validateAccount(a) {
  if (
    !a ||
    !Number.isSafeInteger(a.id) ||
    a.id < 1 ||
    typeof a.token !== "string" ||
    !a.token.trim() ||
    a.token.startsWith("Bot ") ||
    !/^[0-9]{17,20}$/.test(a.server_id) ||
    !/^[0-9]{17,20}$/.test(a.channel_id)
  )
    throw new Error("invalid_account");
  if (
    !Number.isInteger(a.concurrency) ||
    a.concurrency < 1 ||
    a.concurrency > 100
  )
    throw new Error("invalid_concurrency");
  if (
    a.proxy &&
    !["http:", "https:", "socks:", "socks5:", "socks5h:"].includes(
      new URL(a.proxy).protocol,
    )
  )
    throw new Error("invalid_proxy");
}
const accountKey = (a) =>
  digest([a.id, a.token, a.server_id, a.channel_id, a.proxy || ""]);
function publicResult(r) {
  const u = new URL(r?.uri);
  if (
    u.protocol !== "https:" ||
    u.username ||
    u.password ||
    ![
      "cdn.discordapp.com",
      "media.discordapp.net",
      "cdn.midjourney.com",
    ].includes(u.hostname) ||
    !r.id
  )
    throw new Error("invalid_result");
  return {
    status: "completed",
    content: String(r.content || ""),
    uri: u.href,
    message_id: String(r.id),
    hash: String(r.hash || ""),
    flags: Number(r.flags) || 0,
    width: Number(r.width) || 0,
    height: Number(r.height) || 0,
    options: (r.options || [])
      .filter(
        (o) =>
          typeof o.label === "string" &&
          typeof o.custom === "string" &&
          o.custom.startsWith("MJ::"),
      )
      .map((o) => ({ label: o.label, custom: o.custom })),
  };
}
export class Bridge {
  constructor({ filename, secret, createClient, timeoutMs = 20 * 60 * 1000 }) {
    if (typeof secret !== "string" || secret.length < 32)
      throw new Error(
        "MIDJOURNEY_BRIDGE_SECRET must contain at least 32 characters",
      );
    this.secret = Buffer.from(`Bearer ${secret}`);
    this.createClient = createClient;
    this.timeoutMs = timeoutMs;
    this.lastPruneAt = Date.now();
    this.sessions = new Map();
    this.running = new Map();
    this.active = new Map();
    if (filename !== ":memory:")
      mkdirSync(dirname(filename), { recursive: true, mode: 0o700 });
    this.db = new DatabaseSync(filename);
    this.db.exec(
      `PRAGMA journal_mode=WAL;CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,account_key TEXT NOT NULL,result TEXT NOT NULL,created_at INTEGER NOT NULL);`,
    );
    // Uncertain paid submissions are never repeated after restart.
    this.db.exec(
      `UPDATE tasks SET result='{"status":"interrupted","error":"bridge_restarted"}' WHERE json_extract(result,'$.status') IN ('queued','processing')`,
    );
    this.db
      .prepare("DELETE FROM tasks WHERE created_at < ?")
      .run(Date.now() - 7 * 86400000);
  }
  authorized(value = "") {
    const b = Buffer.from(value);
    return b.length === this.secret.length && timingSafeEqual(b, this.secret);
  }
  get(id) {
    const r = this.db.prepare("SELECT result FROM tasks WHERE id=?").get(id);
    return r && JSON.parse(r.result);
  }
  save(id, r) {
    this.db
      .prepare("UPDATE tasks SET result=? WHERE id=?")
      .run(JSON.stringify(r), id);
  }
  async session(a) {
    const key = accountKey(a);
    if (!this.sessions.has(key)) {
      this.sessions.set(
        key,
        this.createClient(a).catch((e) => {
          this.sessions.delete(key);
          throw e;
        }),
      );
    }
    return this.sessions.get(key);
  }
  async check(a) {
    validateAccount(a);
    await this.session(a);
    return { connected: true };
  }
  submit(j) {
    if (Date.now() - this.lastPruneAt > 3600000) {
      this.db
        .prepare(
          "DELETE FROM tasks WHERE created_at < ? AND json_extract(result,'$.status') NOT IN ('queued','processing')",
        )
        .run(Date.now() - 7 * 86400000);
      this.lastPruneAt = Date.now();
    }
    validateAccount(j.account);
    if (!/^imgtask_[A-Za-z0-9_-]{1,100}$/.test(j.id) || !actions.has(j.action))
      throw new Error("invalid_job");
    if (["imagine", "edits"].includes(j.action)) {
      if (
        typeof j.prompt !== "string" ||
        !j.prompt.trim() ||
        j.prompt.length > 16000
      )
        throw new Error("invalid_prompt");
    } else {
      const p = j.parent;
      if (
        !p ||
        p.account_id !== j.account.id ||
        p.channel_id !== j.account.channel_id ||
        p.server_id !== j.account.server_id ||
        !p.message_id ||
        !Array.isArray(p.options)
      )
        throw new Error("invalid_parent");
      if (
        j.action === "select" &&
        (!Number.isInteger(j.index) || j.index < 1 || j.index > 4)
      )
        throw new Error("invalid_index");
      if (j.action === "upscale" && !["subtle", "creative"].includes(j.mode))
        throw new Error("invalid_mode");
      if (j.action === "variations" && !["subtle", "strong"].includes(j.mode))
        throw new Error("invalid_mode");
      if (
        !p.options.some(
          (o) =>
            o.label?.toLowerCase() === labelFor(j).toLowerCase() &&
            o.custom?.startsWith("MJ::"),
        )
      )
        throw new Error("action_unavailable");
    }
    const key = accountKey(j.account),
      fingerprint = digest(j);
    const existing = this.db
      .prepare("SELECT fingerprint FROM tasks WHERE id=?")
      .get(j.id);
    if (existing) {
      if (existing.fingerprint !== fingerprint)
        throw new Error("idempotency_conflict");
      return { id: j.id };
    }
    if ((this.active.get(key) || 0) >= j.account.concurrency)
      throw new Error("account_busy");
    this.db
      .prepare("INSERT INTO tasks VALUES(?,?,?,?,?)")
      .run(j.id, fingerprint, key, '{"status":"processing"}', Date.now());
    this.active.set(key, (this.active.get(key) || 0) + 1);
    const run = this.execute(j).finally(() => {
      this.active.set(key, this.active.get(key) - 1);
      this.running.delete(j.id);
    });
    this.running.set(j.id, run);
    return { id: j.id };
  }
  async execute(j) {
    let timer;
    let timedOut = false;
    try {
      const work = (async () => {
        const { client } = await this.session(j.account);
        let result;
        if (["imagine", "edits"].includes(j.action))
          result = await client.Imagine(j.prompt);
        else {
          const p = j.parent;
          const o = p.options.find(
            (o) => o.label.toLowerCase() === labelFor(j).toLowerCase(),
          );
          result = await client.Custom({
            msgId: p.message_id,
            customId: o.custom,
            flags: p.flags,
            content: j.action === "variations" ? p.prompt : undefined,
          });
        }
        return publicResult(result);
      })();
      const timeout = new Promise((_, reject) => {
        timer = setTimeout(() => {
          timedOut = true;
          reject(new Error("timeout"));
        }, this.timeoutMs);
      });
      this.save(j.id, await Promise.race([work, timeout]));
    } catch {
      if (timedOut) {
        const key = accountKey(j.account);
        const session = this.sessions.get(key);
        this.sessions.delete(key);
        session?.then((connected) => connected.close()).catch(() => {});
      }
      this.save(j.id, { status: "failed", error: "discord_task_failed" });
    } finally {
      clearTimeout(timer);
    }
  }
  async close() {
    await Promise.allSettled([...this.running.values()]);
    for (const s of this.sessions.values())
      (await s.catch(() => null))?.close();
    this.db.close();
  }
  server() {
    return createServer(async (req, res) => {
      const reply = (status, data) => {
        res.writeHead(status, {
          "Content-Type": "application/json",
          "Cache-Control": "no-store",
        });
        res.end(JSON.stringify(data));
      };
      if (req.method === "GET" && req.url === "/health")
        return reply(200, { ok: true });
      if (!this.authorized(req.headers.authorization))
        return reply(401, { error: "unauthorized" });
      try {
        if (
          req.method === "GET" &&
          /^\/tasks\/imgtask_[A-Za-z0-9_-]+$/.test(req.url)
        ) {
          const data = this.get(req.url.slice(7));
          return reply(data ? 200 : 404, data || { error: "task_not_found" });
        }
        if (
          req.method !== "POST" ||
          !["/tasks", "/accounts/check"].includes(req.url)
        )
          return reply(404, { error: "not_found" });
        const chunks = [];
        let bytes = 0;
        for await (const chunk of req) {
          bytes += chunk.length;
          if (bytes > 128 * 1024) {
            reply(413, { error: "body_too_large" });
            return;
          }
          chunks.push(chunk);
        }
        const data = JSON.parse(Buffer.concat(chunks).toString("utf8"));
        return req.url === "/accounts/check"
          ? reply(200, await this.check(data))
          : reply(202, this.submit(data));
      } catch (error) {
        const known = [
          "invalid_account",
          "invalid_concurrency",
          "invalid_proxy",
          "invalid_job",
          "invalid_prompt",
          "invalid_parent",
          "invalid_index",
          "invalid_mode",
          "action_unavailable",
          "idempotency_conflict",
          "account_busy",
        ];
        const code = known.includes(error.message)
          ? error.message
          : "bridge_request_failed";
        reply(
          code === "account_busy"
            ? 429
            : code === "idempotency_conflict"
              ? 409
              : 400,
          { error: code },
        );
      }
    });
  }
}
