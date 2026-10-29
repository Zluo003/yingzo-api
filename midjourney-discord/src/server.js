import { Bridge } from "./bridge.js";
import { createClient } from "./client.js";
const bridge = new Bridge({
  filename: process.env.MIDJOURNEY_DATA_FILE || "/data/tasks.sqlite",
  secret: process.env.MIDJOURNEY_BRIDGE_SECRET,
  createClient,
});
const server = bridge.server();
server.requestTimeout = 30000;
server.headersTimeout = 10000;
server.listen(Number(process.env.PORT || 8081), process.env.HOST || "0.0.0.0");
for (const signal of ["SIGINT", "SIGTERM"])
  process.on(signal, () => {
    server.close(async () => {
      await bridge.close();
      process.exit(0);
    });
    setTimeout(() => process.exit(1), 25000).unref();
  });
