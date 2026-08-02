import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { RELOAD, SHOT, createGunAudio } from '../lib/vanyadumSound';

/**
 * The обрез's voice.
 *
 * WHAT IS ASSERTED AND WHAT IS NOT. Whether the result sounds like a shotgun is
 * not a claim a test can make, and nothing here pretends to: what these pin is
 * that the module wakes the hardware only when a gesture asked it to, that the
 * mute is honoured, that a browser without Web Audio plays the game silently
 * rather than throwing on the first shot, and that the context is handed back.
 * Every one of those is a defect that would otherwise be found by a player.
 *
 * jsdom implements no Web Audio at all, so `window.AudioContext` is stubbed —
 * which is a BROWSER API being replaced rather than a seam cut into the module
 * for the test's benefit. The production code reads the same global a real
 * browser provides, and answers a missing one the same way it answers a browser
 * with the feature switched off.
 */

interface Started {
  kind: 'buffer' | 'oscillator';
  start: number;
  stop: number;
}

/** Records what a burst asked the hardware to do, without making any noise. */
class FakeAudioContext {
  static built = 0;
  static live: FakeAudioContext[] = [];

  currentTime = 0;
  sampleRate = 48_000;
  state: AudioContextState = 'suspended';
  destination = {} as AudioDestinationNode;
  resumed = 0;
  closed = 0;
  started: Started[] = [];

  constructor() {
    FakeAudioContext.built += 1;
    FakeAudioContext.live.push(this);
  }

  resume(): Promise<void> {
    this.resumed += 1;
    this.state = 'running';
    return Promise.resolve();
  }

  close(): Promise<void> {
    this.closed += 1;
    this.state = 'closed';
    return Promise.resolve();
  }

  createBuffer(_channels: number, frames: number): AudioBuffer {
    const data = new Float32Array(frames);
    return { getChannelData: () => data, length: frames } as unknown as AudioBuffer;
  }

  private param() {
    return { setValueAtTime: () => {}, exponentialRampToValueAtTime: () => {} };
  }

  createGain() {
    return { gain: this.param(), connect: () => {} } as unknown as GainNode;
  }

  createBiquadFilter() {
    return {
      type: '',
      frequency: this.param(),
      connect: () => {},
    } as unknown as BiquadFilterNode;
  }

  createBufferSource() {
    const rec: Started = { kind: 'buffer', start: -1, stop: -1 };
    this.started.push(rec);
    return {
      buffer: null,
      connect: () => {},
      start: (t: number) => {
        rec.start = t;
      },
      stop: (t: number) => {
        rec.stop = t;
      },
    } as unknown as AudioBufferSourceNode;
  }

  createOscillator() {
    const rec: Started = { kind: 'oscillator', start: -1, stop: -1 };
    this.started.push(rec);
    return {
      type: '',
      frequency: this.param(),
      connect: () => {},
      start: (t: number) => {
        rec.start = t;
      },
      stop: (t: number) => {
        rec.stop = t;
      },
    } as unknown as OscillatorNode;
  }
}

const globals = window as unknown as { AudioContext?: unknown; webkitAudioContext?: unknown };

beforeEach(() => {
  FakeAudioContext.built = 0;
  FakeAudioContext.live = [];
  globals.AudioContext = FakeAudioContext;
});

afterEach(() => {
  delete globals.AudioContext;
  delete globals.webkitAudioContext;
});

/** The one context a test has armed. */
function ctx(): FakeAudioContext {
  expect(FakeAudioContext.live.length).toBe(1);
  return FakeAudioContext.live[0];
}

describe('the gun’s voice', () => {
  it('wakes no audio hardware until somebody has pulled the trigger', () => {
    // Merely opening the game must not start an audio context. A browser would
    // refuse one anyway before a gesture, and taking a device from whatever else
    // the phone is playing is not a thing a rules screen should do.
    createGunAudio();
    expect(FakeAudioContext.built).toBe(0);
  });

  it('creates and resumes one on the gesture that asked', () => {
    const audio = createGunAudio();
    audio.arm();
    expect(FakeAudioContext.built).toBe(1);
    expect(ctx().resumed).toBe(1);
  });

  it('reuses the context rather than building one per shot', () => {
    const audio = createGunAudio();
    audio.arm();
    audio.arm();
    audio.arm();
    expect(FakeAudioContext.built).toBe(1);
  });

  it('plays a shot that lasts as long as the catalogue of bursts says', () => {
    const audio = createGunAudio();
    audio.arm();
    audio.shot();
    // The noise and the weight under it: both start now and both stop together,
    // so the burst cannot outlive its own envelope.
    expect(ctx().started).toHaveLength(2);
    for (const s of ctx().started) {
      expect(s.start).toBe(0);
      expect(s.stop).toBeCloseTo(SHOT.seconds, 9);
    }
  });

  it('plays a smaller, shorter thing for a reload', () => {
    // The reload's real acknowledgement is a second and a half of an empty HUD.
    // This only marks the moment it started, and it must not be mistakable for a
    // shot by somebody who is not looking at the screen.
    const audio = createGunAudio();
    audio.arm();
    audio.reload();
    expect(RELOAD.seconds).toBeLessThan(SHOT.seconds);
    expect(RELOAD.gain).toBeLessThan(SHOT.gain);
    // One source and no weight under it — that is what makes it a click.
    expect(ctx().started).toHaveLength(1);
    expect(RELOAD.thump).toBe(0);
  });

  it('says nothing at all while muted, and starts audible', () => {
    const audio = createGunAudio();
    expect(audio.muted()).toBe(false);
    audio.arm();
    expect(audio.toggleMuted()).toBe(true);
    audio.shot();
    audio.reload();
    expect(ctx().started).toHaveLength(0);
  });

  it('comes back when the mute is pressed again', () => {
    const audio = createGunAudio();
    audio.arm();
    audio.toggleMuted();
    expect(audio.toggleMuted()).toBe(false);
    audio.shot();
    expect(ctx().started.length).toBeGreaterThan(0);
  });

  it('plays nothing before it has been armed, rather than throwing', () => {
    // The render loop can reach a shot on the same frame as the tap that caused
    // it, and an exception there would take the whole draw loop down — the
    // camera, the peers and the HUD with it, for a sound effect.
    const audio = createGunAudio();
    expect(() => audio.shot()).not.toThrow();
    expect(FakeAudioContext.built).toBe(0);
  });

  it('plays the game silently on a browser with no Web Audio', () => {
    // A REAL path: the API can be absent behind a privacy setting, exactly as
    // WebGL can. Silence is the right answer; an exception on the first shot is
    // not.
    delete globals.AudioContext;
    const audio = createGunAudio();
    expect(() => {
      audio.arm();
      audio.shot();
      audio.reload();
      audio.close();
    }).not.toThrow();
  });

  it('falls back to the prefixed constructor older Safari has', () => {
    delete globals.AudioContext;
    globals.webkitAudioContext = FakeAudioContext;
    createGunAudio().arm();
    expect(FakeAudioContext.built).toBe(1);
  });

  it('gives the hardware back, and keeps the mute when it does', () => {
    // Leaving a page with a live context holds a device open for a game nobody
    // is playing. The mute is the player's decision and survives the visit that
    // set it.
    const audio = createGunAudio();
    audio.arm();
    audio.toggleMuted();
    const first = ctx();
    audio.close();
    expect(first.closed).toBe(1);
    expect(audio.muted()).toBe(true);

    // And it can be armed again afterwards, because leaving the building is not
    // the same as leaving the page.
    audio.arm();
    expect(FakeAudioContext.built).toBe(2);
  });
});
