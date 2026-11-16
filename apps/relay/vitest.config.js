import { defineConfig } from "vitest/config";
import { cloudflareTest } from "@cloudflare/vitest-plugin";

export default defineConfig({
  plugins: [
    cloudflareTest({
      wrangler: { configPath: "./wrangler.jsonc" },
      miniflare: {
        bindings: {
          // 試験用の鍵（32バイトの 0x01 を base64url にしたもの）。
          RELAY_KEY: "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE",
          APP_WAIT_MS: "400",
        },
      },
    }),
  ],
  test: { include: ["test/**/*.test.js"] },
});
