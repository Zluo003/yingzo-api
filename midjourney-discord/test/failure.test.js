import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { discordFailure } from "../src/failure.js";
import { Bridge } from "../src/bridge.js";
const require = createRequire(import.meta.url);
const wsPath = join(dirname(require.resolve("midjourney")), "discord.ws.js");
const { WsMessage } = require(wsPath);
const moderation = { title: "Banned Prompt Detected", description: "Sorry! Our AI moderator thinks this prompt is probably against our Community Guidelines. /imagine private prompt token-secret" };
function sdkError(method, embed) {
  let failure;
  const receiver = { updateMjEventIdByNonce() {}, log() {}, EventError(_id, error) { failure = error; }, messageUpdate() {} };
  const settled = WsMessage.prototype[method].call(receiver, { id: "message", nonce: "nonce", embeds: [{ color: 16711680, ...embed }] });
  return Promise.resolve(settled).then(() => failure);
}
test("patched SDK forwards create and update moderation diagnostics without acknowledgement", async () => {
  for (const method of ["messageCreate", "messageUpdate"]) {
    const error = await sdkError(method, moderation);
    assert.equal(discordFailure(error), "content_policy_violation");
    assert.equal(error.message.includes("private prompt"), false);
  }
});
test("manual attention, invalid options and unknown failures are not moderation", async () => {
  assert.equal(discordFailure(await sdkError("messageCreate", { title: "Action needed to continue" })), "manual_action_required");
  assert.equal(discordFailure(await sdkError("messageCreate", { title: "Invalid parameter", description: "Check --ar" })), "invalid_parameters");
  assert.equal(discordFailure(Error("Banned Prompt Detected in arbitrary network text")), "discord_task_failed");
  assert.equal(discordFailure(Error("network"), true), "discord_task_timeout");
});
test("bridge persists only the classified reason and never resubmits a refused job", async () => {
  let calls = 0;
  const b = new Bridge({ filename: ":memory:", secret: "test-secret-more-than-32-characters", createClient: async () => ({ client: { Imagine: async () => {
    calls++; throw await sdkError("messageCreate", moderation);
  } }, close() {} }) });
  const job = { id: "imgtask_refused", account: { id: 1, token: "secret", server_id: "123456789012345678", channel_id: "123456789012345679", concurrency: 1 }, action: "imagine", prompt: "private prompt" };
  try {
    b.submit(job);
    await b.running.get(job.id);
    assert.deepEqual(b.get(job.id), { status: "failed", error: "content_policy_violation" });
    b.submit(job);
    assert.equal(calls, 1);
  } finally { await b.close(); }
});
test("postinstall patch can run repeatedly without changing the SDK again", () => {
  const before = readFileSync(wsPath, "utf8");
  execFileSync(process.execPath, ["scripts/patch-sdk.js"]);
  assert.equal(readFileSync(wsPath, "utf8"), before);
});
