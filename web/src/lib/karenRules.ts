/**
 * The rules cheatsheet «СИМУЛЯТОР КАРЕНА» shows before you clock in.
 *
 * WHY IT IS DERIVED RATHER THAN WRITTEN OUT. `CLAUDE.md` requires every game to
 * state its current rules on its own splash screen — the actual numbers and the
 * actual consequences, because the audience is a handful of friends who will
 * open this once, on a phone, with no intention of reading anything else. A rule
 * that only exists in `content.go` is a rule nobody playing the game knows.
 *
 * So the numbers come from `GET /api/game-karen/config`, which already carries
 * the base rate, the ramp, the cap, the grace window, both speeds, the dash and
 * the boss. Typing them out here would be a second copy of the catalogue, wrong
 * the first afternoon somebody retuned a constant, and silently wrong because
 * nothing compares the two. The layout suite's stub carries deliberately
 * non-production numbers for exactly that reason: a hand-typed line fails there.
 *
 * The part that CANNOT be derived is at the bottom of this file: read the
 * comment on KAREN_PROSE before adding a line to it.
 *
 * Everything here is pure, which is the point — the template renders rows and
 * never builds a sentence, so the interesting half of the splash is testable
 * without a browser.
 */

import { decimal } from './karenPlane';
import type { KarenConfig } from '../api/types';

/** One line of the cheatsheet. */
export interface RuleLine {
  /** A short label, usually an icon and a couple of words. */
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

/** Is this something the catalogue actually gave us a number for? */
function has(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v);
}

/**
 * Builds the whole cheatsheet.
 *
 * Returns blocks rather than a flat list so the splash can lay them out without
 * knowing what is in them, and so a block whose half of the catalogue has not
 * arrived — or was never served — simply does not appear rather than rendering
 * `NaN м/с`. That tolerance is not defensive decoration: this runs against a
 * server that may be a version ahead of the browser, and a splash that threw
 * would take the start button down with it.
 */
export function buildRules(config: KarenConfig | null): RuleBlock[] {
  if (!config) return [];
  const blocks: RuleBlock[] = [];

  const money = config.money;
  if (money && has(money.base_per_second) && has(money.ramp_seconds) && has(money.max_multiplier)) {
    const lines: RuleLine[] = [
      {
        label: '💸 ставка',
        text: `${decimal(money.base_per_second, 0)} ₽ в секунду — пока стоишь и ничего не делаешь.`,
      },
      {
        label: '📈 разгон',
        text: `Простоял ${decimal(money.ramp_seconds)} с — ставка выросла до ×${decimal(
          money.max_multiplier,
        )}. Пошёл — всё сначала.`,
      },
    ];
    if (has(money.grace_ms)) {
      lines.push({
        label: '⏳ поблажка',
        text: `Движение короче ${decimal(money.grace_ms / 1000, 2)} с множитель не сбивает.`,
      });
    }
    blocks.push({ title: 'Деньги', lines });
  }

  const move = config.move;
  if (move && has(move.walk_speed) && has(move.dash_speed)) {
    const lines: RuleLine[] = [
      { label: '🚶 шаг', text: `${decimal(move.walk_speed)} м/с. Пока идёшь — не капает.` },
    ];
    if (has(move.dash_ms) && has(move.dash_cooldown_ms)) {
      lines.push({
        label: '💨 рывок',
        text:
          `${decimal(move.dash_speed)} м/с, ${decimal(move.dash_ms / 1000, 2)} с, ` +
          `перезарядка ${decimal(move.dash_cooldown_ms / 1000)} с. ` +
          `Множитель не сбивает и денег не приносит.`,
      });
    }
    blocks.push({ title: 'Ноги', lines });
  }

  const boss = config.boss;
  if (boss && has(boss.speed) && has(boss.catch_radius)) {
    blocks.push({
      title: 'Лысый',
      lines: [
        {
          label: '👨‍🦲 скорость',
          text: `${decimal(boss.speed)} м/с. Медленнее твоего шага — но он не устаёт.`,
        },
        {
          label: '🤝 радиус',
          text: `Подойдёт на ${decimal(boss.catch_radius, 2)} м — и смена кончилась.`,
        },
      ],
    });
  }

  const endings = Array.isArray(config.endings) ? config.endings : [];
  const endingLines = endings
    .filter((e) => e && typeof e.title === 'string' && e.title.length > 0)
    .map((e) => ({ label: e.title, text: typeof e.sub === 'string' ? e.sub : '—' }));
  if (endingLines.length > 0) {
    blocks.push({ title: 'Чем кончится', lines: endingLines });
  }

  blocks.push({ title: 'Коротко', prose: true, lines: KAREN_PROSE });

  return blocks;
}

/**
 * The part that cannot be derived, and the part a rules change must come back
 * and edit BY HAND.
 *
 * Everything here is either a fact about the CONTROLS — which the server does
 * not publish because it has no opinion about thumbs — or the one-sentence shape
 * of the game, which is not a number and never will be.
 *
 * If you add a mechanic, ask first whether the catalogue could carry it. It
 * usually can, and then it belongs above rather than here.
 */
export const KAREN_PROSE: RuleLine[] = [
  {
    label: 'Как играть',
    text: 'Стик слева — идти. Кнопка справа — рывок: короткий, неуязвимый, и не сбивает множитель.',
  },
  {
    label: 'Смысл',
    text: 'Стоишь — капает. Бежишь — не капает. Стоишь дольше — капает сильнее.',
  },
  {
    label: 'Как это кончается',
    text: 'Лысый подходит и здоровается. Всё.',
  },
];

/**
 * The lore paragraph under the title.
 *
 * Here rather than in the template for the same reason the prose block is: it is
 * hand-written copy, so it lives beside the other hand-written copy where a
 * rules change can find all of it at once.
 */
export const KAREN_LORE =
  'Ты — Карен. Работа у тебя простая: не работать и получать за это деньги. ' +
  'Зарплата капает, только пока ты стоишь и ничего не делаешь, — а по офису ' +
  'ходит лысый и очень тебе рад.';

/**
 * The ending the catalogue describes for this cause, or nothing.
 *
 * THE TITLE AND THE SUB COME FROM THE SERVER, ALWAYS. The client knows the cause
 * — it is on the `karen_over` frame, and when you walk out it is the client that
 * did the walking — but it does not know what that cause is called, and it must
 * not: adding `exposed` in a later iteration is a catalogue change, not a
 * deploy.
 */
export function endingFor(config: KarenConfig | null, cause: string) {
  if (!config || !Array.isArray(config.endings)) return undefined;
  return config.endings.find((e) => e?.key === cause);
}
