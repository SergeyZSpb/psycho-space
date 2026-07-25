import { defineStore } from 'pinia';
import { ref, shallowRef } from 'vue';
import type { CloseDetail, ConnectionStatus } from '../realtime/socket';

// Discrete state for «Ванягоччи», and ONLY discrete state.
//
// The split this file exists to enforce: *who is on the plane* is reactive and
// lives here; *where they are* is not, and never enters Vue at all. A roster
// frame arrives five times a second, so binding positions to reactivity would
// turn every frame into a scheduler pass and a vdom patch per entity — around a
// thousand patches a second at twenty entities, all of it to produce a
// `transform` the compositor could have been handed directly. The view keeps
// positions in a plain Map and writes them to CSS custom properties itself.
//
// Membership, by contrast, changes a few times a minute, so a keyed `v-for` over
// it is exactly the right tool — provided it is only re-assigned when the set
// really changed, which is what sameIds below is for.

/**
 * Do these two id lists describe the same set of peers?
 *
 * Order-insensitive on purpose: the server builds the roster by iterating a Go
 * map, so the order is arbitrary and differs between frames that describe an
 * identical world. Comparing order-sensitively would report a membership change
 * five times a second and defeat the whole point of the split.
 */
export function sameIds(a: readonly string[], b: readonly string[]): boolean {
  if (a.length !== b.length) return false;
  const seen = new Set(a);
  for (const id of b) {
    if (!seen.has(id)) return false;
  }
  return true;
}

export const useGameVanyagotchiStore = defineStore('gameVanyagotchi', () => {
  /** What the socket is doing. Drives the status line, nothing else. */
  const status = ref<ConnectionStatus>('idle');

  /**
   * Why the socket last went away, when the server said so — it arrives as a
   * `bye` frame rather than a close code. Recorded so the screen can tell a
   * planned restart from a revoked session instead of showing one grey message
   * for every kind of disconnect.
   */
  const closeDetail = ref<CloseDetail | undefined>(undefined);

  /**
   * Who is on the plane. A shallowRef holding a frozen array: the contents are
   * never mutated, the whole array is replaced when membership changes, and
   * nothing deeper than the array itself is tracked.
   */
  const peerIds = shallowRef<readonly string[]>([]);

  /**
   * Which entity is this player's own Ваня.
   *
   * It has to be told to us: the id on the wire is a per-account pseudonym the
   * server derives, deliberately not the account id, so nothing the client
   * already knows can be compared against it. The server unicasts it in reply
   * to a hello, and it changes on every reconnect — the derivation key lives and
   * dies with the server process, exactly like presence itself.
   */
  const youId = ref<string | undefined>(undefined);

  function setStatus(next: ConnectionStatus, detail?: CloseDetail) {
    status.value = next;
    if (next === 'closed' || next === 'terminal') closeDetail.value = detail;
    if (next === 'connecting' || next === 'open') closeDetail.value = undefined;
    // A pseudonym belongs to one connection's lifetime. Keeping it across a
    // reconnect would mark the wrong dot as "you" — or, worse, mark nobody and
    // leave the player unable to find themselves.
    if (next !== 'open') youId.value = undefined;
  }

  /** Records the server's answer to our hello. */
  function setYou(id: string) {
    youId.value = id;
  }

  /**
   * Takes the ids from a roster frame. Assigns only on a real change, so the
   * keyed list re-renders when somebody joins or leaves and not otherwise.
   * Returns whether it changed, which the tests assert on directly.
   */
  function applyRoster(ids: readonly string[]): boolean {
    if (sameIds(peerIds.value, ids)) return false;
    peerIds.value = Object.freeze([...ids]);
    return true;
  }

  /** Forgets everybody. Used when the socket goes down, so the plane empties. */
  function clearRoster() {
    if (peerIds.value.length === 0) return;
    peerIds.value = Object.freeze([]);
  }

  return { status, closeDetail, peerIds, youId, setStatus, setYou, applyRoster, clearRoster };
});
