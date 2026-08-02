import { describe, expect, it } from 'vitest';
import { FLASH_FRAMES, createFlash, createPeerFlashes, createSlopMarks } from '../lib/vanyadumFlash';
import { PEER_DOWN, PEER_FIRED, PEER_HIT, PEER_PROTECTED } from '../lib/vanyadumRoster';

/**
 * «ВАНЯДУМ» — the marks a shot leaves: the flash at the muzzle that fired it,
 * and the blow on whoever it landed on.
 *
 * THE REGRESSION THIS FILE EXISTS FOR. The flash used to be a number of seconds
 * decremented by the render loop's own `dt`, in the same statement that decided
 * whether to draw it — so a frame longer than the flash cleared the mark and
 * then rendered without it. The view clamps `dt` at 0.1, so at twenty frames a
 * second and below the flash was drawn zero times: never seen at all, on exactly
 * the cheap phone that reaches that rate, and on that phone it is also the only
 * zero-latency mark left once `prefers-reduced-motion` has damped the kick and a
 * thumb has pressed the mute.
 *
 * So the loops below are driven at a frame rate rather than in the abstract, and
 * the twenty-hertz one is the one that used to fail.
 */

/**
 * Runs `n` drawn frames over one mark and answers which of them showed it.
 *
 * Deliberately shaped like the renderer: one `frame()` per drawn frame and
 * nothing else, because the bug was in what a loop did per frame rather than in
 * any arithmetic.
 */
function drawFrames(flash: ReturnType<typeof createFlash>, n: number): boolean[] {
  const drawn: boolean[] = [];
  for (let i = 0; i < n; i++) drawn.push(flash.frame());
  return drawn;
}

describe('createFlash', () => {
  it('is drawn in the frame that started it, at twenty frames a second', () => {
    // The regression, at the rate it happened at. A shot is granted by the
    // prediction inside a frame and the same frame renders — so the mark has to
    // survive to that draw whatever the elapsed time was, and there is no
    // elapsed time here at all because the count is of frames.
    const flash = createFlash();
    flash.fire();
    expect(flash.frame()).toBe(true);
  });

  it('lasts exactly FLASH_FRAMES drawn frames, whatever the phone manages', () => {
    // The property a frame count buys: the number of frames the mark appears in
    // is fixed, so a slow phone shows it for longer in wall-clock terms rather
    // than not at all.
    const flash = createFlash();
    flash.fire();
    const drawn = drawFrames(flash, FLASH_FRAMES + 2);
    expect(drawn.slice(0, FLASH_FRAMES).every(Boolean)).toBe(true);
    expect(drawn.slice(FLASH_FRAMES)).toEqual([false, false]);
  });

  it('draws nothing until something fires', () => {
    expect(drawFrames(createFlash(), 4)).toEqual([false, false, false, false]);
  });

  it('restarts rather than extending, so the second barrel is the same flash', () => {
    // The обрез fires twice a cadence apart. Two marks that added would leave
    // the second one on screen for twice as long as the first, which reads as
    // two different events.
    const flash = createFlash();
    flash.fire();
    expect(flash.frame()).toBe(true);
    flash.fire();
    const drawn = drawFrames(flash, FLASH_FRAMES + 1);
    expect(drawn.slice(0, FLASH_FRAMES).every(Boolean)).toBe(true);
    expect(drawn[FLASH_FRAMES]).toBe(false);
  });
});

