import { expect, test, type Locator, type Page, type Route } from '@playwright/test';

// «Ванягоччи» — the shared plane, at phone widths, with the socket faked.
//
// Everything is stubbed inside this file rather than in e2e/fixtures.ts, and the
// helpers below are copies rather than imports, so the game keeps its own
// fixtures and this spec stands alone. Deleting this game means deleting its
// files and nothing else (ARCHITECTURE ADR-028) — which is only true if no other
// spec has to be edited on the way out.
//
// The socket is intercepted with page.routeWebSocket, so the test *is* the
// server: it sends roster frames and reads the moves the client sends back.
// Nothing here talks to Go.

/** Mirrored from internal/gamevanyagotchi/message.go. Duplicated on purpose: a
 *  wire-format change should fail this test rather than silently follow along. */
const TYPE_ROSTER = 'vanyagotchi_roster';
const TYPE_MOVE = 'vanyagotchi_move';
const TYPE_HELLO = 'vanyagotchi_hello';
const TYPE_YOU = 'vanyagotchi_you';

/** Mirrored from src/lib/vanyagotchiPlane.ts, for the same reason. */
const X_PROPERTY = '--x';
const Y_PROPERTY = '--y';
/** Which depth band an entity is in. The stylesheet uses it as the z-index. */
const BAND_PROPERTY = '--band';
/** That band's scale factor. */
const DEPTH_PROPERTY = '--depth';
/** 1 when a balloon has to hang below its entity instead of above it. */
const SAY_BELOW_PROPERTY = '--say-below';

/**
 * The longest name a dot will show, in code points. Mirrored from
 * src/lib/vanyagotchiPlane.ts, for the same reason as everything else here: a
 * cap that quietly moved should fail this test rather than be followed.
 */
const LABEL_MAX = 16;

/**
 * What each depth band multiplies an entity's size by, back of the plane to
 * front. Mirrored from DEPTH_SCALES.
 *
 * The FIRST entry being 1 is the load-bearing part, and it is why this is
 * written down here rather than measured: depth can only make an entity bigger,
 * so the 44 px tap-target floor is the CSS size itself rather than a product of
 * two numbers that could drift apart. A change that scaled the far band DOWN
 * would still draw a perfectly plausible yard, and should fail here.
 */
const DEPTH_SCALES = [1, 1.08, 1.16, 1.24] as const;

/**
 * The SMALLEST a dot is ever drawn — the floor of `--unit`, not the size.
 *
 * `.peer` is `var(--unit)` and `--unit` is `clamp(44px, 13cqw, 88px)` on
 * `.plane`, so an entity grows with the yard and 44 is the bottom of that clamp.
 * It is a LEGIBILITY floor rather than a tap-target one, which matters for
 * reading the assertions that name it: `.peer` is `pointer-events: none` and the
 * plane takes every tap, so a dot has never been tappable.
 */
const PEER_BASE_PX = 44;
/** The top of the clamp, and the fraction of the plane between the two. */
const PEER_UNIT_MAX_PX = 88;
const PEER_UNIT_PLANE_FRACTION = 0.13;

/**
 * `--unit` at a given plane width, mirroring the stylesheet's clamp.
 *
 * Every fixed length inside the plane is a multiple of this, so every assertion
 * about one of them has to compute it rather than hardcode a pixel count — that
 * is the whole difference between the old rule and the new one. Mirrored rather
 * than read out of the computed style on purpose: a test that asked the browser
 * what the unit is would agree with the stylesheet by construction and could
 * never catch it being wrong.
 */
function unitFor(planeWidth: number): number {
  return Math.min(PEER_UNIT_MAX_PX, Math.max(PEER_BASE_PX, planeWidth * PEER_UNIT_PLANE_FRACTION));
}

/**
 * Above this height a balloon no longer fits over its entity and hangs below it
 * instead. Mirrored from SAY_FLIP_Y: the flip is the only thing between a line
 * near the top of the plane and a line clipped away to nothing, and unlike a cut
 * name — which is still legibly somebody's name — a clipped balloon is nothing
 * at all.
 */
const SAY_FLIP_Y = 0.15;

/**
 * The stylesheet's cap on a balloon's WIDTH, in world units: `--unit * 2.182`.
 *
 * It used to be `min(96px, 34cqw)`, and the two branches were asserted
 * separately because they could each be the binding one. There is one branch
 * now: `--unit` is itself clamped, so the balloon cannot grow past legible
 * without a second ceiling to stop it. 2.182 is 96/44 — the same cap a phone
 * always had, now expressed as what it was a proportion OF.
 *
 * The plane-fraction bound below is kept as an independent second opinion. It is
 * now the looser of the two everywhere (2.182 x 0.13 = 28.4% of the plane at
 * most), which is the point: it is derived from a different quantity, so it
 * still fails if the unit rule itself is wrong.
 */
const SAY_MAX_UNITS = 96 / 44;
const SAY_MAX_PLANE_FRACTION = 0.34;

/** The longest line a balloon will show, in code points. Mirrored from SAY_MAX. */
const SAY_MAX = 24;

/**
 * A line for the fake server in this file to put over somebody's head.
 *
 * FIXTURE TEXT, and mirrored from nothing. Nothing here talks to Go — the test
 * *is* the server — so this string only ever travels from a frame this file
 * writes to the DOM this file then reads, and its value is arbitrary: any
 * non-empty line the client can be asked to draw would do. It deliberately does
 * NOT track `tiredSays` or `idleSays` in internal/gamevanyagotchi/content.go,
 * and nobody should make it: the client is kind-agnostic about a balloon (it
 * draws `say`, whoever said it and whyever), so a suite that pinned this to a
 * server pool would be asserting a coupling the product does not have.
 *
 * The suites that must agree with the server's own words are the Go tests, which
 * own the pools, and the full-stack suite, which reads what the real server put
 * on the wire.
 */
const SAY_FIXTURE = 'устал';

/**
 * Where the client asks for the face of the person behind an entity. Mirrored
 * from avatarEndpoint in src/lib/vanyagotchiPlane.ts, and from the route in
 * internal/httpapi/router.go, for the reason everything else in this file is
 * mirrored: an address that quietly moved should fail here rather than be
 * followed along to.
 *
 * The id is the LAST segment, which is the property the assertions below lean
 * on — a client that asked for the wrong entity's face, or for one shared
 * address for the whole yard, would draw one person on every Ваня and would
 * otherwise look perfectly correct.
 */
const AVATAR_PATH = '/api/game-vanyagotchi/avatar/';

/**
 * The bytes the stub hands back for somebody who has a picture: a 1x1 PNG,
 * base64, decoded into the response body.
 *
 * BYTES RATHER THAN A URL, and that is the shape of the whole iteration rather
 * than a detail of the fixture. A face is no longer a string on a frame that
 * this fake server could simply hand over — no roster frame carries one, because
 * a couple of hundred unchanging characters have no business being re-sent five
 * times a second and because a URL out of Postgres would be the one durable
 * thing on a deliberately per-process frame. The client derives the address from
 * the id it is drawing and fetches it, so the only way to give somebody a face
 * here is to answer that request, exactly as the real server does.
 *
 * A 1x1 PNG served from this stub rather than an https URL followed to a CDN,
 * so the suite stays deterministic and needs no network at all: pointing the
 * fixture at a real avatar would make this a check of somebody else's uptime.
 * The same bytes as PAINTED_IMAGE in gamevanyagotchi-pet.spec.ts.
 */
const FACE_PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

/**
 * What a dot draws for an art key the catalogue does not describe. Mirrored from
 * UNKNOWN_ART in src/lib/vanyagotchiPlane.ts — a silhouette rather than a blank,
 * because the entity is real and standing there and the only thing missing is
 * this client's idea of what it looks like.
 *
 * It is what the fallbacks in this suite land on, because the stub answers the
 * catalogue request with an empty object: no skins, so no sprite and no skin
 * emoji either. That is the placeholder, and what the assertions using it mean
 * is "a glyph was drawn here rather than a picture".
 */
const UNKNOWN_ART = '👤';

/**
 * How far a picture is inset inside its dot, in world units: `--unit * 0.136`,
 * which is the 6/44 the rim always was.
 *
 * The inset IS the rim, and the rim is what stops a yard of avatars losing the
 * one thing that says which Ваня is which. Mirrored rather than measured, so a
 * picture quietly grown to fill its dot fails here instead of being agreed with.
 */
const PEER_SPRITE_INSET_UNITS = 6 / 44;

/**
 * Which band an entity at this height belongs to. Mirrored from `bandFor`.
 *
 * Re-derived here rather than read off the element, so that a test asserting
 * "this y is in band 2" is asserting something about the DESIGN rather than
 * agreeing with whatever the code happened to write.
 */
function bandFor(y: number): number {
  return Math.min(DEPTH_SCALES.length - 1, Math.max(0, Math.floor(y * DEPTH_SCALES.length)));
}

/**
 * One entry of a roster frame.
 *
 * A frame says two kinds of thing about an entity, and the split is the whole
 * design: WHERE it stands changes five times a second and is written straight to
 * CSS, WHAT it looks like changes a few times an hour and goes through Vue.
 *
 * `art`, `label` and `pose` are optional here rather than required because they
 * are optional on the WIRE too — `label` is omitted entirely for a Ваня who has
 * no name, and a server halfway through a deploy may send none of the three.
 * Every one of those has to draw somebody.
 */
interface Peer {
  id: string;
  x: number;
  y: number;
  /** A catalogue skin key, never a picture — see the appearance suite below. */
  art?: string;
  /** The pet's name. Left out, never empty, when it has none. */
  label?: string;
  /**
   * 'fine' | 'poorly' | 'dead' | 'asleep', as the SERVER decided it.
   *
   * `asleep` is a Ваня whose owner is away, lying where he last stood. It is
   * what makes a solo visit a place rather than an empty field, and it is why an
   * entry in this list stopped meaning "a player who is here".
   */
  pose?: string;
  /**
   * A line over his head, for the few seconds the server keeps it on the wire.
   *
   * A string rather than a second pose because the server draws the same
   * distinction: a pose is how he LOOKS and this is something he SAID.
   */
  say?: string;
  // THERE IS NO AVATAR FIELD, and a test that gave a peer one would be
  // describing a server that no longer exists. A face is fetched by id from
  // AVATAR_PATH rather than broadcast, so the way this suite gives somebody a
  // picture is stubFaces below — which is also the only way the real client can
  // be given one, and therefore the only shape worth asserting against.
}

/**
 * A roster frame, with the head count the server would have put on it.
 *
 * `here` is how many PEOPLE are in the yard, which stopped being `peers.length`
 * the moment the roster started carrying the NPCs and everybody asleep in it as
 * well. Every peer built through this helper is a person, so the two agree —
 * which is exactly what a real server with no cast and no sleepers sends, and
 * what keeps every assertion written before the count existed honest.
 * `rosterHere` is for the frames where they deliberately differ.
 *
 * `JSON.stringify` drops an undefined property rather than writing `null`, which
 * is exactly what the Go `omitempty` on `Label` and `Say` does — so a peer built
 * without a name here reaches the client in the same shape a nameless one
 * really does.
 */
function roster(...peers: Peer[]): string {
  return rosterHere(peers.length, ...peers);
}

/** A roster frame whose head count is NOT the number of things in it. */
function rosterHere(here: number, ...peers: Peer[]): string {
  return JSON.stringify({ t: TYPE_ROSTER, peers, here });
}

/**
 * A roster frame from a server that predates the head count.
 *
 * Not a hypothetical: a deploy is a window in which one is live, and the client
 * has a documented fallback for it. Exercised rather than assumed, because a
 * fallback nothing runs is a fallback nobody knows is broken.
 */
function rosterWithoutHere(...peers: Peer[]): string {
  return JSON.stringify({ t: TYPE_ROSTER, peers });
}

/** Everything the socket handler hands back to the test. */
interface SocketHarness {
  /** Frames the page sent us, parsed. */
  sent: Record<string, unknown>[];
  /** Pushes a frame to the page. Resolves once a socket exists to push it to. */
  push: (payload: string) => Promise<void>;
  /** Drops the connection, optionally saying why first. */
  drop: (bye?: { code: number; reason: string }) => Promise<void>;
  /** How many times the page has opened a socket — reconnects included. */
  connections: () => number;
  /**
   * Refuse every further connection until released, so the client genuinely
   * stays down. Without this a reconnect can complete inside a single Vue flush
   * and the disconnected state never reaches the DOM at all — which is lovely in
   * production and untestable in a browser.
   */
  holdDown: (down: boolean) => void;
  /** Waits until the page has opened at least n sockets. */
  waitForConnections: (n: number) => Promise<void>;
}

