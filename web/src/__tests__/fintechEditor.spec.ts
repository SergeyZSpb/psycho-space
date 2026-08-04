import { describe, expect, it } from 'vitest';
import {
  GRID,
  LAYOUT_PROBLEM_CODES,
  MAX_SIDE,
  MIN_SIDE,
  NEW_WINDOW_LEN,
  PICK_TOLERANCE_PX,
  WINDOW_EDGE,
  WINDOW_MIN_LEN,
  clampWindow,
  cycleSelection,
  draftFrom,
  freeSpot,
  freeWindowAt,
  installReport,
  kindLabel,
  metres,
  moveSolidTo,
  newSize,
  pickAt,
  pointToMetres,
  problemLine,
  problemTarget,
  problemText,
  problemsFrom,
  resizeSolidTo,
  sameLayout,
  selectionReadout,
  snap,
  stepSolid,
  stepWindow,
  toleranceMetres,
  wallLabel,
  wallLength,
} from '../lib/fintechEditor';
import type { FintechAdminKind, FintechLayoutDraft, FintechRect } from '../api/types';

/**
 * «АДМИН ФИНТЕХА» — the field constructor's arithmetic.
 *
 * The view drags boxes and computes nothing, so this is where the interesting
 * half of the editor is asserted: pixels into metres, metres onto the lattice,
 * a rectangle put back inside the room, which object a tap meant, and the
 * Russian for every code the server can answer with.
 *
 * WHAT IS DELIBERATELY NOT TESTED HERE IS WHETHER A FLOOR IS LEGAL, because
 * nothing in this module decides that. The separation rule, the spot rule and
 * the connectivity flood fill live on the server and are tested there; a second
 * copy of them in the browser is exactly what this file's subject was written to
 * avoid.
 */

/** The room the game actually ships with, so the numbers below are the real ones. */
const ROOM = { w: 16, h: 22 };

/**
 * A plan box as `getBoundingClientRect()` would report it.
 *
 * IT IS THE ROOM AND NOTHING ELSE — the plan draws its edge with an inset ring
 * rather than a border precisely so that this is true, because a border would
 * put the pointer and the drawing a pixel out of step. The offsets are non-zero
 * on purpose: a mapping that forgot to subtract the box's origin passes against
 * a rect at (0, 0) and fails the moment the page is scrolled.
 */
const BOX = { left: 32, top: 120, width: 296, height: 296 * (22 / 16) };

describe('snap', () => {
  it('puts a value on the quarter-metre lattice the generator uses', () => {
    expect(snap(4.5)).toBe(4.5);
    expect(snap(4.6)).toBe(4.5);
    expect(snap(4.63)).toBe(4.75);
    expect(snap(0)).toBe(0);
  });

  it('rounds an exact half towards +∞, which is the rule and not an accident', () => {
    // Nothing in the editor turns on which way a halfway drag falls — it is a
    // fifth of a pixel at 360 px — but a rule nobody wrote down is a rule that
    // changes by accident, so it is pinned here.
    expect(snap(0.125)).toBe(0.25);
    expect(snap(0.375)).toBe(0.5);
    expect(snap(-0.375)).toBe(-0.25);
  });

  it('never answers negative zero', () => {
    // `Math.round(-0.4)` is -0, which formats as «-0,00» and compares equal to
    // zero everywhere a test would normally look. `Object.is` is the one place
    // it shows, which is why the assertion is written this way.
    expect(Object.is(snap(-0.1), 0)).toBe(true);
    expect(Object.is(snap(-0.125), 0)).toBe(true);
    expect(metres(snap(-0.1))).toBe('0,00');
  });

  it('answers zero for a number that is not one', () => {
    expect(snap(Number.NaN)).toBe(0);
    expect(snap(Number.POSITIVE_INFINITY)).toBe(0);
  });
});

