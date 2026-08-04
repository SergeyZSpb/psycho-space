import { expect, test } from '@playwright/test';
import { loginAs } from './fixtures';

/**
 * «АДМИН ФИНТЕХА» — full stack: the real Go binary, a real PostgreSQL and a real
 * session cookie.
 *
 * WHAT THE STUBBED SUITE CANNOT SAY. Everything about how the plan LOOKS is
 * asserted in `web/e2e/gamefintechAdmin.spec.ts`, where the test is the server and
 * can serve any floor it likes. What no stub can say is that the payload has the
 * shape this client believes it has, and that the floor an admin is shown is the
 * same floor the players are standing in — two endpoints, two audiences, one
 * stored layout. A stub would agree with itself whatever either side did.
 *
 * Note on API calls: always `page.request`, never the bare `request` fixture. The
 * fixture is a separate context with no cookies, so it would silently test the
 * anonymous path while looking like it tested the authenticated one.
 *
 * The harness shares one database, one server and six fixed accounts at
 * `workers: 1` — see playwright.stack.config.ts. Nothing here writes anything, so
 * nothing here perturbs another spec's setup.
 */

/** The floor as the control room serves it. */
interface AdminFloor {
  office: { w: number; h: number; player_radius: number; boss_radius: number; min_gap: number };
  layout: { id: string; solids: { x: number; y: number; w: number; h: number; kind: string }[]; windows: unknown[] };
  source: string;
  installed_at: string;
  occupants: number;
  kinds: { key: string; label: string }[];
  spots: { x: number; y: number; what: string }[];
}

test.describe('«АДМИН ФИНТЕХА»', () => {
  test('an admin is shown the same floor the players are standing in', async ({ context, page }) => {
    // THE SUPERADMIN STANDS IN FOR THE ADMIN HERE, because the seeded accounts
    // carry no plain admin — and the gate is `requireAdmin`, which both roles
    // pass. That the ordinary admin role passes it too is asserted in the stubbed
    // suite, where a role is a fixture rather than a row somebody has to seed.
    await loginAs(context, 'superadmin');
    await page.goto('/app/game-fintech-admin');

    const res = await page.request.get('/api/game-fintech/admin/layout');
    expect(res.status()).toBe(200);
    const floor = (await res.json()) as AdminFloor;

    // THE ROOM AND ITS RULES. Not asserted as literals: these are the game's own
    // constants and pinning them here would make this suite a second copy of
    // `content.go`. What matters is that the control room is told all five, since
    // an editor cannot judge a layout without them.
    expect(floor.office.w).toBeGreaterThan(0);
    expect(floor.office.h).toBeGreaterThan(0);
    expect(floor.office.player_radius).toBeGreaterThan(0);
    expect(floor.office.boss_radius).toBeGreaterThan(0);
    expect(floor.office.min_gap).toBeGreaterThan(0);

    // THE TWO ENDPOINTS DESCRIBE ONE FLOOR, and this is the assertion that makes
    // the whole page trustworthy: the plan an admin rearranges has to be the room
    // the players are actually in. A content hash and the solids themselves,
    // because the id alone would pass while the geometry disagreed and the
    // geometry alone would pass while the cache key drifted.
    const played = await page.request.get('/api/game-fintech/config');
    expect(played.status()).toBe(200);
    const config = (await played.json()) as {
      office: { id: string; w: number; h: number; solids: unknown[]; windows: unknown[] };
    };
    expect(floor.layout.id).toBe(config.office.id);
    expect(floor.layout.solids).toEqual(config.office.solids);
    expect(floor.layout.windows).toEqual(config.office.windows);
    expect(floor.office.w).toBe(config.office.w);
    expect(floor.office.h).toBe(config.office.h);

    // WHERE IT CAME FROM AND WHEN, which is everything about the layout that is
    // not in its geometry. A fresh database opens on the starting floor; the
    // source is asserted as «one of the ones this client can name» rather than as
    // `starting`, so the generator and the editor do not have to come back here.
    expect(['starting', 'generated', 'edited']).toContain(floor.source);
    expect(Number.isNaN(new Date(floor.installed_at).getTime())).toBe(false);
    expect(floor.occupants).toBeGreaterThanOrEqual(0);

    // THE TAXONOMY AND THE FIXED POINTS. Every kind the layout uses has to be in
    // the legend's list, or the plan would colour a square it cannot name; and
    // every spot the validator protects has to be on the plan, or «spot_blocked»
    // names a place with nothing on it.
    const kinds = floor.kinds.map((k) => k.key);
    expect(kinds.length).toBeGreaterThan(0);
    for (const kind of new Set(floor.layout.solids.map((s) => s.kind))) {
      expect(kinds).toContain(kind);
    }
    for (const kind of floor.kinds) expect(kind.label.length).toBeGreaterThan(0);
    expect(floor.spots.length).toBeGreaterThan(0);
    for (const spot of floor.spots) {
      expect(spot.what.length).toBeGreaterThan(0);
      expect(spot.x).toBeGreaterThanOrEqual(0);
      expect(spot.x).toBeLessThanOrEqual(floor.office.w);
      expect(spot.y).toBeGreaterThanOrEqual(0);
      expect(spot.y).toBeLessThanOrEqual(floor.office.h);
    }

    // AND THE PAGE DREW IT. The whole route, guard, view and payload, against a
    // floor nobody stubbed: as many boxes as the server sent, and as many
    // markers.
    await expect(page.getByTestId('fintech-admin-plan')).toBeVisible();
    await expect(page.getByTestId('fintech-admin-solid')).toHaveCount(floor.layout.solids.length);
    await expect(page.getByTestId('fintech-admin-spot')).toHaveCount(floor.spots.length);
    await expect(page.getByTestId('fintech-admin-id')).toHaveText(floor.layout.id);
    await expect(page.getByTestId('fintech-admin-occupants')).toHaveText(String(floor.occupants));
  });

  test('an approved user is refused the control room', async ({ context, page }) => {
    // The router's guard turns them back at the door, so this is the SECOND lock:
    // a browser that never loaded this SPA — or a role that changed under a tab
    // that was already open — has to be refused by the server too.
    await loginAs(context, 'user');
    await page.goto('/app/game-fintech');

    const res = await page.request.get('/api/game-fintech/admin/layout');
    expect(res.status()).toBe(403);
    expect((await res.json()).error).toBe('forbidden');
  });
});