/**
 * Intercepts the WebSocket. Must be installed before `goto`, and the pattern
 * needs the trailing `*` — the client appends `?room=yard`, and a glob without
 * it does not match a query string.
 */
async function stubSocket(page: Page): Promise<SocketHarness> {
  const sent: Record<string, unknown>[] = [];
  let ws: { send: (m: string) => void; close: (o?: { code?: number }) => void } | undefined;
  let count = 0;
  let resolveReady: () => void;
  let ready = new Promise<void>((r) => {
    resolveReady = r;
  });

  let down = false;
  await page.routeWebSocket('**/api/realtime*', (route) => {
    count += 1;
    if (down) {
      route.close({ code: 1006 });
      return;
    }
    ws = route;
    route.onMessage((message) => {
      const text = typeof message === 'string' ? message : message.toString();
      try {
        sent.push(JSON.parse(text));
      } catch {
        sent.push({ unparsed: text });
      }
    });
    resolveReady();
  });

  return {
    sent,
    connections: () => count,
    holdDown(value: boolean) {
      down = value;
    },
    async waitForConnections(n: number) {
      const deadline = Date.now() + 15_000;
      while (count < n) {
        if (Date.now() > deadline) throw new Error(`only ${count} of ${n} connections opened`);
        await new Promise((r) => setTimeout(r, 50));
      }
    },
    async push(payload: string) {
      await ready;
      ws?.send(payload);
    },
    async drop(bye) {
      await ready;
      if (bye) ws?.send(JSON.stringify({ t: 'bye', ...bye }));
      const dying = ws;
      // The next connection gets its own readiness gate, so a push after a
      // reconnect waits for the NEW socket rather than racing the dead one.
      ready = new Promise<void>((r) => {
        resolveReady = r;
      });
      ws = undefined;
      dying?.close({ code: 1006 });
    },
  };
}

/** Stubs the HTTP the app shell needs to let an approved user through. */
async function stubBackend(page: Page): Promise<void> {
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
        contentType: 'application/json',
        headers: { 'X-Trace-Id': 'e2e-trace-id' },
        body: JSON.stringify(body),
      });
    if (path === '/api/auth/me') {
      return json({
        account: {
          id: 'acc-1',
          display_name: 'Тест Пользователь',
          avatar_url: '',
          role: 'user',
          status: 'approved',
        },
      });
    }
    // NOBODY HAS A FACE UNLESS A TEST SAYS SO. The client asks for one for every
    // entity it draws — it holds no notion of what an NPC is and cannot tell a
    // person from a regular — so without this every dot in every test in this
    // file would fetch an avatar and be handed the catch-all's JSON, which is a
    // 200 the browser then fails to decode. That is a slower and much stranger
    // route to the same fallback than the answer the real server gives, which
    // for an entity nobody has a picture of is exactly this 404.
    if (path.startsWith(AVATAR_PATH)) return noFace(route);
    return json({});
  });
}

/**
 * The answer the real endpoint gives for an entity with no picture: 404, and
 * cacheable, so the browser stops asking.
 *
 * Mirrored from internal/httpapi/gamevanyagotchi.go, including the header,
 * because a 404 that a shared cache could keep would be a real bug there and a
 * stub that quietly dropped the header would let this suite agree with it.
 */
function noFace(route: Route): Promise<void> {
  return route.fulfill({
    status: 404,
    contentType: 'application/json',
    headers: { 'Cache-Control': 'private, max-age=1800', 'X-Trace-Id': 'e2e-trace-id' },
    body: JSON.stringify({ error: 'no_avatar', trace_id: 'e2e-trace-id' }),
  });
}

/**
 * Gives a named few entities a face, and records every id the client asked
 * about.
 *
 * The recording is half the point. What tells "the yard drew a photograph" from
 * "the yard drew SOMEBODY'S photograph" is which address was fetched, and the
 * address is now derived from an id rather than sent — so a client that built
 * it from the viewer's own id, or from the first peer in the frame, would paint
 * one person's face onto everybody and look entirely correct in a screenshot.
 *
 * Registered AFTER stubBackend on purpose: Playwright tries the most recently
 * added route first, so this one wins for the faces it knows and the catch-all's
 * 404 covers the rest.
 */
async function stubFaces(page: Page, withFace: readonly string[]): Promise<{ asked: string[] }> {
  const asked: string[] = [];
  await page.route(`**${AVATAR_PATH}*`, async (route) => {
    const path = new URL(route.request().url()).pathname;
    const id = decodeURIComponent(path.slice(path.lastIndexOf('/') + 1));
    asked.push(id);
    if (!withFace.includes(id)) return noFace(route);
    return route.fulfill({
      status: 200,
      contentType: 'image/png',
      headers: { 'Cache-Control': 'private, max-age=1800' },
      // The real route answers a 302 to VK's CDN. A redirect to a `data:` URI is
      // blocked by the browser and a redirect to a real host would put this
      // suite on the network, so the stub serves the bytes the redirect would
      // have led to — which is what the client actually renders either way.
      body: Buffer.from(FACE_PNG_BASE64, 'base64'),
    });
  });
  return { asked };
}

/** Copied, not imported — see the header. */
async function expectNoOverflow(page: Page, label: string): Promise<void> {
  const diff = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(diff, `horizontal overflow on "${label}": ${diff}px`).toBeLessThanOrEqual(0);
}

/** The play screen must never scroll vertically either — that is the layout rule. */
async function expectNoVerticalScroll(page: Page, label: string): Promise<void> {
  const diff = await page.evaluate(
    () => document.documentElement.scrollHeight - document.documentElement.clientHeight,
  );
  expect(diff, `vertical scroll on "${label}": ${diff}px`).toBeLessThanOrEqual(1);
}

function isMobile(page: Page): boolean {
  const vp = page.viewportSize();
  return !!vp && vp.width <= 600;
}

async function expectTapTarget(loc: Locator, label: string): Promise<void> {
  await expect(loc, `${label} should be visible`).toBeVisible();
  const box = await loc.boundingBox();
  expect(box, `${label} has no bounding box`).not.toBeNull();
  if (box) {
    const min = Math.round(Math.min(box.width, box.height));
    expect(
      min,
      `${label} tap target too small: ${Math.round(box.width)}x${Math.round(box.height)}`,
    ).toBeGreaterThanOrEqual(44);
  }
}

/** Reads a dot's resolved custom properties — the only place a position lives. */
async function peerPosition(page: Page, id: string): Promise<{ x: string; y: string }> {
  return page.evaluate(
    ([peerId, xProp, yProp]) => {
      const el = document.querySelector<HTMLElement>(`[data-peer="${peerId}"]`);
      if (!el) throw new Error(`no dot for ${peerId}`);
      const style = getComputedStyle(el);
      return {
        x: style.getPropertyValue(xProp).trim(),
        y: style.getPropertyValue(yProp).trim(),
      };
    },
    [id, X_PROPERTY, Y_PROPERTY] as const,
  );
}

/** One entity's depth: the band it is in, the scale that band gives it, and the
 *  stacking order the stylesheet derives from the band.
 *
 *  `zIndex` is read as the browser RESOLVED it rather than as the declaration
 *  was written, which is the whole point of reading it here: `z-index:
 *  var(--band)` is only depth if the custom property really reaches the used
 *  value, and a typo in either name would leave it `auto` while every other
 *  assertion in the suite still passed. */
async function peerDepth(
  page: Page,
  id: string,
): Promise<{ band: number; depth: number; zIndex: string }> {
  return page.evaluate(
    ([peerId, bandProp, depthProp]) => {
      const el = document.querySelector<HTMLElement>(`[data-peer="${peerId}"]`);
      if (!el) throw new Error(`no dot for ${peerId}`);
      const style = getComputedStyle(el);
      return {
        band: Number.parseFloat(style.getPropertyValue(bandProp)),
        depth: Number.parseFloat(style.getPropertyValue(depthProp)),
        zIndex: style.zIndex,
      };
    },
    [id, BAND_PROPERTY, DEPTH_PROPERTY] as const,
  );
}

/** One entity's balloon, if the server is currently giving it one. */
const sayBubble = (page: Page, id: string) =>
  page.locator(`[data-peer="${id}"] [data-test="peer-say"]`);

/** Whether this entity's balloon has been told to hang below it. */
async function saysBelow(page: Page, id: string): Promise<string> {
  return page.evaluate(
    ([peerId, prop]) => {
      const el = document.querySelector<HTMLElement>(`[data-peer="${peerId}"]`);
      if (!el) throw new Error(`no dot for ${peerId}`);
      return getComputedStyle(el).getPropertyValue(prop).trim();
    },
    [id, SAY_BELOW_PROPERTY] as const,
  );
}

/**
 * How far a face has been rotated, in degrees.
 *
 * Sleep is the one condition drawn as a rotation rather than as a filter, so
 * this is what tells "he is lying down" from "he is a slightly dimmer circle".
 * Read out of the used `transform` matrix rather than off the declaration: the
 * browser has already resolved it, so this fails if the rule stops applying for
 * any reason at all rather than only if somebody deletes it.
 */
async function faceTilt(page: Page, id: string): Promise<number> {
  return page.evaluate((peerId) => {
    const el = document.querySelector<HTMLElement>(
      `[data-peer="${peerId}"] [data-test="peer-face"]`,
    );
    if (!el) throw new Error(`no face for ${peerId}`);
    const t = getComputedStyle(el).transform;
    if (!t || t === 'none') return 0;
    const parts = t.slice(t.indexOf('(') + 1, t.lastIndexOf(')')).split(',').map(Number);
    // matrix(a, b, c, d, e, f): the first column is the rotated x axis.
    return (Math.atan2(parts[1] ?? 0, parts[0] ?? 1) * 180) / Math.PI;
  }, id);
}

/** Whatever the stylesheet is drawing in a dot's `::after` — the 💤, for a sleeper. */
async function badge(page: Page, id: string): Promise<string> {
  return page.evaluate((peerId) => {
    const el = document.querySelector<HTMLElement>(`[data-peer="${peerId}"]`);
    if (!el) throw new Error(`no dot for ${peerId}`);
    return getComputedStyle(el, '::after').content;
  }, id);
}

/** A dot's resolved opacity, which is how "not here" reads at a glance. */
async function peerOpacity(page: Page, id: string): Promise<number> {
  return page.evaluate((peerId) => {
    const el = document.querySelector<HTMLElement>(`[data-peer="${peerId}"]`);
    if (!el) throw new Error(`no dot for ${peerId}`);
    return Number.parseFloat(getComputedStyle(el).opacity);
  }, id);
}

/**
 * Records every stale/live transition of the plane, from before the app boots.
 *
 * A reconnect is over in well under a second, so the stale window is far too
 * short to catch with a polled assertion — under load the check simply arrives
 * after it has passed. A MutationObserver installed before boot cannot miss it,
 * so the test asserts the recorded SEQUENCE instead of trying to be quick. Same
 * technique, and the same reason, as the drawer-peek test in mobile.spec.ts.
 */
async function recordStale(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const log: boolean[] = [];
    (window as unknown as { __staleLog: boolean[] }).__staleLog = log;
    // Whether the recorder is running at all. An empty log is a legitimate
    // result — a fast reconnect never paints the stale state — so without this
    // flag "no transitions" and "no recorder" are indistinguishable, and the
    // assertion that the plane ended live would pass for want of any evidence.
    (window as unknown as { __staleWatching: boolean }).__staleWatching = false;
    const read = () => !!document.querySelector('[data-test="plane"][data-stale="1"]');
    let last: boolean | undefined;
    new MutationObserver(() => {
      const now = read();
      if (now !== last) {
        last = now;
        log.push(now);
      }
    }).observe(document, {
      // THE DOCUMENT, not `document.documentElement`. An init script runs before
      // the page's own scripts, and at that moment `documentElement` can still
      // be null — `observe` then throws, the recorder quietly records nothing,
      // and an assertion of the form "the log never ended stale" passes because
      // the log is empty. A Document is a Node and covers every descendant, so
      // this attaches whatever stage the parse has reached.
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ['data-stale'],
    });
    (window as unknown as { __staleWatching: boolean }).__staleWatching = true;
  });
}