describe('the bounds the server enforces', () => {
  it('rounds the minimum side INWARDS onto the lattice', () => {
    // The server's floor is 0.6 m, which is not a multiple of a quarter. Rounding
    // down would let a drag produce a side the validator refuses outright — a
    // control that offers an illegal move, which is the one failure the clamp
    // exists to prevent.
    expect(MIN_SIDE).toBe(0.75);
    expect(MIN_SIDE).toBeGreaterThanOrEqual(0.6);
    expect(MIN_SIDE % GRID).toBeCloseTo(0, 10);
  });

  it('rounds the maximum side inwards too', () => {
    expect(MAX_SIDE).toBe(6);
    expect(MAX_SIDE).toBeLessThanOrEqual(6);
  });

  it('rounds the window bounds the same way', () => {
    expect(WINDOW_MIN_LEN).toBe(1.25);
    expect(WINDOW_MIN_LEN).toBeGreaterThanOrEqual(1.2);
    expect(WINDOW_EDGE).toBe(0.75);
    expect(WINDOW_EDGE).toBeGreaterThanOrEqual(0.6);
  });
});

describe('metres', () => {
  it('writes a length the way the readout does', () => {
    expect(metres(4.5)).toBe('4,50');
    expect(metres(6)).toBe('6,00');
    expect(metres(0.75)).toBe('0,75');
  });

  it('says nothing rather than NaN', () => {
    expect(metres(Number.NaN)).toBe('?');
  });
});

describe('pointToMetres', () => {
  it('maps a pixel inside the plan onto the floor it is drawn on', () => {
    // The exact centre of the plan is the exact centre of the room, whatever the
    // page has been scrolled to.
    const middle = pointToMetres(BOX.left + BOX.width / 2, BOX.top + BOX.height / 2, BOX, ROOM);
    expect(middle.x).toBeCloseTo(8, 6);
    expect(middle.y).toBeCloseTo(11, 6);
  });

  it('puts the origin at the top-left corner, with +Y downwards', () => {
    // The same convention the office uses, which is why there is no axis flip
    // anywhere in this client.
    const corner = pointToMetres(BOX.left, BOX.top, BOX, ROOM);
    expect(corner.x).toBeCloseTo(0, 6);
    expect(corner.y).toBeCloseTo(0, 6);

    const far = pointToMetres(BOX.left + BOX.width, BOX.top + BOX.height, BOX, ROOM);
    expect(far.x).toBeCloseTo(16, 6);
    expect(far.y).toBeCloseTo(22, 6);
  });

  it('answers the origin for a plan that has not been laid out yet', () => {
    // A width of zero would otherwise be a division producing Infinity, which
    // `snap` turns into 0 anyway — but going through NaN first is how a drag
    // ends up somewhere nobody can explain.
    expect(pointToMetres(100, 100, { ...BOX, width: 0 }, ROOM)).toEqual({ x: 0, y: 0 });
    expect(pointToMetres(100, 100, BOX, { w: 0, h: 0 })).toEqual({ x: 0, y: 0 });
  });
});

describe('toleranceMetres', () => {
  it('turns a thumb into a distance on the floor', () => {
    // The number that makes `pickAt` two-pass rather than one: 22 px on a 296 px
    // plan of a 16 m room is well over a metre, against a validator minimum
    // separation of 1.5 m.
    const tolerance = toleranceMetres(PICK_TOLERANCE_PX, BOX, ROOM);
    expect(tolerance).toBeCloseTo((22 * 16) / 296, 6);
    expect(tolerance).toBeGreaterThan(1);
  });

  it('answers nothing for a plan with no size', () => {
    expect(toleranceMetres(22, { ...BOX, width: 0, height: 0 }, ROOM)).toBe(0);
  });
});

