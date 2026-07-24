import { describe, expect, it } from 'vitest';
import { BOARDS, boardMeta } from '../lib/gameBoards';

describe('game record boards', () => {
  it('covers exactly the four boards the backend serves, in display order', () => {
    expect(BOARDS.map((b) => b.key)).toEqual([
      'longest_win',
      'shortest_win',
      'longest_loss',
      'shortest_loss',
    ]);
  });

  it('keeps tab labels short enough for four tabs at 360px', () => {
    for (const b of BOARDS) {
      // ~90px per tab at 360px; anything past ~10 chars starts wrapping.
      expect(b.tab.length).toBeLessThanOrEqual(10);
      expect(b.caption.length).toBeGreaterThan(0);
      expect(b.empty.length).toBeGreaterThan(0);
    }
  });

  it('tells win and loss boards apart in their empty state', () => {
    expect(boardMeta('longest_win').empty).toContain('прошёл');
    expect(boardMeta('shortest_loss').empty).toContain('проигрывал');
  });

  it('falls back to the first board for an unknown key', () => {
    expect(boardMeta('nope').key).toBe('longest_win');
  });
});
