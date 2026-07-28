import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

// Pacing of дядя Ваня's rapped reply — the "post-credits" roll in
// GameKhimkiView.vue, paced by lib/gameKhimkiCredits.ts and driven by
// composables/useVersePacing.ts.
//
// The unit suite proves the arithmetic and the state machine; this proves the
// two things only a browser can show: that the verse really does stand still
// before it starts moving, and that tap / Enter really do drop the whole text
// onto the screen without breaking the never-scroll play layout.
//
// Everything is stubbed inside this file rather than in e2e/fixtures.ts, so the
// game keeps its own fixtures and this spec stands alone.

// Constants mirrored from src/lib/gameKhimkiCredits.ts. Duplicated on purpose:
// if someone changes the pacing, this suite should fail and be looked at rather
// than silently follow along.
const START_DELAY_SECONDS = 2.5;
const CYCLES = 2;
// Upper bound on the roll speed the player may be subjected to, in CSS px/s. A
// verse line is ~21px, so this is about a line every 1.75s — already faster than
// the tuned pace, and set as a ceiling rather than an equality so a deliberate
// re-tune inside the readable range does not fail the suite.
const MAX_READABLE_PX_PER_SEC = 12;

// Twelve bars — comfortably taller than the verse window at every viewport the
// suite runs, so the roll is guaranteed to arm.
const LONG_VERSE = [
  'Ты пришёл ко мне во двор, а я тут сторож и король,',
  'У меня тут свой закон, у тебя тут только роль.',
  'Ты стоишь передо мной, весь такой из себя гость,',
  'А я видел сто таких, и у каждого был хвост.',
  'Химки спят, а я не сплю, я тут слушаю бетон,',
  'Он мне шепчет по ночам про подъезд и домофон.',
  'Ты сказал бы что-нибудь, да язык твой как кирпич,',
  'Тут словами не возьмёшь, тут не надо строить дичь.',
  'Я тут двадцать восемь лет, я тут каждый камень знал,',
  'А ты ключ свой потерял и на лавочку упал.',
  'Ну давай, попробуй, друг, расскажи мне про своё,',
  'Может быть и пропущу, а пока стоим вдвоём.',
].join('\n');

const ART = {
  key: 'neutral',
  emoji: '🧔',
  gradient: 'linear-gradient(160deg, #333, #111)',
};

const CONFIG = {
  game_key: 'smalltalk_khimki',
  title: 'Смолтолк в Химках',
  intro: 'Ты потерял ключи. Дядя Ваня стоит у подъезда.',
  default_character: 'vanya',
  max_anger: 100,
  anger_lose_at: 90,
  start_anger: 20,
  characters: [
    {
      key: 'vanya',
      name: 'Дядя Ваня',
      goal: 'Попасть домой',
      greeting: LONG_VERSE,
      opening_options: ['Здравствуйте', 'Я тут живу', 'Пропустите', 'Молча стоять'],
      arts: [ART],
    },
  ],
};

const EMPTY_BOARDS = {
  boards: { longest_win: [], shortest_win: [], longest_loss: [], shortest_loss: [] },
};

async function stubGame(page: Page): Promise<void> {
  await page.addInitScript(() => {
    try {
      localStorage.setItem('ps-theme', 'dark');
      localStorage.setItem('ps-cookie-consent', '1');
    } catch {
      /* ignore */
    }
  });

  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const json = (body: unknown) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json; charset=utf-8',
        headers: { 'X-Trace-Id': 'e2e-trace-id' },
        body: JSON.stringify(body),
      });

    if (path === '/api/auth/me') {
      return json({
        account: {
          id: 'me-1',
          display_name: 'Тест Пользователь',
          avatar_url: '',
          vk_url: 'https://vk.com/id1',
          role: 'user',
          status: 'approved',
          handle: 'ab12cd34',
        },
      });
    }
    if (path === '/api/game-khimki/config') return json(CONFIG);
    if (path === '/api/game-khimki/runs/leaderboard') return json(EMPTY_BOARDS);
    if (path === '/api/game-khimki/runs/me') return json({ successes: 0, plays: 0, best_steps: 0 });
    return json({});
  });
}

/** Open the game and start a run, leaving the long opening verse on screen. */
async function startRun(page: Page) {
  await page.goto('/app/game-khimki');
  await page.getByRole('button', { name: 'Погнали домой' }).click();
  const reel = page.locator('.verse-reel');
  await expect(reel).toBeVisible();
  return reel;
}

