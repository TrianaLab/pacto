import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig(({ mode }) => ({
  plugins: [svelte()],
  build: {
    outDir: '../ui',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
      '/metrics': 'http://localhost:8080',
    },
  },
  test: {
    environment: 'jsdom',
    // e2e/ and e2e-live/ hold Playwright browser specs (*.spec.ts) driven by a real
    // browser, not vitest/jsdom — exclude them from the unit run.
    exclude: ['e2e/**', 'e2e-live/**', '**/node_modules/**', '**/dist/**'],
  },
  ...(mode === 'test' ? { resolve: { conditions: ['browser'] } } : {}),
}));
