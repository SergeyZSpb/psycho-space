import { describe, expect, it } from 'vitest';
import { recordExchange } from '../lib/transcript';

describe('transcript entries', () => {
  it('records the offered options and the tension alongside the reply', () => {
    const ex = recordExchange('привет', 'ну чё', ['a', 'b', 'c', 'd'], 55);
    expect(ex).toEqual({
      choice: 'привет',
      reply: 'ну чё',
      options: ['a', 'b', 'c', 'd'],
      anger: 55,
    });
  });

  it('normalises a missing options list to an empty array', () => {
    // The judge returns no options on the final turn (won or lost), and the field
    // must still be an array so the backend never sees a null.
    expect(recordExchange('привет', 'заходи', undefined, 0).options).toEqual([]);
    expect(recordExchange('привет', 'заходи', null, 0).options).toEqual([]);
    expect(recordExchange('привет', 'заходи', [], 90).options).toEqual([]);
    // The tension is still recorded on a final turn.
    expect(recordExchange('привет', 'заходи', [], 90).anger).toBe(90);
  });
});