describe('pickAt', () => {
  // A desk and a flowerpot standing the legal minimum apart: the desk's right
  // edge is at 6 and the pot starts at 7.5, which is exactly `min_gap`.
  const solids: FintechRect[] = [
    { x: 3, y: 5, w: 3, h: 1.2 },
    { x: 7.5, y: 5, w: 0.75, h: 0.75 },
  ];

  it('selects whatever contains the point, however big the tolerance is', () => {
    expect(pickAt(solids, { x: 4, y: 5.5 }, 5)).toBe(0);
    expect(pickAt(solids, { x: 7.8, y: 5.3 }, 5)).toBe(1);
  });

  it('reaches the small thing standing the legal distance from the big one', () => {
    // THE WHOLE REASON CONTAINMENT IS A SEPARATE PASS. A single pass taking the
    // nearest object within a thumb's width would answer 0 here — the pot's
    // centre is 1.9 m from the desk, inside a tolerance of over a metre and a
    // half — and the flowerpot would be unselectable for ever.
    const tolerance = toleranceMetres(PICK_TOLERANCE_PX, BOX, ROOM);
    expect(tolerance).toBeGreaterThan(1);
    expect(pickAt(solids, { x: 7.875, y: 5.375 }, tolerance)).toBe(1);
  });

  it('falls back to the nearest thing when the tap lands on bare floor', () => {
    // A near miss below the desk: nothing contains it, so the tolerance answers.
    expect(pickAt(solids, { x: 4, y: 6.5 }, 1)).toBe(0);
  });

  it('answers nothing when the nearest thing is out of reach', () => {
    expect(pickAt(solids, { x: 14, y: 18 }, 1)).toBe(-1);
    expect(pickAt(solids, { x: 4, y: 6.5 }, 0)).toBe(-1);
  });

  it('gives a tie to the smaller object', () => {
    // Equidistant, and the small one is what somebody was plausibly aiming at —
    // a desk is not something you miss.
    const pair: FintechRect[] = [
      { x: 4, y: 5, w: 3, h: 3 },
      { x: 4, y: 9, w: 0.75, h: 0.75 },
    ];
    expect(pickAt(pair, { x: 4.2, y: 8.5 }, 1)).toBe(1);
  });

  it('gives overlapping boxes to the one on top', () => {
    // Later solids paint over earlier ones, so the last one in the list is the
    // one the eye sees — a draft may well have two overlapping while somebody is
    // dragging.
    const stacked: FintechRect[] = [
      { x: 4, y: 5, w: 3, h: 3 },
      { x: 5, y: 6, w: 1, h: 1 },
    ];
    expect(pickAt(stacked, { x: 5.5, y: 6.5 }, 1)).toBe(1);
  });

  it('answers nothing at all for an empty floor', () => {
    expect(pickAt([], { x: 4, y: 4 }, 2)).toBe(-1);
  });
});

describe('moveSolidTo', () => {
  const desk = { x: 3, y: 5, w: 3, h: 1.2, kind: 'desk' };

  it('lands on the lattice and keeps its size', () => {
    const moved = moveSolidTo(desk, 4.6, 6.1, ROOM);
    expect(moved).toEqual({ x: 4.5, y: 6, w: 3, h: 1.2, kind: 'desk' });
  });

  it('stops at each of the four walls', () => {
    expect(moveSolidTo(desk, -9, 5, ROOM).x).toBe(0);
    expect(moveSolidTo(desk, 3, -9, ROOM).y).toBe(0);
    // The FAR edge is what is clamped, not the origin: a 3 m desk in a 16 m room
    // stops with its origin at 13.
    expect(moveSolidTo(desk, 99, 5, ROOM).x).toBe(16 - 3);
    expect(moveSolidTo(desk, 3, 99, ROOM).y).toBe(22 - 1.2);
  });

  it('survives a room bigger than the object is not', () => {
    // An object wider than the room cannot be placed anywhere legal; putting it
    // at the origin is the answer that at least draws.
    const huge = { ...desk, w: 40 };
    expect(moveSolidTo(huge, 5, 5, ROOM).x).toBe(0);
  });
});

describe('resizeSolidTo', () => {
  const desk = { x: 3, y: 5, w: 3, h: 1.2, kind: 'desk' };

  it('keeps the far corner where it was', () => {
    const bigger = resizeSolidTo(desk, 4.1, 2, ROOM);
    expect(bigger.x).toBe(3);
    expect(bigger.y).toBe(5);
    expect(bigger.w).toBe(4);
    expect(bigger.h).toBe(2);
  });

  it('will not go below the smallest side the server accepts', () => {
    const tiny = resizeSolidTo(desk, 0.1, 0.1, ROOM);
    expect(tiny.w).toBe(MIN_SIDE);
    expect(tiny.h).toBe(MIN_SIDE);
  });

  it('will not go past the longest side the server accepts', () => {
    const long = resizeSolidTo({ ...desk, x: 1, y: 1 }, 99, 99, ROOM);
    expect(long.w).toBe(MAX_SIDE);
    expect(long.h).toBe(MAX_SIDE);
  });

  it('is cut short by the wall before the maximum', () => {
    // At x = 13 there are three metres of room left, which is less than the six
    // the server would otherwise allow.
    const cramped = resizeSolidTo({ ...desk, x: 13, y: 5 }, 99, 1, ROOM);
    expect(cramped.w).toBe(3);
    expect(cramped.x + cramped.w).toBe(16);
  });
});

