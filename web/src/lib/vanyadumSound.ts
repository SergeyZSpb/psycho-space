/**
 * «ВАНЯДУМ» — the обрез, as Web Audio. There are no sound files in this game.
 *
 * WHY IT IS SYNTHESISED. Everything else here is generated rather than authored:
 * the building from a seed, the textures from a noise function, the gun itself
 * from three boxes. A .wav would be the only asset in the repository, it would
 * be the largest thing on the route, and it would be downloaded by everybody who
 * opened the game whether or not they ever pulled the trigger. A shotgun is a
 * burst of noise through a filter that shuts, which is four nodes and no bytes.
 *
 * WHY IT IS HERE AND NOT IN `render/`. It holds no GPU context and no scene
 * graph — it is a small amount of state and some arithmetic over an API jsdom
 * does not implement, which is exactly what `lib/` is for. The tests stub
 * `window.AudioContext`, which is a browser API being replaced rather than a
 * seam cut into this file for their benefit.
 *
 * WHAT IT IS FOR. A shot has to be acknowledged AT THE INSTANT the thumb lands,
 * because that is most of what "responsive" means in a shooter, and the
 * prediction is what makes that honest: the browser has already run the same
 * refusal the server is about to run, so a sound is only made for a shot that
 * was really granted. See the muzzle flash in `render/vanyadumScene.ts`, which
 * is the same event drawn rather than heard.
 *
 * IT IS NOT GATED ON `prefers-reduced-motion`. That setting is about motion, and
 * a person who asked for less of it did not ask for less sound — the recoil is
 * what damps. What this offers instead is a mute the player can actually reach,
 * on the play surface, next to the trigger.
 */

/** One burst: a noise envelope, a filter that shuts, and optionally some weight. */
export interface Burst {
  /** How long the whole thing lasts, in seconds. */
  seconds: number;
  /** Peak gain, 0..1. */
  gain: number;
  /** Where the low-pass starts, in Hz — the crack. */
  from: number;
  /** Where it has fallen to by the end — the thud the room gives back. */
  to: number;
  /**
   * A sine under the noise, in Hz, or zero for none.
   *
   * Noise alone reads as static rather than as a gun: what a phone speaker can
   * actually reproduce of a shotgun is mostly this, and what a pair of
   * headphones adds is mostly the noise above it.
   */
  thump: number;
}

/**
 * The двустволка going off. Short, loud, and over before the cadence is a third
 * spent, so a double tap is two sounds rather than one smear.
 */
export const SHOT: Burst = { seconds: 0.22, gain: 0.85, from: 4200, to: 260, thump: 90 };

/**
 * The гильзы coming out and the breech shutting — deliberately small.
 *
 * The reload's real acknowledgement is a second and a half of an empty HUD; this
 * only marks the moment it STARTED, which is otherwise indistinguishable from a
 * trigger that did nothing. Quiet and bright, so it cannot be mistaken for a
 * shot by somebody who is not looking at the screen.
 */
export const RELOAD: Burst = { seconds: 0.09, gain: 0.28, from: 2600, to: 900, thump: 0 };

/** How long the reusable noise buffer is. Longer than the longest burst. */
const NOISE_SECONDS = 0.3;

type AudioContextCtor = new () => AudioContext;

/**
 * Ignores whatever a Web Audio promise does, including not being one.
 *
 * `resume` and `close` are specified to return promises, and an older
 * implementation returns `undefined` instead — so this has to tolerate both
 * rather than calling `.catch` on whatever came back.
 */
function swallow(p: unknown): void {
  void Promise.resolve(p).catch(() => {});
}

/**
 * The constructor this browser has, or null.
 *
 * A REAL PRODUCTION PATH rather than a test accommodation, and the same shape
 * `webglAvailable` takes: Web Audio can be absent behind a privacy setting, and
 * a browser without it must play the game silently rather than throw on the
 * first shot. That a test can stub the same global is a consequence.
 */
function audioContextCtor(): AudioContextCtor | null {
  if (typeof window === 'undefined') return null;
  const w = window as unknown as {
    AudioContext?: AudioContextCtor;
    webkitAudioContext?: AudioContextCtor;
  };
  return w.AudioContext ?? w.webkitAudioContext ?? null;
}

/**
 * Builds the gun's voice.
 *
 * Everything is lazy: no context is created until `arm` is called from a real
 * gesture, so a player who never opens the game — or never touches the trigger —
 * has no audio hardware woken on his behalf.
 */
