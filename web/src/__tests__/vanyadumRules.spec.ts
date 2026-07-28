import { describe, expect, it } from 'vitest';
import { VANYADUM_PROSE, buildRules, pickupLine } from '../lib/vanyadumRules';
import type { VanyadumConfig } from '../api/types';

/**
 * The splash-screen cheatsheet.
 *
 * `CLAUDE.md` makes this a gate rather than a nicety: every game states its
 * current rules on its own splash screen, and a rule that only exists in
 * `content.go` is a rule nobody playing the game knows. These tests are what
 * make "derived from the catalogue" true rather than aspirational — retune a
 * constant on the server and the screen must follow it with no frontend change.
 */

const config: VanyadumConfig = {
  player: {
    radius: 0.35,
    eye_height: 1.65,
    walk_speed: 5,
    max_step: 0.6,
    max_pitch: 1.5,
    max_health: 100,
    start_health: 100,
  },
  pickups: [
    {
      key: 'beer',
      title: 'пиво',
      icon: '🍺',
      grants: 'beer',
      amount: 1,
      max: 9,
      tint: '#c8892f',
      blurb: 'Заливаешь — и панчи сами идут.',
    },
  ],
  surfaces: [],
  sim: {
    hz: 20,
    snapshot_hz: 20,
    input_hz: 10,
    max_commands: 4,
    max_step_seconds: 0.2,
    redundant: 6,
    interp_delay_ms: 120,
    collision_passes: 3,
  },
};

describe('buildRules', () => {
  it('says nothing at all before the catalogue has arrived', () => {
    expect(buildRules(null)).toEqual([]);
  });

  it('takes the numbers from the catalogue rather than from this file', () => {
    const retuned = {
      ...config,
      player: { ...config.player, walk_speed: 9.5, max_step: 1.2, start_health: 40 },
    };
    const text = buildRules(retuned)
      .flatMap((b) => b.lines)
      .map((l) => l.text)
      .join(' ');
    expect(text).toContain('9,5 м/с');
    expect(text).toContain('120 см');
    expect(text).toContain('40 из 100');
  });

  it('describes every pickup the catalogue carries, and only those', () => {
    const two = {
      ...config,
      pickups: [
        ...config.pickups,
        {
          key: 'syringe',
          title: 'шприц',
          icon: '💉',
          grants: 'health',
          amount: 25,
          max: 0,
          tint: '#c9d6d2',
          blurb: 'В руку и на поршень.',
        },
      ],
    };
    const block = buildRules(two).find((b) => b.title === 'Что валяется');
    expect(block?.lines).toHaveLength(2);
    expect(block?.lines[1].label).toContain('шприц');
    expect(block?.lines[1].text).toContain('В руку и на поршень.');
  });

  it('omits the pickup block entirely when there is nothing to pick up', () => {
    const bare = buildRules({ ...config, pickups: [] });
    expect(bare.some((b) => b.title === 'Что валяется')).toBe(false);
  });

  it('marks the hand-written block, and only that one', () => {
    const blocks = buildRules(config);
    const prose = blocks.filter((b) => b.prose);
    expect(prose).toHaveLength(1);
    expect(prose[0].lines).toBe(VANYADUM_PROSE);
  });

  it('never renders an empty line', () => {
    for (const block of buildRules(config)) {
      expect(block.title.length).toBeGreaterThan(0);
      for (const line of block.lines) {
        expect(line.label.trim().length).toBeGreaterThan(0);
        expect(line.text.trim().length).toBeGreaterThan(0);
      }
    }
  });
});

describe('pickupLine', () => {
  it('states what it grants and what the ceiling is', () => {
    const line = pickupLine(config.pickups[0]);
    expect(line.label).toBe('🍺 пиво');
    expect(line.text).toContain('+1');
    expect(line.text).toContain('максимум 9');
  });

  it('says nothing about a ceiling when there is not one', () => {
    const line = pickupLine({ ...config.pickups[0], max: 0 });
    expect(line.text).not.toContain('максимум');
  });

  it('tells the player it is picked up by walking, because there is no button', () => {
    expect(pickupLine(config.pickups[0]).text).toContain('наступишь');
  });
});