/** The recorded stale/live transitions so far. */
function staleLog(page: Page): Promise<boolean[]> {
  return page.evaluate(() => (window as unknown as { __staleLog: boolean[] }).__staleLog ?? []);
}

/** Whether the recorder above ever attached. See `recordStale`. */
function staleWatching(page: Page): Promise<boolean> {
  return page.evaluate(
    () => (window as unknown as { __staleWatching: boolean }).__staleWatching === true,
  );
}

const plane = (page: Page) => page.locator('[data-test="plane"]');
const dots = (page: Page) => page.locator('[data-test="peer"]');
/** One entity's face. On EVERY dot now, not only on your own. */
const face = (page: Page, id: string) =>
  page.locator(`[data-peer="${id}"] [data-test="peer-face"]`);
/** One entity's name, if it has one — the element is absent when it does not. */
const nameTag = (page: Page, id: string) =>
  page.locator(`[data-peer="${id}"] [data-test="peer-label"]`);
/**
 * The person's own photograph on a dot, if one is being drawn.
 *
 * There is ONE img element on a dot and it carries either kind of picture — a
 * VK avatar or the catalogue's sprite — so what tells them apart is the
 * `data-test`, not the tag or the class. That distinction is the whole point of
 * the attribute: `img.peer-sprite` would match either, so a suite written
 * against it could not tell "the avatar reached the screen" from "a picture
 * did", which is precisely the regression this iteration can produce.
 */
const avatarOf = (page: Page, id: string) =>
  page.locator(`[data-peer="${id}"] [data-test="peer-avatar"]`);
/** The catalogue's picture for an entity's skin, drawn when there is no avatar. */
const spriteOf = (page: Page, id: string) =>
  page.locator(`[data-peer="${id}"] [data-test="peer-sprite"]`);

/**
 * The stylesheet's cap on a name's WIDTH, in world units: `--unit * 1.818`.
 *
 * Was `min(80px, 30cqw)`. 1.818 is 80/44, so a phone is unchanged and every
 * wider screen now grows the name with the yard instead of leaving it behind —
 * the same correction `--unit` makes to the dot itself. The plane-fraction bound
 * is kept as an independent second opinion, derived from a different quantity.
 */
const LABEL_MAX_UNITS = 80 / 44;
const LABEL_MAX_PLANE_FRACTION = 0.3;

/**
 * Asserts one dot's name is no wider than the cap allows, at the height it is
 * standing at.
 *
 * This is the assertion that has teeth. A name is centred on its dot, so an
 * uncapped one covers every neighbour it reaches — which is what a player would
 * actually see, and which no page-level scroll check can detect: `.stage` clips
 * its own overflow and the root stylesheet sets `overflow-x: hidden`, so the
 * page cannot grow a scrollbar however wide a label gets.
 *
 * THE HEIGHT IS AN ARGUMENT because depth arrived and the cap is no longer the
 * drawn width. `max-width` bounds the label's own layout box, but the box is
 * then scaled by the `transform` on its parent dot — so a name in the nearest
 * band is drawn `DEPTH_SCALES[3]` times its cap, and a bound of a flat 80 px
 * became wrong for three of the four bands the moment the yard gained depth.
 * That is consistent rather than a defect (a nearer Ваня is bigger, name and
 * all), and the cap still has teeth against what it exists to stop: an uncapped
 * name here measures a couple of hundred pixels, not 24% over.
 *
 * `y` is the fixture's own coordinate rather than anything read back off the
 * element, so this stays an assertion about the DESIGN — a band written wrongly
 * onto the dot would otherwise move the goalposts with it.
 *
 * A pixel of slack on each bound, because a fractional layout size rounds.
 */
async function expectLabelWithinCap(page: Page, id: string, y: number): Promise<void> {
  const label = await nameTag(page, id).boundingBox();
  const box = await plane(page).boundingBox();
  expect(label, `no label box for ${id}`).not.toBeNull();
  expect(box, 'the plane has no box').not.toBeNull();
  const width = label?.width ?? 0;
  const planeWidth = box?.width ?? 1;
  const scale = DEPTH_SCALES[bandFor(y)];
  const cap = LABEL_MAX_UNITS * unitFor(planeWidth);
  expect(
    width,
    `${id}'s name is ${Math.round(width)}px wide in band ${bandFor(y)} against a ${Math.round(cap)}px cap (×${scale})`,
  ).toBeLessThanOrEqual(cap * scale + 1);
  expect(
    width / planeWidth,
    `${id}'s name is ${Math.round((width / planeWidth) * 100)}% of the yard wide`,
  ).toBeLessThanOrEqual(LABEL_MAX_PLANE_FRACTION * scale + 0.01);
}

/** Loads the game and steps past the intro into the yard. */
async function enterYard(page: Page): Promise<void> {
  await page.goto('/app/game-vanyagotchi');
  await page.getByRole('button', { name: 'Во двор' }).click();
  await expect(plane(page)).toBeVisible();
}

test.describe('«Ванягоччи» — the shared plane', () => {
  test('the intro carries the fiction disclaimer before play begins', async ({ page }) => {
    // The game names real meme characters and parodies a real subculture, so the
    // disclaimer is a requirement rather than decoration — and it must be on the
    // surface that is read BEFORE play, not buried behind it.
    await stubBackend(page);
    await stubSocket(page);
    await page.goto('/app/game-vanyagotchi');

    await expect(
      page.getByText('Все персонажи вымышлены; любые совпадения с реальными людьми случайны.'),
    ).toBeVisible();
    await expectNoOverflow(page, 'vanyagotchi intro');

    if (isMobile(page)) {
      await expectTapTarget(page.getByRole('button', { name: 'Во двор' }), 'enter-yard CTA');
    }
  });

  test('the disclaimer is never the thing squeezed off a short screen', async ({ page }) => {
    // It sits in a fixed-size row, never in the one flexible child, so shrinking
    // the viewport takes height from the artwork and not from the legal line.
    await stubBackend(page);
    await stubSocket(page);
    await page.setViewportSize({ width: 320, height: 568 });
    await page.goto('/app/game-vanyagotchi');

    const disclaimer = page.getByText(
      'Все персонажи вымышлены; любые совпадения с реальными людьми случайны.',
    );
    await expect(disclaimer).toBeVisible();
    const box = await disclaimer.boundingBox();
    expect(box).not.toBeNull();
    expect((box?.y ?? 0) + (box?.height ?? 0)).toBeLessThanOrEqual(568);
    await expectNoOverflow(page, 'vanyagotchi intro at 320x568');
  });

  test('peers from a roster frame appear on the plane at their coordinates', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(
      roster({ id: 'peer-a', x: 0.25, y: 0.75 }, { id: 'peer-b', x: 0.5, y: 0.5 }),
    );

    await expect(dots(page)).toHaveCount(2);
    // The position lives in the custom properties and nowhere else — no inline
    // transform, no left/top. Reading them back is how we know the frame
    // actually reached the DOM.
    expect(await peerPosition(page, 'peer-a')).toEqual({ x: '0.25', y: '0.75' });
    expect(await peerPosition(page, 'peer-b')).toEqual({ x: '0.5', y: '0.5' });
    await expect(page.getByText('во дворе: 2')).toBeVisible();
  });

  test('a later frame moves a peer without re-creating it', async ({ page }) => {
    // The membership tier must not churn when only positions changed: the dot
    // has to be the same DOM node, or every frame would be re-keying the list.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(roster({ id: 'peer-a', x: 0.1, y: 0.1 }));
    await expect(dots(page)).toHaveCount(1);
    await page.evaluate(() => {
      document.querySelector('[data-peer="peer-a"]')?.setAttribute('data-marked', '1');
    });

    await socket.push(roster({ id: 'peer-a', x: 0.9, y: 0.2 }));
    await expect
      .poll(async () => (await peerPosition(page, 'peer-a')).x)
      .toBe('0.9');
    await expect(page.locator('[data-peer="peer-a"][data-marked="1"]')).toHaveCount(1);
  });

  test('a peer that leaves the frame leaves the plane', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(roster({ id: 'peer-a', x: 0.2, y: 0.2 }, { id: 'peer-b', x: 0.8, y: 0.8 }));
    await expect(dots(page)).toHaveCount(2);

    await socket.push(roster({ id: 'peer-a', x: 0.2, y: 0.2 }));
    await expect(dots(page)).toHaveCount(1);
    await expect(page.getByText('во дворе: 1')).toBeVisible();
  });

  test('tapping the plane sends one normalised move, and nothing moves locally', async ({
    page,
  }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'me', x: 0.5, y: 0.5 }));
    await expect(dots(page)).toHaveCount(1);

    const box = await plane(page).boundingBox();
    expect(box).not.toBeNull();
    // A quarter across, three quarters down.
    await page.mouse.click(
      (box?.x ?? 0) + (box?.width ?? 0) * 0.25,
      (box?.y ?? 0) + (box?.height ?? 0) * 0.75,
    );

    // Exactly one move — the hello sent on open is not one of them.
    await expect.poll(() => socket.sent.filter((m) => m.t === TYPE_MOVE).length).toBe(1);
    const move = socket.sent.filter((m) => m.t === TYPE_MOVE)[0];
    expect(move.x as number).toBeCloseTo(0.25, 1);
    expect(move.y as number).toBeCloseTo(0.75, 1);

    // The server owns the position: the dot must NOT have moved optimistically.
    expect(await peerPosition(page, 'me')).toEqual({ x: '0.5', y: '0.5' });
  });

  test('the plane never scrolls, and the status row stays on screen', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(
      roster(
        ...Array.from({ length: 12 }, (_, i) => ({
          id: `peer-${i}`,
          x: (i % 4) / 3,
          y: Math.floor(i / 4) / 2,
        })),
      ),
    );
    await expect(dots(page)).toHaveCount(12);

    await expectNoOverflow(page, 'vanyagotchi yard');
    const scrolls = await page.evaluate(
      () => document.documentElement.scrollHeight - document.documentElement.clientHeight,
    );
    expect(scrolls, 'the play screen must never scroll vertically either').toBeLessThanOrEqual(1);

    // The status row is the fixed-size child; a crowded plane must not push it off.
    const hud = page.getByText(/во дворе:/);
    await expect(hud).toBeVisible();
    const hudBox = await hud.boundingBox();
    const viewportHeight = page.viewportSize()?.height ?? 0;
    expect((hudBox?.y ?? 0) + (hudBox?.height ?? 0)).toBeLessThanOrEqual(viewportHeight);
  });

  test('a dot is a full-size tap target even though the plane takes the tap', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.5, y: 0.5 }));
    await expect(dots(page)).toHaveCount(1);

    if (isMobile(page)) {
      // The 44 px rule binds the sprite size. Depth scaling has since arrived
      // and can only ever make a dot BIGGER — the far band's scale is 1 — so
      // this stays the floor rather than becoming an average. The band-by-band
      // version of the check is in the depth suite below.
      const box = await dots(page).first().boundingBox();
      expect(box).not.toBeNull();
      expect(Math.round(Math.min(box?.width ?? 0, box?.height ?? 0))).toBeGreaterThanOrEqual(
        PEER_BASE_PX,
      );
    }
  });

  test('a close tells the player which of the three things happened', async ({ page }) => {
    // The reason arrives as a bye FRAME, not a close code — a browser reports
    // 1006 for every disconnect. Showing "доступ отозван" rather than a generic
    // failure is the entire payoff of that frame existing.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.5, y: 0.5 }));
    await expect(dots(page)).toHaveCount(1);

    await socket.drop({ code: 4001, reason: 'unauthorized' });

    await expect(page.getByText('доступ отозван')).toBeVisible();
    // And the world empties rather than freezing on a snapshot that is no
    // longer being updated.
    await expect(dots(page)).toHaveCount(0);
  });

  test('a plain drop says so without inventing a reason', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.drop();
    await expect(page.getByText('связь потеряна')).toBeVisible();
  });

  test('the nav offers the yard', async ({ page }) => {
    await stubBackend(page);
    await stubSocket(page);
    await page.goto('/app/wishlist');

    const link = page.getByRole('link', { name: 'Ванягоччи' });
    await expect(link).toBeVisible();
    if (isMobile(page)) {
      await expectTapTarget(link, 'Ванягоччи nav link');
    }
  });
});

