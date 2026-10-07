/// <reference types="vitest/config" />
import { cpSync, writeFileSync } from 'node:fs';
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';

// Excalidraw falls back to esm.sh for canvas fonts. Point that fallback at our own copy, served under
// /excalidraw/, so the page never names a CDN. Xiaolai (CJK, 13 MB) goes to dist-fonts/ instead of the
// embedded dist/; the server serves it from KY_EXCALIDRAW_FONTS_DIR.
function excalidrawSelfHosted(): Plugin {
  const cdn = /`https:\/\/esm\.sh\/.*?\/dist\/prod\/`/;
  let replaced = false;
  return {
    name: 'excalidraw-self-hosted',
    apply: 'build',
    transform(code, id) {
      if (!id.includes('@excalidraw/excalidraw/dist/prod/') || !cdn.test(code)) return null;
      replaced = true;
      return code.replace(cdn, 'window.location.origin + "/excalidraw/"');
    },
    buildEnd() {
      if (!replaced) this.error('Excalidraw CDN fallback not found; re-check self-hosting after upgrading.');
    },
    writeBundle() {
      const fonts = 'node_modules/@excalidraw/excalidraw/dist/prod/fonts';
      cpSync(fonts, 'dist/excalidraw/fonts', { recursive: true, filter: src => !src.includes('/Xiaolai') });
      cpSync(`${fonts}/Xiaolai`, 'dist-fonts/Xiaolai', { recursive: true });
      // dist/ is not committed; this tracked placeholder lets go:embed compile on a fresh clone.
      writeFileSync('dist/.gitkeep', '');
    },
  };
}

export default defineConfig({
  plugins: [react(), excalidrawSelfHosted()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/scim': 'http://localhost:8080',
      '/saml': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      input: { main: 'index.html', whiteboard: 'whiteboard.html' },
    },
  },
  test: {
    include: ['src/**/*.test.{ts,tsx}'],
    environment: 'jsdom',
  },
});
