// The realtime client: one WebSocket for the whole app, owned at module scope
// rather than by a component.
//
// It lives here, outside Vue, for one concrete reason. The yard is a lazy child
// route, so a component-owned socket would be torn down and rebuilt on every
// navigation away and back — a full handshake, a fresh registration against the
// hub's per-account connection cap, and a visible gap in the world. Ownership by
// the module outlives the route.
//
// Connection lifetime follows a SUBSCRIPTION REFCOUNT with a grace period, not
// the route: the last unsubscribe starts a timer rather than closing, so
// yard → wishlist → yard reuses the socket it already had.
//
// It also survives a deploy. The service restarts several times a day, and every
// socket dies at the same instant; reconnecting is therefore the normal case
// rather than an error path, and the policy for it lives in ./backoff.ts.

import {
  CLOSE_UNAUTHORIZED,
  HIDDEN_CLOSE_MS,
  policyForClose,
  reconnectDelay,
  type ReconnectPolicy,
} from './backoff';

/**
 * What the connection is doing.
 *
 * `terminal` is the one that never recovers on its own: the session is gone, so
 * every future handshake would be refused and retrying would mean a blocked
 * account's browser hammering the server for as long as the tab stays open.
 */
export type ConnectionStatus = 'idle' | 'connecting' | 'open' | 'closed' | 'terminal';

/** Every frame is a JSON object carrying a "t" discriminator. */
export interface RealtimeFrame {
  t: string;
  [key: string]: unknown;
}

/**
 * Why the socket went away, when the server managed to say.
 *
 * The reason arrives as an ordinary `bye` FRAME, not as a WebSocket close code:
 * a browser reports 1006 for every disconnect, so `CloseEvent.code` cannot carry
 * it. See the server's docs/ARCHITECTURE.md ADR-018.
 */
export interface CloseDetail {
  /** 1001 planned restart · 1013 evicted or over a cap · 4001 session revoked. */
  code?: number;
  reason?: string;
}

export type FrameListener = (frame: RealtimeFrame) => void;
export type StatusListener = (status: ConnectionStatus, detail?: CloseDetail) => void;

/** The transport-level `bye` frame's discriminator. */
export const TYPE_BYE = 'bye';

/**
 * How long the socket is kept alive after the last subscriber leaves.
 *
 * Long enough to cover a glance at another section and back, short enough that
 * a tab left on the wishlist is not holding one of the three connections the
 * server allows per account.
 */
export const IDLE_GRACE_MS = 10_000;

/** Builds the socket URL for a room from an http(s) page origin. */
export function realtimeURL(origin: string, room: string): string {
  const url = new URL('/api/realtime', origin);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.searchParams.set('room', room);
  return url.toString();
}

/**
 * Reads one inbound message.
 *
 * Returns null for anything that is not a JSON object with a string `t`, and
 * never throws: the socket carries frames from a server that may be a version
 * ahead of this client, and a frame this build does not understand must be
 * ignored rather than allowed to break the stream.
 */
