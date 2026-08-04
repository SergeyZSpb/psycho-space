import { describe, expect, it } from 'vitest';
import {
  formatInstalled,
  formatRoom,
  planBoxFor,
  planSolidStyle,
  planSpotStyle,
  planWindowStyle,
  sourceLabel,
  spotLabel,
  spotLegend,
} from '../lib/fintechAdmin';
import type { FintechAdminSpot } from '../api/types';

/**
 * «АДМИН ФИНТЕХА» — everything the floor plan derives.
 *
 * The page renders rows and boxes and computes nothing, so this is where the
 * interesting half of it is asserted: the Russian for a source and a spot, the
 * Moscow timestamp, the legend roll-up, and the placement arithmetic that has to
 * agree with the game's own — because the plan and the office draw the same room
 * and a plan that placed a desk somewhere else would be believable and wrong.
 */

describe('sourceLabel', () => {
  it('names the source a fresh database opens with', () => {
    expect(sourceLabel('starting')).toBe('стартовый');
  });

  it('already answers the two sources later iterations will send', () => {
    // Landing the generator and the editor has to be a server change, not a
    // two-repository one — see the comment on sourceLabel.
    expect(sourceLabel('generated')).toBe('сгенерированный');
    expect(sourceLabel('edited')).toBe('изменённый вручную');
  });

  it('shows a source it has never heard of rather than guessing at it', () => {
    // A plain string on the wire, exactly like a solid's kind: a newer server
    // must not blank this readout in an older browser.
    expect(sourceLabel('imported')).toBe('imported');
  });

  it('says something when the source is missing entirely', () => {
    expect(sourceLabel('')).toBe('неизвестно');
    expect(sourceLabel('   ')).toBe('неизвестно');
  });
});

describe('formatInstalled', () => {
  it('writes the instant in Moscow time and says so', () => {
    // 15:37 UTC is 18:37 in Moscow. The zone is fixed rather than the runner's,
    // which is what makes this assertion the same on every machine.
    const text = formatInstalled('2026-08-04T15:37:53.439338Z');
    expect(text).toContain('04.08.2026');
    expect(text).toContain('18:37');
    expect(text).toContain('МСК');
  });

  it('crosses midnight the way Moscow does, not the way UTC does', () => {
    // 22:10 UTC on the 4th is 01:10 on the 5th in Moscow — the case a
    // browser-local rendering would get wrong for anybody travelling.
    const text = formatInstalled('2026-08-04T22:10:00Z');
    expect(text).toContain('05.08.2026');
    expect(text).toContain('01:10');
  });

  it('shows an unparseable timestamp verbatim', () => {
    // It is a value off the wire, and showing it is what lets somebody say what
    // the server actually sent.
    expect(formatInstalled('not-a-date')).toBe('not-a-date');
    expect(formatInstalled('')).toBe('');
  });
});

describe('spotLabel', () => {
  it('names every point the server sends today', () => {
    expect(spotLabel('player')).toBe('старт игрока');
    expect(spotLabel('boss')).toBe('старт лысого');
    expect(spotLabel('chaser')).toBe('старт клода');
    expect(spotLabel('npc')).toBe('Серега и Тёма');
    expect(spotLabel('bottle')).toBe('бутылка');
    expect(spotLabel('hookah')).toBe('кальян');
  });

  it('falls back to the raw marker rather than dropping it', () => {
    // A point the validator protects and the plan cannot name is still a point
    // the plan has to draw.
    expect(spotLabel('printer')).toBe('printer');
    expect(spotLabel('')).toBe('точка');
  });
});

describe('spotLegend', () => {
  const spots: FintechAdminSpot[] = [
    { x: 8, y: 4, what: 'player' },
    { x: 8, y: 20, what: 'boss' },
    { x: 2, y: 20, what: 'chaser' },
    { x: 1, y: 1, what: 'npc' },
    { x: 15, y: 1, what: 'npc' },
    { x: 3, y: 9, what: 'bottle' },
    { x: 9, y: 4, what: 'bottle' },
    { x: 6, y: 16, what: 'bottle' },
  ];

  it('rolls a kind up into one row carrying its count', () => {
    const rows = spotLegend(spots);
    expect(rows.map((r) => r.what)).toEqual(['player', 'boss', 'chaser', 'npc', 'bottle']);
    expect(rows.map((r) => r.count)).toEqual([1, 1, 1, 2, 3]);
  });

  it('keeps the server order rather than sorting', () => {
    // The server assembles the list the way the game thinks about its cast —
    // spawns, then Claude, then the colleagues, then the props — and that order
    // carries more than the alphabet would.
    const rows = spotLegend([
      { x: 1, y: 1, what: 'hookah' },
      { x: 2, y: 2, what: 'boss' },
    ]);
    expect(rows.map((r) => r.what)).toEqual(['hookah', 'boss']);
  });

  it('names each row with the Russian the plan uses', () => {
    expect(spotLegend(spots)[3]).toEqual({ what: 'npc', label: 'Серега и Тёма', count: 2 });
  });

  it('answers nothing for a floor with no spots at all', () => {
    expect(spotLegend([])).toEqual([]);
  });
});

