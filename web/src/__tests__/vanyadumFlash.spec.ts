import { describe, expect, it } from 'vitest';
import { FLASH_FRAMES, createFlash, createPeerFlashes } from '../lib/vanyadumFlash';

/**
 * «ВАНЯДУМ» — the muzzle flash, which is the acknowledgement a shot gets.
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
  it('flashes once for a marker that is set on consecutive frames', () => {
    // THE LEVEL-VERSUS-TRANSITION RULE, and it is the whole reason this is not
    // `if (peer.firing) draw()`. The marker rides the snapshot for the tick a
    // shot happened, and a tick is three frames at sixty hertz — more while the
    // interpolation buffer is holding its newest frame — so drawing the level
    // would light the peer for as long as that frame was the newest one, and a
    // peer firing every tick would simply glow.
    const flashes = createPeerFlashes();
    const lit: boolean[] = [];
    for (let i = 0; i < 10; i++) lit.push(flashes.frame([{ slot: 1, firing: true }]).has(1));
    expect(lit.slice(0, FLASH_FRAMES).every(Boolean)).toBe(true);
    expect(lit.slice(FLASH_FRAMES).some(Boolean)).toBe(false);
  });

  it('flashes again the next time the marker arrives', () => {
    // Once the marker has gone back down, the next one is a new shot and a new
    // mark. Without this the peer would fire once per session.
    const flashes = createPeerFlashes();
    expect(flashes.frame([{ slot: 2, firing: true }]).has(2)).toBe(true);
    for (let i = 0; i < 6; i++) flashes.frame([{ slot: 2 }]);
    expect(flashes.frame([{ slot: 2, firing: true }]).has(2)).toBe(true);
  });

  it('never marks a peer whose frame carries no marker', () => {
    const flashes = createPeerFlashes();
    for (let i = 0; i < 5; i++) {
      expect(flashes.frame([{ slot: 0 }, { slot: 1, firing: false }]).size).toBe(0);
    }
  });

  it('is per slot, so one man firing does not light the man beside him', () => {
    const flashes = createPeerFlashes();
    const marked = flashes.frame([
      { slot: 0, firing: true },
      { slot: 1 },
    ]);
    expect([...marked]).toEqual([0]);
  });

  it('holds an unfinished mark across frames the peer is missing from', () => {
    // A peer drops out of a snapshot by walking two rooms away, and the frames
    // he is absent from draw no figure at all — so there is nothing for his mark
    // to be shown on, and spending it on those frames would mean a shot fired at
    // a doorway was invisible by the time its author was back in view.
    const flashes = createPeerFlashes();
    expect(flashes.frame([{ slot: 3, firing: true }]).has(3)).toBe(true);
    for (let i = 0; i < 4; i++) expect(flashes.frame([]).size).toBe(0);
    expect(flashes.frame([{ slot: 3 }]).has(3)).toBe(true);
  });
});
