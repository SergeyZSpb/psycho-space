import type { Page, Route } from '@playwright/test';

// Realistic backend fixtures + Playwright route interception, so the authed views
// render without a real backend or VK. The stubbing lives ONLY here in the tests.

// `admin` and `superadmin` are two different roles and not two names for one:
// everything admin-gated is open to both, while a handful of things (promote,
// demote, forget, open registration) are the superadmin's alone. A suite that
// only ever stubbed the superadmin could not tell a rule about admins from a
// rule about that one account.
export type StubRole = 'user' | 'admin' | 'superadmin' | 'pending' | 'blocked' | 'anon';

// Inline SVG data-uri avatar — "loads" offline so avatar layout is deterministic.
const AVATAR =
  'data:image/svg+xml;utf8,' +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" width="80" height="80"><rect width="80" height="80" fill="#2dd4bf"/></svg>',
  );

// A long unbroken string to prove overflow-wrap works (no spaces, no overflow).
const LONG_UNBROKEN = 'этооооченьдлинноесловобезпробеловкотороедолжнопереноситьсяаненевызыватьгоризонтальныйскролл';

// The /me (and login) account for a given stub kind. Now always carries `handle`
// and a status matching the kind (pending/blocked users have sessions too), plus
// `provider` + `profile_url` — the pair that replaced `vk_url` when Яндекс ID
// became a second way in.
function account(kind: Exclude<StubRole, 'anon'>) {
  const role = kind === 'superadmin' ? 'superadmin' : kind === 'admin' ? 'admin' : 'user';
  const status = kind === 'pending' ? 'pending' : kind === 'blocked' ? 'blocked' : 'approved';
  return {
    id: 'me-1',
    display_name:
      kind === 'superadmin' ? 'Сергей Зобнин' : kind === 'admin' ? 'Обычный Админ' : 'Тест Пользователь',
    avatar_url: AVATAR,
    provider: 'vk',
    profile_url: 'https://vk.com/id1',
    role,
    status,
    handle: 'ab12cd34',
  };
}

// An author/player blob. No `provider` — the backend does not send one on these,
// only on an account. A '' url is the Яндекс (and forgotten-account) case, and
// every surface that renders one has to survive it.
const author = (id: string, name: string) => ({
  display_name: name,
  avatar_url: AVATAR,
  profile_url: id ? `https://vk.com/id${id}` : '',
});

const ITEMS = [
  {
    id: 'i1',
    title: 'Тёмная тема для всего',
    body: 'Хочу чтобы вообще всё было тёмным, глазам больно от белого экрана в 3 часа ночи.',
    votes: 12,
    voted_by_me: true,
    created_at: '2026-07-20T10:00:00Z',
    author: author('101', 'Аня Смородина'),
    mine: false,
    comment_count: 3,
  },
  {
    id: 'i2',
    title: LONG_UNBROKEN,
    body: LONG_UNBROKEN + ' ' + LONG_UNBROKEN,
    votes: 4,
    voted_by_me: false,
    created_at: '2026-07-21T12:30:00Z',
    author: author('102', 'Пётр Длиннофамильевский-Переносовский'),
    mine: false,
    comment_count: 1,
  },
  {
    id: 'i3',
    title: 'Ещё разделы кроме вишлиста',
    body: '',
    votes: 7,
    voted_by_me: false,
    created_at: '2026-07-22T08:15:00Z',
    author: author('me-1', 'Тест Пользователь'),
    mine: true,
    comment_count: 2,
  },
  {
    id: 'i4',
    title: 'Пуш-уведомления',
    body: 'Чтобы не пропустить новую идею.',
    votes: 0,
    voted_by_me: false,
    created_at: '2026-07-23T09:00:00Z',
    // A Яндекс author: no profile page anywhere, so no link and no handle. In
    // the fixtures on purpose — the layout has to hold for an author whose name
    // links nowhere, and that is now an ordinary user rather than an edge case.
    author: author('', 'Маша К'),
    mine: false,
    comment_count: 0,
  },
];

const COMMENTS = [
  {
    id: 'c1',
    item_id: 'i1',
    body: 'Полностью поддерживаю! ' + LONG_UNBROKEN,
    votes: 5,
    voted_by_me: false,
    created_at: '2026-07-20T11:00:00Z',
    author: author('201', 'Игорь Ночной'),
    mine: false,
  },
  {
    id: 'c2',
    item_id: 'i1',
    body: '+1',
    votes: 2,
    voted_by_me: true,
    created_at: '2026-07-20T12:00:00Z',
    author: author('me-1', 'Тест Пользователь'),
    mine: true,
  },
  {
    id: 'c3',
    item_id: 'i1',
    body: 'А можно ещё и авто-переключение по времени суток, было бы супер удобно вообще.',
    votes: 0,
    voted_by_me: false,
    created_at: '2026-07-20T13:30:00Z',
    author: author('', 'Лена'), // Яндекс: byline falls back to the name alone

    mine: false,
  },
];

