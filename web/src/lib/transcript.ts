import type { GameExchange } from '../api/types';

// The transcript is the conversation the client carries between turns (the
// backend is stateless per turn). Each entry records what the player said, how
// the character answered, and which options the judge offered afterwards — the
// options travel back so the judge can see what it has already put in front of
// the player and stop recycling the same lines.

/**
 * One completed turn, appended to the transcript the next request sends. The
 * offered options and the tension the turn ended at are recorded alongside the
 * lines, so the judge reads its own past state out of the history rather than
 * from a summary we rebuild and resend every turn.
 */
export function recordExchange(
  choice: string,
  reply: string,
  options: string[] | null | undefined,
  anger: number,
): GameExchange {
  return { choice, reply, options: options ?? [], anger };
}