/** Vertical offset of the reel, which the roll animation drags upward. */
async function reelTop(page: Page): Promise<number> {
  const box = await page.locator('.verse-reel').boundingBox();
  return box?.y ?? Number.NaN;
}

test.describe('rap pacing', () => {
  test.beforeEach(async ({ page }) => {
    await stubGame(page);
  });

  test('the verse holds still long enough to read the first bar', async ({ page }) => {
    const reel = await startRun(page);
    await expect(reel).toHaveClass(/rolling/);

    const atStart = await reelTop(page);
    // Well inside the opening hold: nothing may have moved yet. Before this
    // change the verse started sliding on the very first frame, which is what
    // made the opening line unreadable.
    await page.waitForTimeout(START_DELAY_SECONDS * 1000 * 0.6);
    expect(Math.abs((await reelTop(page)) - atStart)).toBeLessThan(2);

    // ...and once the hold is over it really does move, upward.
    await page.waitForTimeout(START_DELAY_SECONDS * 1000 * 0.4 + 2200);
    expect(await reelTop(page)).toBeLessThan(atStart - 4);
  });

  test('the roll is armed with the slow, bounded pacing', async ({ page }) => {
    const reel = await startRun(page);
    const style = await reel.evaluate((el) => {
      const s = getComputedStyle(el);
      return {
        delay: s.animationDelay,
        count: s.animationIterationCount,
        duration: parseFloat(s.animationDuration),
        timing: s.animationTimingFunction,
      };
    });
    expect(style.delay).toBe(`${START_DELAY_SECONDS}s`);
    // It settles after a couple of passes instead of rotating forever.
    expect(style.count).toBe(String(CYCLES));
    expect(style.timing).toBe('linear');

    // One pass travels exactly one copy of the verse, so height over duration is
    // the reading speed the player actually gets — derived from the rendered
    // verse rather than from a constant, so it holds at every viewport. The
    // original pacing came out at 22px/s here, which is what was unreadable.
    const heightPx = await page.locator('.verse').first().evaluate((el) => el.scrollHeight);
    expect(heightPx).toBeGreaterThan(0);
    expect(heightPx / style.duration).toBeLessThanOrEqual(MAX_READABLE_PX_PER_SEC);
  });

  test('tapping the verse skips straight to the full text', async ({ page }) => {
    await startRun(page);
    const frame = page.getByRole('button', { name: 'Показать куплет целиком' });
    await expect(frame).toBeVisible();

    // Tap target rule: the affordance has to be reachable with a thumb.
    const box = await frame.boundingBox();
    expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);

    await frame.click();
    await expect(page.locator('.verse-viewport')).toHaveClass(/verse-viewport--revealed/);
    await expect(page.locator('.verse-reel')).not.toHaveClass(/rolling/);
    // Once everything is on screen the control retires rather than lingering.
    await expect(frame).toHaveCount(0);
  });

  test('the skip affordance answers the keyboard too', async ({ page }) => {
    await startRun(page);
    const frame = page.getByRole('button', { name: 'Показать куплет целиком' });
    await frame.focus();
    await expect(frame).toBeFocused();
    await frame.press('Enter');
    await expect(page.locator('.verse-viewport')).toHaveClass(/verse-viewport--revealed/);
  });

  test('revealing the verse keeps the options on screen and the page unscrolled', { tag: '@wide' }, async ({ page }) => {
    await startRun(page);
    await page.getByRole('button', { name: 'Показать куплет целиком' }).click();

    // The expanded verse takes its height from the picture above it, not from
    // the answer buttons below it: all four must still be within the viewport.
    const options = page.locator('.option-btn');
    await expect(options).toHaveCount(4);
    const viewportHeight = page.viewportSize()?.height ?? 0;
    const last = await options.last().boundingBox();
    expect(last).not.toBeNull();
    expect((last?.y ?? 0) + (last?.height ?? 0)).toBeLessThanOrEqual(viewportHeight);

    // ...and the play screen still never scrolls sideways.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test('reduced motion gets the whole verse at once and nothing animates', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await startRun(page);

    await expect(page.locator('.verse-viewport')).toHaveClass(/verse-viewport--revealed/);
    await expect(page.locator('.verse-reel')).not.toHaveClass(/rolling/);

    // Nothing moves, at any point.
    const atStart = await reelTop(page);
    await page.waitForTimeout(START_DELAY_SECONDS * 1000 + 1500);
    expect(Math.abs((await reelTop(page)) - atStart)).toBeLessThan(2);

    // The verse is rendered once, not doubled up for a loop that never runs.
    await expect(page.locator('.verse')).toHaveCount(1);
  });
});