describe('stepSolid', () => {
  const desk = { x: 3, y: 5, w: 3, h: 1.2, kind: 'desk' };

  it('nudges by exactly one lattice step', () => {
    expect(stepSolid(desk, 'x', 1, ROOM).x).toBe(3.25);
    expect(stepSolid(desk, 'y', -1, ROOM).y).toBe(4.75);
    expect(stepSolid(desk, 'w', 1, ROOM).w).toBe(3.25);
  });

  it('pulls a value that was off the lattice onto it', () => {
    // A GENERATED FLOOR IS ON THE LATTICE BUT A STORED ONE NEED NOT BE — the
    // server accepts any finite rectangle, so the office in force can carry a
    // 1.2 m desk. The first nudge lands it on a quarter rather than carrying the
    // remainder along for ever, which is what makes the readout's numbers stay
    // readable once anybody has touched them.
    expect(desk.h).toBe(1.2);
    expect(stepSolid(desk, 'h', -1, ROOM).h).toBe(1);
    expect(stepSolid(desk, 'h', 1, ROOM).h).toBe(1.5);
  });

  it('is the same clamp the drag is', () => {
    expect(stepSolid({ ...desk, x: 0 }, 'x', -1, ROOM).x).toBe(0);
  });
});

describe('windows', () => {
  it('knows how long each wall is', () => {
    expect(wallLength('top', ROOM)).toBe(16);
    expect(wallLength('left', ROOM)).toBe(22);
    expect(wallLength('right', ROOM)).toBe(22);
    // A fourth wall this build has never heard of is not one it may guess at.
    expect(wallLength('bottom', ROOM)).toBe(0);
  });

  it('keeps a pane off both ends of its wall', () => {
    expect(clampWindow({ wall: 'top', at: -5, len: 3 }, ROOM).at).toBe(WINDOW_EDGE);
    const pushed = clampWindow({ wall: 'top', at: 99, len: 3 }, ROOM);
    expect(pushed.at + pushed.len).toBe(16 - WINDOW_EDGE);
  });

  it('will not make a pane shorter than one', () => {
    expect(clampWindow({ wall: 'left', at: 4, len: 0.1 }, ROOM).len).toBe(WINDOW_MIN_LEN);
  });

  it('leaves a pane on a wall it cannot place exactly as it found it', () => {
    const odd = { wall: 'bottom', at: 3, len: 4 };
    expect(clampWindow(odd, ROOM)).toEqual(odd);
  });

  it('nudges along the wall and clamps the same way', () => {
    expect(stepWindow({ wall: 'top', at: 4, len: 3 }, 'at', 1, ROOM).at).toBe(4.25);
    expect(stepWindow({ wall: 'top', at: 4, len: 3 }, 'len', -1, ROOM).len).toBe(2.75);
  });

  it('finds a run of wall a new pane fits in', () => {
    // The first pane starts at the edge; the second has to clear it plus the
    // mullion, so it cannot start where the first one did.
    const first = freeWindowAt('top', NEW_WINDOW_LEN, ROOM, []);
    expect(first).toBe(WINDOW_EDGE);
    const second = freeWindowAt('top', NEW_WINDOW_LEN, ROOM, [
      { wall: 'top', at: first, len: NEW_WINDOW_LEN },
    ]);
    expect(second).toBeGreaterThan(first + NEW_WINDOW_LEN);
  });

  it('ignores panes on the other walls', () => {
    const at = freeWindowAt('left', NEW_WINDOW_LEN, ROOM, [{ wall: 'top', at: 0.75, len: 5 }]);
    expect(at).toBe(WINDOW_EDGE);
  });
});