function accountsByStatus(status: string) {
  const base = [
    {
      id: 'a1',
      handle: 'ab12cd34',
      display_name: 'Обычный Юзер',
      avatar_url: AVATAR,
      provider: 'vk',
      profile_url: 'https://vk.com/id1001',
      role: 'user',
      status,
      created_at: '2026-07-19T10:00:00Z',
    },
    {
      id: 'a2',
      handle: 'ff00aa11',
      display_name: LONG_UNBROKEN,
      avatar_url: AVATAR,
      // A Яндекс account: the admin list has to render a name that links
      // nowhere without collapsing, and half the accounts will look like this.
      provider: 'yandex',
      profile_url: '',
      role: 'user',
      status,
      created_at: '2026-07-18T10:00:00Z',
    },
    {
      id: 'a3',
      handle: '99887766',
      display_name: 'Другой Админ',
      avatar_url: AVATAR,
      provider: 'vk',
      profile_url: 'https://vk.com/id1003',
      role: 'admin',
      status,
      created_at: '2026-07-17T10:00:00Z',
    },
    {
      id: 'a4',
      handle: '55554444',
      display_name: 'Сам Суперадмин',
      avatar_url: AVATAR,
      provider: 'yandex',
      profile_url: '',
      role: 'superadmin',
      status,
      created_at: '2026-07-16T10:00:00Z',
    },
  ];
  return base;
}

export async function stubBackend(page: Page, role: StubRole = 'user'): Promise<void> {
  await page.route('**/api/**', async (route: Route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;
    const method = req.method();

    const json = (status: number, body: unknown) =>
      route.fulfill({
        status,
        contentType: 'application/json; charset=utf-8',
        headers: { 'X-Trace-Id': 'e2e-trace-id' },
        body: JSON.stringify(body),
      });
    const noContent = () => route.fulfill({ status: 204, body: '' });

    // --- auth ---
    if (path === '/api/auth/me' && method === 'GET') {
      if (role === 'anon') return json(401, { error: 'unauthorized', trace_id: 'e2e-trace-id' });
      return json(200, { account: account(role) });
    }
    if (path === '/api/auth/vk/state' && method === 'GET') return json(200, { state: 'x' });
    if (path === '/api/auth/vk/callback' && method === 'POST') {
      const acc = account(role === 'anon' ? 'user' : role);
      return json(200, { status: acc.status, account: acc });
    }
    // Яндекс: the server builds the authorize URL, so the stub has to return one
    // too. It points at a page of THIS origin (`/`, the landing) rather than at
    // oauth.yandex.ru: a test that navigated to the real provider would be a
    // test of the internet. What is being asserted is that the browser goes
    // exactly where the server said, and a same-origin URL proves that without
    // leaving the fixture's control.
    if (path === '/api/auth/yandex/state' && method === 'GET') {
      return json(200, {
        state: 'yx',
        authorize_url: `${url.origin}/?stub-yandex-authorize=1`,
      });
    }
    if (path === '/api/auth/yandex/callback' && method === 'POST') {
      const acc = account(role === 'anon' ? 'user' : role);
      return json(200, { status: acc.status, account: acc });
    }
    if (path === '/api/auth/logout') return noContent();

    // --- wishlist ---
    if (path === '/api/wishlist/items' && method === 'GET') return json(200, { items: ITEMS });
    if (path === '/api/wishlist/items' && method === 'POST') {
      const b = (req.postDataJSON() ?? {}) as { title?: string; body?: string };
      return json(201, {
        id: 'new-item',
        title: b.title ?? 'Новая идея',
        body: b.body ?? '',
        votes: 0,
        voted_by_me: false,
        created_at: new Date().toISOString(),
        author: account(role === 'anon' ? 'user' : role),
        mine: true,
        comment_count: 0,
      });
    }
    if (/^\/api\/wishlist\/items\/[^/]+\/comments$/.test(path) && method === 'GET') {
      return json(200, { comments: COMMENTS });
    }
    if (/^\/api\/wishlist\/items\/[^/]+\/comments$/.test(path) && method === 'POST') {
      const b = (req.postDataJSON() ?? {}) as { body?: string };
      return json(201, {
        id: 'new-comment',
        item_id: 'i1',
        body: b.body ?? 'коммент',
        votes: 0,
        voted_by_me: false,
        created_at: new Date().toISOString(),
        author: account(role === 'anon' ? 'user' : role),
        mine: true,
      });
    }
    if (/^\/api\/wishlist\/items\/[^/]+\/vote$/.test(path)) return noContent();
    if (/^\/api\/wishlist\/comments\/[^/]+\/vote$/.test(path)) return noContent();
    // delete an idea / a comment (no trailing segment) -> 204
    if (/^\/api\/wishlist\/items\/[^/]+$/.test(path) && method === 'DELETE') return noContent();
    if (/^\/api\/wishlist\/comments\/[^/]+$/.test(path) && method === 'DELETE') return noContent();

    // --- admin ---
    if (path === '/api/admin/accounts' && method === 'GET') {
      const status = url.searchParams.get('status') ?? 'pending';
      return json(200, { accounts: accountsByStatus(status) });
    }
    if (/^\/api\/admin\/accounts\/[^/]+\/(approve|block|promote|demote|forget)$/.test(path)) return noContent();
    if (path === '/api/admin/settings' && method === 'GET') {
      return json(200, { open_registration: false });
    }
    if (path === '/api/admin/settings/open-registration' && method === 'PUT') {
      const b = (req.postDataJSON() ?? {}) as { enabled?: boolean };
      return json(200, { open_registration: !!b.enabled });
    }

    return json(404, { error: 'not_found', trace_id: 'e2e-trace-id' });
  });
}

// Seed localStorage before the app boots: theme + dismissed cookie banner.
export async function seedClient(page: Page, theme: 'light' | 'dark', dismissCookie = true): Promise<void> {
  await page.addInitScript(
    ([t, dismiss]) => {
      try {
        localStorage.setItem('ps-theme', t as string);
        if (dismiss) localStorage.setItem('ps-cookie-consent', '1');
      } catch {
        /* ignore */
      }
    },
    [theme, dismissCookie] as const,
  );
}