test.describe('«Ванягоччи» — surviving a restart', () => {
  test('the player can tell which Ваня is theirs', async ({ page }) => {
    // The id on the wire is a per-account pseudonym the server derives, not
    // anything the client already knows, so the only way to find yourself is to
    // ask. The client says hello on every open and the server answers.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await expect.poll(() => socket.sent.some((m) => m.t === TYPE_HELLO)).toBe(true);

    await socket.push(JSON.stringify({ t: TYPE_YOU, id: 'me' }));
    await socket.push(roster({ id: 'me', x: 0.3, y: 0.3 }, { id: 'other', x: 0.7, y: 0.7 }));

    await expect(dots(page)).toHaveCount(2);
    await expect(page.locator('[data-test="peer"][data-you="1"]')).toHaveCount(1);
    await expect(page.locator('[data-peer="me"][data-you="1"]')).toHaveCount(1);
  });

  test('a reconnect does not throw the player back to the intro', async ({ page }) => {
    await recordStale(page);
    // Losing the socket is the normal case — the service restarts several times
    // a day. Being bounced back to a splash screen with a "Во двор" button every
    // time would be worse than the disconnect.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.4, y: 0.4 }));
    await expect(dots(page)).toHaveCount(1);

    await socket.drop({ code: 1001, reason: 'restart' });

    // Still in the yard, with the plane on screen and the intro CTA gone.
    await expect(page.locator('[data-test="plane"]')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Во двор' })).toHaveCount(0);

    // And it really does come back, without anybody clicking anything.
    await socket.waitForConnections(2);
    await socket.push(roster({ id: 'peer-a', x: 0.6, y: 0.6 }));
    await expect(dots(page)).toHaveCount(1);
    await expect(page.getByText('на связи')).toBeVisible();

    // Whether the stale state was ever painted depends on how fast the
    // reconnect was — a quick one is coalesced away inside a single Vue flush,
    // which is a feature. What must hold either way is that it ended live and
    // never went stale-and-stayed-there.
    expect(
      await staleWatching(page),
      'the stale recorder never attached, so an empty log would prove nothing',
    ).toBe(true);
    const log = await staleLog(page);
    expect(log.at(-1) ?? false, `stale transitions: ${JSON.stringify(log)}`).toBe(false);
  });

  test('it says hello again after reconnecting, because the pseudonym changed', async ({
    page,
  }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await expect.poll(() => socket.sent.filter((m) => m.t === TYPE_HELLO).length).toBe(1);

    await socket.drop({ code: 1001, reason: 'restart' });
    await socket.waitForConnections(2);

    await expect
      .poll(() => socket.sent.filter((m) => m.t === TYPE_HELLO).length, { timeout: 15_000 })
      .toBe(2);
  });

  test('a revoked session stops for good instead of hammering the handshake', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.5, y: 0.5 }));
    await expect(dots(page)).toHaveCount(1);

    await socket.drop({ code: 4001, reason: 'unauthorized' });

    await expect(page.getByText('доступ отозван')).toBeVisible();
    await expect(dots(page)).toHaveCount(0);
    // The whole point: no retry. A blocked account must not sit there
    // reconnecting until the tab is closed.
    await page.waitForTimeout(3_000);
    expect(socket.connections()).toBe(1);
  });
});

test.describe('«Ванягоччи» — the shape of the world, and losing it briefly', () => {
  test('a blip does not empty the yard, it marks it stale', async ({ page }) => {
    await recordStale(page);
    // The bug this fixes, reported from a phone: "all disappeared". A mobile
    // socket drops constantly — a tunnel, a lock screen, a cell handover — and
    // clearing the plane the instant it did made every one of those look like
    // everybody had left. The dots stay, visibly stale, across a reconnect.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.3, y: 0.3 }, { id: 'peer-b', x: 0.7, y: 0.7 }));
    await expect(dots(page)).toHaveCount(2);

    // Keep it down, so the disconnected state is something a browser can be
    // asked about rather than a moment that has already passed.
    socket.holdDown(true);
    await socket.drop({ code: 1001, reason: 'restart' });

    // The world is still on screen — this is the whole bug — and it says so.
    await expect(page.locator('[data-test="plane"][data-stale="1"]')).toHaveCount(1);
    await expect(dots(page)).toHaveCount(2);
    await expect(page.getByText(/переподключаемся/)).toBeVisible();

    // And the staleness lifts by itself when the socket comes back. `push`
    // already waits for a socket to exist, so it is the readiness gate here —
    // counting connections would be counting the refusals too.
    socket.holdDown(false);
    await socket.push(roster({ id: 'peer-a', x: 0.3, y: 0.3 }, { id: 'peer-b', x: 0.7, y: 0.7 }));
    await expect(page.locator('[data-test="plane"][data-stale="1"]')).toHaveCount(0);
    await expect(dots(page)).toHaveCount(2);
    await expect(page.getByText('на связи')).toBeVisible();
  });

  test('a revoked session empties the yard at once, because nothing is coming back', async ({
    page,
  }) => {
    // The other side of the rule above: holding a world is only honest while a
    // reconnect is still possible.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);
    await socket.push(roster({ id: 'peer-a', x: 0.3, y: 0.3 }));
    await expect(dots(page)).toHaveCount(1);

    await socket.drop({ code: 4001, reason: 'unauthorized' });

    await expect(dots(page)).toHaveCount(0);
    await expect(page.locator('[data-test="plane"][data-stale="1"]')).toHaveCount(0);
  });

  test('the yard is the same shape on every device', async ({ page }) => {
    // Coordinates are normalised per axis, so a plane that took whatever space
    // was left would give a phone a tall world and a tablet a wide one — the
    // same coordinates, different distances between them. Distance becomes a
    // mechanic in Phase 2 (the beer delivery is a race to arrive), so the shape
    // has to be the same for everybody.
    await stubBackend(page);
    await stubSocket(page);
    await enterYard(page);

    const box = await plane(page).boundingBox();
    expect(box).not.toBeNull();
    const ratio = (box?.width ?? 0) / (box?.height ?? 1);
    expect(ratio, `plane is ${box?.width}x${box?.height}, ratio ${ratio}`).toBeCloseTo(3 / 4, 1);

    // And it still fits: no scrolling, at any of the viewports this suite runs.
    await expectNoOverflow(page, 'vanyagotchi yard shape');
    const vScroll = await page.evaluate(
      () => document.documentElement.scrollHeight - document.documentElement.clientHeight,
    );
    expect(vScroll).toBeLessThanOrEqual(1);
  });
});

test.describe('«Ванягоччи» — a yard of people rather than a field of dots', () => {
  // Until this iteration a roster entry was three fields — an id and a pair of
  // coordinates — and the plane drew everybody as an identical coloured circle.
  // The frame now also says what each entity LOOKS like, and it says it about
  // every entity rather than only about the caller's own, which is the
  // difference between one shared world and every player watching a private one.
  //
  // These tests deliberately drive appearance through the SOCKET and assert on a
  // peer that is NOT yours. Everything about a neighbour's Ваня — his skin, his
  // name, how he is doing — can only have come off the wire: this screen holds
  // no state about anybody else and could not derive it if it wanted to.

  test('every dot wears its own face and its own condition, not just yours', async ({ page }) => {
    // Three entities, three different poses, and the two that matter are the
    // ones that are not us. A screen that painted its own pet's condition onto
    // the yard — the obvious shortcut, and the one that makes two players see
    // two different worlds — would give all three the same face.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(JSON.stringify({ t: TYPE_YOU, id: 'me' }));
    await socket.push(
      roster(
        { id: 'me', x: 0.5, y: 0.5, art: 'vanya', label: 'я', pose: 'fine' },
        { id: 'neighbour', x: 0.2, y: 0.3, art: 'vanya', label: 'сосед', pose: 'poorly' },
        { id: 'goner', x: 0.8, y: 0.7, art: 'vanya', label: 'бедолага', pose: 'dead' },
      ),
    );

    await expect(dots(page)).toHaveCount(3);
    // A face per dot — the count is the assertion that this is no longer a
    // privilege of the caller's own entity.
    await expect(page.locator('[data-test="peer-face"]')).toHaveCount(3);

    await expect(face(page, 'me')).toHaveAttribute('data-condition', 'fine');
    await expect(face(page, 'neighbour')).toHaveAttribute('data-condition', 'poorly');
    await expect(face(page, 'goner')).toHaveAttribute('data-condition', 'dead');

    // And each name is on its own dot rather than all of them on one.
    await expect(nameTag(page, 'neighbour')).toHaveText('сосед');
    await expect(nameTag(page, 'goner')).toHaveText('бедолага');
    await expect(page.locator('[data-test="peer-label"]')).toHaveCount(3);
  });

  test('an unnamed Ваня gets no label element, and never the word "undefined"', async ({
    page,
  }) => {
    // Most Ваняs have no name — naming one is a dialog nobody has opened yet —
    // so "no name" is the common case rather than an edge one. It is one state
    // at every layer: the field is absent from the frame, `capLabel` answers
    // undefined, and the template's `v-if` renders no element at all. The bug
    // this forecloses is the classic one, where a missing name reaches the DOM
    // as the four-letter string a template interpolated out of nothing.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(
      roster(
        { id: 'named', x: 0.3, y: 0.3, art: 'vanya', label: 'Ваня', pose: 'fine' },
        { id: 'nameless', x: 0.7, y: 0.7, art: 'vanya', pose: 'fine' },
      ),
    );
    await expect(dots(page)).toHaveCount(2);

    await expect(nameTag(page, 'named')).toHaveText('Ваня');

    // Checked before the element count, so that this assertion is the one a
    // stringifying `capLabel` trips over rather than being shadowed by it.
    await expect(page.locator('[data-peer="nameless"]')).not.toContainText('undefined');
    await expect(plane(page)).not.toContainText('undefined');

    // Absent, not empty: nothing is rendered and nothing occupies the space.
    await expect(nameTag(page, 'nameless')).toHaveCount(0);
    await expect(page.locator('[data-test="peer-label"]')).toHaveCount(1);
    // He is still drawn, with a face — a nameless entity is not a broken one.
    await expect(face(page, 'nameless')).toBeVisible();
  });

  test('a change of pose lands on the dot that is already there', async ({ page }) => {
    // The same guarantee the position tier has, for the tier above it. Poses
    // arrive inside a frame that also carries coordinates, five times a second,
    // and re-keying the list on one would throw away every element on the plane
    // — taking the imperatively-written positions with it and restarting the
    // dot's CSS transition from wherever it happened to be. The marker is how a
    // browser can be asked whether this is literally the same node.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(roster({ id: 'peer-a', x: 0.4, y: 0.4, art: 'vanya', label: 'Ваня', pose: 'fine' }));
    await expect(face(page, 'peer-a')).toHaveAttribute('data-condition', 'fine');
    await page.evaluate(() => {
      document.querySelector('[data-peer="peer-a"]')?.setAttribute('data-marked', '1');
    });

    // Same entity, same place, worse day.
    await socket.push(roster({ id: 'peer-a', x: 0.4, y: 0.4, art: 'vanya', label: 'Ваня', pose: 'dead' }));

    await expect(face(page, 'peer-a')).toHaveAttribute('data-condition', 'dead');
    await expect(page.locator('[data-peer="peer-a"][data-marked="1"]')).toHaveCount(1);
    // The position survived with it, which is the thing a re-keyed list would
    // have silently dropped on the floor.
    expect(await peerPosition(page, 'peer-a')).toEqual({ x: '0.4', y: '0.4' });
  });

  test('long names cost the yard nothing, and never grow past their cap', async ({ page }) => {
    // A busy yard of long names is the case that breaks this screen. Three
    // independent caps stop it — `capLabel` bounds the STRING, the stylesheet
    // bounds the WIDTH, and the plane's `overflow: hidden` is the backstop — and
    // this asserts the two that are load-bearing rather than the backstop.
    //
    // The page-scroll checks below are kept as regression guards, but they are
    // deliberately not the point: `.stage` clips its own overflow and
    // styles/mobile.css puts `overflow-x: hidden` on the root, so a runaway name
    // would be swallowed twice over before it ever reached a scrollbar. What
    // WOULD be visible to a player is a name spilling across the neighbours, and
    // that is a measurement of the label's own box.
    //
    // Dots deliberately ON the edges: a name is centred on its dot, so one at
    // x=0 or x=1 hangs half its width past the plane. That is left clipped on
    // purpose (a drifted name reads as belonging to the dot next to it, which is
    // worse than a cut one), so what is asserted is the width, not containment.
    const LONG = 'Владислав-Афанасий Многобуквенный из Химок';
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    // The yard's geometry before a single name is in it, so the comparison
    // afterwards is against this screen rather than against a hardcoded size.
    const emptyPlane = await plane(page).boundingBox();
    expect(emptyPlane, 'the plane has no box').not.toBeNull();

    await socket.push(
      roster(
        { id: 'left', x: 0, y: 0.5, art: 'vanya', label: LONG, pose: 'fine' },
        { id: 'right', x: 1, y: 0.5, art: 'vanya', label: LONG, pose: 'poorly' },
        { id: 'top', x: 0.5, y: 0, art: 'vanya', label: LONG, pose: 'fine' },
        { id: 'bottom', x: 0.5, y: 1, art: 'vanya', label: LONG, pose: 'dead' },
        ...Array.from({ length: 4 }, (_, i) => ({
          id: `crowd-${i}`,
          x: 0.3 + i * 0.1,
          y: 0.4,
          art: 'vanya',
          label: LONG,
          pose: 'fine',
        })),
      ),
    );
    await expect(dots(page)).toHaveCount(8);

    // THE assertion: a name is a fraction of the world wide, not a third of the
    // screen. Without the width cap this label measures the whole 42 characters
    // and covers every neighbour it passes.
    // Both stand at mid-height, which the depth bands put in band 2 — so the
    // cap they are measured against is scaled by that band, not a flat 80 px.
    await expectLabelWithinCap(page, 'left', 0.5);
    await expectLabelWithinCap(page, 'right', 0.5);

    // And the yard did not move to accommodate any of them — same box, to the
    // pixel. A label that could stretch its parent would show up here as a
    // plane that had shrunk or changed shape.
    const namedPlane = await plane(page).boundingBox();
    expect(namedPlane?.width, 'the plane changed width once names arrived').toBeCloseTo(
      emptyPlane?.width ?? 0,
      1,
    );
    expect(namedPlane?.height, 'the plane changed height once names arrived').toBeCloseTo(
      emptyPlane?.height ?? 0,
      1,
    );

    await expectNoOverflow(page, 'vanyagotchi yard full of long names');
    await expectNoVerticalScroll(page, 'vanyagotchi yard full of long names');
    // The status row is the fixed child; a plane stretched by a name would be
    // the thing that pushed it off.
    const hud = page.getByText(/во дворе:/);
    await expect(hud).toBeVisible();
    const hudBox = await hud.boundingBox();
    const viewportHeight = page.viewportSize()?.height ?? 0;
    expect((hudBox?.y ?? 0) + (hudBox?.height ?? 0)).toBeLessThanOrEqual(viewportHeight);

    // And the STRING was cut, not merely hidden by CSS — so nothing downstream
    // (an aria label, a tooltip, anything added later) has to re-derive the cap,
    // and a server that never validated a name cannot put a kilobyte of it in
    // the DOM. Counted in code points, because that is the unit the cap is in:
    // cutting UTF-16 in the middle of a surrogate pair is how the same field
    // grows a replacement character the first time somebody uses an emoji.
    const shown = (await nameTag(page, 'left').textContent()) ?? '';
    expect([...shown].length, `label rendered ${shown.length} units: ${shown}`).toBeLessThanOrEqual(
      LABEL_MAX,
    );
    expect([...LONG].length, 'the fixture is no longer past the cap').toBeGreaterThan(LABEL_MAX);
  });

  test('long names still fit the smallest screen we support', async ({ page }) => {
    // 320x568 is the floor, and it is the width at which the clamp's FLOOR is
    // the branch in force: the plane is only about 231px across there, so
    // `13cqw` is ~30px and loses to the 44px minimum. Everything inside the
    // plane is a multiple of that unit, so this is the one viewport in the suite
    // where the whole world is drawn at its smallest permitted size rather than
    // in proportion to the yard — which is exactly the case worth pinning, since
    // it is where a name is the largest fraction of the plane it will ever be.
    //
    // Set before `goto` rather than resized afterwards, so the layout is built
    // for this size rather than reflowed into it.
    await page.setViewportSize({ width: 320, height: 568 });
    const LONG = 'Владислав-Афанасий Многобуквенный из Химок';
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(
      roster(
        ...Array.from({ length: 8 }, (_, i) => ({
          id: `peer-${i}`,
          x: (i % 4) / 3,
          y: Math.floor(i / 4),
          art: 'vanya',
          label: LONG,
          pose: (['fine', 'poorly', 'dead'] as const)[i % 3],
        })),
      ),
    );
    await expect(dots(page)).toHaveCount(8);

    // `peer-0` and `peer-3` are the top row, y = 0 — the furthest band, which is
    // the one the cap is stated in: its scale is 1, so these two measure the
    // stylesheet's number exactly.
    await expectLabelWithinCap(page, 'peer-0', 0);
    await expectLabelWithinCap(page, 'peer-3', 0);
    await expectNoOverflow(page, 'vanyagotchi yard of long names at 320x568');
    await expectNoVerticalScroll(page, 'vanyagotchi yard of long names at 320x568');
    // The plane was not squeezed to nothing to make room for the names either.
    const box = await plane(page).boundingBox();
    expect(box?.height ?? 0, 'the plane collapsed under a crowd of names').toBeGreaterThan(120);
  });
});

