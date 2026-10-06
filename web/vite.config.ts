/// <reference types="vitest/config" />
import { createReadStream } from 'node:fs';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

const require = createRequire(import.meta.url);
const src = fileURLToPath(new URL('./src', import.meta.url));
const monacoESM = fileURLToPath(new URL('./node_modules/monaco-editor/esm/vs', import.meta.url));

/**
 * In mock mode (`pnpm dev:mock`) MSW intercepts API calls in the browser.
 * Its service worker script is served straight from node_modules so it never
 * ends up in public/ or the production build.
 */
function mockServiceWorker(): Plugin {
  return {
    name: 'stampede-msw-worker',
    apply: 'serve',
    configureServer(server) {
      const file = require.resolve('msw/mockServiceWorker.js');
      server.middlewares.use('/mockServiceWorker.js', (_req, res) => {
        res.setHeader('Content-Type', 'text/javascript');
        res.setHeader('Service-Worker-Allowed', '/');
        createReadStream(file).pipe(res);
      });
    },
  };
}

export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss(), ...(mode === 'mock' ? [mockServiceWorker()] : [])],
  resolve: {
    alias: {
      '@': src,
      // Deep imports into monaco's ESM tree (contributions, CSS) that its
      // package exports map does not expose.
      '@monaco-esm': monacoESM,
    },
  },
  define: {
    __MOCK__: JSON.stringify(mode === 'mock'),
  },
  server: {
    port: 5173,
    // The scenario JSON Schema lives at the repository root.
    fs: { allow: ['..'] },
    proxy:
      mode === 'mock'
        ? undefined
        : {
            '/api': {
              target: process.env.STAMPEDE_API ?? 'http://localhost:8080',
              changeOrigin: false,
            },
          },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
    target: 'es2022',
    chunkSizeWarningLimit: 4096,
  },
  worker: { format: 'es' },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    include: ['src/**/*.test.{ts,tsx}'],
  },
}));
