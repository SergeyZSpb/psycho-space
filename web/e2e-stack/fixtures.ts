import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import type { BrowserContext } from '@playwright/test';

// Accounts seeded by scripts/e2e-stack.sh into the real database, with the
// session cookie the server itself minted for each one. Reading them here is
// what lets a test be "logged in" without the VK flow, which only works against
// the registered production domain.

/**
 * The seeded accounts, by key in .stack.json.
 *
 * `yandex` is an approved user at the OTHER provider carrying THE SAME provider
 * id as `user` (900001). It exists because uniqueness is over the pair
 * `(provider, identity_ref)` rather than the id alone: if that constraint is
 * ever weakened back to a single column, seeding the stack fails before a
 * single test runs.
 */
export type SeededKind = 'user' | 'superadmin' | 'pending' | 'pending2' | 'blocked' | 'yandex';

export interface SeededAccount {
  account_id: string;
  display_name: string;
  role: string;
  status: string;
  /** Which login provider this identity belongs to: 'vk' | 'yandex'. */
  provider: string;
  /** The user id AT that provider. Unique only together with `provider`. */
  provider_id: string;
  cookie_name: string;
  cookie_value: string;
}

interface Stack extends Record<SeededKind, SeededAccount> {
  baseURL: string;
}

const here = dirname(fileURLToPath(import.meta.url));

let cached: Stack | undefined;

// How long to wait for the seed file to appear before giving up. Seeding takes
// about a second; anything near this limit means the stack script died after the
// health check, and the error below says so.
const SEED_TIMEOUT_MS = 60_000;
const SEED_POLL_MS = 100;

/**
 * The seeded accounts, waiting for them if the stack has not written them yet.
 *
 * This is async, and deliberately so: "the port answers" and "the accounts
 * exist" are two different events, and Playwright only waits for the first.
 * Its `webServer.url` probe is satisfied by /healthz, which the server answers
 * before scripts/e2e-stack.sh has seeded anything — so the first test that logs
 * in genuinely can arrive before the file exists. Failing on that race cost a
 * red deploy once already. The script now renames the finished file into place
 * atomically, so the only state a reader can observe is "not there yet", and
 * this waits it out.
 */
export async function stack(): Promise<Stack> {
  if (cached) return cached;
  const path = join(here, '.stack.json');
  const deadline = Date.now() + SEED_TIMEOUT_MS;
  let lastErr: unknown;
  for (;;) {
    try {
      cached = JSON.parse(await readFile(path, 'utf8')) as Stack;
      return cached;
    } catch (err) {
      lastErr = err;
      if (Date.now() >= deadline) {
        throw new Error(
          `no seeded accounts at ${path} after ${SEED_TIMEOUT_MS} ms — is the stack up? ` +
            `Run ./dev.sh e2e-stack (or scripts/e2e-stack.sh) first. Cause: ${String(lastErr)}`,
        );
      }
      await new Promise((resolve) => setTimeout(resolve, SEED_POLL_MS));
    }
  }
}

/** Install a seeded account's real session cookie into the browser context. */
export async function loginAs(context: BrowserContext, kind: SeededKind): Promise<SeededAccount> {
  const s = await stack();
  const acc = s[kind];
  const url = new URL(s.baseURL);
  await context.addCookies([
    {
      name: acc.cookie_name,
      value: acc.cookie_value,
      domain: url.hostname,
      path: '/',
      httpOnly: true,
      secure: false, // the stack runs over plain HTTP on loopback
      sameSite: 'Strict',
    },
  ]);
  return acc;
}

/** A title no other run (or worker) will collide with. */
export function uniqueTitle(prefix: string): string {
  return `${prefix} ${Date.now()}-${Math.floor(Math.random() * 100000)}`;
}
