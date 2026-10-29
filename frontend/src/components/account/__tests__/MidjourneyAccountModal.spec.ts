import { beforeEach, describe, expect, it, vi } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import MidjourneyAccountModal from "../MidjourneyAccountModal.vue";
import type { Account, AdminGroup } from "@/types";
const { create, update } = vi.hoisted(() => ({
  create: vi.fn(),
  update: vi.fn(),
}));
vi.mock("@/api", () => ({ adminAPI: { accounts: { create, update } } }));
vi.mock("vue-i18n", () => ({ useI18n: () => ({ t: (s: string) => s }) }));
const groups = [
  { id: 7, name: "Agent", platform: "openai", kind: "agent" },
] as AdminGroup[];
function panel(account?: Account) {
  return mount(MidjourneyAccountModal, {
    props: { show: true, groups, proxies: [], account },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' },
      },
    },
  });
}
async function fields(w: ReturnType<typeof panel>) {
  await w.get('[data-testid="mj-name"]').setValue("MJ account");
  await w
    .get('[data-testid="mj-discord-token"]')
    .setValue("new-discord-secret");
  await w.get('[data-testid="mj-server-id"]').setValue("123456789012345678");
  await w.get('[data-testid="mj-channel-id"]').setValue("123456789012345679");
}
describe("Discord MJ account panel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    create.mockResolvedValue({ id: 1 });
    update.mockResolvedValue({ id: 1 });
  });
  it("creates a dedicated Discord account suitable for the pool", async () => {
    const w = panel();
    await fields(w);
    await w.get("form").trigger("submit");
    await flushPromises();
    expect(create).toHaveBeenCalledWith(
      expect.objectContaining({
        platform: "openai",
        type: "apikey",
        group_ids: [7],
        credentials: {
          discord_token: "new-discord-secret",
          discord_server_id: "123456789012345678",
          discord_channel_id: "123456789012345679",
          model_mapping: { "midjourney-v8.2": "midjourney-v8.2" },
        },
        extra: { image_account: true, image_provider: "discord_midjourney" },
      }),
    );
    expect(w.emitted("created")?.[0]).toEqual([
      { imageModels: ["midjourney-v8.2"] },
    ]);
  });
  it("never sends redacted tokens back when editing", async () => {
    const w = panel({
      id: 3,
      name: "Existing MJ",
      group_ids: [7],
      credentials_status: { has_discord_token: true },
      credentials: {
        discord_token: "***",
        discord_server_id: "123456789012345678",
        discord_channel_id: "123456789012345679",
      },
      extra: { image_provider: "discord_midjourney", custom_flag: true },
    } as unknown as Account);
    await w.get("form").trigger("submit");
    await flushPromises();
    expect(update).toHaveBeenCalledWith(
      3,
      expect.objectContaining({
        extra: expect.objectContaining({
          image_provider: "discord_midjourney",
          custom_flag: true,
        }),
      }),
    );
    expect(update.mock.calls[0][1].credentials).not.toHaveProperty(
      "discord_token",
    );
  });
  it("requires server and channel IDs before saving", async () => {
    const w = panel();
    await fields(w);
    await w.get('[data-testid="mj-server-id"]').setValue("bad-id");
    await w.get("form").trigger("submit");
    await flushPromises();
    expect(create).not.toHaveBeenCalled();
    expect(w.get('[role="alert"]').text()).toBe("midjourney.invalidIds");
  });
  it("requires a new Discord token when replacing an APIMart account", async () => {
    const w = panel({
      id: 3,
      name: "Legacy",
      group_ids: [7],
      credentials: { api_key: "***" },
      extra: { image_provider: "apimart_midjourney" },
    } as unknown as Account);
    await w.get("form").trigger("submit");
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    expect(w.get('[role="alert"]').text()).toBe("midjourney.discordRequired");
  });
});
