import type { GameKhimkiBoardKey } from '../api/types';

// Display metadata for the four record boards. Tab labels stay short enough for
// four tabs to fit at 360px; the caption carries the full meaning, and the empty
// state differs per board because "nobody has won yet" and "nobody has lost yet"
// are very different news.
export interface BoardMeta {
  key: GameKhimkiBoardKey;
  /** Short tab label — must survive four-across at 360px. */
  tab: string;
  /** Full explanation shown under the tabs for the selected board. */
  caption: string;
  /** Shown instead of the list when the board has no entries. */
  empty: string;
}

export const BOARDS: BoardMeta[] = [
  {
    key: 'longest_win',
    tab: 'Дольше 🏆',
    caption: 'Кто дольше всех уговаривал Ваню — и всё-таки попал домой',
    empty: 'Пока никто не прошёл. Будь первым.',
  },
  {
    key: 'shortest_win',
    tab: 'Быстрее 🏆',
    caption: 'Кто уговорил Ваню быстрее всех',
    empty: 'Пока никто не прошёл. Будь первым.',
  },
  {
    key: 'longest_loss',
    tab: 'Дольше 💀',
    caption: 'Кто дольше всех мучился и всё равно остался во дворе',
    empty: 'Пока никто не проигрывал. Подожди.',
  },
  {
    key: 'shortest_loss',
    tab: 'Быстрее 💀',
    caption: 'Кто слился быстрее всех',
    empty: 'Пока никто не проигрывал. Подожди.',
  },
];

// boardMeta returns the metadata for a board key, falling back to the first
// board so an unknown key can never blank the UI.
export function boardMeta(key: string): BoardMeta {
  return BOARDS.find((b) => b.key === key) ?? BOARDS[0];
}
