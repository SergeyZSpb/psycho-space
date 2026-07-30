import { describe, expect, it } from 'vitest';
import { FINTECH_LORE, FINTECH_PROSE, buildRules, endingFor } from '../lib/fintechRules';
import type { FintechConfig } from '../api/types';

/**
 * The splash-screen cheatsheet.
 *
 * `CLAUDE.md` makes this a gate rather than a nicety: every game states its
 * current rules on its own splash screen, and a rule that only exists in
 * `content.go` is a rule nobody playing the game knows. These tests are what
 * make "derived from the catalogue" true rather than aspirational — retune a
 * constant on the server and the screen must follow it with no frontend change.
 */

const config: FintechConfig = {
  game_key: 'fintech',
  title: 'СИМУЛЯТОР ФИНТЕХА',
  office: {
    w: 16,
    h: 22,
    desks: [{ x: 2, y: 3, w: 3.4, h: 1.2 }],
    player_radius: 0.35,
    boss_radius: 0.4,
  },
  money: { base_per_second: 120, ramp_seconds: 6, max_multiplier: 3, grace_ms: 300 },
  move: {
    walk_speed: 3.2,
    dash_speed: 9,
    dash_ms: 220,
    dash_cooldown_ms: 4000,
    input_hz: 10,
    max_commands: 4,
  },
  boss: { speed: 2.35, catch_radius: 0.85, grin_range: 6 },
  sim: { hz: 20, snapshot_hz: 10 },
  endings: [
    { key: 'promoted', title: 'ТЕБЯ ПОВЫСИЛИ', sub: 'поздравляем. теперь ты за это отвечаешь.' },
    { key: 'left', title: 'ТЫ ПРОСТО УШЁЛ', sub: 'смена засчитана. никто не заметил.' },
  ],
  boss_lines: ['А ГДЕ?'],
  personas: ['Карен', 'Андрюха', 'Саня', 'Даша'],
  player_lines: ['Я КАРЕН', 'Я ПРОСТО ВОДЫ ПОПИТЬ', 'Я НА ВСТРЕЧУ'],
  max_occupants: 3,
};

const allText = (c: FintechConfig | null) =>
  buildRules(c)
    .flatMap((b) => b.lines)
    .map((l) => `${l.label} ${l.text}`)
    .join(' | ');

