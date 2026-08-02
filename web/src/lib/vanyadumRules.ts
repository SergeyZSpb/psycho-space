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
 * carries the player's speed, what each pickup grants and how much of it, how
 * many people fit in the заброшка and how long a thing takes to come back.
 * Typing them out here would be a second copy of the catalogue, wrong the first
 * afternoon somebody retuned a constant, and silently wrong because nothing
 * compares the two.
 *
 * The part that CANNOT be derived is at the bottom of this file: read the
 * comment on VANYADUM_PROSE before adding a line to it.
 *
 * Everything here is pure and has no side effects, which is the point: the
 * template renders rows and never builds a sentence, so the interesting half of
 * the splash is testable without a browser.
 */

import type { VanyadumConfig, VanyadumPickupKind } from '../api/types';

/**
 * The catalogue entry the gun's ammunition comes from, or null.
 *
 * THE JOIN IS THE POINT. `config.gun.ammo` publishes a COUNTER NAME rather than
 * a description, and the pickup whose `grants` matches it already carries the
 * title, the icon and the blurb — so the cheatsheet says «🍺 пиво» because the
 * catalogue said the gun drinks `beer` and that `beer` is what a пиво grants,
 * not because this file was told twice. The day a second ammunition is added it
 * is one catalogue line and no frontend change.
 *
 * Null when nothing grants it. The server has a test that will not let that
 * happen (a gun spending a counter nothing scatters can never be reloaded), so
 * this is the client refusing to render «undefined» rather than a case anybody
 * expects to see.
 */
export function ammoPickup(config: VanyadumConfig): VanyadumPickupKind | null {
  return config.pickups.find((p) => p.grants === config.gun.ammo) ?? null;
}

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

/**
 * What the standings show a column of, as a parenthetical — «(🍺 пиво)», or
 * «(🍺 пиво, 💉 шприц)» the day a second thing is added to the catalogue.
 *
 * Answers with the empty string for a catalogue with nothing to pick up in it,
 * so the sentence it is embedded in ends after «сколько времени внутри» rather
 * than trailing an empty bracket.
 */
