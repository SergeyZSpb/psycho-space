import { beforeEach, describe, expect, it } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { ApiError } from '../api/client';
import { useErrorStore } from '../stores/error';

describe('error store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it('explains an unusable AI reply in plain Russian and keeps the trace id', () => {
    const store = useErrorStore();
    store.report(new ApiError('llm_unparsable', 422, 'trace-abc'));

    expect(store.open).toBe(true);
    expect(store.code).toBe('llm_unparsable');
    expect(store.status).toBe(422);
    expect(store.traceId).toBe('trace-abc');
    // Tells the player what to do next and who to send the code to...
    expect(store.message).toContain('вариант');
    expect(store.message).toContain('Сергею');
    // ...without guessing the cause. It used to blame the content filter, which
    // was wrong for the far more common case of the model garbling its own JSON.
    expect(store.message).not.toContain('фильтр');
  });

  it.each(['consent_outdated', 'consent_required'])(
    'sends the person back to tick the consent again on %s',
    (code) => {
      // Both mean the server will not record agreement to the text that was on
      // screen — a deploy changed it mid-login, or the edition never arrived.
      // Nothing is broken and nobody needs the trace id: ticking again works.
      const store = useErrorStore();
      store.report(new ApiError(code, code === 'consent_outdated' ? 409 : 400, 'trace-c'));
      expect(store.message).toContain('поставь галочку заново');
      expect(store.message).toContain('Вернись на главную');
    },
  );

  it('leaves the message empty for codes with no special wording', () => {
    const store = useErrorStore();
    store.report(new ApiError('llm_error', 502, 'trace-xyz'));

    expect(store.code).toBe('llm_error');
    expect(store.message).toBe('');
  });

  it('reports a non-ApiError as unexpected, with no trace id', () => {
    const store = useErrorStore();
    store.report(new Error('boom'));

    expect(store.code).toBe('unexpected');
    expect(store.status).toBe(0);
    expect(store.traceId).toBe('');
    expect(store.message).toBe('');
  });

  it('closes', () => {
    const store = useErrorStore();
    store.report(new ApiError('network', 0, ''));
    store.close();
    expect(store.open).toBe(false);
  });
});