describe('buildRules', () => {
  it('says nothing at all before the catalogue has arrived', () => {
    expect(buildRules(null)).toEqual([]);
  });

  it('takes every number from the catalogue rather than from this file', () => {
    // The whole point of the arrangement. These are not the production numbers,
    // and the layout suite's stub uses a third set for the same reason: a
    // hand-typed cheatsheet cannot pass any of them.
    const retuned: FintechConfig = {
      ...config,
      money: { base_per_second: 777, ramp_seconds: 9, max_multiplier: 4, grace_ms: 450 },
      move: { ...config.move, walk_speed: 4.4, dash_speed: 11.5, dash_ms: 330, dash_cooldown_ms: 5500 },
      boss: { speed: 2.9, catch_radius: 1.25, grin_range: 7 },
    };
    const text = allText(retuned);
    expect(text).toContain('777 ₽');
    expect(text).toContain('9 с');
    expect(text).toContain('×4');
    expect(text).toContain('0,45 с');
    expect(text).toContain('4,4 м/с');
    expect(text).toContain('11,5 м/с');
    expect(text).toContain('0,33 с');
    expect(text).toContain('5,5 с');
    expect(text).toContain('2,9 м/с');
    expect(text).toContain('1,25 м');
  });

  it('states both consequences of the dash, because that is the whole game', () => {
    const dash = buildRules(config)
      .flatMap((b) => b.lines)
      .find((l) => l.label.includes('рывок'));
    expect(dash?.text).toContain('не сбивает');
    expect(dash?.text).toContain('денег не приносит');
  });

  it('names both endings, with the catalogue’s own words', () => {
    const block = buildRules(config).find((b) => b.title === 'Чем кончится');
    expect(block?.lines).toHaveLength(2);
    expect(block?.lines[0].label).toBe('ТЕБЯ ПОВЫСИЛИ');
    expect(block?.lines[0].text).toBe('поздравляем. теперь ты за это отвечаешь.');
    expect(block?.lines[1].label).toBe('ТЫ ПРОСТО УШЁЛ');
  });

  it('grows an ending block when the catalogue grows one', () => {
    // Iteration 4 adds `exposed`. It must arrive with no frontend deploy.
    const later: FintechConfig = {
      ...config,
      endings: [...config.endings, { key: 'exposed', title: 'ТЕБЯ СПАЛИЛИ', sub: 'ну и ладно.' }],
    };
    const block = buildRules(later).find((b) => b.title === 'Чем кончится');
    expect(block?.lines.map((l) => l.label)).toContain('ТЕБЯ СПАЛИЛИ');
  });

  it('marks the hand-written block, and only that one', () => {
    const prose = buildRules(config).filter((b) => b.prose);
    expect(prose).toHaveLength(1);
    expect(prose[0].lines).toBe(FINTECH_PROSE);
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

  it('drops a block whose half of the catalogue is missing, and never throws', () => {
    // This runs against a server that may be a version ahead of the browser, and
    // a splash that threw would take the start button down with it.
    const partial = { ...config } as unknown as Record<string, unknown>;
    delete partial.money;
    delete partial.boss;
    delete partial.endings;
    const blocks = buildRules(partial as unknown as FintechConfig);
    // «Коллеги» survives because ITS half of the catalogue did: the block is
    // derived from max_occupants, which is still here. That is the claim — a
    // block is dropped when its own input is missing, not when a neighbour's is.
    expect(blocks.map((b) => b.title)).toEqual(['Ноги', 'Коллеги', 'Коротко']);
  });

  it('still says how to play when the catalogue says nothing at all', () => {
    // The prose is the one block that cannot go missing, because it is the only
    // thing on the screen that explains the controls.
    const blocks = buildRules({} as unknown as FintechConfig);
    expect(blocks).toHaveLength(1);
    expect(blocks[0].prose).toBe(true);
  });
});

describe('the hand-written copy', () => {
  it('is four lines and says what each of them has to', () => {
    expect(FINTECH_PROSE).toHaveLength(4);
    const text = FINTECH_PROSE.map((l) => l.text).join(' ');
    // The controls, which the server has no opinion about and does not publish.
    expect(text).toContain('Стик слева');
    expect(text).toContain('рывок');
    // The shape of the game, which is not a number and never will be.
    expect(text).toContain('Стоишь — капает');
    expect(text).toContain('Лысый подходит и здоровается');
    // What is over a head is a READOUT rather than decoration, and the splash
    // has to say so — the words themselves are derived from the catalogue, but
    // that they mean something is not a number and cannot be.
    expect(text).toContain('Реплика — это индикатор');
  });

  it('sets up the joke before the numbers do', () => {
    expect(FINTECH_LORE).toContain('не работать и получать за это деньги');
    expect(FINTECH_LORE).toContain('лысый');
  });
});

describe('endingFor', () => {
  it('finds the catalogue’s words for a cause', () => {
    expect(endingFor(config, 'promoted')?.title).toBe('ТЕБЯ ПОВЫСИЛИ');
    expect(endingFor(config, 'left')?.sub).toBe('смена засчитана. никто не заметил.');
  });

  it('knows nothing about a cause the catalogue has not named', () => {
    // Adding one is a catalogue change, never a deploy — so the honest answer to
    // an unknown cause is "no idea", and the view falls back to a neutral title.
    expect(endingFor(config, 'exposed')).toBeUndefined();
    expect(endingFor(null, 'left')).toBeUndefined();
  });
});

describe('the colleagues block', () => {
  it('takes the office capacity from the catalogue rather than typing it', () => {
    // The cap is retunable server-side, and a hand-typed number is a number that
    // goes stale the first time somebody changes it.
    const blocks = buildRules({ ...config, max_occupants: 7 });
    const colleagues = blocks.find((b) => b.title === 'Коллеги');
    expect(colleagues).toBeDefined();
    expect(colleagues!.lines.some((l) => l.text.includes('7'))).toBe(true);
    // AND IT SAYS WHICH FIGURE IS YOU. The office now holds several figures built
    // identically, every one wearing a real photograph, and the only thing marking
    // yours is a white ellipse — a rule a player needs before starting, so it
    // belongs on the splash screen. Hardcoded rather than derived, because the
    // marker is a stylesheet rule and the catalogue carries no number for it.
    expect(colleagues!.lines.some((l) => l.text.includes('белом круге'))).toBe(true);
    // AND WHO YOU MIGHT BE, derived from the served cast rather than typed — so
    // retuning the cast updates the screen by itself, which is the whole reason the
    // names are in the catalogue and only an index is on the wire.
    expect(colleagues!.lines.some((l) => l.text.includes('Андрюха'))).toBe(true);
    expect(colleagues!.lines.some((l) => l.text.includes('Даша'))).toBe(true);
  });

  it('says nothing about colleagues in an office that holds one person', () => {
    const blocks = buildRules({ ...config, max_occupants: 1 });
    expect(blocks.find((b) => b.title === 'Коллеги')).toBeUndefined();
  });
});

describe('the redirect verb on the cheatsheet', () => {
  it('takes its label, its words and both timers from the catalogue', () => {
    const blocks = buildRules({
      ...config,
      redirect: { label: 'ЭТО К НЕМУ', say: 'ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО', seconds_ms: 6000, cooldown_ms: 20000 },
    });
    const verb = blocks.find((b) => b.title === 'ЭТО К НЕМУ');
    expect(verb).toBeDefined();
    const text = verb!.lines.map((l) => l.text).join(' ');
    expect(text).toContain('6');
    expect(text).toContain('20');
    expect(text).toContain('ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО');
  });

  it('says nothing about it when the server does not publish it', () => {
    // A server a version behind the browser is a supported state; a splash that
    // threw would take the start button down with it.
    const partial = { ...config } as Record<string, unknown>;
    delete partial.redirect;
    expect(buildRules(partial as never).some((b) => b.title === 'ЭТО К НЕМУ')).toBe(false);
  });
});