function carriedList(config: VanyadumConfig): string {
  const named = config.pickups.map((p) => `${p.icon} ${p.title}`).join(', ');
  return named ? ` и сколько собрал (${named})` : '';
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

  // FIRST, because it is what the game IS: one заброшка, and everybody who
  // opens the game is walking around inside it at the same time. Both numbers
  // below are rules a player would otherwise have to discover by being refused
  // at the door, or by standing over an empty floor wondering whether waiting is
  // worth it.
  blocks.push({
    title: 'Заброшка',
    lines: [
      {
        // «больше N человек» rather than «N человек»: the number is DERIVED, and
        // Russian agreement after a numeral depends on it — «4 человека» but
        // «6 человек». A phrasing that reads correctly for every value is worth
        // more here than a prettier one that goes wrong the day somebody retunes
        // the capacity, which is the entire point of not typing the number out.
        label: '🏚 одна на всех',
        text: `Заброшка одна, и все ходят по ней одновременно. Больше ${config.world.max_occupants} человек внутрь не пустят.`,
      },
      {
        label: '🔁 всё возвращается',
        text: `Подобранное появляется на том же месте через ${num(config.world.respawn_seconds, 0)} с. Уносить нечего — заброшка не пустеет.`,
      },
      {
        // DERIVED, because the board's columns are, and the two must say the
        // same thing. A row renders one number per entry in `config.pickups`,
        // so the sentence naming those columns is generated from that same
        // list — the day a second pickup is added it appears on the board by
        // itself, and this line follows it without anybody remembering to come
        // back. «сколько пива» typed out here is the version that goes stale.
        //
        // It sits in the BUILDING's block rather than in the hand-written one
        // because that is what it is a fact about: the standings are unfiltered
        // and name everybody inside, which is a rule of the place and not a
        // control. What cannot be derived — that a snapshot is cut to the rooms
        // you can see at all — is prose, below.
        label: '📋 табло',
        text: `Справа сверху — все, кто сейчас на заброшке, даже те, кого не видно: сколько времени внутри${carriedList(config)}. Твоя строка со стрелкой.`,
      },
    ],
  });

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

  // THE GUN, and every number on it derived. The cadence, the barrel count, what
  // a reload costs and how long it takes are all served, so retuning any of them
  // on the server updates this screen by itself — which is the whole reason the
  // catalogue carries them rather than the client assuming them.
  //
  // The ammunition is NAMED by the join above rather than by a string here: the
  // gun publishes which counter it spends, and the pickup that grants it already
  // has a title and an icon. A hand-typed «пиво» would be the one word on this
  // screen that a second ammunition would not update.
  {
    const gun = config.gun;
    const ammo = ammoPickup(config);
    const lines: RuleLine[] = [
      {
        label: '🔫 стволов',
        text: `${gun.barrels}. Между выстрелами ${num(gun.fire_cooldown_seconds, 2)} с — дуплетом не выйдет.`,
      },
    ];
    if (ammo) {
      lines.push({
        label: `${ammo.icon} чем заряжать`,
        text: `Одна перезарядка тратит ${gun.reload_cost} · ${ammo.icon} ${ammo.title} и заполняет обрез целиком. Пусто в карманах — обрез молчит.`,
      });
    }
    lines.push({
      label: '⏳ перезарядка',
      text: `${num(gun.reload_seconds)} с с пустыми руками. Сама не начинается: жми на курок с пустым обрезом.`,
    });
    blocks.push({ title: 'Обрез', lines });
  }

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
 * Four kinds of thing end up here, and none of them could honestly be
 * generated. The CONTROLS, because the server has no opinion about thumbs and
 * publishes none. The ABSENCES — that there is no objective, that nothing ends,
 * that leaving is just leaving, and that the обрез has nothing to hit yet —
 * which no catalogue can carry, because a catalogue can only publish what
 * exists. That last one is the newest and the one most likely to go stale: the
 * iteration that gives the ray something to damage has to come back and delete
 * it, and a cheatsheet still promising a shooting range with no targets in it
 * would be believed.
 * The building's LIFECYCLE: the config
 * endpoint describes what a заброшка is made of and how many people fit in it,
 * and carries no field at all for the fact that this one is torn down and
 * generated again once it empties. And the VISIBILITY FILTER — that a snapshot
 * is cut to the room you are standing in and the rooms through its doorways, so
 * a man who walked further off is genuinely not on your screen. That one is a
 * property of how a frame is built rather than a field the catalogue could
 * carry, which is why it is typed out here while the standings that answer it
 * are derived in the block above.
 *
 * The absences and the lifecycle matter more than any number on this screen. A
 * player who assumes there is a win condition will spend the whole visit
 * looking for it, and a player who remembers the layout will decide the game is
 * broken the first time he comes back to a building he has never seen. The
 * filter is the newest of the three and behaves the same way: unstated, a peer
 * vanishing at a doorway reads as the game losing him.
 *
 * If you add a mechanic, ask first whether the catalogue could carry it. It
 * usually can, and then it belongs above rather than here.
 */
export const VANYADUM_PROSE: RuleLine[] = [
  { label: '👈 слева', text: 'Держи палец и веди — Ваня идёт. Стик появляется там, где ты нажал.' },
  { label: '👉 справа', text: 'Веди пальцем — смотришь по сторонам.' },
  {
    label: '🔫 стрелять',
    text: 'Круглая кнопка справа снизу. Держи — обрез стреляет так часто, как умеет. Рядом 🔇 — выключить звук.',
  },
  {
    label: '💥 попасть не в кого',
    text: 'Обрез стреляет, гильзы тратятся, но нейрослопов на заброшке ещё нет. Пока это тир без мишеней.',
  },
  {
    label: '⌨️ на компьютере',
    text: 'WASD — идти, пробел или кнопка мыши — стрелять. Клик по экрану захватывает мышь, дальше смотришь ей как в любом шутере. Esc — отпустить.',
  },
  {
    label: '👀 видно не всех',
    text: 'На экране только те, кто в твоей комнате или в соседней через проём. Ушёл человек дальше — пропадает, хотя он никуда не делся; вернулся — снова видно. Остальные — на табло.',
  },
  {
    label: '🎯 цели нет',
    text: 'Ничего не надо собрать, никуда не надо успеть. Заброшка не кончается, выиграть её нельзя, проиграть тоже.',
  },
  {
    label: '🚪 как уйти',
    text: 'Уйди со страницы или закрой вкладку — на этом визит и кончается. Сколько ты там пробыл, запишется.',
  },
  {
    label: '🔀 не та же самая',
    text: 'Пока внутри хоть кто-то есть, заброшка стоит. Ушёл последний — её сносят, и следующего пустят уже в новую: другие комнаты, пиво в других местах. Планировку запоминать бесполезно.',
  },
  { label: '📡 связь', text: 'Мир считает сервер. Порвётся связь — Ваня постоит на месте и дождётся.' },
];
