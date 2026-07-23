import { defineStore } from 'pinia';
import { ref, watch } from 'vue';
import { LS_THEME } from '../constants';

export type ThemeName = 'light' | 'dark';

// Does the OS/browser prefer a dark scheme? Guarded so it is safe under jsdom /
// SSR where window.matchMedia may be undefined.
function prefersDark(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-color-scheme: dark)').matches
  );
}

function readStored(): ThemeName | null {
  try {
    const v = localStorage.getItem(LS_THEME);
    return v === 'light' || v === 'dark' ? v : null;
  } catch {
    return null;
  }
}

// Initial theme: persisted choice wins, otherwise fall back to the OS preference.
function initialTheme(): ThemeName {
  return readStored() ?? (prefersDark() ? 'dark' : 'light');
}

export const useThemeStore = defineStore('theme', () => {
  const current = ref<ThemeName>(initialTheme());

  function set(name: ThemeName) {
    current.value = name;
  }

  function toggle() {
    current.value = current.value === 'dark' ? 'light' : 'dark';
  }

  // Persist every change so the choice survives reloads.
  watch(current, (v) => {
    try {
      localStorage.setItem(LS_THEME, v);
    } catch {
      /* storage may be unavailable (private mode); ignore */
    }
  });

  const isDark = () => current.value === 'dark';

  return { current, set, toggle, isDark };
});
