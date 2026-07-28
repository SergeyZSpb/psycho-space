import { beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * The VK OneTap widget's two failure paths, which used to share one modal.
 *
 * THE BUG THIS PINS, reported from production in Firefox. Ticking consent
 * mounts the widget; the widget tries to personalise its button by reading your
 * VK session from an iframe on VK's origin; Firefox's Total Cookie Protection
 * partitions third-party storage per top-level site, so that iframe gets an
 * empty cookie jar, sees no session, and fires its ERROR event. That went
 * straight to the global error store, which — for anything that is not an
 * ApiError — renders «Код ошибки: unexpected» with an EMPTY trace id and tells
 * the user to send it to Sergei.
 *
 * Nothing was wrong. The button still logs you in: clicking it opens VK
 * top-level, where VK is first-party and can see your session. Firefox simply
 * showed the generic «Войти с VK ID» instead of «Продолжить как Сергей».
 *
 * A BACKEND REFUSAL IS THE OPPOSITE and must keep the modal: it is a real
 * ApiError with a real trace id, and the user genuinely cannot get in.
 *
 * Tested here with the SDK mocked rather than in Playwright, because the
 * layout suite cannot make the real widget fail on demand — a Playwright test
 * of this passed just as happily with the bug still in place, which is worse
 * than no test.
 */

const events: Record<string, (payload: unknown) => void> = {};
const render = vi.fn();
const close = vi.fn();

vi.mock('@vkid/sdk', () => {
  class OneTap {
    render = render;
    close = close;
    on(event: string, handler: (payload: unknown) => void) {
      events[event] = handler;
      return this;
    }
  }
  return {
    OneTap,
    Config: { init: vi.fn() },
    ConfigResponseMode: { Callback: 'callback', Redirect: 'redirect' },
    ConfigSource: { LOWCODE: 'lowcode' },
    // The two event names the composable subscribes to.
    WidgetEvents: { ERROR: 'error' },
    OneTapInternalEvents: { LOGIN_SUCCESS: 'login_success' },
  };
});

vi.mock('../api/endpoints', () => ({
  authApi: {
    vkState: vi.fn(async () => ({ state: 'test-state' })),
    vkCallback: vi.fn(async () => {
      throw new (await import('../api/client')).ApiError('internal', 500, 'trace-abc');
    }),
  },
}));

vi.mock('../lib/pkce', () => ({
  createPkce: vi.fn(async () => ({ codeVerifier: 'v', codeChallenge: 'c' })),
}));

vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ setAccount: vi.fn() }) }));

import { useVkLogin } from '../composables/useVkLogin';

describe('the OneTap widget’s two failure paths are separate', () => {
  beforeEach(() => {
    for (const k of Object.keys(events)) delete events[k];
    vi.clearAllMocks();
  });

  async function mount() {
    const onWidgetError = vi.fn();
    const onExchangeError = vi.fn();
    const { mountOneTap } = useVkLogin();
    await mountOneTap(document.createElement('div'), { onWidgetError, onExchangeError });
    return { onWidgetError, onExchangeError };
  }

  it('subscribes to both the widget error and the login success', async () => {
    await mount();
    expect(render).toHaveBeenCalled();
    expect(events.error).toBeTypeOf('function');
    expect(events.login_success).toBeTypeOf('function');
  });

  it('sends a widget error ONLY to the widget handler', async () => {
    // The whole fix: this is the Firefox case, and it must not reach the
    // handler that opens the error modal.
    const { onWidgetError, onExchangeError } = await mount();
    events.error({ code: 'whatever' });
    expect(onWidgetError).toHaveBeenCalledTimes(1);
    expect(onExchangeError).not.toHaveBeenCalled();
  });

  it('sends a failed backend exchange ONLY to the exchange handler', async () => {
    // The opposite case, which still deserves the modal — a real ApiError with
    // a real trace id, and the user genuinely cannot log in.
    const { onWidgetError, onExchangeError } = await mount();
    events.login_success({ code: 'vk-code', device_id: 'vk-device' });
    await vi.waitFor(() => expect(onExchangeError).toHaveBeenCalledTimes(1));
    expect(onWidgetError).not.toHaveBeenCalled();

    const err = onExchangeError.mock.calls[0][0] as { code: string; traceId: string };
    // The thing the widget error never has, and the reason these two cannot
    // share a dialog that asks the user to quote a trace id.
    expect(err.code).toBe('internal');
    expect(err.traceId).toBe('trace-abc');
  });
});
