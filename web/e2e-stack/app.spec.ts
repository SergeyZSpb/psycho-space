import { test, expect, type Page } from '@playwright/test';
import { loginAs, stack, uniqueTitle } from './fixtures';

// Full-stack end-to-end: real Go binary, real PostgreSQL, real session cookies.
// Where the stubbed suite asserts that a screen *renders*, these assert that an
// action actually *persisted* — the check is always "reload, or ask the API, and
// it is still true", which only holds if the SQL ran.
//
// Note on API calls: always `page.request`, never the bare `request` fixture.
// The fixture is a separate context with no cookies, so it would silently test
// the anonymous path while looking like it tested the authenticated one.

/** The wishlist card carrying this title. */
const cardFor = (page: Page, title: string) => page.locator('.v-card').filter({ hasText: title }).first();

/** The upvote control: an icon button whose accessible name reflects its state. */
const voteButton = (page: Page, title: string) =>
  cardFor(page, title).getByRole('button', { name: /^(Голос|Убрать голос)$/ }).first();

/** Vote count straight from the API — the authoritative number. */
async function votesViaApi(page: Page, title: string): Promise<number> {
  const res = await page.request.get('/api/wishlist/items');
  expect(res.ok()).toBeTruthy();
  const body = (await res.json()) as { items: Array<{ title: string; votes: number }> };
  const item = body.items.find((i) => i.title === title);
  expect(item, `item ${title} missing from /api/wishlist/items`).toBeTruthy();
  return item!.votes;
}

test.describe('anonymous', () => {
  test('landing renders and the API refuses an unauthenticated caller', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'психоспасе' })).toBeVisible();

    const me = await page.request.get('/api/auth/me');
    expect(me.status()).toBe(401);
    // Every error carries the trace id the UI offers to copy.
    expect(await me.json()).toHaveProperty('trace_id');
    expect(me.headers()['x-trace-id']).toBeTruthy();
  });

  test('gated API routes are closed', async ({ page }) => {
    expect((await page.request.get('/api/wishlist/items')).status()).toBe(401);
    expect((await page.request.get('/api/admin/accounts')).status()).toBe(401);
  });
});

test.describe('approved user', () => {
  test.beforeEach(async ({ context }) => {
    await loginAs(context, 'user');
  });

  test('creates an idea that survives a reload, then upvotes and deletes it', async ({ page }) => {
    const title = uniqueTitle('Идея из e2e');

    await page.goto('/app/wishlist');
    await expect(page.getByRole('heading', { name: 'Вишлист', exact: true })).toBeVisible();

    await page.getByLabel('Идея (обязательно)').fill(title);
    await page.getByLabel('Подробности (необязательно)').fill('Создано полным e2e прогоном.');
    await page.getByRole('button', { name: 'Добавить', exact: true }).click();
    await expect(page.getByText(title)).toBeVisible();

    // The real assertion: it came back from Postgres, not from local state.
    await page.reload();
    await expect(page.getByText(title)).toBeVisible();
    expect(await votesViaApi(page, title)).toBe(0);

    // Upvote — the button's accessible name flips once the server confirms.
    await voteButton(page, title).click();
    await expect(cardFor(page, title).getByRole('button', { name: 'Убрать голос' })).toBeVisible();
    expect(await votesViaApi(page, title)).toBe(1);

    // Still cast after a reload, i.e. it is a row in wishlist_votes.
    await page.reload();
    await expect(cardFor(page, title).getByRole('button', { name: 'Убрать голос' })).toBeVisible();

    // Retracting is persisted too.
    await voteButton(page, title).click();
    await expect(cardFor(page, title).getByRole('button', { name: 'Голос' })).toBeVisible();
    expect(await votesViaApi(page, title)).toBe(0);

    await cardFor(page, title).getByRole('button', { name: 'Удалить идею' }).click();
    // Deletion is confirmed in a dialog («Удалить идею?» → Отмена | Удалить).
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Удалить', exact: true }).click();

    // Counting cards, not text: while the dialog is open the title appears
    // twice on the page, and a strict text locator would refuse to resolve.
    await expect(page.locator('.v-card').filter({ hasText: title })).toHaveCount(0);
    await page.reload();
    await expect(page.locator('.v-card').filter({ hasText: title })).toHaveCount(0);
  });

  test('comments round-trip through the database', async ({ page }) => {
    const title = uniqueTitle('Идея с комментарием');
    const comment = uniqueTitle('Комментарий');

    await page.goto('/app/wishlist');
    await page.getByLabel('Идея (обязательно)').fill(title);
    await page.getByRole('button', { name: 'Добавить', exact: true }).click();
    await expect(page.getByText(title)).toBeVisible();

    // A freshly created item comes back already expanded (WishlistView adds it
    // to `expanded`), so the comment form is on screen — clicking the toggle
    // here would close it. The label must be exact: a loose /коммент/ matches
    // the "Комментарии (N)" toggle button before it reaches the textarea.
    const card = cardFor(page, title);
    await card.getByLabel('Оставьте комментарий…').fill(comment);
    await card.getByRole('button', { name: 'Добавить комментарий' }).click();
    await expect(page.getByText(comment)).toBeVisible();

    await page.reload();
    await expect(page.getByText(comment)).toBeVisible();
  });

  test('the admin API is refused to a plain user', async ({ page }) => {
    await page.goto('/app/wishlist');
    // Authenticated but not an admin: 403, not 401 — the distinction matters,
    // because 401 would send the SPA back to the login screen.
    expect((await page.request.get('/api/admin/accounts')).status()).toBe(403);
  });

  test('the game answers 503 while no LLM is configured', async ({ page }) => {
    // The stack deliberately leaves the judge unconfigured — a real call costs
    // money. The contract under test is that it degrades cleanly.
    await page.goto('/app/wishlist');
    const res = await page.request.post('/api/game-khimki/attempt', {
      data: { game_key: 'smalltalk_khimki', character_key: 'vanya', transcript: [], choice: '', anger: 40 },
    });
    expect(res.status()).toBe(503);
    expect(await res.json()).toHaveProperty('trace_id');
  });
});