describe('planSolidStyle', () => {
  it('places a solid as a percentage of the room', () => {
    // A 3 × 1.2 desk at (2, 3) in a 12 × 18 room.
    expect(planSolidStyle({ x: 2, y: 3, w: 3, h: 1.2 }, 12, 18)).toEqual({
      left: `${(2 / 12) * 100}%`,
      top: `${(3 / 18) * 100}%`,
      width: `${(3 / 12) * 100}%`,
      height: `${(1.2 / 18) * 100}%`,
    });
  });

  it('draws nothing at all for a room with no size', () => {
    // A payload that has not arrived is a plan with nothing on it, never a
    // stack of boxes in the corner.
    expect(planSolidStyle({ x: 2, y: 3, w: 3, h: 1 }, 0, 0)).toEqual({
      left: '0%',
      top: '0%',
      width: '0%',
      height: '0%',
    });
  });
});

describe('planWindowStyle', () => {
  it('places a pane along the wall it is on', () => {
    // The top wall is measured across the room's WIDTH.
    expect(planWindowStyle({ wall: 'top', at: 3, len: 6 }, 12, 18)).toEqual({
      '--at': String(3 / 12),
      '--len': String(6 / 12),
    });
    // A side wall is measured down its HEIGHT — a different axis from a
    // different corner, which is the whole failure mode here.
    expect(planWindowStyle({ wall: 'left', at: 4, len: 9 }, 12, 18)).toEqual({
      '--at': String(4 / 18),
      '--len': String(9 / 18),
    });
  });

  it('refuses a pane it cannot place', () => {
    // An unknown wall, a pane of no length, and a room with no size: all three
    // are dropped rather than drawn at zero size in a corner.
    expect(planWindowStyle({ wall: 'ceiling', at: 1, len: 3 }, 12, 18)).toBeUndefined();
    expect(planWindowStyle({ wall: 'top', at: 1, len: 0 }, 12, 18)).toBeUndefined();
    expect(planWindowStyle({ wall: 'top', at: 1, len: 3 }, 0, 0)).toBeUndefined();
  });

  it('clips a pane that runs past the end of its wall', () => {
    // The game clips rather than letting glazing round a corner, and the plan
    // has to describe the room the game draws.
    expect(planWindowStyle({ wall: 'top', at: 9, len: 6 }, 12, 18)).toEqual({
      '--at': String(9 / 12),
      '--len': String(1 - 9 / 12),
    });
  });
});

describe('planSpotStyle', () => {
  it('puts a point where it stands', () => {
    expect(planSpotStyle({ x: 6, y: 9 }, 12, 18)).toEqual({ left: '50%', top: '50%' });
  });

  it('answers the middle for a room it cannot read, never NaN', () => {
    // NaN in a custom property resolves to nothing, which leaves a marker
    // wherever it last was with no error anywhere.
    expect(planSpotStyle({ x: 6, y: 9 }, 0, 0)).toEqual({ left: '50%', top: '50%' });
  });
});

describe('planBoxFor', () => {
  it('keeps the served ratio rather than a ratio of its own', () => {
    // The one claim the plan makes: draw it at any other shape and it lies about
    // how far apart two desks are.
    expect(planBoxFor(12, 18).ratio).toBe(12 / 18);
    expect(planBoxFor(16, 22).ratio).toBe(16 / 22);
  });

  it('makes both grids one metre of the dimension they run across', () => {
    const box = planBoxFor(12, 18);
    expect(box.rowStripe).toBe(`${100 / 18}%`);
    expect(box.columnStripe).toBe(`${100 / 12}%`);
  });

  it('draws a plausible empty box for a room it cannot read', () => {
    const box = planBoxFor(0, 0);
    expect(box.ratio).toBe(16 / 22);
    expect(box.rowStripe).toBe('100%');
    expect(box.columnStripe).toBe('100%');
  });
});

describe('formatRoom', () => {
  it('writes the room in metres, in the Russian convention', () => {
    expect(formatRoom(16, 22)).toBe('16 × 22 м');
    expect(formatRoom(12.5, 18)).toBe('12,5 × 18 м');
  });

  it('says nothing false about a room it cannot read', () => {
    expect(formatRoom(Number.NaN, 22)).toBe('? × 22 м');
  });
});