describe('createPeerFlashes', () => {
  it('flashes once for a state that is set on consecutive frames', () => {
    // THE LEVEL-VERSUS-TRANSITION RULE, and it is the whole reason this is not
    // `if (peer.st === PEER_FIRED) draw()`. The value rides the snapshot for the
    // tick a shot happened, and a tick is three frames at sixty hertz — more
    // while the interpolation buffer is holding its newest frame — so drawing the
    // level would light the peer for as long as that frame was the newest one,
    // and a peer firing every tick would simply glow.
    const flashes = createPeerFlashes();
    const lit: boolean[] = [];
    for (let i = 0; i < 10; i++) lit.push(flashes.frame([{ slot: 1, st: PEER_FIRED }]).fired.has(1));
    expect(lit.slice(0, FLASH_FRAMES).every(Boolean)).toBe(true);
    expect(lit.slice(FLASH_FRAMES).some(Boolean)).toBe(false);
  });

  it('marks a hit once too, however long the wire holds it', () => {
    // Two shooters landing on the same man on consecutive ticks leave `st` at
    // PEER_HIT throughout, so the same rule has to hold for the second kind —
    // and it is the kind that matters most, because a hit is the only thing that
    // tells a shooter he connected.
    const flashes = createPeerFlashes();
    const lit: boolean[] = [];
    for (let i = 0; i < 10; i++) lit.push(flashes.frame([{ slot: 1, st: PEER_HIT }]).hit.has(1));
    expect(lit.slice(0, FLASH_FRAMES).every(Boolean)).toBe(true);
    expect(lit.slice(FLASH_FRAMES).some(Boolean)).toBe(false);
  });

  it('tells the two kinds apart when one follows the other', () => {
    // A man who fires and is shot for it goes 1 → 2 on consecutive ticks. A rule
    // that only asked "did `st` change" would light the muzzle again, because
    // the value moved and the man is not resting — so each kind is compared
    // against its own previous value.
    const flashes = createPeerFlashes();
    const first = flashes.frame([{ slot: 4, st: PEER_FIRED }]);
    expect(first.fired.has(4)).toBe(true);
    expect(first.hit.has(4)).toBe(false);
    const second = flashes.frame([{ slot: 4, st: PEER_HIT }]);
    expect(second.hit.has(4)).toBe(true);
    // The muzzle is still burning off its own three frames, which is correct:
    // he did fire, and that mark has not expired yet.
    expect(second.fired.has(4)).toBe(true);
  });

  it('marks nothing at all for a man who is merely down or protected', () => {
    // The two STATES are not events and are not in here: they last three seconds
    // and two, and are drawn as properties of the figure for the whole of it. Run
    // through a mark that expires after three frames, a corpse would be
    // acknowledged for a twentieth of the time it is lying there.
    const flashes = createPeerFlashes();
    for (let i = 0; i < 8; i++) {
      const marks = flashes.frame([
        { slot: 0, st: PEER_DOWN },
        { slot: 1, st: PEER_PROTECTED },
      ]);
      expect(marks.fired.size).toBe(0);
      expect(marks.hit.size).toBe(0);
    }
  });

  it('flashes again the next time the value arrives', () => {
    // Once it has gone back to nothing, the next one is a new shot and a new
    // mark. Without this the peer would fire once per session.
    const flashes = createPeerFlashes();
    expect(flashes.frame([{ slot: 2, st: PEER_FIRED }]).fired.has(2)).toBe(true);
    for (let i = 0; i < 6; i++) flashes.frame([{ slot: 2 }]);
    expect(flashes.frame([{ slot: 2, st: PEER_FIRED }]).fired.has(2)).toBe(true);
  });

  it('never marks a peer whose frame says nothing happened', () => {
    const flashes = createPeerFlashes();
    for (let i = 0; i < 5; i++) {
      const marks = flashes.frame([{ slot: 0 }, { slot: 1, st: 0 }]);
      expect(marks.fired.size).toBe(0);
      expect(marks.hit.size).toBe(0);
    }
  });

  it('is per slot, so one man firing does not light the man beside him', () => {
    const flashes = createPeerFlashes();
    const marks = flashes.frame([
      { slot: 0, st: PEER_FIRED },
      { slot: 1 },
    ]);
    expect([...marks.fired]).toEqual([0]);
  });

  it('holds an unfinished mark across frames the peer is missing from', () => {
    // A peer drops out of a snapshot by walking two rooms away, and the frames
    // he is absent from draw no figure at all — so there is nothing for his mark
    // to be shown on, and spending it on those frames would mean a shot fired at
    // a doorway was invisible by the time its author was back in view.
    const flashes = createPeerFlashes();
    expect(flashes.frame([{ slot: 3, st: PEER_FIRED }]).fired.has(3)).toBe(true);
    for (let i = 0; i < 4; i++) expect(flashes.frame([]).fired.size).toBe(0);
    expect(flashes.frame([{ slot: 3 }]).fired.has(3)).toBe(true);
  });
});

