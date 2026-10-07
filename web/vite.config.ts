/// <reference types="vitest/config" />
import { writeFileSync } from 'node:fs';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react(), {
    name: 'dist-gitkeep',
    apply: 'build',
    // dist/ is not committed; this tracked placeholder lets go:embed compile on a fresh clone.
    writeBundle() { writeFileSync('dist/.gitkeep', ''); },
  }],
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
  },
  test: {
    include: ['src/**/*.test.{ts,tsx}'],
    environment: 'jsdom',
  },
});
