import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import type { BrowserContext } from '@playwright/test';

// Accounts seeded by scripts/e2e-stack.sh into the real database, with the
// session cookie the server itself minted for each one. Reading them here is
// what lets a test be "logged in" without the VK flow, which only works against
// the registered production domain.

export type SeededKind = 'user' | 'superadmin' | 'pending' | 'pending2' | 'blocked';

export interface SeededAccount {
  account_id: string;
  display_name: string;
  role: string;
  status: string;
  vk_id: string;
  cookie_name: string;
  cookie_value: string;
}

interface Stack extends Record<SeededKind, SeededAccount> {
  baseURL: string;
}

const here = dirname(fileURLToPath(import.meta.url));

let cached: Stack | undefined;

export function stack(): Stack {
  if (!cached) {
    const path = join(here, '.stack.json');
    try {
      cached = JSON.parse(readFileSync(path, 'utf8')) as Stack;
    } catch (err) {
      throw new Error(
        `could not read seeded accounts at ${path} — is the stack up? ` +
          `Run ./dev.sh e2e-stack (or scripts/e2e-stack.sh) first. Cause: ${String(err)}`,
      );
    }
  }
  return cached;
}

/** Install a seeded account's real session cookie into the browser context. */
export async function loginAs(context: BrowserContext, kind: SeededKind): Promise<SeededAccount> {
  const acc = stack()[kind];
  const url = new URL(stack().baseURL);
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
