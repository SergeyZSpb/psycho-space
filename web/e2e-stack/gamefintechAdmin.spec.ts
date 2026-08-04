import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
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

/** One solid on the floor, as both endpoints carry it. */
interface Solid {
  x: number;
  y: number;
  w: number;
  h: number;
  kind: string;
}

/** A bare layout — geometry and no id, which is what the editor sends. */
interface Draft {
  solids: Solid[];
  windows: unknown[];
}

/** One thing the validator says is wrong, and what it is about. */
interface Problem {
  problem: string;
  index: number;
}

/** The floor as the control room serves it. */
interface AdminFloor {
  office: { w: number; h: number; player_radius: number; boss_radius: number; min_gap: number };
  layout: { id: string } & Draft;
  source: string;
  installed_at: string;
  occupants: number;
  kinds: { key: string; label: string }[];
  spots: { x: number; y: number; what: string }[];
  /** Only on a rebuild's or a save's answer: how many shifts it just threw out. */
  ended?: number;
}

/**
 * How long a shift has to last before the server keeps it.
 *
 * `MinShiftSeconds` in `internal/gamefintech`. It applies to EVERY ending,
 * including this one — a rebuild during the first three seconds of somebody's
 * shift throws them out and writes nothing — so the row this file looks for in
 * `shifts/me` only exists if the shift was worth keeping first. The rule IS a
 * duration and there is no condition to poll that would make it true sooner,
 * which is the same reasoning `gamefintech.spec.ts` states for its own copy.
 */
const MIN_SHIFT_MS = 3_000;
const SLACK_MS = 300;

/**
 * The clearance `gamefintech.spec.ts`'s `clearKey` needs somewhere to find, in
 * metres. See the afterAll hook.
 */
const WALK_M = 0.1;

/** Which layout the game is currently serving to players. */
async function servedOfficeID(page: Page): Promise<string> {
  const res = await page.request.get('/api/game-fintech/config');
  expect(res.status()).toBe(200);
  return ((await res.json()) as { office: { id: string } }).office.id;
}

/**
 * Can somebody dropped anywhere legal on this floor take a step?
 *
 * A GENERATED FLOOR IS VALIDATED, NOT CHOSEN, and this suite's other fintech spec
 * picks its movement key by measuring the room around wherever the spawn landed —
 * throwing outright when no direction has anywhere to go. The validator's own
 * rules (every solid `min_gap` from every wall and from its neighbours, free
 * space one connected region) make that vanishingly unlikely, and this is the
 * check that turns «unlikely» into «this file left behind a floor it looked at».
 *
 * The grid is a quarter of a metre, which is finer than any gap the validator
 * permits, and `free` is deliberately the same predicate `clearKey` uses.
 */
