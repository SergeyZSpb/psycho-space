import { describe, expect, it } from 'vitest';
import { recordExchange } from '../lib/transcript';
import type { GameTurnResult } from '../api/types';

function turn(over: Partial<GameTurnResult> = {}): GameTurnResult {
  return {
    reply: 'ну чё',
    art: 'vanya_angry',
    achieved: false,
    game_over: false,
    anger: 55,
    themes_done: ['sahur'],
    options: ['a', 'b', 'c', 'd'],
    ...over,
  };
}

describe('transcript entries', () => {
  it('records the whole judged turn, not just the prose', () => {
    // The backend replays these as JSON examples of the required output format, so
    // every field the model must produce has to survive the round trip.
    expect(recordExchange('привет', turn())).toEqual({
      choice: 'привет',
      reply: 'ну чё',
      art: 'vanya_angry',
      anger: 55,
      themes_done: ['sahur'],
      options: ['a', 'b', 'c', 'd'],
    });
  });

  it('normalises missing lists to empty arrays', () => {
    // The judge returns no options on the final turn (won or lost), and the fields
    // must still be arrays so the backend never sees a null.
    const ex = recordExchange('привет', turn({
      options: undefined as unknown as string[],
      themes_done: undefined as unknown as string[],
      art: undefined as unknown as string,
      anger: 90,
    }));
    expect(ex.options).toEqual([]);
    expect(ex.themes_done).toEqual([]);
    expect(ex.art).toBe('');
    expect(ex.anger).toBe(90);
  });
});