export function parseFrame(data: unknown): RealtimeFrame | null {
  if (typeof data !== 'string') return null;
  let value: unknown;
  try {
    value = JSON.parse(data);
  } catch {
    return null;
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return null;
  const t = (value as Record<string, unknown>).t;
  if (typeof t !== 'string') return null;
  return value as RealtimeFrame;
}

/** Reads a `bye` frame's payload, if that is what this frame is. */
export function byeDetail(frame: RealtimeFrame): CloseDetail | null {
  if (frame.t !== TYPE_BYE) return null;
  const code = typeof frame.code === 'number' ? frame.code : undefined;
  const reason = typeof frame.reason === 'string' ? frame.reason : undefined;
  return { code, reason };
}

/** The bits of the environment the client needs, so a test can supply its own. */
export interface RealtimeDeps {
  /** Opens a socket. Defaults to the browser's own WebSocket. */
  open: (url: string) => WebSocket;
  /** The page origin the socket URL is derived from. */
  origin: () => string;
  setTimeout: (fn: () => void, ms: number) => number;
  clearTimeout: (handle: number) => void;
  /** Jitter source. Injected so a test can pin the draw. */
  random: () => number;
  /** Is the page currently hidden? */
  hidden: () => boolean;
  /**
   * Asks the HTTP API whether we are still allowed in, returning the status.
   *
   * This exists because **a browser cannot see why a handshake failed.** A
   * WebSocket that is refused with 401 or 403 fires `error` and then `close`
   * with 1006 and no status attached — indistinguishable from the Wi-Fi
   * dropping. The session cookie is the same credential the REST API uses, so
   * asking it is the only way to tell "you are logged out" from "try again".
   */
  probeAuth: () => Promise<number>;
  /** Subscribes to visibility/bfcache events. Returns an unsubscribe. */
  onWake: (fn: () => void) => () => void;
}

interface Subscription {
  frames: FrameListener;
  status?: StatusListener;
}

/**
 * One room's connection.
 *
 * Exported as a factory rather than only as a singleton so a test can build its
 * own instance with its own deps. There is no setter on the shipped instance to
 * swap its transport — a test hook on a production path is forbidden here, and
 * a second constructor call costs nothing.
 */
export function createRealtimeClient(room: string, deps: RealtimeDeps) {
  const subscribers = new Set<Subscription>();
  let socket: WebSocket | null = null;
  let status: ConnectionStatus = 'idle';
  let lastClose: CloseDetail | undefined;
  let idleTimer: number | undefined;
  let retryTimer: number | undefined;
  let hiddenTimer: number | undefined;
  let attempt = 0;
  let everOpened = false;
  let stopWake: (() => void) | undefined;

  function setStatus(next: ConnectionStatus, detail?: CloseDetail) {
    status = next;
    for (const sub of subscribers) sub.status?.(next, detail);
  }

  function clearTimer(handle: number | undefined) {
    if (handle !== undefined) deps.clearTimeout(handle);
  }

  function cancelIdleClose() {
    clearTimer(idleTimer);
    idleTimer = undefined;
  }

  function cancelRetry() {
    clearTimer(retryTimer);
    retryTimer = undefined;
  }

  function cancelHiddenClose() {
    clearTimer(hiddenTimer);
    hiddenTimer = undefined;
  }

  function connect() {
    if (socket || status === 'terminal') return;
    cancelRetry();
    everOpened = false;
    setStatus('connecting');
    const ws = deps.open(realtimeURL(deps.origin(), room));
    socket = ws;

    ws.onopen = () => {
      // A late open for a socket we have already abandoned: close it rather
      // than adopt it, or an unsubscribe during the handshake leaks a
      // connection against the server's per-account cap.
      if (socket !== ws) {
        ws.close();
        return;
      }
      everOpened = true;
      attempt = 0; // a good connection forgives the whole backoff history
      lastClose = undefined;
      setStatus('open');
    };

    ws.onmessage = (event: MessageEvent) => {
      if (socket !== ws) return;
      const frame = parseFrame(event.data);
      if (!frame) return;
      // The server's last word before it drops the socket. Recorded here and
      // acted on in the close handler, because the close always follows.
      const bye = byeDetail(frame);
      if (bye) {
        lastClose = bye;
        return;
      }
      for (const sub of subscribers) sub.frames(frame);
    };

    const finish = () => {
      if (socket !== ws) return;
      socket = null;
      const detail = lastClose;
      setStatus('closed', detail);
      void afterClose(detail, everOpened);
    };
    ws.onclose = finish;
    // An error is always followed by a close, so this exists only to make sure
    // a browser that reports one without the other still settles the state.
    ws.onerror = finish;
  }

  /** Decides what happens next, once the socket is definitely gone. */
  async function afterClose(detail: CloseDetail | undefined, hadOpened: boolean) {
    if (subscribers.size === 0) return; // nobody is watching; stay down

    let policy: ReconnectPolicy = policyForClose(detail?.code);

    // A handshake that never opened may have been refused for auth, and the
    // WebSocket API will not say. Ask the REST API, which shares the cookie.
    if (policy !== 'terminal' && !hadOpened) {
      const code = await deps.probeAuth().catch(() => 0);
      if (code === 401 || code === 403) {
        policy = 'terminal';
        detail = { code: CLOSE_UNAUTHORIZED, reason: 'unauthorized' };
      }
    }

    if (policy === 'terminal') {
      setStatus('terminal', detail);
      return;
    }
    if (subscribers.size === 0 || deps.hidden()) return;

    const delay = reconnectDelay(policy, attempt, deps.random);
    attempt += 1;
    retryTimer = deps.setTimeout(() => {
      retryTimer = undefined;
      if (subscribers.size > 0 && !deps.hidden()) connect();
    }, delay);
  }

  function disconnect(next: ConnectionStatus = 'idle') {
    cancelIdleClose();
    cancelRetry();
    const ws = socket;
    socket = null;
    if (ws) {
      // Detached first, so the handlers above see socket !== ws and go quiet.
      ws.close();
    }
    if (status !== 'terminal') setStatus(next);
  }

  /**
   * Reacts to the page being hidden or coming back.
   *
   * iOS kills a backgrounded socket regardless of what we do, so a tab left
   * hidden is holding a connection that is probably already dead from the
   * server's point of view. Closing it deliberately after a spell makes the
   * return cheap and predictable instead of leaving a zombie to be discovered.
   */
  function handleWake() {
    if (status === 'terminal' || subscribers.size === 0) return;
    if (deps.hidden()) {
      cancelHiddenClose();
      hiddenTimer = deps.setTimeout(() => {
        hiddenTimer = undefined;
        if (deps.hidden() && subscribers.size > 0) disconnect('closed');
      }, HIDDEN_CLOSE_MS);
      return;
    }
    cancelHiddenClose();
    // Back on screen. Reconnect at once rather than serving out a backoff that
    // was counting down while nobody was looking, and reset the attempt count:
    // the delay is meant to protect the server from a herd, not to punish
    // somebody for switching tabs.
    if (!socket) {
      attempt = 0;
      connect();
    }
  }

  /**
   * Registers interest in this room, opening the socket if nothing else already
   * holds it. The returned function releases that interest; the socket closes
   * only once nothing holds it and the grace period has passed.
   */
  function subscribe(sub: Subscription): () => void {
    subscribers.add(sub);
    cancelIdleClose();
    if (!stopWake) stopWake = deps.onWake(handleWake);
    connect();
    sub.status?.(status, status === 'closed' || status === 'terminal' ? lastClose : undefined);

    let released = false;
    return () => {
      if (released) return;
      released = true;
      subscribers.delete(sub);
      if (subscribers.size > 0) return;
      cancelIdleClose();
      cancelRetry();
      idleTimer = deps.setTimeout(() => {
        idleTimer = undefined;
        if (subscribers.size === 0) {
          cancelHiddenClose();
          stopWake?.();
          stopWake = undefined;
          disconnect();
        }
      }, IDLE_GRACE_MS);
    };
  }

  /**
   * Sends a message. Reports whether it went: an action taken while the socket
   * is down is dropped, deliberately and visibly, rather than queued — the
   * server owns the world, and replaying a stale intention after a reconnect
   * would move somebody somewhere they asked to go a minute ago.
   */
  function send(payload: Record<string, unknown>): boolean {
    if (!socket || socket.readyState !== WebSocket.OPEN) return false;
    socket.send(JSON.stringify(payload));
    return true;
  }

  return {
    subscribe,
    send,
    /** Current connection state. */
    getStatus: () => status,
    /** Why the socket last went away, if the server said. */
    getLastClose: () => lastClose,
    /** How many reconnects have been attempted since the last good connection. */
    getAttempt: () => attempt,
    /** Closes immediately, ignoring the grace period. For teardown. */
    disconnect,
    /** How many holders the connection has. Exposed for assertions. */
    subscriberCount: () => subscribers.size,
  };
}

export type RealtimeClient = ReturnType<typeof createRealtimeClient>;

/** The room «Ванягоччи» shares. One room per game, never one per location. */
export const YARD_ROOM = 'yard';

let shared: RealtimeClient | undefined;

/**
 * The app's one client, built lazily so that merely importing this module does
 * not touch `window` — which matters for the unit tests and for any future
 * server-side render.
 */
export function realtimeClient(): RealtimeClient {
  if (!shared) {
    shared = createRealtimeClient(YARD_ROOM, {
      open: (url) => new WebSocket(url),
      origin: () => window.location.origin,
      setTimeout: (fn, ms) => window.setTimeout(fn, ms),
      clearTimeout: (handle) => window.clearTimeout(handle),
      random: () => Math.random(),
      hidden: () => document.visibilityState === 'hidden',
      probeAuth: async () => {
        try {
          const res = await fetch('/api/auth/me', { credentials: 'include' });
          return res.status;
        } catch {
          return 0; // offline: not an auth answer, so not terminal
        }
      },
      // Both events, deliberately. A bfcache restore fires `pageshow` and may
      // NOT fire `visibilitychange` — notably on iOS, which is also the platform
      // most likely to have killed the socket while the tab was away.
      onWake: (fn) => {
        document.addEventListener('visibilitychange', fn);
        window.addEventListener('pageshow', fn);
        return () => {
          document.removeEventListener('visibilitychange', fn);
          window.removeEventListener('pageshow', fn);
        };
      },
    });
  }
  return shared;
}