function walkableEverywhere(office: AdminFloor['office'], solids: readonly Solid[]): boolean {
  const { w, h, player_radius: r } = office;
  const free = (x: number, y: number) =>
    x >= r &&
    y >= r &&
    x <= w - r &&
    y <= h - r &&
    !solids.some((d) => x > d.x - r && x < d.x + d.w + r && y > d.y - r && y < d.y + d.h + r);

  for (let x = r; x <= w - r; x += 0.25) {
    for (let y = r; y <= h - r; y += 0.25) {
      if (!free(x, y)) continue;
      const somewhere =
        free(x + WALK_M, y) || free(x - WALK_M, y) || free(x, y + WALK_M) || free(x, y - WALK_M);
      if (!somewhere) return false;
    }
  }
  return true;
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

  test('rebuilding the office throws out the shift standing on it', async ({
    browser,
    context,
    page,
  }) => {
    // THE LEG NO STUB CAN MAKE. Everything about how the control looks is asserted
    // in `web/e2e/gamefintechAdmin.spec.ts`; what only a real binary can say is
    // that pressing it reaches the simulation somebody else is standing in — the
    // eviction travels over the real socket, the ending comes from the real
    // catalogue, and the row reaches Postgres.
    //
    // TWO CONTEXTS, because the whole point of the button is that it ends
    // SOMEBODY ELSE'S shift. The player is an ordinary approved user; the rebuild
    // comes from the superadmin, who stands in for the admin here because the
    // seeded set carries no plain admin — the same stand-in the read test uses.
    await loginAs(context, 'user');
    await page.goto('/app/game-fintech');
    await expect(page.getByTestId('fintech-splash')).toBeVisible();

    const before = await servedOfficeID(page);

    await page.getByTestId('fintech-start').click();
    await expect(page.getByTestId('fintech-play')).toBeVisible();
    await page.waitForTimeout(MIN_SHIFT_MS + SLACK_MS);

    const adminCtx = await browser.newContext();
    try {
      await loginAs(adminCtx, 'superadmin');
      // The context's own request object, which shares its cookies — NOT the bare
      // `request` fixture, which is a separate context with none and would
      // silently test the anonymous path.
      const res = await adminCtx.request.post('/api/game-fintech/admin/layout/reroll');
      expect(res.status()).toBe(200);
      const rebuilt = (await res.json()) as AdminFloor;

      // It drew a new floor, and it says how many people it cost.
      expect(rebuilt.layout.id).not.toBe(before);
      expect(rebuilt.source).toBe('generated');
      expect(rebuilt.ended ?? 0).toBeGreaterThanOrEqual(1);
      // The office it answers with is empty, because it just emptied it.
      expect(rebuilt.occupants).toBe(0);

      // THE PLAYER IS OUT AND WAS TOLD SO, over the socket the office really
      // holds — and the words are the real `content.go`'s rather than a stub's.
      await expect(page.getByTestId('fintech-over-title')).toHaveText('РЕМОНТ');

      // AND EVERYBODY IS SERVED THE NEW FLOOR NOW, which is what makes the
      // rebuild's answer safe to redraw the control room from: the two endpoints
      // describe one floor before and after.
      expect(await servedOfficeID(page)).toBe(rebuilt.layout.id);

      // AND THE SHIFT REACHED POSTGRES with the ending that ended it. The salary
      // counted, so this is a row rather than a shift thrown away.
      const mine = await page.request.get('/api/game-fintech/shifts/me');
      expect(mine.status()).toBe(200);
      const shifts = ((await mine.json()) as { shifts: { cause: string }[] }).shifts;
      expect(
        shifts.some((s) => s.cause === 'renovated'),
        `no rebuilt shift in ${JSON.stringify(shifts)}`,
      ).toBe(true);
    } finally {
      await adminCtx.close();
    }
  });

  test('a floor drawn by hand is installed, and it is the floor players get', async ({
    context,
    page,
  }) => {
    // WHAT NO STUB CAN SAY. The layout suite proves the editor sends the draft on
    // the screen; only a real binary can say that the draft it sends is a shape
    // the server accepts, that installing it really replaces the office, and that
    // the geometry the game hands to a player afterwards is the geometry an admin
    // drew. Three parties — the editor, the validator, the catalogue — agreeing
    // about one stored row.
    await loginAs(context, 'superadmin');

    // THE DRAFT COMES FROM THE PROPOSAL ENDPOINT rather than being written out
    // here, and that is the point of `layout/proposal` existing: the server draws
    // a legal floor, so this test can install one without a hand-built rectangle
    // list that would quietly go stale the first time a constant was retuned. It
    // also asserts the endpoint's own promise on the way past — drawing a floor
    // must install NOTHING.
    const before = await servedOfficeID(page);
    const drawn = await page.request.get('/api/game-fintech/admin/layout/proposal');
    expect(drawn.status()).toBe(200);
    const proposal = ((await drawn.json()) as { layout: { id: string } & Draft }).layout;
    expect(proposal.solids.length).toBeGreaterThan(0);
    expect(await servedOfficeID(page), 'drawing a floor installed one').toBe(before);
    // And it is never cached, or «СЛУЧАЙНЫЙ» would stop working after one press.
    expect(drawn.headers()['cache-control']).toContain('no-store');

    // THE VALIDATOR AGREES BEFORE THE INSTALL DOES, which is what makes the
    // editor's «ПРИМЕНИТЬ» gate meaningful: the check and the save are the same
    // judgement, so a draft the check passes is one the save accepts.
    const body: Draft = { solids: proposal.solids, windows: proposal.windows };
    const checked = await page.request.post('/api/game-fintech/admin/layout/check', { data: body });
    expect(checked.status()).toBe(200);
    // `null` AND NOT `[]` ON A CLEAN FLOOR, which is the shape of the wire rather
    // than a nicety: the validator answers a nil slice when it has nothing to
    // say and Go marshals that as `null`. Asserted the way the client reads it,
    // so this test would fail if the client ever stopped coping.
    expect(((await checked.json()) as { problems: Problem[] | null }).problems ?? []).toEqual([]);

    const saved = await page.request.put('/api/game-fintech/admin/layout', { data: body });
    expect(saved.status()).toBe(200);
    const floor = (await saved.json()) as AdminFloor;

    // It answers the READ's own payload plus the count, exactly as the rebuild
    // does — which is what lets the page redraw from the response.
    expect(floor.source).toBe('edited');
    expect(floor.layout.solids).toEqual(proposal.solids);
    expect(floor.ended ?? 0).toBeGreaterThanOrEqual(0);
    expect(floor.occupants).toBe(0);

    // AND THE PLAYERS ARE STANDING IN IT. The id is a content hash of exactly
    // these two lists, so this is the assertion that the geometry an admin drew is
    // the geometry the game predicts against.
    expect(await servedOfficeID(page)).toBe(floor.layout.id);
    const config = await page.request.get('/api/game-fintech/config');
    expect(
      ((await config.json()) as { office: { solids: Solid[] } }).office.solids,
    ).toEqual(proposal.solids);
  });

  // «An illegal floor is refused with its problems, and nothing moves» USED TO BE
  // A TEST HERE, and it is gone rather than skipped. Its claim is pure API — post
  // a floor with a desk in the corner, read a 422 and a problem list, read the
  // catalogue back unchanged — and this suite's budget is not the place to spend
  // a login and a browser on one. It lives in
  // test/integration/gamefintech_test.go's TestFintechTheEditorDrawsChecksAndInstallsAFloor,
  // where the same three endpoints are driven end to end against a real database
  // and forty shifts cost no browser at all.
  //
  // THE REASON IS THE SUITE'S SHARED ALLOWANCE. One server, one IP, the
  // production 240/min per-IP limiter — so every test here spends part of a
  // budget the whole file shares, and a spec that has not changed starts failing
  // because three tests were added ahead of it. The runbook names this exact
  // failure («A full-stack test fails with rate_limited, twenty tests away from
  // the cause») and names the fix: denser tests, and a claim that only needs the
  // API belongs in the integration suite.

  test('an approved user is refused the control room, whichever door they try', async ({
    context,
    page,
  }) => {
    // The router's guard turns them back at the door, so this is the SECOND lock:
    // a browser that never loaded this SPA — or a role that changed under a tab
    // that was already open — has to be refused by the server too.
    await loginAs(context, 'user');
    await page.goto('/app/game-fintech');

    const res = await page.request.get('/api/game-fintech/admin/layout');
    expect(res.status()).toBe(403);
    expect((await res.json()).error).toBe('forbidden');

    // AND THE EDITOR'S THREE ARE BEHIND THE SAME LOCK. The gate is `requireAdmin`
    // on the whole route group, so this is really asserting that nothing was
    // added OUTSIDE the group — which is the one way a new endpoint gets shipped
    // open by accident. The install is the one that matters most: it ends every
    // shift in the building.
    const draw = await page.request.get('/api/game-fintech/admin/layout/proposal');
    expect(draw.status()).toBe(403);
    const check = await page.request.post('/api/game-fintech/admin/layout/check', {
      data: { solids: [], windows: [] },
    });
    expect(check.status()).toBe(403);
    const save = await page.request.put('/api/game-fintech/admin/layout', {
      data: { solids: [], windows: [] },
    });
    expect(save.status()).toBe(403);
  });

  test.afterAll(async ({ browser }) => {
    // THE FLOOR THIS FILE LEAVES BEHIND IS PROCESS-GLOBAL. This suite shares one
    // server and one database with every other spec at `workers: 1`, so what is
    // installed here is what `gamefintech.spec.ts` plays on — and its `clearKey`
    // throws outright on a floor where the spawn has nowhere to walk.
    //
    // IT LOOKS BEFORE IT INSTALLS, which is what the editor's own endpoints made
    // possible: `layout/proposal` draws a floor and installs nothing, so this
    // hook can inspect candidates for free and `PUT` only the one it has decided
    // to keep. It used to have to REBUILD to see a floor at all, which meant
    // every rejected candidate was an office the world briefly ran on.
    //
    // Bounded by a DEADLINE rather than by a number of attempts: what matters is
    // how long we are willing to wait, not how many tries fit into it, and a
    // loaded runner changes only the latter.
    const admin = await browser.newContext();
    try {
      await loginAs(admin, 'superadmin');
      const read = await admin.request.get('/api/game-fintech/admin/layout');
      const office = ((await read.json()) as AdminFloor).office;

      const deadline = Date.now() + 30_000;
      let last = '';
      for (;;) {
        const drawn = await admin.request.get('/api/game-fintech/admin/layout/proposal');
        if (drawn.status() === 200) {
          const layout = ((await drawn.json()) as { layout: { id: string } & Draft }).layout;
          last = layout.id;
          if (walkableEverywhere(office, layout.solids)) {
            const saved = await admin.request.put('/api/game-fintech/admin/layout', {
              data: { solids: layout.solids, windows: layout.windows },
            });
            expect(saved.status(), `the walkable floor ${last} was refused`).toBe(200);
            return;
          }
        }
        if (Date.now() >= deadline) {
          throw new Error(
            `no walkable floor could be installed within 30s (last was ${last || 'unreadable'}) — ` +
              'the next fintech spec will fail on a floor it cannot walk in',
          );
        }
      }
    } finally {
      await admin.close();
    }
  });
});
