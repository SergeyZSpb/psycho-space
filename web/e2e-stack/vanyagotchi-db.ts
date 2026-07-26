import { execFileSync } from 'node:child_process';
import { expect } from '@playwright/test';

// «Ванягоччи»'s direct database access, for the full-stack suite only.
//
// Both of this game's full-stack specs need it — one to age and kill a pet, the
// other to put an inherited one back on its feet — so it lives in one file
// rather than being copied into each. This is the game's own, not the suite's:
// it names this game's tables and it goes when this game goes, which is what
// keeps "deleting a game is deleting its files" true. `fixtures.ts` next door is
// the opposite thing — the seeded-account harness is platform, and every suite
// imports it.
//
// WHY A SPEC IS ALLOWED TO WRITE TO THE DATABASE AT ALL. Nothing in this game
// decays fast enough to observe inside a test — health loses one point an hour
// with both needs met — so a test that waited for the model to move would take
// hours. The project's rule is real flows or direct DB setup, and the reason it
// is those two and not three is the third option: inventing a "set hp" endpoint
// to make a test possible is exactly the test-only production path the rule
// exists to forbid. So the setup is whitebox and says so.
//
// The container name is fixed in docker-compose.yml, so this needs no
// compose-project resolution; the suite already requires Docker to run at all.

/** The e2e database's container, as docker-compose names it. */
export const DB_CONTAINER = 'psycho-pg-e2e';

/** Runs one statement against the e2e database and returns its unaligned output. */
export function psql(sql: string): string {
  try {
    return execFileSync(
      'docker',
      ['exec', '-i', DB_CONTAINER, 'psql', '-U', 'psychospace', '-d', 'psychospace', '-At', '-c', sql],
      { encoding: 'utf8', timeout: 30_000 },
    ).trim();
  } catch (err) {
    throw new Error(
      `psql against ${DB_CONTAINER} failed — is the e2e stack up? (scripts/e2e-stack.sh)\n${String(err)}`,
    );
  }
}

/**
 * Guards an id before it is spliced into SQL.
 *
 * The values come from the seed file rather than from anything user-supplied, so
 * this is not a security control — it is a guard against a malformed seed file
 * turning into a confusing SQL syntax error three lines later.
 */
export function uuid(value: string): string {
  expect(value, 'expected a UUID from the seed file').toMatch(
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,
  );
  return value;
}
