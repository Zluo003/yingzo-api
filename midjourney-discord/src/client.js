import { Midjourney } from "midjourney";
import fetch from "node-fetch";
import WebSocket from "ws";
import { ProxyAgent } from "proxy-agent";
export async function createClient(account) {
  const agent = account.proxy
    ? new ProxyAgent({ getProxyForUrl: () => account.proxy })
    : undefined;
  const client = new Midjourney({
    SalaiToken: account.token,
    ServerId: account.server_id,
    ChannelId: account.channel_id,
    Ws: true,
    Debug: false,
    MaxWait: 1200,
    fetch: (url, options) => fetch(url, { ...options, agent }),
    WebSocket: class extends WebSocket {
      constructor(url, options) {
        super(url, { ...options, agent });
      }
    },
  });
  let timer;
  try {
    await Promise.race([
      client.init(),
      new Promise((_, reject) => {
        timer = setTimeout(
          () => reject(new Error("Discord connection timed out")),
          30000,
        );
      }),
    ]);
  } catch {
    client.Close();
    agent?.destroy();
    throw new Error("Discord session or Midjourney channel unavailable");
  } finally {
    clearTimeout(timer);
  }
  return {
    client,
    close() {
      client.Close();
      agent?.destroy();
    },
  };
}
