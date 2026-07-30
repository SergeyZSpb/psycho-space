/**
 * The rules cheatsheet «СИМУЛЯТОР ФИНТЕХА» shows before you clock in.
 *
 * WHY IT IS DERIVED RATHER THAN WRITTEN OUT. `CLAUDE.md` requires every game to
 * state its current rules on its own splash screen — the actual numbers and the
 * actual consequences, because the audience is a handful of friends who will
 * open this once, on a phone, with no intention of reading anything else. A rule
 * that only exists in `content.go` is a rule nobody playing the game knows.
 *
 * So the numbers come from `GET /api/game-fintech/config`, which already carries
 * the base rate, the ramp, the cap, the grace window, both speeds, the dash and
 * the boss. Typing them out here would be a second copy of the catalogue, wrong
 * the first afternoon somebody retuned a constant, and silently wrong because
 * nothing compares the two. The layout suite's stub carries deliberately
 * non-production numbers for exactly that reason: a hand-typed line fails there.
 *
 * The part that CANNOT be derived is at the bottom of this file: read the
 * comment on FINTECH_PROSE before adding a line to it.
 *
 * Everything here is pure, which is the point — the template renders rows and
 * never builds a sentence, so the interesting half of the splash is testable
 * without a browser.
 */

import { decimal } from './fintechPlane';
import type { FintechConfig } from '../api/types';

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
export function buildRules(config: FintechConfig | null): RuleBlock[] {
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

  // The office is SHARED, and until peers were drawn there was no way for a
  // player to find that out. `max_occupants` has been in the served catalogue
  // since iteration 1 and nothing read it — deriving the line from it rather
  // than typing the number keeps it true when the cap is retuned.
  if (has(config.max_occupants) && config.max_occupants > 1) {
    blocks.push({
      title: 'Коллеги',
      lines: [
        {
          label: '🧑‍🤝‍🧑 в опенспейсе',
          text:
            `До ${config.max_occupants} Каренов одновременно. ` +
            `Видно, кто где стоит и кто что говорит.`,
        },
        {
          label: '🎯 кто ближе',
          text: 'Лысый идёт к ближайшему. Отойди — и пойдёт не к тебе.',
        },
        // WHICH FIGURE IS YOU, and this line is HARDCODED because nothing in the
        // catalogue carries it — the marker is a stylesheet rule, not a served
        // number. It exists for the same reason the line above it does: the
        // office now holds several figures built identically, every one of them
        // wearing a real photograph, and the only thing separating yours is a
        // white ellipse that nothing on screen explains. A rule the player needs
        // in order to play belongs on the splash screen.
        {
          label: '⚪ где ты',
          text: 'Ты — тот, кто стоит в белом круге. Лица настоящие, из профиля.',
        },
        ...(Array.isArray(config.personas) && config.personas.length > 1
          ? [
              {
                label: '🎲 кем ты будешь',
                text:
                  `Каждая смена — кто-то из своих: ${config.personas.join(', ')}. ` +
                  `Выбирает не ты. На игру не влияет — только на то, как ты себя представляешь.`,
              },
            ]
          : []),
      ],
    });
  }

  // The verb, generated from what the server publishes — the label, the words
  // and both timers — so retuning any of them updates the screen by itself.
  const redirect = config.redirect;
  if (redirect && has(redirect.seconds_ms) && has(redirect.cooldown_ms)) {
    blocks.push({
      title: redirect.label,
      lines: [
        {
          label: '🫱 перевести стрелки',
          text:
            `Ткни в коллегу — и лысый пойдёт к нему ${decimal(redirect.seconds_ms / 1000)} с, ` +
            `кто бы ни был ближе. Перезарядка ${decimal(redirect.cooldown_ms / 1000)} с.`,
        },
        { label: '🗣 и все услышат', text: `Над тобой загорится «${redirect.say}».` },
      ],
    });
  }

  // The bottle, generated from what the server publishes — how long he wobbles,
  // how slow he gets, and how long until there is another one.
  const bottle = config.bottle;
  if (bottle && has(bottle.drunk_ms) && has(bottle.return_ms)) {
    blocks.push({
      title: 'Бутылка',
      lines: [
        {
          label: '🍾 набухать лысого',
          text:
            `Дойди до бутылки — он выпьет, позеленеет и поплывёт: ` +
            `${decimal(bottle.drunk_ms / 1000)} с на ${bottle.slow_pct} % скорости.`,
        },
        {
          label: '🚶 но он всё равно идёт',
          text:
            `Шатается, а не стоит. Через ${decimal(bottle.return_ms / 1000)} с ` +
            `появится новая — в другом месте.`,
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

  blocks.push({ title: 'Коротко', prose: true, lines: FINTECH_PROSE });

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
export const FINTECH_PROSE: RuleLine[] = [
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
  {
    // NOT derivable, and it is a rule rather than flavour: what is over a head
    // is a readout. Yours says what the simulation thinks you are doing — so
    // «Я КАРЕН» means the money is running and anything else means it is not —
    // and his changes the moment he is close enough to be a problem, which is
    // the same instant his face does. The lines themselves ARE derived: they
    // come from the catalogue's `player_lines` and `boss_lines`.
    label: 'Что над головой',
    text: 'Реплика — это индикатор. Своя говорит, что с тобой сейчас, а лысый меняет свою, когда подошёл близко.',
  },
];

/**
 * The lore paragraph under the title.
 *
 * Here rather than in the template for the same reason the prose block is: it is
 * hand-written copy, so it lives beside the other hand-written copy where a
 * rules change can find all of it at once.
 */
export const FINTECH_LORE =
  'Ты — Карен. Работа у тебя простая: не работать и получать за это деньги. ' +
  'Зарплата капает, только пока ты стоишь и ничего не делаешь, — а по офису ' +
  'ходит лысый и очень тебе рад.';

/**
 * The disclaimer, and it is the one line on this screen that is not a joke.
 *
 * The game is named after somebody, the лысый is recognisably somebody, and the
 * whole thing is played by the handful of people who would recognise both. So it
 * says plainly that the cast is invented — no wordplay, no undercutting punch
 * line, because a disclaimer that reads as part of the bit is not a disclaimer.
 *
 * A CLIENT CONSTANT RATHER THAN A SERVED ONE, following `FINTECH_LORE` directly
 * above it: the catalogue serves what can be RETUNED without a deploy — the
 * speeds, the ramp, the endings, his lines — and this is prose that will never
 * change. A config key with exactly one possible value is not a config key.
 */
export const FINTECH_DISCLAIMER = 'Все персонажи вымышлены, любые совпадения случайны.';

/**
 * The ending the catalogue describes for this cause, or nothing.
 *
 * THE TITLE AND THE SUB COME FROM THE SERVER, ALWAYS. The client knows the cause
 * — it is on the `fintech_over` frame, and when you walk out it is the client that
 * did the walking — but it does not know what that cause is called, and it must
 * not: adding `exposed` in a later iteration is a catalogue change, not a
 * deploy.
 */
export function endingFor(config: FintechConfig | null, cause: string) {
  if (!config || !Array.isArray(config.endings)) return undefined;
  return config.endings.find((e) => e?.key === cause);
}