describe('freeSpot', () => {
  it('puts the first object near the middle of an empty floor', () => {
    const at = freeSpot({ w: 2.5, h: 1 }, ROOM, [], [], 1.5);
    expect(at.x).toBeCloseTo((16 - 2.5) / 2, 1);
    expect(at.y).toBeCloseTo((22 - 1) / 2, 1);
    expect(at.x % GRID).toBeCloseTo(0, 10);
  });

  it('does not drop a second object on top of the first', () => {
    // The palette's whole usefulness: two presses have to produce two objects
    // somebody can see, not one hiding inside another.
    const size = { w: 2.5, h: 1 };
    const first = freeSpot(size, ROOM, [], [], 1.5);
    const second = freeSpot(size, ROOM, [{ ...first, ...size }], [], 1.5);
    expect(second).not.toEqual(first);
    const apart =
      second.x >= first.x + size.w + 1.5 ||
      first.x >= second.x + size.w + 1.5 ||
      second.y >= first.y + size.h + 1.5 ||
      first.y >= second.y + size.h + 1.5;
    expect(apart).toBe(true);
  });

  it('keeps clear of the fixed catalogue points when it can', () => {
    const points = [{ x: 8, y: 11 }];
    const at = freeSpot({ w: 1, h: 1 }, ROOM, [], points, 1.5);
    const dx = Math.max(0, Math.max(at.x - 8, 8 - (at.x + 1)));
    const dy = Math.max(0, Math.max(at.y - 11, 11 - (at.y + 1)));
    expect(Math.hypot(dx, dy)).toBeGreaterThanOrEqual(1.5);
  });

  it('still places something on a floor with nowhere clear left', () => {
    // IT PICKS A POSITION AND JUDGES NOTHING — the server is what refuses a
    // floor, so «no room anywhere» has to end in an object somebody can see and
    // drag rather than in a press that appears not to have worked.
    const wall: FintechRect[] = [{ x: 0, y: 0, w: 16, h: 22 }];
    const at = freeSpot({ w: 1, h: 1 }, ROOM, wall, [], 1.5);
    expect(Number.isFinite(at.x)).toBe(true);
    expect(Number.isFinite(at.y)).toBe(true);
  });
});

describe('newSize', () => {
  it('starts each kind at something shaped like itself', () => {
    expect(newSize('desk').w).toBeGreaterThan(newSize('flower').w);
    expect(newSize('flower').w).toBeGreaterThanOrEqual(MIN_SIDE);
  });

  it('has an answer for a kind this build has never heard of', () => {
    // A kind is a plain string on the wire, so the palette can offer one the
    // server named and this build does not know.
    expect(newSize('printer')).toEqual({ w: 1, h: 1 });
  });
});

describe('cycleSelection', () => {
  const counts = { solids: 2, windows: 1 };

  it('starts at the first object and wraps round the whole floor', () => {
    expect(cycleSelection(null, counts, 1)).toEqual({ list: 'solid', index: 0 });
    expect(cycleSelection({ list: 'solid', index: 0 }, counts, 1)).toEqual({ list: 'solid', index: 1 });
    // Solids first, then panes — which is a pane's ONLY route to being selected,
    // since five pixels of wall is not a tap target.
    expect(cycleSelection({ list: 'solid', index: 1 }, counts, 1)).toEqual({ list: 'window', index: 0 });
    expect(cycleSelection({ list: 'window', index: 0 }, counts, 1)).toEqual({ list: 'solid', index: 0 });
  });

  it('goes backwards from the end', () => {
    expect(cycleSelection(null, counts, -1)).toEqual({ list: 'window', index: 0 });
    expect(cycleSelection({ list: 'solid', index: 0 }, counts, -1)).toEqual({ list: 'window', index: 0 });
  });

  it('answers nothing on an empty floor, so focus can leave', () => {
    expect(cycleSelection(null, { solids: 0, windows: 0 }, 1)).toBeNull();
  });
});