test.describe('allowlist states', () => {
  // Uses pending2 so the superadmin test below can approve `pending` without
  // this one's expectations depending on which ran first.
  test('a pending account is held on the pending screen with its code', async ({ context, page }) => {
    const acc = await loginAs(context, 'pending2');
    await page.goto('/app/wishlist');
    await expect(page.getByText(/Попроси Сергея/)).toBeVisible();

    // The handle shown is what the admin will be given — 8 hex characters.
    const handle = page.locator('.handle-code');
    await expect(handle).toBeVisible();
    expect((await handle.innerText()).trim()).toMatch(/^[0-9a-f]{8}$/);
    expect(acc.status).toBe('pending');

    // And the gated API stays shut for it.
    expect((await page.request.get('/api/wishlist/items')).status()).toBe(403);
  });

  test('a blocked account is told access was revoked', async ({ context, page }) => {
    await loginAs(context, 'blocked');
    await page.goto('/app/wishlist');
    await expect(page.getByRole('heading', { name: /Доступ отозван/ })).toBeVisible();
  });
});

test.describe('superadmin', () => {
  test.beforeEach(async ({ context }) => {
    await loginAs(context, 'superadmin');
  });

  test('approves the seeded pending account, and it moves tabs', async ({ page }) => {
    const pending = (await stack()).pending;

    await page.goto('/app/admin');
    await expect(page.getByRole('heading', { name: 'Админка' })).toBeVisible();

    const row = page.locator('.v-card').filter({ hasText: pending.display_name }).first();
    await expect(row).toBeVisible();
    await row.getByRole('button', { name: 'принять' }).click();
    await expect(page.getByText(pending.display_name)).toBeHidden();

    await page.getByRole('tab', { name: 'Одобрены' }).click();
    await expect(page.getByText(pending.display_name)).toBeVisible();

    // And it stuck: a reload re-reads the row from Postgres.
    await page.reload();
    await page.getByRole('tab', { name: 'Одобрены' }).click();
    await expect(page.getByText(pending.display_name)).toBeVisible();
  });

  test('open-registration toggle persists', async ({ page }) => {
    await page.goto('/app/admin');
    const sw = page.locator('.v-switch input[type="checkbox"]');
    const before = await sw.isChecked();

    await page.locator('.v-switch').click();
    await expect(sw).toBeChecked({ checked: !before });

    const settings = await (await page.request.get('/api/admin/settings')).json();
    expect(settings.open_registration).toBe(!before);

    // Put it back, so the suite is safe to re-run against a reused stack.
    await page.locator('.v-switch').click();
    await expect(sw).toBeChecked({ checked: before });
    const restored = await (await page.request.get('/api/admin/settings')).json();
    expect(restored.open_registration).toBe(before);
  });
});
