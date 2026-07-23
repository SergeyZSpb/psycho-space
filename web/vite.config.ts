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
  },
});
