/**
 * The rules cheatsheet «ВАНЯДУМ» shows before you go in.
 *
 * WHY IT IS DERIVED RATHER THAN WRITTEN OUT. `CLAUDE.md` requires every game to
 * state its current rules on its own splash screen — the actual numbers and the
 * actual consequences, because the audience is a handful of friends who will
 * open this once, on a phone, with no intention of reading anything else. A rule
 * that only exists in `content.go` is a rule nobody playing the game knows.
 *
 * So the numbers come from `GET /api/game-vanyadum/config`, which already
 * carries the player's speed, what each pickup grants and how much of it, and
 * the rates the client has to match. Typing them out here would be a second copy
 * of the catalogue, wrong the first afternoon somebody retuned a constant, and
 * silently wrong because nothing compares the two.
 *
 * The part that CANNOT be derived is at the bottom of this file: read the
 * comment on VANYADUM_PROSE before adding a line to it.
 *
 * Everything here is pure and has no side effects, which is the point: the
 * template renders rows and never builds a sentence, so the interesting half of
 * the splash is testable without a browser.
 */

import type { VanyadumConfig, VanyadumPickupKind } from '../api/types';

/** One line of the cheatsheet. */
export interface RuleLine {
  /** A short label, usually an icon or a couple of words. */
  label: string;
  /** The rule itself. */
  text: string;
}

/** A titled block of lines. */
export interface RuleBlock {
  title: string;
  lines: RuleLine[];
  /**
   * True when the block's contents were written by a human rather than derived
   * from the catalogue. Rendered no differently — it is here so a test can
   * assert that the derived blocks really are derived.
   */
  prose?: boolean;
}

/** Formats a number without a trailing `.0`, in the Russian convention. */
function num(v: number, digits = 1): string {
  const rounded = Number(v.toFixed(digits));
  return String(rounded).replace('.', ',');
}

/** «пиво» → «пиво (+1, максимум 9)» — the whole line derived from the entry. */
export function pickupLine(p: VanyadumPickupKind): RuleLine {
  const parts = [`+${p.amount}`];
  if (p.max > 0) parts.push(`максимум ${p.max}`);
  return {
    label: `${p.icon} ${p.title}`,
    text: `${p.blurb} Подбирается сам, когда наступишь (${parts.join(', ')}).`,
  };
}

/**
 * Builds the whole cheatsheet.
 *
 * Returns blocks rather than a flat list so the splash can lay them out without
 * knowing what is in them, and so a block that turns out to be empty — a
 * catalogue with no pickups in it, say — simply does not appear.
 */
export function buildRules(config: VanyadumConfig | null): RuleBlock[] {
  if (!config) return [];
  const blocks: RuleBlock[] = [];

  blocks.push({
    title: 'Ваня',
    lines: [
      {
        label: '🏃 скорость',
        text: `${num(config.player.walk_speed)} м/с. Бегать быстрее не выйдет — сервер считает сам.`,
      },
      {
        label: '🪜 ступенька',
        text: `Заходит на ${num(config.player.max_step * 100, 0)} см без прыжка. Выше — стена.`,
      },
      {
        label: '♥ здоровье',
        text: `${config.player.start_health} из ${config.player.max_health}. Пока отнять его некому.`,
      },
    ],
  });

  if (config.pickups.length > 0) {
    blocks.push({
      title: 'Что валяется',
      lines: config.pickups.map(pickupLine),
    });
  }

  blocks.push({
    title: 'Как играть',
    prose: true,
    lines: VANYADUM_PROSE,
  });

  return blocks;
}

/**
 * The part that cannot be derived, and the part a rules change must come back
 * and edit BY HAND.
 *
 * Everything here is either a fact about the controls — which the server does
 * not publish because it has no opinion about thumbs — or a fact about this
 * iteration's objective, which is deliberately the smallest one that closes the
 * loop and will be replaced by the keys and the locked door.
 *
 * If you add a mechanic, ask first whether the catalogue could carry it. It
 * usually can, and then it belongs above rather than here.
 */
export const VANYADUM_PROSE: RuleLine[] = [
  { label: '👈 слева', text: 'Держи палец и веди — Ваня идёт. Стик появляется там, где ты нажал.' },
  { label: '👉 справа', text: 'Веди пальцем — смотришь по сторонам.' },
  {
    label: '⌨️ на компьютере',
    text: 'WASD — идти. Клик по экрану захватывает мышь, дальше смотришь ей как в любом шутере. Esc — отпустить.',
  },
  { label: '🍺 цель', text: 'Собрать всё пиво на заброшке. Всё — значит всё, потом забег закончится.' },
  { label: '📡 связь', text: 'Мир считает сервер. Порвётся связь — Ваня постоит на месте и дождётся.' },
];
