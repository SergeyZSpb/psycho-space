import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import { useThemeStore } from '../stores/theme';
import { LS_THEME } from '../constants';

function stubMatchMedia(dark: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: dark,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    onchange: null,
    dispatchEvent: () => false,
  }));
}

describe('theme store', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
    setActivePinia(createPinia());
  });

  it('toggles between light and dark', () => {
    localStorage.setItem(LS_THEME, 'light');
    const store = useThemeStore();
    expect(store.current).toBe('light');

    store.toggle();
    expect(store.current).toBe('dark');

    store.toggle();
    expect(store.current).toBe('light');
  });

  it('persists the choice to localStorage on change', async () => {
    localStorage.setItem(LS_THEME, 'light');
    const store = useThemeStore();

    store.toggle(); // -> dark
    // watchers flush on the next microtask tick
    await Promise.resolve();
    expect(localStorage.getItem(LS_THEME)).toBe('dark');

    store.set('light');
    await Promise.resolve();
    expect(localStorage.getItem(LS_THEME)).toBe('light');
  });

  it('defaults to the persisted value when present', () => {
    localStorage.setItem(LS_THEME, 'dark');
    const store = useThemeStore();
    expect(store.current).toBe('dark');
  });

  it('defaults to prefers-color-scheme when nothing is stored (dark)', () => {
    stubMatchMedia(true);
    const store = useThemeStore();
    expect(store.current).toBe('dark');
    expect(store.isDark()).toBe(true);
  });

  it('defaults to light when prefers-color-scheme is light and nothing stored', () => {
    stubMatchMedia(false);
    const store = useThemeStore();
    expect(store.current).toBe('light');
  });
});
