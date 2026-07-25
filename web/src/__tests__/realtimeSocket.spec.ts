import { describe, expect, it, vi } from 'vitest';
import { BASE_DELAY_MS, HIDDEN_CLOSE_MS } from '../realtime/backoff';
import {
  IDLE_GRACE_MS,
  byeDetail,
  createRealtimeClient,
  parseFrame,
  realtimeURL,
  type RealtimeDeps,
} from '../realtime/socket';

// A fake socket that records what was sent and lets a test drive the callbacks
// the browser would fire. The real WebSocket does not exist under jsdom, which
// is exactly why the client takes its opener as a dependency.
class FakeSocket {
  static OPEN = 1;
  readyState = 0;
  sent: string[] = [];
  closed = 0;
  onopen: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(public url: string) {}

  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  deliver(data: unknown) {
    this.onmessage?.({ data: typeof data === 'string' ? data : JSON.stringify(data) } as MessageEvent);
  }
  close() {
    this.closed += 1;
    this.readyState = 3;
    this.onclose?.();
  }
  send(payload: string) {
    this.sent.push(payload);
  }
}

/** Lets the async close path (which may await an auth probe) settle. */
async function flush() {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

interface HarnessOptions {
  /** What the auth probe answers when a handshake never opened. */
  authStatus?: number;
  hidden?: boolean;
  /** Jitter draw. 1 = the full computed delay, which keeps assertions readable. */
  random?: number;
}

/** Builds a client whose transport, clock, jitter and page state the test owns. */
function harness(options: HarnessOptions = {}) {
  const opened: FakeSocket[] = [];
  const timers = new Map<number, { fn: () => void; ms: number }>();
  let nextTimer = 1;
  let hidden = options.hidden ?? false;
  let authStatus = options.authStatus ?? 200;
  const wakeListeners = new Set<() => void>();

  const deps: RealtimeDeps = {
    open: (url) => {
      const s = new FakeSocket(url);
      opened.push(s);
      return s as unknown as WebSocket;
    },
    origin: () => 'https://psycho-space.ru',
    setTimeout: (fn, ms) => {
      const id = nextTimer++;
      timers.set(id, { fn, ms });
      return id;
    },
    clearTimeout: (id) => {
      timers.delete(id);
    },
    random: () => options.random ?? 1,
    hidden: () => hidden,
    probeAuth: async () => authStatus,
    onWake: (fn) => {
      wakeListeners.add(fn);
      return () => wakeListeners.delete(fn);
    },
  };

  return {
    client: createRealtimeClient('yard', deps),
    opened,
    /** Fires every pending timer, standing in for time passing. */
    runTimers() {
      const pending = [...timers.values()];
      timers.clear();
      for (const t of pending) t.fn();
    },
    pendingTimers: () => timers.size,
    /** The delays currently scheduled, so a test can assert the backoff. */
    delays: () => [...timers.values()].map((t) => t.ms),
    setHidden(value: boolean) {
      hidden = value;
      for (const fn of wakeListeners) fn();
    },
    setAuthStatus(value: number) {
      authStatus = value;
    },
  };
}

describe('realtimeURL', () => {
  it('upgrades the page scheme rather than assuming one', () => {
    expect(realtimeURL('https://psycho-space.ru', 'yard')).toBe(
      'wss://psycho-space.ru/api/realtime?room=yard',
    );
    // The full-stack e2e harness runs over plain http on loopback, so ws: has
    // to come out of the same code path rather than being a special case.
    expect(realtimeURL('http://127.0.0.1:8081', 'yard')).toBe(
      'ws://127.0.0.1:8081/api/realtime?room=yard',
    );
  });
});

describe('parseFrame', () => {
  it('accepts an object carrying a string discriminator', () => {
    expect(parseFrame('{"t":"vanyagotchi_roster","peers":[]}')).toEqual({
      t: 'vanyagotchi_roster',
      peers: [],
    });
  });

  it.each([
    ['not json', 'nonsense'],
    ['an array', '[1,2,3]'],
    ['a bare string', '"hello"'],
    ['null', 'null'],
    ['an object with no t', '{"peers":[]}'],
    ['a non-string t', '{"t":7}'],
  ])('returns null for %s rather than throwing', (_name, payload) => {
    expect(parseFrame(payload)).toBeNull();
  });

  it('ignores a binary payload', () => {
    // The server only ever sends text, so anything else is a bug or an attack;
    // either way it must not reach a listener.
    expect(parseFrame(new ArrayBuffer(4))).toBeNull();
  });
});

describe('byeDetail', () => {
  it('reads the code and reason the transport close cannot carry', () => {
    expect(byeDetail({ t: 'bye', code: 4001, reason: 'unauthorized' })).toEqual({
      code: 4001,
      reason: 'unauthorized',
    });
  });

  it('is not confused by a game frame', () => {
    expect(byeDetail({ t: 'vanyagotchi_roster', peers: [] })).toBeNull();
  });

  it('tolerates a bye with fields missing', () => {
    expect(byeDetail({ t: 'bye' })).toEqual({ code: undefined, reason: undefined });
  });
});

describe('the realtime client', () => {
  it('opens one socket however many subscribers there are', () => {
    const h = harness();
    const a = h.client.subscribe({ frames: vi.fn() });
    const b = h.client.subscribe({ frames: vi.fn() });

    expect(h.opened).toHaveLength(1);
    expect(h.client.subscriberCount()).toBe(2);
    a();
    b();
  });

  it('delivers a frame to every subscriber', () => {
    const h = harness();
    const first = vi.fn();
    const second = vi.fn();
    h.client.subscribe({ frames: first });
    h.client.subscribe({ frames: second });
    h.opened[0].open();

    h.opened[0].deliver({ t: 'vanyagotchi_roster', peers: [{ id: 'a', x: 0.5, y: 0.5 }] });

    expect(first).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenCalledTimes(1);
    expect(first.mock.calls[0][0]).toMatchObject({ t: 'vanyagotchi_roster' });
  });

  it('swallows a malformed frame instead of breaking the stream', () => {
    const h = harness();
    const frames = vi.fn();
    h.client.subscribe({ frames });
    h.opened[0].open();

    h.opened[0].deliver('not json at all');
    h.opened[0].deliver({ t: 'vanyagotchi_roster', peers: [] });

    expect(frames).toHaveBeenCalledTimes(1);
  });

  it('keeps a bye frame to itself and reports it as the close reason', () => {
    const h = harness();
    const frames = vi.fn();
    const status = vi.fn();
    h.client.subscribe({ frames, status });
    h.opened[0].open();

    h.opened[0].deliver({ t: 'bye', code: 1001, reason: 'restart' });
    // A bye is transport business, not a game frame.
    expect(frames).not.toHaveBeenCalled();

    h.opened[0].close();
    expect(h.client.getLastClose()).toEqual({ code: 1001, reason: 'restart' });
    expect(status).toHaveBeenLastCalledWith('closed', { code: 1001, reason: 'restart' });
  });

  it('survives the route churn the module-level singleton exists for', () => {
    // yard → wishlist → yard. The socket must be the same one throughout, or
    // every navigation costs a handshake and a slot against the per-account cap.
    const h = harness();
    const leave = h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();

    leave();
    expect(h.opened[0].closed).toBe(0); // still inside the grace period
    expect(h.pendingTimers()).toBe(1);

    h.client.subscribe({ frames: vi.fn() });
    expect(h.pendingTimers()).toBe(0); // the pending close was cancelled
    expect(h.opened).toHaveLength(1);
    expect(h.client.getStatus()).toBe('open');
  });

  it('closes once the grace period passes with nobody holding it', () => {
    const h = harness();
    const leave = h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();

    leave();
    h.runTimers();

    expect(h.opened[0].closed).toBe(1);
    expect(h.client.getStatus()).toBe('idle');
  });

  it('does not close when somebody else is still subscribed', () => {
    const h = harness();
    const leave = h.client.subscribe({ frames: vi.fn() });
    h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();

    leave();
    h.runTimers();

    expect(h.opened[0].closed).toBe(0);
  });

  it('ignores a repeated unsubscribe', () => {
    // Vue can invoke a cleanup more than once; a second call must not schedule
    // a close that a still-present subscriber would be caught by.
    const h = harness();
    const leave = h.client.subscribe({ frames: vi.fn() });
    h.client.subscribe({ frames: vi.fn() });

    leave();
    leave();

    expect(h.client.subscriberCount()).toBe(1);
    h.runTimers();
    expect(h.opened[0].closed).toBe(0);
  });

  it('abandons a socket that opens after everyone has gone', () => {
    // Unsubscribing mid-handshake: the socket is already in flight and will
    // still fire onopen. Adopting it would leak a connection against the cap.
    const h = harness();
    const leave = h.client.subscribe({ frames: vi.fn() });
    leave();
    h.runTimers();

    h.opened[0].open();

    expect(h.opened[0].closed).toBeGreaterThan(0);
    expect(h.client.getStatus()).toBe('idle');
  });

  it('sends only while the socket is open, and never queues', () => {
    const h = harness();
    h.client.subscribe({ frames: vi.fn() });

    // Still handshaking: the move is dropped, and reported as dropped.
    expect(h.client.send({ t: 'vanyagotchi_move', x: 0.5, y: 0.5 })).toBe(false);
    expect(h.opened[0].sent).toHaveLength(0);

    h.opened[0].open();
    expect(h.client.send({ t: 'vanyagotchi_move', x: 0.25, y: 0.75 })).toBe(true);
    expect(JSON.parse(h.opened[0].sent[0])).toEqual({
      t: 'vanyagotchi_move',
      x: 0.25,
      y: 0.75,
    });
  });

  it('tells a late subscriber the state it is joining', () => {
    const h = harness();
    h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();

    const status = vi.fn();
    h.client.subscribe({ frames: vi.fn(), status });

    // Otherwise a screen mounted after the socket came up would sit showing
    // "connecting" until something else happened to change.
    expect(status).toHaveBeenCalledWith('open', undefined);
  });

  it('settles the state when the socket errors without closing', () => {
    const h = harness();
    const status = vi.fn();
    h.client.subscribe({ frames: vi.fn(), status });
    h.opened[0].open();

    h.opened[0].onerror?.();

    expect(h.client.getStatus()).toBe('closed');
  });

  it('holds the socket for the documented grace period', () => {
    // Guards the constant itself: long enough to cover a glance at another
    // section, short enough not to hold one of the three per-account slots.
    expect(IDLE_GRACE_MS).toBeGreaterThanOrEqual(5_000);
    expect(IDLE_GRACE_MS).toBeLessThanOrEqual(30_000);
  });
});

describe('reconnecting', () => {
  // The service restarts several times a day, so losing the socket is the
  // normal case rather than an error path. These pin the policy end to end,
  // through the client rather than only through the pure helpers.

  it('comes back after an ordinary drop', async () => {
    const h = harness();
    h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();

    h.opened[0].close();
    await flush();

    expect(h.delays()).toEqual([BASE_DELAY_MS.normal]);
    h.runTimers();
    expect(h.opened).toHaveLength(2);
  });

  it('comes back sooner when the server said it was restarting', async () => {
    const h = harness();
    h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();

    h.opened[0].deliver({ t: 'bye', code: 1001, reason: 'restart' });
    h.opened[0].close();
    await flush();

    expect(h.delays()).toEqual([BASE_DELAY_MS.prompt]);
    expect(BASE_DELAY_MS.prompt).toBeLessThan(BASE_DELAY_MS.normal);
  });

  it('backs off harder when the server asked it to go away', async () => {
    const h = harness();
    h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();

    h.opened[0].deliver({ t: 'bye', code: 1013, reason: 'too many connections' });
    h.opened[0].close();
    await flush();

    expect(h.delays()).toEqual([BASE_DELAY_MS.slow]);
  });

  it('stops for good when the session was revoked', async () => {
    // The whole point of close code 4001. Reconnecting here would be a blocked
    // account's browser hammering the handshake until the tab is closed.
    const h = harness();
    const status = vi.fn();
    h.client.subscribe({ frames: vi.fn(), status });
    h.opened[0].open();

    h.opened[0].deliver({ t: 'bye', code: 4001, reason: 'unauthorized' });
    h.opened[0].close();
    await flush();

    expect(h.client.getStatus()).toBe('terminal');
    expect(h.pendingTimers()).toBe(0);
    h.runTimers();
    expect(h.opened).toHaveLength(1);
    expect(status).toHaveBeenLastCalledWith('terminal', { code: 4001, reason: 'unauthorized' });
  });

  it('stops for good when a handshake is refused for auth', async () => {
    // A browser CANNOT see why a handshake failed: a 401 arrives as an error
    // and a 1006 close, indistinguishable from the Wi-Fi dropping. Asking the
    // REST API — which shares the cookie — is the only way to tell.
    const h = harness({ authStatus: 401 });
    h.client.subscribe({ frames: vi.fn() });

    h.opened[0].close(); // never opened
    await flush();

    expect(h.client.getStatus()).toBe('terminal');
    expect(h.pendingTimers()).toBe(0);
  });

  it('keeps trying when a failed handshake was not an auth problem', async () => {
    // The server was restarting, so the handshake failed but the session is
    // fine. Treating this as terminal would strand every player on every deploy.
    const h = harness({ authStatus: 200 });
    h.client.subscribe({ frames: vi.fn() });

    h.opened[0].close();
    await flush();

    expect(h.client.getStatus()).toBe('closed');
    expect(h.delays()).toEqual([BASE_DELAY_MS.normal]);
  });

  it('keeps trying when the probe itself cannot answer', async () => {
    // Offline: the probe rejects. "I could not ask" is not "you are logged out",
    // and treating it as terminal would make a tunnel make the app unusable.
    const h = harness();
    h.setAuthStatus(0);
    h.client.subscribe({ frames: vi.fn() });

    h.opened[0].close();
    await flush();

    expect(h.client.getStatus()).toBe('closed');
    expect(h.pendingTimers()).toBe(1);
  });

  it('grows the delay across consecutive failures and forgives it on success', async () => {
    const h = harness();
    h.client.subscribe({ frames: vi.fn() });

    h.opened[0].open();
    h.opened[0].close();
    await flush();
    expect(h.delays()).toEqual([BASE_DELAY_MS.normal]);

    h.runTimers();
    h.opened[1].close(); // this one never opened; the probe says 200
    await flush();
    expect(h.delays()).toEqual([BASE_DELAY_MS.normal * 2]);

    h.runTimers();
    h.opened[2].open(); // a good connection
    expect(h.client.getAttempt()).toBe(0);

    h.opened[2].close();
    await flush();
    expect(h.delays()).toEqual([BASE_DELAY_MS.normal]);
  });

  it('does not reconnect while the tab is hidden', async () => {
    // Reconnecting into a backgrounded tab spends a connection slot on a page
    // nobody is looking at, and iOS is likely to kill it again immediately.
    const h = harness({ hidden: true });
    h.client.subscribe({ frames: vi.fn() });

    h.opened[0].close();
    await flush();

    expect(h.pendingTimers()).toBe(0);
    expect(h.opened).toHaveLength(1);
  });

  it('reconnects at once when the tab comes back, without serving out a backoff', async () => {
    const h = harness();
    h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();
    h.opened[0].close();
    await flush();

    h.setHidden(true); // a pending retry now belongs to a tab nobody can see
    h.setHidden(false);

    // Straight back, and the attempt counter reset: the delay protects the
    // server from a herd, it is not a punishment for switching tabs.
    expect(h.opened).toHaveLength(2);
    expect(h.client.getAttempt()).toBe(0);
  });

  it('closes a socket that has been hidden too long', () => {
    const h = harness();
    h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();

    h.setHidden(true);
    expect(h.delays()).toEqual([HIDDEN_CLOSE_MS]);
    h.runTimers();

    expect(h.opened[0].closed).toBe(1);
    expect(h.client.getStatus()).toBe('closed');
  });

  it('does not reconnect once nobody is watching', async () => {
    const h = harness();
    const leave = h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();

    leave();
    h.runTimers(); // grace period elapses, socket closes
    await flush();

    expect(h.client.getStatus()).toBe('idle');
    expect(h.opened).toHaveLength(1);
  });

  it('tells a subscriber that arrives after a revocation that it is terminal', async () => {
    // Otherwise a screen mounted after the fact would show "connecting" forever.
    const h = harness();
    h.client.subscribe({ frames: vi.fn() });
    h.opened[0].open();
    h.opened[0].deliver({ t: 'bye', code: 4001, reason: 'unauthorized' });
    h.opened[0].close();
    await flush();

    const status = vi.fn();
    h.client.subscribe({ frames: vi.fn(), status });
    expect(status).toHaveBeenCalledWith('terminal', { code: 4001, reason: 'unauthorized' });
    expect(h.opened).toHaveLength(1); // and it did NOT try to open another
  });
});
