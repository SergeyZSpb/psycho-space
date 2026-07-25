import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';
import vuetify from 'vite-plugin-vuetify';

// The SPA is embedded into the Go binary: Vite emits the build straight into
// internal/web/dist (which //go:embed picks up). Nothing outside web/ is touched
// except that output directory.
export default defineConfig({
  root: '.',
  base: '/',
  plugins: [
    vue(),
    // autoImport treeshakes Vuetify components/directives used in templates.
    vuetify({ autoImport: true }),
  ],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    // Dev convenience: proxy API calls to the local Go server (`./dev.sh run`).
    proxy: {
      '/api': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
    },
  },
  test: {
    environment: 'jsdom',
    globals: false,
    include: ['src/**/*.spec.ts'],
    coverage: {
      provider: 'v8',
      // `json-summary` is what the CI step summary reads; `text` keeps the
      // local run readable. Views/components are excluded because the unit
      // suite covers the lib/store logic — the rendered UI is verified by the
      // Playwright suite instead, and counting it here would report a
      // misleadingly low number for code that IS tested, just not by vitest.
      reporter: ['text', 'json-summary'],
      reportsDirectory: './coverage',
      include: ['src/lib/**/*.ts', 'src/stores/**/*.ts', 'src/api/**/*.ts'],
    },
  },
});