describe('createSlopMarks', () => {
  const at = (id: number, x: number) => ({ id, x, y: 2, z: 0.5 });

  it('marks the place a нейрослоп stopped being, on the frame it stopped', () => {
    // ABSENCE IS THE WHOLE OF DYING, because the wire carries no health, no
    // state and no death event for one: a слоп is worth exactly one barrel, so
    // it is simply not in the next frame's array. That makes this the only
    // acknowledgement a kill can have, and a kill with none would be
    // indistinguishable from a miss.
    const marks = createSlopMarks();
    expect(marks.frame([at(0, 5)]).gone.size).toBe(0);
    const gone = marks.frame([]).gone;
    expect(gone.get(0)).toEqual({ x: 5, y: 2, z: 0.5 });
  });

  it('marks where it was last seen rather than where it is, because it is not', () => {
    const marks = createSlopMarks();
    marks.frame([at(0, 1)]);
    marks.frame([at(0, 9)]);
    expect(marks.frame([]).gone.get(0)).toEqual({ x: 9, y: 2, z: 0.5 });
  });

  it('lasts a count of frames and then stops, whatever the frame rate is', () => {
    // Counted in DRAWN FRAMES rather than in seconds, for the reason at the top
    // of this file: a mark on a clock can expire inside the very frame that was
    // about to draw it, and a phone managing twenty frames a second is exactly
    // where that happens.
    const marks = createSlopMarks(3);
    marks.frame([at(1, 0)]);
    const shown: boolean[] = [];
    for (let i = 0; i < 5; i++) shown.push(marks.frame([]).gone.has(1));
    expect(shown).toEqual([true, true, true, false, false]);
  });

  it('marks nothing at all while everything is still standing there', () => {
    // The commonest frame in the game, and the one that has to cost nothing.
    const marks = createSlopMarks();
    marks.frame([at(0, 1), at(1, 2)]);
    for (let i = 0; i < 6; i++) {
      expect(marks.frame([at(0, 1 + i), at(1, 2)]).gone.size).toBe(0);
    }
  });

  it('is per creature, so one dying does not mark the one beside it', () => {
    const marks = createSlopMarks();
    marks.frame([at(0, 1), at(1, 7)]);
    const gone = marks.frame([at(1, 7)]).gone;
    expect([...gone.keys()]).toEqual([0]);
  });

  it('marks the transition and not the absence, so an empty building is quiet', () => {
    // The rule this project states for every mark: compare against the previous
    // frame per entity, BEFORE the value being compared to is overwritten. Drawn
    // off "there is no слоп with this id" instead, the mark would be lit on
    // every frame of the eight seconds the building waits for the next one.
    const marks = createSlopMarks(3);
    marks.frame([at(0, 4)]);
    for (let i = 0; i < 3; i++) expect(marks.frame([]).gone.has(0)).toBe(true);
    for (let i = 0; i < 20; i++) expect(marks.frame([]).gone.size).toBe(0);
  });

  it('marks again when the place is taken and emptied a second time', () => {
    // An id is a place in the building and is handed to the next creature after
    // a spawn interval, so the same number dies more than once in a visit.
    const marks = createSlopMarks(2);
    marks.frame([at(0, 1)]);
    expect(marks.frame([]).gone.has(0)).toBe(true);
    for (let i = 0; i < 5; i++) marks.frame([]);
    marks.frame([at(0, 30)]);
    expect(marks.frame([]).gone.get(0)).toEqual({ x: 30, y: 2, z: 0.5 });
  });
});
