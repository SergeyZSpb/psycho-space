import { describe, expect, it } from 'vitest';
import { recordExchange } from '../lib/transcript';

describe('transcript entries', () => {
  it('records the offered options alongside the reply', () => {
    const ex = recordExchange('привет', 'ну чё', ['a', 'b', 'c', 'd']);
    expect(ex).toEqual({ choice: 'привет', reply: 'ну чё', options: ['a', 'b', 'c', 'd'] });
  });

  it('normalises a missing options list to an empty array', () => {
    // The judge returns no options on the final turn (won or lost), and the field
    // must still be an array so the backend never sees a null.
    expect(recordExchange('привет', 'заходи', undefined).options).toEqual([]);
    expect(recordExchange('привет', 'заходи', null).options).toEqual([]);
    expect(recordExchange('привет', 'заходи', []).options).toEqual([]);
  });
});