test.describe('«Ванягоччи» — the yard is no longer a list of the people in it', () => {
  // A roster entry used to mean "a player who is here", so counting the entries
  // counted the players. It does not any more: the same list now carries the
  // yard's regulars, who have no accounts, and everybody who is ASLEEP in it,
  // who has an account but is not here. Both are entities to draw and neither is
  // somebody you can talk to.
  //
  // So the head count moved onto the wire as `here`, counted by the server. That
  // is not a convenience: deriving it in the browser would mean the browser
  // holding an opinion about which entity is a real person, and a character
  // added on the server would then silently inflate the count on every client
  // that had not been redeployed.

  test('the head count is the one the server sent, not the number of things on the plane', async ({
    page,
  }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    // Five entities, two of whom are people: one other player, one Ваня asleep
    // where his owner left him, and a couple of regulars.
    //
    // HOW MANY regulars is arbitrary and deliberately not a mirror of the real
    // cast — the point of the fixture is that the frame contains entities that
    // are not people, and two is enough to make it. The client is kind-agnostic,
    // so casting a third on the server changes nothing here; that the count on
    // screen follows the SERVER's cast rather than a number written down is
    // asserted in the full-stack suite, against the served catalogue.
    await socket.push(
      rosterHere(
        2,
        { id: 'me', x: 0.5, y: 0.5, art: 'vanya', label: 'я', pose: 'fine' },
        { id: 'neighbour', x: 0.3, y: 0.4, art: 'vanya', label: 'сосед', pose: 'fine' },
        { id: 'dozing', x: 0.2, y: 0.8, art: 'vanya', label: 'соня', pose: 'asleep' },
        { id: 'npc-sahur', x: 0.6, y: 0.3, art: 'npc_sahur', label: 'Тунг Тунг Сахур', pose: 'fine' },
        { id: 'npc-ballerina', x: 0.7, y: 0.9, art: 'npc_ballerina', label: 'Балерина', pose: 'fine' },
      ),
    );

    // Everything in the frame is drawn — the client stays kind-agnostic, which
    // is what makes "add a character" a backend-only change.
    await expect(dots(page)).toHaveCount(5);

    // And exactly two of them are counted as people.
    await expect(page.getByText('во дворе: 2')).toBeVisible();
    await expect(page.getByText('во дворе: 5')).toHaveCount(0);
    // The same number reaches a screen reader, from the same source. These used
    // to be two independent reads of `peerIds.length` and could not disagree;
    // now they are two reads of one ref, and this is what says so.
    await expect(plane(page)).toHaveAttribute('aria-label', 'Двор, во дворе 2');
  });

  test('the count follows the server down as well as up', async ({ page }) => {
    // The count is a ref of a primitive, so an unchanged yard costs no render —
    // which is exactly the shape of bug where a number goes up and never comes
    // back down.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    // Background scenery for the count, and a fixture rather than a mirror: how
    // many regulars the real yard has is the server's business and is asserted
    // against the served catalogue in the full-stack suite. What matters here is
    // only that some of the entities in the frame are not people.
    const cast = [
      { id: 'npc-sahur', x: 0.6, y: 0.3, art: 'npc_sahur', pose: 'fine' },
      { id: 'npc-ballerina', x: 0.7, y: 0.9, art: 'npc_ballerina', pose: 'fine' },
    ];
    await socket.push(
      rosterHere(
        3,
        { id: 'a', x: 0.2, y: 0.2, art: 'vanya', pose: 'fine' },
        { id: 'b', x: 0.4, y: 0.4, art: 'vanya', pose: 'fine' },
        { id: 'c', x: 0.6, y: 0.6, art: 'vanya', pose: 'fine' },
        ...cast,
      ),
    );
    await expect(page.getByText('во дворе: 3')).toBeVisible();

    // Two of the three go home. They are past the grace, so they are still on
    // the plane — asleep — and the yard is emphatically NOT emptier to look at.
    await socket.push(
      rosterHere(
        1,
        { id: 'a', x: 0.2, y: 0.2, art: 'vanya', pose: 'fine' },
        { id: 'b', x: 0.4, y: 0.4, art: 'vanya', pose: 'asleep' },
        { id: 'c', x: 0.6, y: 0.6, art: 'vanya', pose: 'asleep' },
        ...cast,
      ),
    );
    await expect(page.getByText('во дворе: 1')).toBeVisible();
    await expect(dots(page), 'the sleepers left the plane instead of lying down').toHaveCount(5);
  });

  test('a server that does not count falls back to counting entities', async ({ page }) => {
    // A deploy is a window in which the old server is still live, and it sends
    // no `here`. Falling back to the number of entities is exactly right for it:
    // a server with no cast and no sleepers puts nothing in a frame that is not
    // a person. Exercised rather than assumed — an untried fallback is one
    // nobody knows is broken.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(
      rosterWithoutHere(
        { id: 'a', x: 0.2, y: 0.2, art: 'vanya', pose: 'fine' },
        { id: 'b', x: 0.8, y: 0.8, art: 'vanya', pose: 'fine' },
      ),
    );
    await expect(page.getByText('во дворе: 2')).toBeVisible();

    // And the field wins over the fallback the moment it appears, rather than
    // the first frame's answer sticking.
    await socket.push(
      rosterHere(
        1,
        { id: 'a', x: 0.2, y: 0.2, art: 'vanya', pose: 'fine' },
        { id: 'b', x: 0.8, y: 0.8, art: 'vanya', pose: 'asleep' },
      ),
    );
    await expect(page.getByText('во дворе: 1')).toBeVisible();
  });

  test('a sleeping Ваня reads as dormant rather than as somebody standing still', async ({
    page,
  }) => {
    // The sleepers are what stop a solo visit being an empty field, so a sleeper
    // has to read as "somebody, not here" rather than as "somebody, slightly
    // faded" — one that looked merely dim would be indistinguishable from a
    // player standing still, and the yard would seem full of people ignoring
    // you. Three cues, none of them colour alone, and all three are asserted:
    // the dot dims, the face topples over, and a 💤 floats off it.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(
      rosterHere(
        1,
        { id: 'awake', x: 0.3, y: 0.6, art: 'vanya', label: 'бодрый', pose: 'fine' },
        { id: 'dozing', x: 0.7, y: 0.6, art: 'vanya', label: 'соня', pose: 'asleep' },
      ),
    );
    await expect(dots(page)).toHaveCount(2);

    // The pose came off the wire and reached the DOM. A client that did not know
    // the fourth pose would fall back to `fine` here rather than fail, which is
    // why this is asserted at all: the fallback is deliberate and would hide it.
    await expect(face(page, 'dozing')).toHaveAttribute('data-condition', 'asleep');
    await expect(face(page, 'awake')).toHaveAttribute('data-condition', 'fine');

    // Dimmed, and dimmed as a WHOLE dot rather than only the face — he is not a
    // player who is doing badly, he is a player who is not here.
    await expect(page.locator('[data-peer="dozing"]')).toHaveClass(/peer--asleep/);
    await expect(page.locator('[data-peer="awake"]')).not.toHaveClass(/peer--asleep/);
    const dozingOpacity = await peerOpacity(page, 'dozing');
    const awakeOpacity = await peerOpacity(page, 'awake');
    expect(awakeOpacity, 'a Ваня who is present should be drawn at full strength').toBeCloseTo(1, 1);
    expect(
      dozingOpacity,
      `a sleeper is drawn at ${dozingOpacity}, which is not visibly dimmer than the player next to him`,
    ).toBeLessThan(0.9);

    // Lying down. At 44 px a change of ORIENTATION carries across the yard where
    // a change of tone does not, and it is literally what the server means.
    const tilt = await faceTilt(page, 'dozing');
    expect(Math.abs(tilt), `a sleeping face is tilted ${Math.round(tilt)}°`).toBeGreaterThan(45);
    expect(Math.abs(await faceTilt(page, 'awake'))).toBeLessThan(1);

    // And the 💤, which is the cue that survives a greyscale screenshot.
    expect(await badge(page, 'dozing')).toContain('💤');
    expect(await badge(page, 'awake')).not.toContain('💤');

    // He is still a real entity: drawn where the server put him, with a face and
    // his name, rather than a placeholder.
    expect(await peerPosition(page, 'dozing')).toEqual({ x: '0.7', y: '0.6' });
    await expect(nameTag(page, 'dozing')).toHaveText('соня');
  });

  test('a line over his head appears when the server sends it, and goes when it stops', async ({
    page,
  }) => {
    // The frames below are IDENTICAL apart from the line, which is the whole
    // point of the test. A balloon is on the wire for a few seconds and then
    // gone, so it is the one appearance field that reliably changes twice a
    // minute — and the guard that keeps 299 frames out of 300 from re-rendering
    // compares appearance field by field. Left out of that comparison the
    // balloon would only ever be noticed on a frame that happened to change
    // something else, which in a quiet yard is never.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    const SAME = { id: 'walker', x: 0.5, y: 0.5, art: 'vanya', label: 'Ваня', pose: 'fine' };

    await socket.push(roster(SAME));
    await expect(dots(page)).toHaveCount(1);
    await expect(sayBubble(page, 'walker')).toHaveCount(0);
    // Marked so the assertions below can tell "the balloon appeared on this dot"
    // from "the list was re-keyed and this is a different dot that has one".
    await page.evaluate(() => {
      document.querySelector('[data-peer="walker"]')?.setAttribute('data-marked', '1');
    });

    await socket.push(roster({ ...SAME, say: SAY_FIXTURE }));

    await expect(sayBubble(page, 'walker')).toBeVisible();
    await expect(sayBubble(page, 'walker')).toHaveText(SAY_FIXTURE);
    await expect(page.locator('[data-peer="walker"][data-marked="1"]')).toHaveCount(1);
    // The position tier was not disturbed by the appearance change.
    expect(await peerPosition(page, 'walker')).toEqual({ x: '0.5', y: '0.5' });

    // A DIFFERENT line on an otherwise identical frame replaces the first one.
    //
    // Absent → present is not the whole of it any more. The server draws its
    // lines from pools rather than from one constant, so the same Ваня standing
    // in the same spot can say two different things, and the appearance guard
    // that keeps 299 frames in 300 from re-rendering compares field by field.
    // A guard that treated `say` as a boolean — or diffed it only against
    // absence — would leave the first line frozen over his head while the server
    // had moved on, and every other assertion in this file would still pass.
    const SECOND_LINE = 'пивка бы';
    expect(SECOND_LINE, 'the two fixture lines are the same string').not.toBe(SAY_FIXTURE);
    await socket.push(roster({ ...SAME, say: SECOND_LINE }));
    await expect(sayBubble(page, 'walker')).toHaveText(SECOND_LINE);
    await expect(page.locator('[data-peer="walker"][data-marked="1"]')).toHaveCount(1);

    // And it goes when the server stops saying it — absent, not empty, so there
    // is no stray box left sitting over his head.
    await socket.push(roster(SAME));
    await expect(sayBubble(page, 'walker')).toHaveCount(0);
    await expect(page.locator('[data-peer="walker"][data-marked="1"]')).toHaveCount(1);
  });

  test('a balloon stays on the plane and never grows past its cap, at any width', async ({
    page,
  }) => {
    // Three independent caps, the same shape as the name's: `capSay` bounds the
    // STRING, the stylesheet bounds the WIDTH, and the plane's `overflow:
    // hidden` is the backstop. The first two are load-bearing and are what this
    // asserts.
    //
    // The vertical half is the flip. A balloon hangs off the top of a dot, so
    // near the top of the plane there is no room for it and it goes underneath
    // instead. That matters more than it sounds: unlike a clipped NAME, which is
    // still legibly somebody's name, a clipped balloon is nothing at all — the
    // line is transient, so if it is not readable now it never will be.
    const LONG = 'мне очень нехорошо после вчерашнего, братан';
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    const emptyPlane = await plane(page).boundingBox();
    expect(emptyPlane, 'the plane has no box').not.toBeNull();

    await socket.push(
      roster(
        { id: 'middle', x: 0.5, y: 0.5, art: 'vanya', pose: 'fine', say: SAY_FIXTURE },
        { id: 'high', x: 0.5, y: 0.03, art: 'vanya', pose: 'fine', say: SAY_FIXTURE },
        { id: 'wordy', x: 0.5, y: 0.6, art: 'vanya', pose: 'fine', say: LONG },
        // Deliberately ON the edge. A balloon is centred on its dot, so this one
        // hangs half its width off the plane and is left clipped on purpose —
        // what is asserted for it is its WIDTH, not its containment.
        { id: 'edge', x: 0, y: 0.55, art: 'vanya', pose: 'fine', say: LONG },
      ),
    );
    await expect(dots(page)).toHaveCount(4);

    const planeBox = await plane(page).boundingBox();
    expect(planeBox).not.toBeNull();
    const planeWidth = planeBox?.width ?? 1;
    const cap = Math.min(SAY_MAX_UNITS * unitFor(planeWidth), planeWidth * SAY_MAX_PLANE_FRACTION);

    // Scaled by the band, for the same reason a name is: `max-width` bounds the
    // balloon's own layout box and the dot's depth `transform` then scales it,
    // so the DRAWN width of a line in the nearest band is a quarter over the
    // number in the stylesheet. Both of these stand at heights whose scale the
    // fixture states, rather than being measured back off the element.
    for (const [id, y] of [
      ['wordy', 0.6],
      ['edge', 0.55],
    ] as const) {
      const scale = DEPTH_SCALES[bandFor(y)];
      const box = await sayBubble(page, id).boundingBox();
      expect(box, `no balloon box for ${id}`).not.toBeNull();
      expect(
        box?.width ?? 0,
        `${id}'s balloon is ${Math.round(box?.width ?? 0)}px wide in band ${bandFor(y)} against a ${Math.round(cap)}px cap (×${scale})`,
      ).toBeLessThanOrEqual(cap * scale + 1);
    }

    // The STRING was cut too, not merely hidden by CSS — so nothing downstream
    // has to re-derive the cap, and a server that never validated a line cannot
    // put a kilobyte of it in the DOM. Counted in code points, because cutting
    // UTF-16 mid-surrogate is how this field grows a replacement character the
    // first time somebody uses an emoji.
    const shown = (await sayBubble(page, 'wordy').textContent()) ?? '';
    expect([...shown].length, `the balloon rendered ${shown}`).toBeLessThanOrEqual(SAY_MAX);
    expect([...LONG].length, 'the fixture is no longer past the cap').toBeGreaterThan(SAY_MAX);

    // A balloon in the body of the plane hangs ABOVE its dot and is wholly on
    // the plane.
    expect(await saysBelow(page, 'middle')).toBe('0');
    const middleBubble = await sayBubble(page, 'middle').boundingBox();
    const middleDot = await page.locator('[data-peer="middle"]').boundingBox();
    expect(middleBubble?.y ?? 0, "the middle balloon is not above its own dot").toBeLessThan(
      middleDot?.y ?? 0,
    );
    expect(middleBubble?.y ?? -1, 'the middle balloon starts above the plane').toBeGreaterThanOrEqual(
      (planeBox?.y ?? 0) - 1,
    );
    expect(
      (middleBubble?.y ?? 0) + (middleBubble?.height ?? 0),
      'the middle balloon runs off the bottom of the plane',
    ).toBeLessThanOrEqual((planeBox?.y ?? 0) + (planeBox?.height ?? 0) + 1);
    expect(middleBubble?.x ?? -1).toBeGreaterThanOrEqual((planeBox?.x ?? 0) - 1);
    expect((middleBubble?.x ?? 0) + (middleBubble?.width ?? 0)).toBeLessThanOrEqual(
      (planeBox?.x ?? 0) + (planeBox?.width ?? 0) + 1,
    );

    // And one near the top of the plane has flipped underneath its dot, which is
    // the only thing between it and being clipped away to nothing.
    expect(bandFor(0.03), 'the fixture is no longer in the flip zone').toBe(0);
    expect(0.03, 'the fixture is no longer above the flip line').toBeLessThan(SAY_FLIP_Y);
    expect(await saysBelow(page, 'high')).toBe('1');
    const highBubble = await sayBubble(page, 'high').boundingBox();
    expect(
      highBubble?.y ?? -1,
      'a balloon at the top of the plane was drawn off the top of it, where nobody can read it',
    ).toBeGreaterThanOrEqual(planeBox?.y ?? 0);
    const highDot = await page.locator('[data-peer="high"]').boundingBox();
    expect(highBubble?.y ?? 0, 'the balloon near the top did not flip below its dot').toBeGreaterThan(
      highDot?.y ?? 0,
    );

    // The yard did not move to accommodate any of it, and the page still does
    // not scroll — at whichever of the suite's three widths this ran.
    const talkingPlane = await plane(page).boundingBox();
    expect(talkingPlane?.width, 'the plane changed width once somebody spoke').toBeCloseTo(
      emptyPlane?.width ?? 0,
      1,
    );
    expect(talkingPlane?.height, 'the plane changed height once somebody spoke').toBeCloseTo(
      emptyPlane?.height ?? 0,
      1,
    );
    await expectNoOverflow(page, 'vanyagotchi yard full of talking Ваняs');
    await expectNoVerticalScroll(page, 'vanyagotchi yard full of talking Ваняs');
  });

  test('a balloon does not swallow the tap underneath it', async ({ page }) => {
    // The plane is the tap target, not the things drawn on it. Ground that goes
    // briefly unwalkable whenever somebody speaks is an intermittent bug of
    // exactly the kind nobody ever reports usefully, because by the time you try
    // again the line has gone.
    //
    // What actually holds it up is a PAIR, and it is worth knowing which half is
    // which. `pointer-events: none` on `.peer-say` stops the balloon being the
    // event's target; the plane's handler being an ANCESTOR means the event
    // reaches it by bubbling even when something else is. So neither half alone
    // is load-bearing today — mutating the balloon to `pointer-events: auto`
    // leaves this test green — and the failure this guards is the pair going
    // wrong together, which is what happens the day somebody "tidies" the
    // handler into `if (event.target !== el) return`. Asserted as behaviour
    // rather than as either declaration, so it keeps its meaning whichever half
    // moves.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(
      roster({ id: 'me', x: 0.5, y: 0.5, art: 'vanya', label: 'Ваня', pose: 'fine', say: SAY_FIXTURE }),
    );
    await expect(sayBubble(page, 'me')).toBeVisible();

    const bubble = await sayBubble(page, 'me').boundingBox();
    const planeBox = await plane(page).boundingBox();
    expect(bubble, 'the balloon has no box to tap').not.toBeNull();
    expect(planeBox).not.toBeNull();
    const tapX = (bubble?.x ?? 0) + (bubble?.width ?? 0) / 2;
    const tapY = (bubble?.y ?? 0) + (bubble?.height ?? 0) / 2;

    await page.mouse.click(tapX, tapY);

    // Exactly one move, and it is the point under the balloon rather than
    // wherever a swallowed tap might have ended up. The hello sent on open is
    // not a move.
    await expect.poll(() => socket.sent.filter((m) => m.t === TYPE_MOVE).length).toBe(1);
    const move = socket.sent.filter((m) => m.t === TYPE_MOVE)[0];
    expect(move.x as number).toBeCloseTo((tapX - (planeBox?.x ?? 0)) / (planeBox?.width ?? 1), 2);
    expect(move.y as number).toBeCloseTo((tapY - (planeBox?.y ?? 0)) / (planeBox?.height ?? 1), 2);
  });
});

