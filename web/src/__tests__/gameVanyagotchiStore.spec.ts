import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it } from 'vitest';
import { sameIds, useGameVanyagotchiStore } from '../stores/gameVanyagotchi';

describe('sameIds', () => {
  it('ignores order', () => {
    // The server builds the roster by ranging over a Go map, so the order is
    // arbitrary and differs between frames describing an identical world.
    expect(sameIds(['a', 'b', 'c'], ['c', 'a', 'b'])).toBe(true);
  });

  it('notices a join, a leave, and a swap', () => {
    expect(sameIds(['a'], ['a', 'b'])).toBe(false);
    expect(sameIds(['a', 'b'], ['a'])).toBe(false);
    expect(sameIds(['a', 'b'], ['a', 'c'])).toBe(false);
  });

  it('treats two empty planes as the same', () => {
    expect(sameIds([], [])).toBe(true);
  });
});

describe('the «Ванягоччи» store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it('does not re-assign the roster when membership is unchanged', () => {
    // This is the whole point of the two-tier split. Frames arrive five times a
    // second with the same people in them; re-assigning would re-key the v-for
    // on every one of them.
    const store = useGameVanyagotchiStore();
    expect(store.applyRoster(['a', 'b'])).toBe(true);
    const first = store.peerIds;

    expect(store.applyRoster(['b', 'a'])).toBe(false);
    expect(store.peerIds).toBe(first); // the identical array, not an equal one
  });

  it('re-assigns when somebody joins or leaves', () => {
    const store = useGameVanyagotchiStore();
    store.applyRoster(['a']);
    const before = store.peerIds;

    expect(store.applyRoster(['a', 'b'])).toBe(true);
    expect(store.peerIds).not.toBe(before);
    expect([...store.peerIds].sort()).toEqual(['a', 'b']);
  });

  it('holds the roster frozen, so nothing can mutate it in place', () => {
    const store = useGameVanyagotchiStore();
    store.applyRoster(['a']);
    expect(Object.isFrozen(store.peerIds)).toBe(true);
  });

  it('empties the plane without churning an already-empty one', () => {
    const store = useGameVanyagotchiStore();
    store.applyRoster(['a']);
    store.clearRoster();
    expect(store.peerIds).toEqual([]);

    const empty = store.peerIds;
    store.clearRoster();
    expect(store.peerIds).toBe(empty);
  });

  it('remembers why the socket closed, and forgets it on the way back up', () => {
    const store = useGameVanyagotchiStore();
    store.setStatus('closed', { code: 4001, reason: 'unauthorized' });
    expect(store.status).toBe('closed');
    expect(store.closeDetail).toEqual({ code: 4001, reason: 'unauthorized' });

    // A stale reason shown next to a live connection would be worse than none.
    store.setStatus('connecting');
    expect(store.closeDetail).toBeUndefined();
  });
});

describe('the "you" marker', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it('records the pseudonym the server answered with', () => {
    const store = useGameVanyagotchiStore();
    store.setStatus('open');
    store.setYou('abc123');
    expect(store.youId).toBe('abc123');
  });

  it('forgets it the moment the socket is not open', () => {
    // The pseudonym is derived per connection lifetime, so carrying it across a
    // reconnect would mark the wrong dot as "you" — or mark nobody, leaving the
    // player unable to find themselves on a crowded plane.
    const store = useGameVanyagotchiStore();
    store.setStatus('open');
    store.setYou('abc123');

    store.setStatus('closed', { code: 1001 });
    expect(store.youId).toBeUndefined();
  });

  it('is gone on a terminal close too, and keeps the reason', () => {
    const store = useGameVanyagotchiStore();
    store.setStatus('open');
    store.setYou('abc123');

    store.setStatus('terminal', { code: 4001, reason: 'unauthorized' });
    expect(store.youId).toBeUndefined();
    expect(store.closeDetail).toEqual({ code: 4001, reason: 'unauthorized' });
  });
});