export function createGunAudio() {
  let ctx: AudioContext | null = null;
  let noise: AudioBuffer | null = null;
  let muted = false;

  /**
   * Wakes the audio hardware, and must be called from inside a real gesture.
   *
   * BROWSERS REFUSE TO START AN AUDIO CONTEXT THAT NO USER ASKED FOR, which is
   * the whole reason this is a separate method rather than something `play`
   * does for itself. A shot is emitted from the render loop a frame or two after
   * the tap that caused it, and by then the gesture is over — so the context is
   * created and resumed in the handler that saw the tap, and the loop only ever
   * finds one that is already running. Without this the first shot of a visit is
   * silent, which is the one shot a player is listening for.
   */
  function arm(): void {
    if (!ctx) {
      const Ctor = audioContextCtor();
      if (!Ctor) return;
      try {
        ctx = new Ctor();
      } catch {
        // A browser that has the constructor and still refuses to build one —
        // an exhausted context limit, a policy. Silence is the right answer and
        // the only one available.
        return;
      }
    }
    // Both of this module's promises are swallowed, and deliberately. A resume
    // the browser declines and a close on a context that has already gone are
    // both ordinary, neither has an answer beyond staying silent, and an
    // unhandled rejection out of a sound effect would land in the page's error
    // reporting looking like something worth reading.
    if (ctx.state === 'suspended') swallow(ctx.resume?.());
  }

  /** White noise, generated once and reused by every burst. */
  function noiseBuffer(c: AudioContext): AudioBuffer {
    if (noise) return noise;
    const frames = Math.floor(c.sampleRate * NOISE_SECONDS);
    const buffer = c.createBuffer(1, frames, c.sampleRate);
    const data = buffer.getChannelData(0);
    // Math.random rather than crypto: this is the colour of a sound effect, and
    // nothing about it is security-sensitive.
    for (let i = 0; i < frames; i++) data[i] = Math.random() * 2 - 1;
    noise = buffer;
    return buffer;
  }

  /**
   * Plays one burst, or does nothing at all.
   *
   * Nodes are built per shot and thrown away. They are cheap, they stop
   * themselves, and a pool of them would be state to keep correct in exchange
   * for allocations a phone makes thousands of times a second elsewhere.
   */
  function play(burst: Burst): void {
    if (muted || !ctx || ctx.state === 'closed') return;
    const c = ctx;
    const now = c.currentTime;
    const end = now + burst.seconds;

    const gain = c.createGain();
    gain.gain.setValueAtTime(burst.gain, now);
    // Exponential, and never to zero — an exponential ramp to zero is undefined
    // and browsers answer it with a click, which is a second, worse sound.
    gain.gain.exponentialRampToValueAtTime(0.0001, end);
    gain.connect(c.destination);

    const filter = c.createBiquadFilter();
    filter.type = 'lowpass';
    filter.frequency.setValueAtTime(burst.from, now);
    filter.frequency.exponentialRampToValueAtTime(burst.to, end);
    filter.connect(gain);

    const source = c.createBufferSource();
    source.buffer = noiseBuffer(c);
    source.connect(filter);
    source.start(now);
    source.stop(end);

    if (burst.thump > 0) {
      const osc = c.createOscillator();
      osc.type = 'sine';
      osc.frequency.setValueAtTime(burst.thump, now);
      // Down half an octave as it dies, which is what a room does to a bang.
      osc.frequency.exponentialRampToValueAtTime(burst.thump / 2, end);
      const low = c.createGain();
      low.gain.setValueAtTime(burst.gain, now);
      low.gain.exponentialRampToValueAtTime(0.0001, end);
      osc.connect(low);
      low.connect(c.destination);
      osc.start(now);
      osc.stop(end);
    }
  }

  return {
    arm,
    /** The обрез. Called for a shot the prediction GRANTED, never for a tap. */
    shot(): void {
      play(SHOT);
    },
    /** The breech. Called the instant a reload starts, not when it finishes. */
    reload(): void {
      play(RELOAD);
    },
    /** Whether anything is currently audible. */
    muted(): boolean {
      return muted;
    },
    /** Flips the mute and answers where it landed, so a caller needs no second read. */
    toggleMuted(): boolean {
      muted = !muted;
      return muted;
    },
    /**
     * Gives the audio hardware back. Leaving a page with a live context holds a
     * device open for a game nobody is playing.
     */
    close(): void {
      const dying = ctx;
      ctx = null;
      noise = null;
      swallow(dying?.close?.());
    },
  };
}

export type GunAudio = ReturnType<typeof createGunAudio>;
