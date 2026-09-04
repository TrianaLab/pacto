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
    // jsdom has no Web Animations API and no matchMedia; Svelte's transitions,
    // animate:flip and prefersReducedMotion all need them. See src/test-setup.ts.
    setupFiles: ['./src/test-setup.ts'],
    // e2e/, e2e-live/ and e2e-docs-site/ hold Playwright browser specs (*.spec.ts)
    // driven by a real browser, not vitest/jsdom — exclude them from the unit run.
    exclude: ['e2e/**', 'e2e-live/**', 'e2e-docs-site/**', '**/node_modules/**', '**/dist/**'],
  },
  ...(mode === 'test' ? { resolve: { conditions: ['browser'] } } : {}),
}));
