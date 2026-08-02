import { describe, expect, it } from 'vitest';
import {
  VANYADUM_PROSE,
  ammoPickup,
  buildRules,
  carriedKinds,
  medicinePickup,
  pickupLine,
} from '../lib/vanyadumRules';
import type { VanyadumConfig, VanyadumPickupKind } from '../api/types';

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
    body_height: 1.8,
    walk_speed: 5,
    max_step: 0.6,
    max_pitch: 1.5,
    max_health: 100,
    start_health: 100,
  },
  gun: {
    barrels: 2,
    fire_cooldown_seconds: 0.35,
    reload_seconds: 1.5,
    reload_cost: 1,
    ammo: 'beer',
    damage: 50,
  },
  // Deliberately not the server's numbers, exactly like everything else here: a
  // cheatsheet with any of them typed into it would pass against production and
  // fail on this fixture, which is what makes "derived" a claim rather than a
  // hope. `health` against the gun's `damage` above is what says how many
  // barrels one takes, and `damage` against the player's `max_health` is what
  // says how many touches a man survives — both totals are joins, neither is a
  // field.
  slop: {
    title: 'нейрослоп',
    blurb: 'Ходит на тебя.',
    population: 3,
    health: 150,
    damage: 20,
    touch_seconds: 2,
    speed: 3.5,
    spawn_seconds: 12,
    kills_title: 'слопы',
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
  world: {
    max_occupants: 6,
    respawn_seconds: 30,
    down_seconds: 3,
    protect_seconds: 2,
    betrayals_title: 'предательства',
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

  it("takes the building's own rules from the catalogue too", () => {
    // Capacity and the respawn interval are the two rules a player would
    // otherwise discover by being refused at the door, or by standing over an
    // empty floor wondering whether waiting is worth it. Both are served, so
    // both are derived — retune either on the server and this screen follows.
    const retuned = {
      ...config,
      world: { ...config.world, max_occupants: 12, respawn_seconds: 45 },
    };
    const block = buildRules(retuned).find((b) => b.title === 'Заброшка');
    const text = block?.lines.map((l) => l.text).join(' ') ?? '';
    expect(text).toContain('12');
    expect(text).toContain('45 с');
    // And it is not marked as prose, because none of it was typed out.
    expect(block?.prose).toBeFalsy();
  });

  it('says the building is shared and that nothing in it ends', () => {
    // The two facts that changed everything about this game, and the two a
    // player will otherwise assume the opposite of: a lone arena with a win
    // condition is what every other shooter has trained them to expect.
    const text = buildRules(config)
      .flatMap((b) => b.lines)
      .map((l) => `${l.label} ${l.text}`)
      .join(' ');
    expect(text).toContain('одна');
    expect(text).toContain('цели нет');
    expect(text).toMatch(/не кончается/);
  });

  it('says the building is torn down and generated again once it empties', () => {
    // «Заброшка одна» on its own reads as one PERMANENT building, and a player
    // who believes that will remember the layout, come back to somewhere else
    // entirely and decide the game is broken. It is a rule of the game, so it is
    // on the screen — and it is in the hand-written block because the catalogue
    // has no field for it: the config endpoint publishes what a заброшка is made
    // of, not what happens to this one when the last person walks out.
    const prose = buildRules(config).find((b) => b.prose);
    const text = prose?.lines.map((l) => `${l.label} ${l.text}`).join(' ') ?? '';
    expect(text).toContain('сносят');
    expect(text).toContain('в новую');
  });

  it('says you cannot see everybody, which no catalogue could have told it', () => {
    // Half of W1b's rules change, and the half that has to be typed out: the
    // filter is a property of how a snapshot is built, and the config endpoint
    // has no field for it. A player who is not told reads a peer vanishing at a
    // doorway as the game losing him.
    const prose = buildRules(config).find((b) => b.prose);
    const text = prose?.lines.map((l) => `${l.label} ${l.text}`).join(' ') ?? '';
    expect(text).toContain('в соседней через проём');
    expect(text).toContain('пропадает');
    // And it points at where the rest of them are, rather than leaving the
    // player to discover that the building is fuller than it looks.
    expect(text).toContain('табло');
  });

  it('describes the standings, and names their columns from the catalogue', () => {
    // The other half, and it is DERIVED: a board row renders one number per
    // entry in `config.pickups`, so the line describing those columns is
    // generated from the same list. «сколько пива собрал» typed out here would
    // be wrong the first afternoon a second pickup landed — the board would grow
    // a column and the cheatsheet would not.
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
    const block = buildRules(two).find((b) => b.title === 'Заброшка');
    const line = block?.lines.find((l) => l.label.includes('табло'));
    expect(line?.text).toContain('даже те, кого не видно');
    expect(line?.text).toContain('🍺 пиво');
    expect(line?.text).toContain('💉 шприц');
    // Not marked as prose, because none of it was typed out.
    expect(block?.prose).toBeFalsy();
  });

  it('leaves the bag out of the standings line when there is nothing to carry', () => {
    // A catalogue with nothing to pick up in it must not produce «и сколько
    // собрал ()». The board has no bag columns in that case either, so the two
    // still agree.
    const bare = buildRules({ ...config, pickups: [] }).find((b) => b.title === 'Заброшка');
    const line = bare?.lines.find((l) => l.label.includes('табло'));
    expect(line?.text).toContain('сколько времени внутри.');
    expect(line?.text).not.toContain('собрал');
  });

  it('no longer claims that whoever walked in is visible', () => {
    // The tail «кто зашёл, того и видно» was true for exactly one iteration. It
    // is now the opposite of the rule, and a believed cheatsheet that is wrong
    // is worse than no cheatsheet.
    const text = buildRules(config)
      .flatMap((b) => b.lines)
      .map((l) => l.text)
      .join(' ');
    expect(text).not.toContain('кто зашёл, того и видно');
  });

  it('no longer tells anybody to collect all the beer', () => {
    // The objective the game had until W1a. A cheatsheet that describes the
    // previous version of a game is worse than none, because it is believed.
    const text = buildRules(config)
      .flatMap((b) => b.lines)
      .map((l) => l.text)
      .join(' ');
    expect(text).not.toContain('Собрать всё пиво');
    expect(text).not.toContain('забег');
  });

  it("takes the gun's numbers from the catalogue, all of them", () => {
    // The rule the splash-screen gate exists for: retune the cadence or the
    // reload on the server and this screen follows with no frontend change. A
    // hand-typed «0,35 с» is a number that is wrong the first afternoon somebody
    // decides the обрез is too slow.
    const retuned = {
      ...config,
      gun: { ...config.gun, barrels: 4, fire_cooldown_seconds: 0.8, reload_seconds: 2.5, reload_cost: 3 },
    };
    const block = buildRules(retuned).find((b) => b.title === 'Обрез');
    const text = block?.lines.map((l) => l.text).join(' ') ?? '';
    expect(text).toContain('4');
    expect(text).toContain('0,8 с');
    expect(text).toContain('2,5 с');
    expect(text).toContain('3');
    // Not marked as prose, because none of it was typed out.
    expect(block?.prose).toBeFalsy();
  });

  it('names the ammunition by joining the gun to the pickup that grants it', () => {
    // THE JOIN IS THE POINT, and it is what a second ammunition would ride on.
    // The catalogue publishes a counter NAME, and the entry whose `grants`
    // matches already carries the title and the icon — so the screen says «🍺
    // пиво» because the two agree, not because this file was told twice.
    const renamed = {
      ...config,
      gun: { ...config.gun, ammo: 'juice' },
      pickups: [
        { ...config.pickups[0], grants: 'juice', title: 'сок', icon: '🧃' },
      ],
    };
    const block = buildRules(renamed).find((b) => b.title === 'Обрез');
    const text = block?.lines.map((l) => `${l.label} ${l.text}`).join(' ') ?? '';
    expect(text).toContain('🧃');
    expect(text).toContain('сок');
    expect(text).not.toContain('пиво');
  });

  it('says nothing about ammunition when nothing in the catalogue grants it', () => {
    // The server has a test that will not let a gun spend a counter nothing
    // scatters, so this is the client refusing to render «undefined» rather than
    // a case anybody expects. The rest of the block still says what it can.
    const orphaned = { ...config, gun: { ...config.gun, ammo: 'petrol' } };
    const block = buildRules(orphaned).find((b) => b.title === 'Обрез');
    expect(block?.lines.length).toBe(2);
    for (const line of block?.lines ?? []) {
      expect(`${line.label} ${line.text}`).not.toContain('undefined');
    }
  });

  it('says the trigger is held and that the sound can be turned off', () => {
    // Controls are prose — the server has no opinion about thumbs and publishes
    // none — but they are still rules, and a fire button nobody knows is held
    // rather than tapped is a gun that feels broken on a phone.
    const prose = buildRules(config).find((b) => b.prose);
    const text = prose?.lines.map((l) => `${l.label} ${l.text}`).join(' ') ?? '';
    expect(text).toContain('Держи');
    expect(text).toContain('🔇');
    expect(text).toContain('пробел');
  });

  it('no longer says the обрез has nothing to hit', () => {
    // It said so for exactly one iteration, and its own comment predicted this:
    // an absence can never be derived, so the change that gave the ray something
    // to damage had to come back and delete the line. Believed, it would now be
    // a lie about the central rule of the game — a player told the building is a
    // shooting range with no targets will not think to look behind him.
    const text = buildRules(config)
      .flatMap((b) => b.lines)
      .map((l) => `${l.label} ${l.text}`)
      .join(' ');
    expect(text).not.toContain('нейрослопов');
    expect(text).not.toContain('тир без мишеней');
  });

  it('says what a hit looks like, which no catalogue could have told it', () => {
    // A RENDERING DECISION rather than a rule: the server sends a small integer
    // saying somebody was shot, and drawing that as red spreading over him is
    // this client's own choice — so it is prose. It is also the sharpest line on
    // the screen, because it is the ONLY thing that distinguishes a shot that
    // connected from one that missed.
    const prose = buildRules(config).find((b) => b.prose);
    const text = prose?.lines.map((l) => `${l.label} ${l.text}`).join(' ') ?? '';
    expect(text).toContain('красным');
    expect(text).toContain('Промазал');
  });

  it("takes death's own numbers from the catalogue", () => {
    // C1b's rules change, and the reason the splash is a gate: a player who does
    // not know he gets up by himself will reload the page, and one who does not
    // know he is untouchable for two seconds afterwards will spend them standing
    // still. Both numbers are the RETUNED ones here rather than production's, so
    // a hand-typed «3 с» fails.
    const retuned = {
      ...config,
      world: { ...config.world, down_seconds: 7, protect_seconds: 4 },
    };
    const block = buildRules(retuned).find((b) => b.title === 'Смерть');
    const text = block?.lines.map((l) => `${l.label} ${l.text}`).join(' ') ?? '';
    expect(text).toContain('7 с');
    expect(text).toContain('4 с');
    // Not marked as prose, because none of it was typed out.
    expect(block?.prose).toBeFalsy();
  });

  it('says the spawn shield covers walking in as well as getting up', () => {
    // The window is opened in two places on the server — `rise` grants it to a
    // man who has just got up, and `Join` grants the same one to a man who has
    // just walked in. A cheatsheet naming only the respawn is a cheatsheet a
    // newcomer disproves in his first two seconds, pulling a trigger the shield
    // is refusing with nothing on screen to say why.
    const block = buildRules(config).find((b) => b.title === 'Смерть');
    const text = block?.lines.map((l) => `${l.label} ${l.text}`).join(' ') ?? '';
    expect(text).toContain('когда только зашёл');
    expect(text).toContain('когда встал после смерти');
    // And both halves of what the shield does, because the second one is the
    // surprising half: it takes your обрез away for as long as it protects you.
    expect(text).toContain('не стреляешь');
  });

  it('says the spawn shield stops нейрослопы as well as bullets', () => {
    // C2's rule, and the half of the shield a player cannot work out for
    // himself: that bullets stop is demonstrated the first time somebody shoots
    // at him, but a creature not setting off towards him is a thing that does
    // not happen, and nothing that does not happen leaves a mark on the screen.
    // The server builds the слоп's targets AND its victims out of the same
    // filter the hit test uses, so a protected man is neither walked at nor
    // touched — which is what makes the seconds after getting up survivable at a
    // spawn everybody knows the way to.
    const block = buildRules(config).find((b) => b.title === 'Смерть');
    const shield = block?.lines.find((l) => l.label.includes('щит'));
    expect(shield?.text).toContain('нейрослопы не трогают');
    expect(shield?.text).toContain('не идут в твою сторону');
  });

  it('says нейрослопы ignore a man on the floor', () => {
    // The same filter, read the other way round, and the same reason it is on
    // the screen: a player lying there watching one walk past has no way of
    // telling whether he was spared or merely missed. Typed out rather than
    // derived — the catalogue publishes how long he lies there and carries no
    // field at all for who ignores him while he does.
    const block = buildRules(config).find((b) => b.title === 'Смерть');
    const down = block?.lines.find((l) => l.label.includes('лёг'));
    expect(down?.text).toContain('к лежачему не идут');
    expect(down?.text).toContain('не трогают его');
  });

  it('says friends are killable and that killing them scores nothing', () => {
    // THE RULE THE WHOLE ITERATION TURNS ON. Every other shooter has trained a
    // player to expect that the man beside him is safe from him; here he is not,
    // and the only thing a kill produces is a line on the board under a name the
    // SERVER chose — so the word is derived too, and a second copy of the joke
    // typed in here would be the one thing on the screen a retune could not fix.
    const renamed = {
      ...config,
      world: { ...config.world, betrayals_title: 'подставы' },
    };
    const text = buildRules(renamed)
      .flatMap((b) => b.lines)
      .map((l) => `${l.label} ${l.text}`)
      .join(' ');
    expect(text).toContain('Огонь по своим включён');
    expect(text).toContain('подставы');
    expect(text).not.toContain('предательства');
  });

  it('states how much a barrel takes off, by joining the gun to the player', () => {
    // Two halves of the catalogue meeting: the damage is on the gun and the
    // health is on the player, and the only thing anybody wants to know about
    // either is how they meet. Both retuned here, so a hand-typed «50» fails.
    const retuned = {
      ...config,
      player: { ...config.player, max_health: 60, start_health: 60 },
      gun: { ...config.gun, damage: 20 },
    };
    const block = buildRules(retuned).find((b) => b.title === 'Ваня');
    const text = block?.lines.map((l) => l.text).join(' ') ?? '';
    expect(text).toContain('60 из 60');
    expect(text).toContain('20');
    // And the claim it replaced is gone: there was nobody to take health off,
    // for exactly one iteration.
    expect(text).not.toContain('отнять его некому');
  });

  it('names all three standings columns from the catalogue', () => {
    // The board grows a column with the thing it counts, which is the rule: 💀
    // and 🔪 arrived with the обрез reaching people, and 👾 arrived with there
    // being something in the building worth killing. The line describing the
    // board has to grow with it — a cheatsheet that lists three of a row's five
    // numbers is a cheatsheet a player stops reading. Two of the three words are
    // the SERVER's, so a retune of either follows here.
    const renamed = {
      ...config,
      slop: { ...config.slop, kills_title: 'слопики' },
      world: { ...config.world, betrayals_title: 'подставы' },
    };
    const block = buildRules(renamed).find((b) => b.title === 'Заброшка');
    const line = block?.lines.find((l) => l.label.includes('табло'));
    expect(line?.text).toContain('👾');
    expect(line?.text).toContain('💀');
    expect(line?.text).toContain('🔪');
    expect(line?.text).toContain('слопики');
    expect(line?.text).toContain('подставы');
    // And it says they are different numbers, because two counters a player
    // cannot tell apart are worse than one.
    expect(line?.text).toContain('три разных числа');
  });

  it('states the нейрослоп’s rules, every number of them derived', () => {
    // C2's rules change, and the biggest one since the обрез started landing:
    // there is now something in the building that is not a friend, it walks at
    // you, and standing still is what it punishes. Every number is the fixture's
    // rather than production's, so a hand-typed cheatsheet fails here.
    const block = buildRules(config).find((b) => b.title === 'Нейрослопы');
    const text = block?.lines.map((l) => `${l.label} ${l.text}`).join(' ') ?? '';
    expect(text).toContain('нейрослоп');
    expect(text).toContain('Ходит на тебя.');
    // How many of them, what a touch costs, how often it may charge it, how fast
    // it walks and how long the building waits before making another.
    expect(text).toContain('3');
    expect(text).toContain('20 здоровья');
    expect(text).toContain('2 с');
    expect(text).toContain('3,5 м/с');
    expect(text).toContain('12 с');
    // None of it was typed out.
    expect(block?.prose).toBeFalsy();
  });

  it('says how many barrels one takes by joining its health to the gun', () => {
    // A JOIN AND NOT A FIELD, exactly like the ammunition line: the server
    // publishes the creature's health and the gun's damage and no total, because
    // a third number saying the same thing is a third number to keep in step by
    // hand. 150 against 50 is three; against 75 it is two.
    const line = (damage: number) =>
      buildRules({ ...config, gun: { ...config.gun, damage } })
        .find((b) => b.title === 'Нейрослопы')
        ?.lines.find((l) => l.label.includes('убить'))?.text ?? '';
    expect(line(50)).toContain('нужно: 3');
    expect(line(75)).toContain('нужно: 2');
    expect(line(1000)).toContain('нужно: 1');
  });

  it('says how many touches a man survives by joining it to his own health', () => {
    // The other join, the other way round: what the creature does against what
    // the player has. 100 against 20 is five.
    const line = (max: number) =>
      buildRules({ ...config, player: { ...config.player, max_health: max } })
        .find((b) => b.title === 'Нейрослопы')
        ?.lines.find((l) => l.label.includes('достанет'))?.text ?? '';
    expect(line(100)).toContain('Касаний до смерти: 5');
    expect(line(50)).toContain('Касаний до смерти: 3');
  });

  it('says you are faster than it, which is the rule that makes it fair', () => {
    // Both speeds are served, so the sentence states which is bigger rather than
    // asking a player to hold two figures in his head — and it is the one thing
    // that makes a creature you cannot leave a building to escape survivable.
    const block = buildRules(config).find((b) => b.title === 'Нейрослопы');
    const line = block?.lines.find((l) => l.label.includes('медленнее'));
    expect(line?.text).toContain('3,5 м/с');
    expect(line?.text).toContain('5 м/с');
    expect(line?.text).toContain('уйти можно всегда');
  });

  it('no longer claims there is nobody in the building but friends', () => {
    // THE CLAIM THIS ITERATION MADE FALSE, and the rule that a cheatsheet
    // describing the previous version of a game is worse than none: «чужих тут
    // нет» was true for exactly two iterations and would now be a lie about the
    // central rule of the game.
    const text = buildRules(config)
      .flatMap((b) => b.lines)
      .map((l) => `${l.label} ${l.text}`)
      .join(' ');
    expect(text).not.toContain('чужих тут нет');
    expect(text).not.toContain('все убийства свои');
    // And a friend shot still scores nothing towards the kills, which is what
    // makes the two columns a joke rather than a total.
    expect(text).toContain('В слопы он не пойдёт');
  });

  it('says what a hit on each kind of target looks like, because nothing else will', () => {
    // A rendering decision rather than a rule, so it is prose — and it is the
    // only thing distinguishing a shot that connected from one that missed. The
    // two kinds look different, so both are named.
    const prose = buildRules(config).find((b) => b.prose);
    const text = prose?.lines.map((l) => `${l.label} ${l.text}`).join(' ') ?? '';
    expect(text).toContain('красным');
    expect(text).toContain('голубым');
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

/**
 * The same catalogue with medicine scattered in it.
 *
 * ITS NUMBERS ARE NOT THE SERVER'S, exactly like everything else in this file: a
 * cheatsheet with 50 or 2,5 typed into it would pass against production and fail
 * here, which is the whole point of a fixture that is wrong on purpose. `grants`
 * is EMPTY, because that is what the catalogue says about a thing that is used on
 * the spot rather than carried — and it is the field three readouts filter on.
 */
const MEDICINE: VanyadumPickupKind = {
  key: 'med',
  title: 'шприц',
  icon: '💉',
  grants: '',
  amount: 0,
  max: 0,
  tint: '#9fd6c8',
  blurb: 'Чинит, но стоя.',
  heals: 35,
  inject_seconds: 4,
};

const medConfig: VanyadumConfig = { ...config, pickups: [...config.pickups, MEDICINE] };

describe('the шприц block', () => {
  it('is absent when the building scatters no medicine', () => {
    // A heading over nothing is worse than a heading that is not there — the
    // same rule the pickup block already follows.
    expect(buildRules(config).some((b) => b.title === 'Шприц')).toBe(false);
  });

  it('takes how much and how long from the catalogue, and the cap from the player', () => {
    const block = buildRules(medConfig).find((b) => b.title === 'Шприц');
    const text = block?.lines.map((l) => l.text).join(' ') ?? '';
    expect(text).toContain('+35 здоровья');
    // The cap the heal is clamped to, which is what makes injecting at nearly
    // full health a bad trade rather than a free top-up.
    expect(text).toContain('100');
    expect(text).toContain('4 с');
    // And it is not marked as prose: the numbers are all derived, and the two
    // typed-out lines live in a block whose other lines are not.
    expect(block?.prose).toBeFalsy();
  });

  it('follows a retune with no frontend change, which is the whole claim', () => {
    const retuned = buildRules({
      ...medConfig,
      pickups: [config.pickups[0], { ...MEDICINE, heals: 12, inject_seconds: 7.5 }],
    })
      .find((b) => b.title === 'Шприц')
      ?.lines.map((l) => l.text)
      .join(' ');
    expect(retuned).toContain('+12 здоровья');
    expect(retuned).toContain('7,5 с');
    expect(retuned).not.toContain('35');
  });

  it('puts the cost next to the reload rather than claiming which is worse', () => {
    // BOTH NUMBERS, NO COMPARISON. Either can be retuned, so a sentence saying
    // «дольше перезарядки» is a sentence that goes quietly false the afternoon
    // somebody moves one of them — the same reasoning the capacity line follows.
    const line = buildRules(medConfig)
      .find((b) => b.title === 'Шприц')
      ?.lines.find((l) => l.label.includes('стоять'));
    expect(line?.text).toContain('4 с');
    expect(line?.text).toContain('1,5 с');
  });

  it('says how far a нейрослоп walks in that time, which is a join that cannot invert', () => {
    // The only unit that answers the question the injection actually asks —
    // "am I far enough from it" — and it is the creature's own speed times the
    // ampoule's own duration, so it stays true whatever either becomes.
    const line = buildRules(medConfig)
      .find((b) => b.title === 'Шприц')
      ?.lines.find((l) => l.label.includes('стоять'));
    expect(line?.text).toContain('14 м');
  });

  it('says what interrupts it, and that nothing else can', () => {
    // TYPED OUT, and the line a rules change has to come back and edit by hand:
    // the catalogue publishes how much and how long and carries no field at all
    // for what ends an injection early or for what happens to the remainder.
    const text =
      buildRules(medConfig)
        .find((b) => b.title === 'Шприц')
        ?.lines.map((l) => l.text)
        .join(' ') ?? '';
    expect(text).toContain('Только урон');
    expect(text).toContain('Сам отменить не можешь');
    expect(text).toContain('остаток пропадает');
  });

  it('says a whole man walks straight over it, which is what makes it a landmark', () => {
    const text =
      buildRules(medConfig)
        .find((b) => b.title === 'Шприц')
        ?.lines.map((l) => l.text)
        .join(' ') ?? '';
    expect(text).toContain('На полном здоровье');
    expect(text).toContain('вернись');
  });

  it('says the whole room can see it, because that is the rule worth knowing', () => {
    // How a peer is DRAWN is this client's decision alone — the server sends a
    // small integer and has no opinion about the colour — so it is typed out. It
    // is on the screen because a man mid-injection is the most exploitable thing
    // in the building, and a player who did not know that would take one in the
    // open.
    const text =
      buildRules(medConfig)
        .find((b) => b.title === 'Шприц')
        ?.lines.map((l) => l.text)
        .join(' ') ?? '';
    expect(text).toContain('зелён');
    expect(text).toContain('Чужой шприц видно так же');
  });

  it('never renders an empty line here either', () => {
    for (const block of buildRules(medConfig)) {
      expect(block.title.length).toBeGreaterThan(0);
      for (const line of block.lines) {
        expect(line.label.trim().length).toBeGreaterThan(0);
        expect(line.text.trim().length).toBeGreaterThan(0);
      }
    }
  });
});

describe('medicinePickup', () => {
  it('finds the entry by what it heals, which is the server’s own test', () => {
    expect(medicinePickup(medConfig)?.key).toBe('med');
  });

  it('answers null when the catalogue scatters none', () => {
    expect(medicinePickup(config)).toBeNull();
  });

  it('does not decide by the key, which would be a second definition of medicine', () => {
    // `collect` asks whether `heals` is above zero and nothing else, so a client
    // matching on «med» would part company with it the first time a second kind
    // of medicine was added as a catalogue line.
    const renamed = { ...medConfig, pickups: [config.pickups[0], { ...MEDICINE, key: 'ampoule' }] };
    expect(medicinePickup(renamed)?.key).toBe('ampoule');
  });
});

describe('carriedKinds', () => {
  it('leaves out anything that is used rather than carried', () => {
    // AN EMPTY `grants` IS THE CATALOGUE SAYING SO. The HUD and the standings
    // draw one column per kind this returns, and a column for the шприц would
    // sit at zero for the whole visit — there is no counter behind it.
    expect(carriedKinds(medConfig).map((p) => p.key)).toEqual(['beer']);
  });

  it('answers nothing at all before the catalogue has arrived', () => {
    expect(carriedKinds(null)).toEqual([]);
  });

  it('keeps the standings sentence to what is actually carried', () => {
    const line = buildRules(medConfig)
      .find((b) => b.title === 'Заброшка')
      ?.lines.find((l) => l.label.includes('табло'));
    expect(line?.text).toContain('🍺 пиво');
    expect(line?.text).not.toContain('💉 шприц');
  });
});

describe('ammoPickup', () => {
  it('finds the entry whose grants the gun spends', () => {
    expect(ammoPickup(config)?.title).toBe('пиво');
  });

  it('answers null rather than guessing when nothing grants it', () => {
    expect(ammoPickup({ ...config, gun: { ...config.gun, ammo: 'petrol' } })).toBeNull();
  });

  it('matches on the counter and not on the pickup’s own key', () => {
    // `key` names the thing on the floor and `grants` names the counter it
    // fills; they happen to be equal for beer today, which is exactly the sort
    // of coincidence a join can be written wrong against and still pass.
    const split = {
      ...config,
      gun: { ...config.gun, ammo: 'shells' },
      pickups: [{ ...config.pickups[0], key: 'beer', grants: 'shells' }],
    };
    expect(ammoPickup(split)?.key).toBe('beer');
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
