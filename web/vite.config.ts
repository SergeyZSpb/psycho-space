import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { defineConfig } from 'vitest/config';
import type { Plugin } from 'vite';
import vue from '@vitejs/plugin-vue';
import vuetify from 'vite-plugin-vuetify';

const OUT_DIR = '../internal/web/dist';

// Put back the placeholder this build just deleted.
//
// internal/web/embed.go declares `//go:embed all:dist`, which fails to COMPILE
// when that directory holds nothing — so a tracked dist/.gitkeep is what lets
// `go build ./...` work on a checkout where the SPA has never been built (a
// fresh clone, and the Go job of the CI pipeline, which installs no npm). But
// `emptyOutDir` wipes the directory on every build, taking the tracked file
// with it and leaving a deletion staged in everybody's working tree, one
// `git commit -a` away from breaking the build again.
//
// So the build owns restoring it. It is a plugin rather than a line in the npm
// script because the directory belongs to Vite: whoever empties it is who has
// to put the marker back, including a bare `vite build`.
function keepEmbedPlaceholder(): Plugin {
  return {
    name: 'psycho-keep-embed-placeholder',
    apply: 'build',
    closeBundle() {
      writeFileSync(join(__dirname, OUT_DIR, '.gitkeep'), '');
    },
  };
}

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
    keepEmbedPlaceholder(),
  ],
  build: {
    outDir: OUT_DIR,
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
