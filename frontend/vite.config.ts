import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  base: "/app/",
  plugins: [react()],
  build: {
    outDir: "../pkg/webapp/static/app",
    emptyOutDir: true
  }
});
