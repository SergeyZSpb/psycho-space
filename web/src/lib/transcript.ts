import type { GameExchange, GameTurnResult } from '../api/types';

// The transcript is the conversation the client carries between turns (the
// backend is stateless per turn). Each entry records what the player said, how
// the character answered, and which options the judge offered afterwards — the
// options travel back so the judge can see what it has already put in front of
// the player and stop recycling the same lines.

/**
 * One completed turn, appended to the transcript the next request sends.
 *
 * The whole judged result is recorded, not just the prose: the backend replays
 * each past turn to the model as the JSON it produced, so the conversation the
 * model reads is a series of correctly-formatted examples of its own output. Drop
 * a field here and the example teaches the model to drop it too.
 */
export function recordExchange(choice: string, res: GameTurnResult): GameExchange {
  return {
    choice,
    reply: res.reply,
    options: res.options ?? [],
    art: res.art ?? '',
    anger: res.anger,
    themes_done: res.themes_done ?? [],
  };
}