test.describe('«Ванягоччи» — a yard of faces, not a yard of the same face', () => {
  // Everybody in the yard wears the same skin, so the only thing telling two
  // players apart used to be the hashed colour under their Ваня. Each person's
  // own VK photograph answers the question the yard actually exists to answer —
  // which of these is my friend — and the whole of this suite is about the way
  // it gets there.
  //
  // IT IS NOT ON THE WIRE. No roster frame carries a URL: a couple of hundred
  // characters that change perhaps once a year, re-sent for every entity five
  // times a second to everybody watching, is most of what the socket would cost
  // at ten players on mobile data — and a URL out of Postgres would be the one
  // thing on a deliberately per-process frame that survived a restart, linking
  // two frames from either side of a deploy that nothing else could. So the
  // client derives `/api/game-vanyagotchi/avatar/<id>` from the id it is already
  // drawing and fetches it.
  //
  // The consequence these tests exist to pin is that it asks for EVERYTHING it
  // draws. It holds no notion of what an NPC is and no list of who is a person,
  // so 404 is not an error path here — it is the ordinary other half of the
  // yard, and what it means is "draw the catalogue art".

  test("a player's own face is drawn on their Ваня, and one without keeps its art", async ({
    page,
  }) => {
    await stubBackend(page);
    const faces = await stubFaces(page, ['photographed']);
    const socket = await stubSocket(page);
    await enterYard(page);

    // Both at the same height, so the two are drawn at the same scale and any
    // difference between them is the photograph rather than the depth band.
    const Y = 0.6;
    await socket.push(
      roster(
        { id: 'photographed', x: 0.3, y: Y, art: 'vanya', label: 'сосед', pose: 'fine' },
        { id: 'anonymous', x: 0.7, y: Y, art: 'vanya', label: 'аноним', pose: 'fine' },
      ),
    );
    await expect(dots(page)).toHaveCount(2);

    // The photograph, marked as a photograph. The `data-test` is what carries
    // the distinction — one img element draws either kind of picture, so a
    // selector on the tag or the class could not tell an avatar from a sprite.
    await expect(avatarOf(page, 'photographed')).toHaveCount(1);
    // ASKED FOR UNDER HIS OWN NAME. This is the assertion with teeth: the
    // address is derived rather than delivered, so a client that derived it from
    // the wrong thing — the viewer's id, the first peer in the frame, a single
    // shared route — would hang one person's face on the whole yard and look
    // perfectly right until two people with pictures stood in it at once.
    await expect(avatarOf(page, 'photographed')).toHaveAttribute(
      'src',
      `${AVATAR_PATH}photographed`,
    );
    // The request starts here and ends at a CDN, and no part of that journey
    // tells anybody which page of this site the viewer is on.
    await expect(avatarOf(page, 'photographed')).toHaveAttribute(
      'referrerpolicy',
      'no-referrer',
    );
    // It is the photograph and not the catalogue's own sprite, and there is
    // exactly one picture — nothing was drawn a second time underneath it.
    await expect(spriteOf(page, 'photographed')).toHaveCount(0);
    await expect(page.locator('[data-peer="photographed"] img')).toHaveCount(1);
    // And no glyph behind it: the emoji is the fallback, not a backdrop.
    await expect(face(page, 'photographed')).toHaveText('');

    // The Ваня nobody has a picture of falls back to the art he would have had
    // anyway — a glyph, drawn by the same element the photograph is not in. The
    // stub answers the catalogue with an empty object, so the glyph here is the
    // placeholder; what the assertion means is that a character was drawn rather
    // than a picture.
    await expect(page.locator('[data-peer="anonymous"] img')).toHaveCount(0);
    await expect(avatarOf(page, 'anonymous')).toHaveCount(0);
    await expect(face(page, 'anonymous')).toHaveText(UNKNOWN_ART);

    // KIND-AGNOSTIC, which is the property that lets an NPC arrive with no
    // client deploy: it asked about the entity that turned out to have no face
    // just as readily as about the one that had. A client that decided in
    // advance which ids were worth asking about would be carrying the cast list
    // in the browser, and would never have made this request at all.
    await expect.poll(() => faces.asked).toContain('anonymous');
    expect(faces.asked).toContain('photographed');

    // Both are still whole entities standing where the server put them, which is
    // the thing a rendering change is most likely to quietly cost.
    expect(await peerPosition(page, 'photographed')).toEqual({ x: '0.3', y: String(Y) });
    await expect(nameTag(page, 'photographed')).toHaveText('сосед');
    await expect(nameTag(page, 'anonymous')).toHaveText('аноним');
  });

  test('a photographed Ваня keeps the identity colour as a rim around his face', async ({
    page,
  }) => {
    // THE property that makes avatars affordable at all. A picture answers "who
    // is that" far better than a hue does — but only if you can see it, and at
    // one world unit across a photograph is a thumbnail. The hue is what still
    // reads from the other side of the yard, and it survives because the picture
    // is inset rather than filling the dot: the colour is the rim, always, for
    // an avatar exactly as it already was for a catalogue sprite.
    await stubBackend(page);
    await stubFaces(page, ['photographed']);
    const socket = await stubSocket(page);
    await enterYard(page);

    const Y = 0.6;
    await socket.push(roster({ id: 'photographed', x: 0.5, y: Y, art: 'vanya', pose: 'fine' }));
    await expect(avatarOf(page, 'photographed')).toHaveCount(1);

    // Every box at one instant, for the reason the unit test above the depth
    // suite states: read as separate round trips they can straddle a reflow, and
    // a face measured before one against a picture measured after is a mismatch
    // that looks exactly like the bug this asserts against.
    const drawn = await page.evaluate((id) => {
      const dot = document.querySelector<HTMLElement>(`[data-peer="${id}"]`);
      const faceEl = dot?.querySelector<HTMLElement>('[data-test="peer-face"]');
      const img = dot?.querySelector<HTMLImageElement>('[data-test="peer-avatar"]');
      if (!dot || !faceEl || !img) throw new Error(`no photograph on ${id}`);
      const style = getComputedStyle(img);
      return {
        face: faceEl.getBoundingClientRect().width,
        img: img.getBoundingClientRect().width,
        colour: getComputedStyle(dot).backgroundColor,
        radius: style.borderRadius,
        fit: style.objectFit,
        // Whether the browser really decoded the picture, rather than reserving
        // a box for one it never got. Without this every assertion below would
        // pass just as happily for a broken image.
        loaded: img.naturalWidth > 0,
      };
    }, 'photographed');

    expect(drawn.loaded, 'the browser never decoded the avatar it was given').toBe(true);
    expect(
      drawn.colour,
      'the dot lost the identity colour that the photograph is supposed to be rimmed with',
    ).not.toBe('rgba(0, 0, 0, 0)');
    expect(drawn.colour, `the dot is painted ${drawn.colour}`).toMatch(/^rgba?\(/);

    // The inset, against the stylesheet's own number rather than against
    // "smaller than the dot": a picture one pixel in from the edge is
    // technically inset and shows no rim a player could see.
    const planeBox = await plane(page).boundingBox();
    expect(planeBox, 'the plane has no box').not.toBeNull();
    const inset =
      PEER_SPRITE_INSET_UNITS * unitFor(planeBox?.width ?? 1) * DEPTH_SCALES[bandFor(Y)];
    expect(
      drawn.img,
      'the photograph fills its dot edge to edge, leaving no identity rim at all',
    ).toBeLessThan(drawn.face);
    expect(
      Math.abs(drawn.face - drawn.img - inset),
      `the rim is ${(drawn.face - drawn.img).toFixed(1)}px against the ${inset.toFixed(1)}px the stylesheet asks for`,
    ).toBeLessThanOrEqual(1);

    // Round and cropped rather than square and squashed: a VK avatar is not
    // necessarily square, and `object-fit: cover` is what stops a wide one being
    // drawn as a wide face.
    expect(drawn.radius, 'a square photograph on a round Ваня').not.toBe('0px');
    expect(drawn.fit, 'a photograph that is not square is being distorted to fit').toBe('cover');
  });

  test('a face nobody has leaves a Ваня rather than a hole, and is asked for once', async ({
    page,
  }) => {
    // A MISSING FACE MUST NEVER LEAVE A HOLE WHERE A ВАНЯ IS, and missing is the
    // common case rather than the exceptional one: 404 is what the endpoint says
    // about every NPC in the yard and about every player VK has no picture of.
    // Left to itself the browser answers that with its broken-image mark, which
    // reads as a bug in the yard rather than as a face nobody has. The `error`
    // handler forgets the entity's picture, and the dot falls back to exactly
    // what it would have drawn had there never been one to fetch.
    await stubBackend(page);
    const faces = await stubFaces(page, []);
    const socket = await stubSocket(page);
    await enterYard(page);

    const standing = { id: 'faceless', x: 0.5, y: 0.6, art: 'vanya', label: 'сосед', pose: 'fine' };
    await socket.push(roster(standing));
    await expect(dots(page)).toHaveCount(1);

    // The img goes and the glyph takes its place — not an empty face, and not an
    // img still sitting there with nothing in it.
    await expect(face(page, 'faceless')).toHaveText(UNKNOWN_ART);
    await expect(page.locator('[data-peer="faceless"] img')).toHaveCount(0);
    await expect.poll(() => faces.asked).toEqual(['faceless']);

    // ASKED ONCE, and this is the assertion that costs the most to get wrong.
    // Frames arrive five times a second and every one of them describes the same
    // entity; a client that re-derived the fallback per frame, or that let a
    // re-render put the img back with the same address in it, would keep the
    // whole yard's worth of 404s on the wire for as long as the yard was open —
    // and most of the yard is regulars, for whom the answer will never change.
    //
    // The frames below say something DIFFERENT each time on purpose. A frame
    // that repeats the last one is stopped by the appearance guard and never
    // reaches Vue at all, so a suite that pushed ten identical rosters would be
    // asserting that nothing happened rather than that a re-render kept the
    // fallback. A line appearing and going is the cheapest real change there is.
    for (let i = 0; i < 10; i += 1) {
      await socket.push(roster({ ...standing, say: i % 2 ? SAY_FIXTURE : undefined }));
      await expect(page.locator('[data-peer="faceless"] img')).toHaveCount(0);
    }
    await expect(face(page, 'faceless')).toHaveText(UNKNOWN_ART);
    expect(faces.asked, 'the yard is re-asking for a face that is not there').toEqual(['faceless']);

    // He is otherwise untouched: same place, same name, still on the plane.
    expect(await peerPosition(page, 'faceless')).toEqual({ x: '0.5', y: '0.6' });
    await expect(nameTag(page, 'faceless')).toHaveText('сосед');
  });
});

test.describe('«Ванягоччи» — further down the plane is nearer', () => {
  // Depth is drawn by two mechanisms that agree only because it is DISCRETE: the
  // size is a `transform`, which the compositor interpolates over the 220 ms a
  // move takes, and the stacking order is a `z-index`, which can only jump.
  // Both are written imperatively alongside the position, off the same `--y`,
  // for the same reason the position is: at 5 Hz and twenty entities, putting
  // this through Vue would be a thousand vdom patches a second to produce a
  // transform the compositor could have been handed straight away.

  test('a lower dot is in a nearer band, drawn larger, and stacked in front', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    // One entity per band, at the middle of each quarter of the plane.
    const ys = [0.1, 0.35, 0.6, 0.85];
    await socket.push(
      roster(
        ...ys.map((y, i) => ({ id: `band-${i}`, x: 0.5, y, art: 'vanya', pose: 'fine' })),
      ),
    );
    await expect(dots(page)).toHaveCount(4);

    let previous: { band: number; depth: number; zIndex: string } | undefined;
    for (const [i, y] of ys.entries()) {
      const seen = await peerDepth(page, `band-${i}`);
      // The band the DESIGN puts this height in, not whatever was written.
      expect(seen.band, `y=${y} landed in band ${seen.band}`).toBe(bandFor(y));
      expect(seen.depth, `band ${seen.band} scales by ${seen.depth}`).toBeCloseTo(
        DEPTH_SCALES[bandFor(y)],
        3,
      );
      // The band is the stacking order, resolved by the browser rather than read
      // off the declaration — `z-index: var(--band)` is only depth if the custom
      // property really reaches the used value.
      expect(seen.zIndex, `band ${seen.band} stacks at ${seen.zIndex}`).toBe(String(bandFor(y)));

      if (previous) {
        expect(seen.band, 'a lower entity is in a further band').toBeGreaterThan(previous.band);
        expect(seen.depth, 'a lower entity is not drawn any larger').toBeGreaterThan(previous.depth);
        expect(
          Number(seen.zIndex),
          'a lower entity does not stack in front of the one behind it',
        ).toBeGreaterThan(Number(previous.zIndex));
      }
      previous = seen;
    }

    // And the scale is really being drawn, not merely declared: the nearest band
    // measures bigger than the furthest by its own ratio.
    //
    // A RATIO, not two absolute sizes, and that is the claim this was always
    // making. It used to read `far.width ≈ 44 && near.width ≈ 44 * scale`, which
    // was the same statement only because a dot was 44px everywhere; a dot is
    // now `var(--unit)` and grows with the plane, so at this project's width the
    // absolute numbers are different on every viewport and mean nothing on any
    // of them. What has to be true is that depth multiplies the unit — whatever
    // the unit currently is — and that survives every future change to how big a
    // Ваня is drawn.
    const far = await dots(page).nth(0).boundingBox();
    const near = await dots(page).nth(3).boundingBox();
    expect(far?.width ?? 0, 'the furthest dot has no width at all').toBeGreaterThan(0);
    expect(
      (near?.width ?? 0) / (far?.width ?? 1),
      'the near band is not drawn larger than the far one by its own scale',
    ).toBeCloseTo(DEPTH_SCALES[3] / DEPTH_SCALES[0], 2);
  });

  // The regression test this whole investigation exists to produce.
  //
  // A Ваня used to be a fixed 44px standing on a plane sized from its container,
  // so the same world drew at 19.1% dot-to-plane on a 320px phone and 7.2% on a
  // 1920px desktop — a 2.7x spread, which is two different games. Nothing caught
  // it because nothing ever opened this screen wider than a tablet.
  //
  // It resizes the viewport itself rather than relying on the projects, for two
  // reasons: the spread is NOT monotonic in viewport width (`min(100cqw, 75cqh)`
  // switches which term binds, so height matters as much as width, and a
  // breakpoint-keyed fix would have been wrong), and a claim about two widths
  // agreeing cannot be made by a test that only ever sees one.
  test('a Ваня is the same fraction of the yard at every width', async ({ page }) => {
    await stubBackend(page);
    const socket = await stubSocket(page);

    const seen: { label: string; plane: number; dot: number }[] = [];
    for (const size of [
      { width: 360, height: 800 },
      { width: 390, height: 844 },
      { width: 768, height: 1024 },
      { width: 1440, height: 900 },
      { width: 1920, height: 1080 },
      // Landscape, and specifically under the `(orientation: landscape) and
      // (max-height: 600px)` rule that puts the panel BESIDE the plane. That
      // changes the frame's box and therefore the unit, which is the one case
      // where reasoning about this from the portrait numbers would be wrong —
      // so it is measured rather than assumed.
      { width: 844, height: 390 },
    ]) {
      // Sized BEFORE the page is built, then loaded fresh, rather than resized
      // between measurements. The same discipline the 320x568 test states, and
      // for a sharper reason here: `.stage` is `calc(100dvh - 72px)` and the
      // plane is `min(100cqw, 75cqh)` of what the panel leaves, so a page
      // reflowed into a new viewport was measured mid-settle and reported a
      // plane a quarter of its real width. A load per size costs a second and
      // removes the whole class of question.
      await page.setViewportSize(size);
      await enterYard(page);
      // Band 0, so `--depth` is 1 and the measured box is the unit itself rather
      // than the unit times a scale.
      await socket.push(roster({ id: 'me', x: 0.5, y: 0.1, art: 'vanya', pose: 'fine' }));
      await expect(dots(page)).toHaveCount(1);

      // BOTH BOXES IN ONE EVALUATE, at one instant. Read as two round trips they
      // can straddle a reflow, and a plane from before it against a dot from
      // after is a mismatch that looks exactly like the bug this test is for.
      const m = await page.evaluate(() => {
        const p = document.querySelector('[data-test="plane"]');
        const d = document.querySelector('[data-test="peer"]');
        return {
          plane: p?.getBoundingClientRect().width ?? 0,
          dot: d?.getBoundingClientRect().width ?? 0,
        };
      });
      seen.push({ label: `${size.width}x${size.height}`, ...m });
    }

    // What the stylesheet says, asserted at every width INCLUDING the ones where
    // the clamp binds: `--unit: clamp(44px, 13cqw, 88px)`, and `cqw` inside a
    // `.peer` resolves against the plane. Written as the formula rather than as
    // measured numbers so it stays true when the plane's own sizing changes.
    for (const s of seen) {
      const want = unitFor(s.plane);
      expect(
        s.dot,
        `at ${s.label} the plane is ${s.plane.toFixed(1)}px and a Ваня is ${s.dot.toFixed(1)}px, want ${want.toFixed(1)}px`,
      ).toBeCloseTo(want, 0);
    }

    // And where neither end of the clamp binds, the fraction is the SAME — which
    // is the property the bug violated. Filtered rather than hardcoded so the
    // test keeps meaning this when the clamp's bounds are retuned.
    const free = seen.filter(
      (s) =>
        s.plane * PEER_UNIT_PLANE_FRACTION > PEER_BASE_PX &&
        s.plane * PEER_UNIT_PLANE_FRACTION < PEER_UNIT_MAX_PX,
    );
    expect(
      free.length,
      'no viewport here draws a plane in the clamp\'s free range, so this asserts nothing',
    ).toBeGreaterThan(1);
    const ratios = free.map((s) => s.dot / s.plane);
    expect(
      Math.max(...ratios) - Math.min(...ratios),
      `dot-to-plane ratios disagree across widths: ${free
        .map((s, i) => `${s.label}=${(ratios[i] * 100).toFixed(1)}%`)
        .join(' ')}`,
    ).toBeLessThan(0.01);
  });

  test('the nearer of two overlapping Ваняs is the one you can see', async ({ page }) => {
    // The claim `z-index` actually makes, asked of the RENDERER rather than
    // re-derived from the stylesheet in the test.
    //
    // The near dot is FIRST in the frame, and therefore first in the DOM. Source
    // order alone would paint the second one on top, so a build that lost the
    // stacking order — or applied it from something other than the band —
    // answers with the far dot here. That is the mutation this is for.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    // A hair either side of a band boundary, so they overlap almost completely
    // and are still one band apart.
    const NEAR_Y = 0.51;
    const FAR_Y = 0.49;
    expect(bandFor(NEAR_Y), 'the fixture no longer straddles a band boundary').toBe(
      bandFor(FAR_Y) + 1,
    );

    await socket.push(
      roster(
        { id: 'near', x: 0.5, y: NEAR_Y, art: 'vanya', pose: 'fine' },
        { id: 'far', x: 0.5, y: FAR_Y, art: 'vanya', pose: 'fine' },
      ),
    );
    await expect(dots(page)).toHaveCount(2);

    // The dots are `pointer-events: none` in production — the plane takes the
    // tap, and the balloon test above is what pins that. Hit-testing is
    // nevertheless the only way to ask the BROWSER which of two overlapping
    // things is in front, because `elementFromPoint` walks the same painting
    // order `z-index` defines. Turning the dots on for this one measurement
    // touches neither `z-index` nor `transform`, which are what is under test.
    await page.addStyleTag({
      content: '[data-test="peer"] { pointer-events: auto !important; }',
    });

    const nearBox = await page.locator('[data-peer="near"]').boundingBox();
    const farBox = await page.locator('[data-peer="far"]').boundingBox();
    expect(nearBox, 'no box for the near dot').not.toBeNull();
    expect(farBox, 'no box for the far dot').not.toBeNull();

    // The middle of wherever the two actually overlap, computed rather than
    // guessed — the plane is a different size at each of the suite's widths.
    const left = Math.max(nearBox?.x ?? 0, farBox?.x ?? 0);
    const right = Math.min(
      (nearBox?.x ?? 0) + (nearBox?.width ?? 0),
      (farBox?.x ?? 0) + (farBox?.width ?? 0),
    );
    const top = Math.max(nearBox?.y ?? 0, farBox?.y ?? 0);
    const bottom = Math.min(
      (nearBox?.y ?? 0) + (nearBox?.height ?? 0),
      (farBox?.y ?? 0) + (farBox?.height ?? 0),
    );
    expect(right - left, 'the two dots do not overlap horizontally').toBeGreaterThan(4);
    expect(bottom - top, 'the two dots do not overlap vertically').toBeGreaterThan(4);

    const frontmost = await page.evaluate(
      ([x, y]) =>
        document
          .elementFromPoint(x, y)
          ?.closest('[data-peer]')
          ?.getAttribute('data-peer') ?? null,
      [(left + right) / 2, (top + bottom) / 2] as const,
    );
    expect(
      frontmost,
      'the Ваня standing further down the plane was drawn BEHIND the one further up',
    ).toBe('near');
  });

  test('your own Ваня does not float above somebody standing nearer than he is', async ({
    page,
  }) => {
    // Your own dot used to carry a `z-index` of its own, which was harmless
    // while everything else was 0 and is a lie now that the stacking order MEANS
    // something: it would draw you in front of a player standing nearer the
    // viewer, in a yard where that is the only cue for how far away anybody is.
    // The ring already says which one is you, and it says it without moving you.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    await socket.push(JSON.stringify({ t: TYPE_YOU, id: 'me' }));
    await socket.push(
      roster(
        { id: 'me', x: 0.5, y: 0.1, art: 'vanya', label: 'я', pose: 'fine' },
        { id: 'neighbour', x: 0.5, y: 0.9, art: 'vanya', label: 'сосед', pose: 'fine' },
      ),
    );
    await expect(dots(page)).toHaveCount(2);
    // The marking really did apply, so this is a test of the stacking rather
    // than of a class that silently stopped being added.
    await expect(page.locator('[data-peer="me"][data-you="1"]')).toHaveCount(1);
    await expect(page.locator('[data-peer="me"]')).toHaveClass(/peer--you/);

    const mine = await peerDepth(page, 'me');
    const theirs = await peerDepth(page, 'neighbour');
    expect(mine.zIndex, 'your own Ваня was lifted out of his own band').toBe(String(bandFor(0.1)));
    expect(theirs.zIndex).toBe(String(bandFor(0.9)));
    expect(
      Number(theirs.zIndex),
      'you are drawn in front of somebody standing nearer the viewer than you',
    ).toBeGreaterThan(Number(mine.zIndex));
  });

  test('the furthest band is still a 44 px tap target', async ({ page }) => {
    // The floor, band by band. It holds without arithmetic because the FIRST
    // depth scale is 1: depth can only ever make an entity bigger, so 44 px is
    // the smallest anything is ever drawn rather than the product of two numbers
    // that could drift apart. A change that scaled the far band DOWN instead
    // would draw a perfectly plausible yard and quietly break the mobile rule.
    await stubBackend(page);
    const socket = await stubSocket(page);
    await enterYard(page);

    const ys = [0.1, 0.35, 0.6, 0.85];
    await socket.push(
      roster(...ys.map((y, i) => ({ id: `band-${i}`, x: 0.5, y, art: 'vanya', pose: 'fine' }))),
    );
    await expect(dots(page)).toHaveCount(4);

    expect(DEPTH_SCALES[0], 'the furthest band no longer draws at full size').toBe(1);
    expect(Math.min(...DEPTH_SCALES), 'some band now draws an entity SMALLER than its CSS size').toBe(
      1,
    );

    if (isMobile(page)) {
      for (const [i, y] of ys.entries()) {
        const box = await page.locator(`[data-peer="band-${i}"]`).boundingBox();
        expect(box, `no box for the dot in band ${bandFor(y)}`).not.toBeNull();
        const min = Math.round(Math.min(box?.width ?? 0, box?.height ?? 0));
        expect(
          min,
          `band ${bandFor(y)} draws a ${Math.round(box?.width ?? 0)}x${Math.round(box?.height ?? 0)} tap target`,
        ).toBeGreaterThanOrEqual(PEER_BASE_PX);
      }
    }
  });
});
