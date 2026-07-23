// One-off generator for the Open Graph preview image (web/public/og.png, 1200x630).
// Messengers/crawlers need a raster image, so we screenshot an inline branded HTML
// with the already-installed Chromium. Run: `mise exec -- node scripts/gen-og.mjs`.
// The committed PNG is the artifact; this does NOT run in CI.

import { chromium } from '@playwright/test';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const WIDTH = 1200;
const HEIGHT = 630;

const html = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  html, body { width: ${WIDTH}px; height: ${HEIGHT}px; }
  body {
    background: #08201b;
    font-family: 'DejaVu Sans', 'Liberation Sans', system-ui, sans-serif;
    color: #e6fffb;
    overflow: hidden;
    position: relative;
  }
  .glow {
    position: absolute; inset: 0;
    background: radial-gradient(60% 65% at 50% 38%, rgba(45,212,191,0.45) 0%, rgba(45,212,191,0) 70%);
  }
  .frame {
    position: absolute; inset: 24px;
    border: 2px solid rgba(45,212,191,0.35);
    border-radius: 28px;
  }
  .wrap {
    position: absolute; inset: 0;
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    text-align: center; padding: 0 80px;
  }
  .brand {
    font-size: 150px; font-weight: 800; letter-spacing: 2px; line-height: 1;
    color: #5eead4;
    text-shadow: 0 0 60px rgba(45,212,191,0.75), 0 0 22px rgba(45,212,191,0.6);
  }
  .tagline {
    margin-top: 34px; font-size: 40px; font-weight: 400; line-height: 1.25;
    color: #c7f5ee; opacity: 0.92; max-width: 900px;
  }
  .footer {
    position: absolute; bottom: 54px; left: 0; right: 0;
    text-align: center; font-size: 26px; color: #2dd4bf; font-weight: 700; letter-spacing: 1px;
  }
</style></head>
<body>
  <div class="glow"></div>
  <div class="frame"></div>
  <div class="wrap">
    <div class="brand">психоспасе</div>
    <div class="tagline">это супер нейрослоп приложулька оххх оххх</div>
  </div>
  <div class="footer">вишлист идей · голосования · вход через VK ID</div>
</body></html>`;

const outPath = resolve(dirname(fileURLToPath(import.meta.url)), '..', 'public', 'og.png');

const browser = await chromium.launch();
try {
  const page = await browser.newPage({ viewport: { width: WIDTH, height: HEIGHT }, deviceScaleFactor: 1 });
  await page.setContent(html, { waitUntil: 'networkidle' });
  await page.screenshot({ path: outPath, clip: { x: 0, y: 0, width: WIDTH, height: HEIGHT } });
  console.log('wrote', outPath);
} finally {
  await browser.close();
}
