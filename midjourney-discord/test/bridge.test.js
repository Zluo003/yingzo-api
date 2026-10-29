import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Bridge } from "../src/bridge.js";
const secret = "test-secret-that-is-more-than-32-characters";
const account = (id) => ({
  id,
  token: `test-user-token-${id}`,
  server_id: "123456789012345678",
  channel_id: "123456789012345679",
  concurrency: 1,
});
const result = {
  uri: "https://cdn.discordapp.com/attachments/grid.png",
  id: "123456789012345680",
  hash: "hash",
  flags: 0,
  options: [{ label: "U1", custom: "MJ::JOB::upsample::1::hash" }],
  width: 2048,
  height: 2048,
};
async function finish(b, id) {
  for (let i = 0; i < 30; i++) {
    const r = b.get(id);
    if (r.status !== "processing") return r;
    await new Promise((r) => setTimeout(r, 5));
  }
  throw Error("test task did not finish");
}
const bridge = (factory) =>
  new Bridge({
    filename: ":memory:",
    secret,
    createClient:
      factory ||
      (async () => ({ client: { Imagine: async () => result }, close() {} })),
  });
test("two accounts have independent sessions and concurrency limits", async () => {
  const calls = [],
    release = [];
  const b = bridge(async (a) => ({
    client: {
      Imagine: () =>
        new Promise((resolve) => {
          calls.push(a.id);
          release.push(() => resolve(result));
        }),
    },
    close() {},
  }));
  try {
    b.submit({
      id: "imgtask_one",
      account: account(1),
      action: "imagine",
      prompt: "cat",
    });
    assert.throws(
      () =>
        b.submit({
          id: "imgtask_busy",
          account: account(1),
          action: "imagine",
          prompt: "cat",
        }),
      /account_busy/,
    );
    b.submit({
      id: "imgtask_two",
      account: account(2),
      action: "edits",
      prompt: "change cat --edit https://x.test/cat.png",
    });
    await new Promise((r) => setImmediate(r));
    assert.deepEqual(calls, [1, 2]);
    release.forEach((r) => r());
    assert.equal((await finish(b, "imgtask_one")).status, "completed");
    assert.equal((await finish(b, "imgtask_two")).status, "completed");
  } finally {
    release.forEach((r) => r());
    await b.close();
  }
});
test("task IDs never repeat Discord commands and conflicting replays fail", async () => {
  let calls = 0;
  const b = bridge(async () => ({
    client: {
      Imagine: async () => {
        calls++;
        return result;
      },
    },
    close() {},
  }));
  const j = {
    id: "imgtask_once",
    account: account(1),
    action: "imagine",
    prompt: "cat",
  };
  try {
    b.submit(j);
    b.submit(j);
    await finish(b, j.id);
    b.submit(j);
    assert.equal(calls, 1);
    assert.throws(
      () => b.submit({ ...j, prompt: "dog" }),
      /idempotency_conflict/,
    );
  } finally {
    await b.close();
  }
});
test("follow-up uses returned parent button and rejects another account", async () => {
  const calls = [];
  const b = bridge(async () => ({
    client: {
      Custom: async (p) => {
        calls.push(p);
        return { ...result, options: [] };
      },
    },
    close() {},
  }));
  const parent = {
    account_id: 1,
    channel_id: account(1).channel_id,
    server_id: account(1).server_id,
    message_id: result.id,
    hash: "hash",
    flags: 0,
    options: [
      {
        label: "Upscale (Creative)",
        custom: "MJ::JOB::upsample_v8_creative::actual",
      },
    ],
  };
  try {
    assert.throws(
      () =>
        b.submit({
          id: "imgtask_wrong",
          account: account(2),
          action: "upscale",
          mode: "creative",
          parent,
        }),
      /invalid_parent/,
    );
    b.submit({
      id: "imgtask_hd",
      account: account(1),
      action: "upscale",
      mode: "creative",
      parent,
    });
    await finish(b, "imgtask_hd");
    assert.equal(calls[0].customId, "MJ::JOB::upsample_v8_creative::actual");
    assert.equal(calls[0].msgId, result.id);
  } finally {
    await b.close();
  }
});
test("durable receipts contain no token and interrupted commands are not replayed", async () => {
  const dir = mkdtempSync(join(tmpdir(), "yingzo-mj-")),
    filename = join(dir, "tasks.sqlite");
  let b = new Bridge({
    filename,
    secret,
    createClient: async () => ({
      client: { Imagine: async () => result },
      close() {},
    }),
  });
  const j = {
    id: "imgtask_saved",
    account: account(1),
    action: "imagine",
    prompt: "cat",
  };
  try {
    b.submit(j);
    await finish(b, j.id);
    await b.close();
    assert.equal(
      readFileSync(filename).includes(Buffer.from(j.account.token)),
      false,
    );
    let calls = 0;
    b = new Bridge({
      filename,
      secret,
      createClient: async () => {
        calls++;
        throw Error("must not connect");
      },
    });
    b.submit(j);
    assert.equal(b.get(j.id).status, "completed");
    assert.equal(calls, 0);
    b.db
      .prepare("UPDATE tasks SET result=? WHERE id=?")
      .run('{"status":"processing"}', j.id);
    await b.close();
    b = new Bridge({
      filename,
      secret,
      createClient: async () => {
        calls++;
        throw Error("must not connect");
      },
    });
    b.submit(j);
    assert.equal(b.get(j.id).status, "interrupted");
    assert.equal(calls, 0);
  } finally {
    await b.close();
    rmSync(dir, { recursive: true, force: true });
  }
});
test("HTTP requires secret and returns task receipts", async () => {
  const b = bridge(),
    server = b.server();
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const url = `http://127.0.0.1:${server.address().port}`;
  try {
    assert.equal((await fetch(url + "/tasks/imgtask_x")).status, 401);
    assert.equal((await fetch(url + "/health")).status, 200);
    const r = await fetch(url + "/tasks", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${secret}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        id: "imgtask_http",
        account: account(1),
        action: "imagine",
        prompt: "cat",
      }),
    });
    assert.equal(r.status, 202);
    assert.equal((await r.json()).id, "imgtask_http");
  } finally {
    await new Promise((r) => server.close(r));
    await b.close();
  }
});
test("SDK patch and expected APIs are available", async () => {
  const { Midjourney } = await import("midjourney");
  assert.equal(typeof Midjourney.prototype.Imagine, "function");
  assert.equal(typeof Midjourney.prototype.Custom, "function");
  const s = readFileSync(
    new URL("../node_modules/midjourney/libs/discord.ws.js", import.meta.url),
    "utf8",
  );
  assert.equal(s.includes("await this.verifyHuman(message);"), false);
  assert.equal(s.includes("return this.continue(message);"), false);
});