describe('the readout', () => {
  const kinds: FintechAdminKind[] = [
    { key: 'desk', label: 'стол' },
    { key: 'flower', label: 'цветок' },
  ];
  const draft: FintechLayoutDraft = {
    solids: [{ x: 4.5, y: 6, w: 2.5, h: 1, kind: 'desk' }],
    windows: [{ wall: 'top', at: 2, len: 4 }],
  };

  it('writes the selected object exactly as the tests read it', () => {
    expect(selectionReadout({ list: 'solid', index: 0 }, draft, kinds)).toBe(
      'стол · X 4,50 · Y 6,00 · Ш 2,50 · В 1,00',
    );
  });

  it('writes a pane along its own wall', () => {
    expect(selectionReadout({ list: 'window', index: 0 }, draft, kinds)).toBe(
      'окно · верх · от 2,00 · длина 4,00',
    );
  });

  it('says so when there is nothing selected, including an index off the end', () => {
    expect(selectionReadout(null, draft, kinds)).toBe('ничего не выбрано');
    expect(selectionReadout({ list: 'solid', index: 9 }, draft, kinds)).toBe('ничего не выбрано');
  });

  it('names a kind by the server’s word, and quotes one it was not given', () => {
    expect(kindLabel('desk', kinds)).toBe('стол');
    expect(kindLabel('printer', kinds)).toBe('printer');
    expect(kindLabel('', kinds)).toBe('предмет');
  });

  it('names the three walls and quotes a fourth', () => {
    expect(wallLabel('top')).toBe('верх');
    expect(wallLabel('left')).toBe('лево');
    expect(wallLabel('right')).toBe('право');
    expect(wallLabel('bottom')).toBe('bottom');
  });
});

describe('problemText', () => {
  it('has Russian for every code the validator can answer with', () => {
    // THE EXHAUSTIVENESS CHECK. The switch itself is guarded at compile time by
    // a `never`, and this is the runtime half: a code in the list with no
    // sentence would fall through to the unknown-code branch, which names the
    // code and would therefore contain it.
    for (const code of LAYOUT_PROBLEM_CODES) {
      const text = problemText(code);
      expect(text.length).toBeGreaterThan(0);
      expect(text).not.toContain(code);
      expect(text).not.toContain('непонятная');
    }
    expect(new Set(LAYOUT_PROBLEM_CODES.map(problemText)).size).toBe(LAYOUT_PROBLEM_CODES.length);
  });

  it('covers exactly the eight the server has', () => {
    // Pinned as a list rather than a count so that adding one to the server
    // without adding one here is a failure with a name in it.
    expect([...LAYOUT_PROBLEM_CODES]).toEqual([
      'bad_kind',
      'bad_size',
      'off_floor',
      'too_close',
      'spot_blocked',
      'split_floor',
      'too_many',
      'bad_window',
    ]);
  });

  it('quotes a code it has never heard of rather than going blank', () => {
    // A newer server must not be able to make the panel say nothing at all.
    expect(problemText('teleporter_in_the_way')).toContain('teleporter_in_the_way');
  });
});

describe('problemTarget and problemLine', () => {
  const kinds: FintechAdminKind[] = [{ key: 'desk', label: 'стол' }];
  const draft: FintechLayoutDraft = {
    solids: [
      { x: 1, y: 1, w: 2, h: 1, kind: 'desk' },
      { x: 4, y: 1, w: 2, h: 1, kind: 'desk' },
    ],
    windows: [{ wall: 'top', at: 2, len: 4 }],
  };

  it('sends a window problem to the windows and everything else to the solids', () => {
    expect(problemTarget({ problem: 'bad_window', index: 0 })).toEqual({ list: 'window', index: 0 });
    expect(problemTarget({ problem: 'too_close', index: 1 })).toEqual({ list: 'solid', index: 1 });
  });

  it('sends a problem about the whole floor to nothing in particular', () => {
    expect(problemTarget({ problem: 'split_floor', index: -1 })).toBeNull();
    expect(problemTarget({ problem: 'too_many', index: -1 })).toBeNull();
  });

  it('names the object in the list, counting from one', () => {
    // From one because the panel is read by a person looking at a plan, not by
    // somebody indexing an array.
    expect(problemLine({ problem: 'too_close', index: 1 }, draft, kinds)).toBe(
      'стол 2 — слишком близко к соседнему предмету — раздвиньте их',
    );
    expect(problemLine({ problem: 'bad_window', index: 0 }, draft, kinds)).toContain('окно 1 —');
    expect(problemLine({ problem: 'split_floor', index: -1 }, draft, kinds)).toContain('весь этаж —');
  });

  it('still says something about an index off the end of the draft', () => {
    // A superseded answer is dropped rather than rendered, but a payload from a
    // server this build has never met could carry anything at all.
    expect(problemLine({ problem: 'too_close', index: 9 }, draft, kinds)).toContain('предмет 10');
  });
});

