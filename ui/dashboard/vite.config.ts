import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "path";

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 51735,
    strictPort: true,
    proxy: {
      // changeOrigin must stay false. The dashboard compares the Origin
      // header against the request Host (internal/dashboard/server/middleware.go),
      // so the two have to describe the same origin. The shorthand form
      // ("/api": "<url>") forces changeOrigin: true, which rewrites Host to
      // 127.0.0.1:18795 while the browser still sends Origin: ...:51735 -> 403
      // "origin not allowed" -> the SPA reports "Invalid password" even when
      // the password is correct.
      "/api": {
        target: "http://127.0.0.1:18795",
        changeOrigin: false,
      },
    },
  },
});
