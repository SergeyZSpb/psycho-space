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
import { BETRAYALS_ICON, DEATHS_ICON, KILLS_ICON } from './vanyadumRoster';

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
        text: `Справа сверху — все, кто сейчас на заброшке, даже те, кого не видно: сколько времени внутри${carriedList(config)}. Там же ${KILLS_ICON} ${config.slop.kills_title}, ${DEATHS_ICON} сколько раз лёг и ${BETRAYALS_ICON} ${config.world.betrayals_title} — три разных числа, не перепутай. Твоя строка со стрелкой.`,
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
        // DERIVED FROM TWO PLACES AT ONCE — the player's health and the gun's
        // damage — because the only thing anybody wants to know about either is
        // how they meet. The catalogue publishes both, so the day somebody
        // retunes the обрез this sentence follows it.
        label: '♥ здоровье',
        text: `${config.player.start_health} из ${config.player.max_health}. Одно попадание из обреза снимает ${config.gun.damage}.`,
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

  // THE НЕЙРОСЛОП, which is the first thing in this building that is not a
  // friend, and the reason the обрез finally has a use that is not a betrayal.
  // Every number is served — how many there are, what one costs to be reached
  // by, how fast it walks and how long the building waits before making another
  // — so retuning any of them on the server changes what a player is told with
  // no client change at all.
  //
  // TWO OF THESE LINES ARE JOINS RATHER THAN FIELDS, which is the same trick the
  // gun's ammunition line plays. How many barrels one takes is its health
  // against the gun's damage, and how many touches a man survives is his health
  // against the слоп's — both pairs are published, neither total is, and a third
  // number saying the same thing is a third number to keep in step by hand.
  {
    const slop = config.slop;
    const barrels = Math.max(1, Math.ceil(slop.health / Math.max(1, config.gun.damage)));
    const touches = Math.max(
      1,
      Math.ceil(config.player.max_health / Math.max(1, slop.damage)),
    );
    blocks.push({
      title: 'Нейрослопы',
      lines: [
        {
          label: `${KILLS_ICON} ${slop.title}`,
          text: `${slop.blurb} Больше ${slop.population} штук разом в заброшке не бывает.`,
        },
        {
          // «Попаданий нужно: N» rather than «N попаданий», because the number
          // is DERIVED and Russian agreement after a numeral depends on it. The
          // same reasoning as the capacity line above: a phrasing that reads
          // correctly for every value beats a prettier one that goes wrong the
          // afternoon somebody retunes the обрез.
          label: '💥 чем убить',
          text: `Попаданий из обреза нужно: ${barrels}. Больше о попадании никто не скажет — здоровья у него нет, падать он не умеет, просто пропадает со вспышкой.`,
        },
        {
          label: '🩸 если достанет',
          text: `${slop.damage} здоровья за касание, не чаще раза в ${num(slop.touch_seconds)} с. Касаний до смерти: ${touches}. Счёт у каждого свой, так что вдвоём они снимают вдвое быстрее.`,
        },
        {
          // THE ONE RULE THAT MAKES THE CREATURE FAIR, and it is a comparison
          // rather than a number: both speeds are served, so the sentence says
          // which is bigger instead of asking a player to hold two figures in
          // his head.
          label: '🏃 медленнее тебя',
          text: `${num(slop.speed)} м/с против твоих ${num(config.player.walk_speed)} — уйти можно всегда. Стоять на месте нельзя.`,
        },
        {
          label: '⏳ приходят сами',
          text: `Раз в ${num(slop.spawn_seconds, 0)} с в заброшке заводится новый, пока их не станет ${slop.population}. Перебил всех — столько же тишины, и всё сначала.`,
        },
      ],
    });
  }

  // WHAT HAPPENS WHEN SOMEBODY HITS YOU, and it is its own block because it is
  // the newest and most surprising set of rules in the game: the обрез now
  // reaches people, the people it reaches are your friends, and none of it
  // scores anything. Every number is served, including the word for the
  // standings column — the joke is written down in the catalogue, not here.
  blocks.push({
    title: 'Смерть',
    lines: [
      {
        // THE NUMBER IS DERIVED AND THE НЕЙРОСЛОП CLAUSE IS TYPED OUT, which is
        // this file's standing division stated once more: the catalogue
        // publishes how long a man lies there and carries no field at all for
        // who ignores him while he does. A слоп neither walks at a man on the
        // floor nor touches one — the world builds its targets and its victims
        // out of the same filter (world.go, stepSlops) — so a change to that
        // filter has to come back and edit this sentence by hand.
        label: '💀 лёг',
        text: `Здоровье кончилось — валяешься ${num(config.world.down_seconds, 0)} с. Смотреть по сторонам можно, идти и стрелять нельзя. Нейрослопы к лежачему не идут и не трогают его. Потом сам встаёшь на спавне с полным обрезом, а собранное остаётся при тебе.`,
      },
      {
        // BOTH WAYS OF ARRIVING AT THE SPAWN, because the window now opens for
        // both: the world grants it to a man who has just got up and to a man who
        // has just walked in through the door (world.go — `Join` calls the same
        // `protect` that `rise` does). It sits in this block anyway, because the
        // rule is about the spawn and this is where the player is told he wakes
        // up on one; but a cheatsheet naming only the respawn leaves a newcomer
        // pulling a dead trigger for his first two seconds in the building with
        // nothing on the screen to explain it, which reads as a broken обрез.
        //
        // The number stays derived, which is the part that goes stale. WHAT
        // opens the window and WHAT IT STOPS are both TYPED OUT — the catalogue
        // publishes how long it lasts and carries no field for either — so a
        // third way of becoming untouchable, or anything new the shield turns
        // out not to cover, has to come back and edit this sentence by hand.
        //
        // AND THE НЕЙРОСЛОПЫ ARE THE HALF A PLAYER CANNOT WORK OUT FOR HIMSELF.
        // That bullets stop is demonstrated the first time somebody shoots at
        // him; that a creature will not even set off towards him is a thing that
        // does not happen, and nothing that does not happen leaves a mark on the
        // screen. It is the same filter the hit test applies, read the only way
        // it can be (world.go, stepSlops builds its targets and its victims out
        // of it), and it is what makes the seconds after getting up survivable
        // at a spawn everybody in the building knows the way to — so it is worth
        // its clause.
        label: '🛡 щит на спавне',
        text: `Первые ${num(config.world.protect_seconds, 0)} с на спавне — и когда только зашёл, и когда встал после смерти — пули тебя не берут, нейрослопы не трогают и даже не идут в твою сторону, но и сам не стреляешь и не перезаряжаешься. Кто светится синим, того трогать бесполезно.`,
      },
      {
        // THE LINE THAT SAID THERE WAS NOTHING HERE BUT FRIENDS IS GONE, and its
        // own comment on the block above predicted exactly this. There are
        // strangers in the building now, so «чужих тут нет» would be a lie about
        // the central rule of the game — and the joke is better for it: the two
        // columns are what the board thinks of what you have been shooting.
        label: `${BETRAYALS_ICON} ${config.world.betrayals_title}`,
        text: `Огонь по своим включён, и друг от обреза ложится, как и всё остальное. В ${config.slop.kills_title} он не пойдёт: за него отдельная строчка «${config.world.betrayals_title}» на табло рядом с ${DEATHS_ICON}, и больше ничего.`,
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
 * Five kinds of thing end up here, and none of them could honestly be
 * generated. The CONTROLS, because the server has no opinion about thumbs and
 * publishes none. The ABSENCES — that there is no objective, that nothing ends,
 * and that leaving is just leaving — which no catalogue can carry, because a
 * catalogue can only publish what exists. The building's LIFECYCLE: the config
 * endpoint describes what a заброшка is made of and how many people fit in it,
 * and carries no field at all for the fact that this one is torn down and
 * generated again once it empties. The VISIBILITY FILTER — that a snapshot is
 * cut to the room you are standing in and the rooms through its doorways, so a
 * man who walked further off is genuinely not on your screen. And what a HIT
 * LOOKS LIKE, which is a rendering decision rather than a rule: the server sends
 * a small integer saying somebody was shot and simply stops sending a нейрослоп
 * that has been, and the choice to draw the first as red spreading over him and
 * the second as a blue flash where he stood belongs to this client alone.
 *
 * The absences and the lifecycle matter more than any number on this screen. A
 * player who assumes there is a win condition will spend the whole visit
 * looking for it, and a player who remembers the layout will decide the game is
 * broken the first time he comes back to a building he has never seen. The
 * filter behaves the same way: unstated, a peer vanishing at a doorway reads as
 * the game losing him. And the hit mark is the sharpest of the lot — it is the
 * only thing that distinguishes a shot that connected from one that missed, so a
 * player who does not know what he is looking for will conclude the обрез does
 * nothing.
 *
 * THE LINE THAT USED TO SAY THE ОБРЕЗ HAD NOTHING TO HIT IS GONE, and its own
 * comment predicted exactly this: it was true for one iteration, it could never
 * be derived, and the change that gave the ray something to damage had to come
 * back and delete it. Believed, it would now be a lie about the central rule of
 * the game.
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
    label: '💥 попал или нет',
    text: 'По человеку попал — по нему разойдётся красным. По нейрослопу — на его месте вспыхнет голубым, и его не станет. Промазал — не будет ничего. Больше тебе об этом никто не скажет.',
  },
  {
    label: '⌨️ на компьютере',
    text: 'WASD — идти, пробел или кнопка мыши — стрелять. Клик по экрану захватывает мышь, дальше смотришь ей как в любом шутере. Esc — отпустить.',
  },
  {
    label: '👀 видно не всех',
    text: 'На экране только те, кто в твоей комнате или в соседней через проём. Ушёл человек дальше — пропадает, хотя он никуда не делся; вернулся — снова видно. Нейрослопы прячутся так же, поэтому вспышка за стеной ничего не значит. Остальные — на табло.',
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