describe('problemsFrom', () => {
  it('reads the list off a refusal body', () => {
    expect(problemsFrom({ error: 'layout_invalid', problems: [{ problem: 'too_close', index: 3 }] })).toEqual([
      { problem: 'too_close', index: 3 },
    ]);
  });

  it('tells «nothing wrong» apart from «nothing known»', () => {
    // The two mean opposite things: an empty list would enable a save, and an
    // unreadable body must not.
    expect(problemsFrom({ problems: [] })).toEqual([]);
    expect(problemsFrom(null)).toBeNull();
    expect(problemsFrom('boom')).toBeNull();
    expect(problemsFrom({ error: 'internal' })).toBeNull();
  });

  it('treats an explicitly null list as unknown rather than as clean', () => {
    // THE WIRE REALLY SENDS THIS. The validator answers a nil slice when it has
    // nothing to say and Go marshals that as `null`, so `{"problems": null}` is
    // what a CLEAN floor looks like on the check endpoint — and the caller there
    // reads the typed response and coalesces it. Here, on a 422, the same shape
    // would mean «refused, and we cannot say why»: answering `[]` would claim the
    // floor was legal and re-enable the button the server had just turned down.
    expect(problemsFrom({ error: 'layout_invalid', problems: null })).toBeNull();
  });

  it('drops entries it cannot read rather than the whole answer', () => {
    expect(
      problemsFrom({ problems: [{ problem: 'too_close', index: 1 }, 42, { index: 2 }, { problem: 'x' }] }),
    ).toEqual([{ problem: 'too_close', index: 1 }]);
  });
});

describe('draftFrom and sameLayout', () => {
  const layout = {
    solids: [{ x: 1, y: 2, w: 3, h: 1, kind: 'desk' }],
    windows: [{ wall: 'top', at: 2, len: 4 }],
  };

  it('copies rather than aliases, so dragging cannot edit the installed floor', () => {
    const draft = draftFrom(layout);
    draft.solids[0].x = 9;
    expect(layout.solids[0].x).toBe(1);
  });

  it('calls a fresh copy the same floor', () => {
    expect(sameLayout(draftFrom(layout), draftFrom(layout))).toBe(true);
  });

  it('notices a move, a resize, a retype, a deletion and a new pane', () => {
    // «ПРИМЕНИТЬ» is destructive, so an unchanged draft must never be savable —
    // which makes every one of these a difference that has to be seen.
    const move = draftFrom(layout);
    move.solids[0].x = 1.25;
    expect(sameLayout(draftFrom(layout), move)).toBe(false);

    const resize = draftFrom(layout);
    resize.solids[0].w = 3.25;
    expect(sameLayout(draftFrom(layout), resize)).toBe(false);

    const retype = draftFrom(layout);
    retype.solids[0].kind = 'tree';
    expect(sameLayout(draftFrom(layout), retype)).toBe(false);

    const removed = draftFrom(layout);
    removed.solids.splice(0, 1);
    expect(sameLayout(draftFrom(layout), removed)).toBe(false);

    const glazed = draftFrom(layout);
    glazed.windows.push({ wall: 'left', at: 3, len: 4 });
    expect(sameLayout(draftFrom(layout), glazed)).toBe(false);

    const moved = draftFrom(layout);
    moved.windows[0].at = 2.25;
    expect(sameLayout(draftFrom(layout), moved)).toBe(false);
  });
});

describe('installReport', () => {
  it('declines the noun and the verb with the number', () => {
    expect(installReport(1)).toBe('Этаж поставлен. Закончилась 1 смена.');
    expect(installReport(2)).toBe('Этаж поставлен. Закончились 2 смены.');
    expect(installReport(5)).toBe('Этаж поставлен. Закончилось 5 смен.');
    expect(installReport(21)).toBe('Этаж поставлен. Закончилась 21 смена.');
  });

  it('gives an empty office its own sentence', () => {
    // «Закончилось 0 смен» is grammatical and reads like a failure.
    expect(installReport(0)).toBe('Этаж поставлен. В офисе никого не было.');
    expect(installReport(Number.NaN)).toBe('Этаж поставлен. В офисе никого не было.');
    expect(installReport(-3)).toBe('Этаж поставлен. В офисе никого не было.');
  });
});
