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
// yard → wishlist → yard reuses the socket it already had. Only a real absence
// closes it.
//
// What deliberately is NOT here yet: reconnect. Backoff, the deliberate close
// after a spell hidden, resync on visibilitychange/pageshow, and treating an
// HTTP 401/403 at the handshake as terminal are the next iteration's work. This
// one reports its state honestly and stops; the state machine below is the seam
// that work hooks into.

/** What the connection is doing, as far as anything outside here needs to know. */
export type ConnectionStatus = 'idle' | 'connecting' | 'open' | 'closed';

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
 * it. See the server's docs/ARCHITECTURE.md ADR-018. `code` here is therefore
 * the one from that frame, and it is absent when the socket simply dropped.
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

  function setStatus(next: ConnectionStatus, detail?: CloseDetail) {
    status = next;
    for (const sub of subscribers) sub.status?.(next, detail);
  }

  function cancelIdleClose() {
    if (idleTimer !== undefined) {
      deps.clearTimeout(idleTimer);
      idleTimer = undefined;
    }
  }

  function connect() {
    if (socket) return;
    lastClose = undefined;
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
      setStatus('open');
    };

    ws.onmessage = (event: MessageEvent) => {
      if (socket !== ws) return;
      const frame = parseFrame(event.data);
      if (!frame) return;
      // The server's last word before it drops the socket. Recorded, not acted
      // on: the close itself follows immediately, and what to do about each code
      // is the reconnect iteration's business.
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
      setStatus('closed', lastClose);
    };
    ws.onclose = finish;
    // An error is always followed by a close, so this exists only to make sure
    // a browser that reports one without the other still settles the state.
    ws.onerror = finish;
  }

  function disconnect() {
    cancelIdleClose();
    const ws = socket;
    socket = null;
    if (ws) {
      // Detached first, so the handlers above see socket !== ws and go quiet.
      ws.close();
    }
    setStatus('idle');
  }

  /**
   * Registers interest in this room, opening the socket if nothing else already
   * holds it. The returned function releases that interest; the socket closes
   * only once nothing holds it and the grace period has passed.
   */
  function subscribe(sub: Subscription): () => void {
    subscribers.add(sub);
    cancelIdleClose();
    connect();
    sub.status?.(status, status === 'closed' ? lastClose : undefined);

    let released = false;
    return () => {
      if (released) return;
      released = true;
      subscribers.delete(sub);
      if (subscribers.size > 0) return;
      cancelIdleClose();
      idleTimer = deps.setTimeout(() => {
        idleTimer = undefined;
        if (subscribers.size === 0) disconnect();
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
    });
  }
  return shared;
}
