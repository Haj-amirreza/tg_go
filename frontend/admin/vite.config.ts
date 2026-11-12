import { env } from "node:process";
import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import tailwind from "@tailwindcss/vite";
const apiTarget = env.NEXT_API_TARGET || "http://127.0.0.1:8080";
export default defineConfig({
  plugins: [vue(), tailwind()],
  publicDir: "../../public/assets/fonts",
  server: {
    proxy: {
      "/api": apiTarget,
      "/readyz": apiTarget,
      "/healthz": apiTarget,
    },
  },
  build: { sourcemap: false },
});
