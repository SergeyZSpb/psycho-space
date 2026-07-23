import 'vuetify/styles';
import { createVuetify } from 'vuetify';
import { aliases, mdi } from 'vuetify/iconsets/mdi';
import { BRAND_ACCENT } from '../constants';

// Material look via Vuetify defaults; teal brand accent as `primary`.
// Deliberately simple — the design is expected to be reworked later.
export const vuetify = createVuetify({
  icons: {
    defaultSet: 'mdi',
    aliases,
    sets: { mdi },
  },
  theme: {
    // Actual active theme is driven at runtime by the theme store (see App.vue).
    defaultTheme: 'dark',
    themes: {
      light: {
        dark: false,
        colors: {
          primary: '#0d9488', // teal-600 — readable on light
          secondary: '#14b8a6', // teal-500
          surface: '#ffffff',
          background: '#f0fdfa', // teal-50
        },
      },
      dark: {
        dark: true,
        colors: {
          primary: BRAND_ACCENT, // teal-400 (#2dd4bf) — pops on the dark bg
          secondary: '#14b8a6', // teal-500
          surface: '#12201e', // dark teal surface
          background: '#0b1513', // near-black teal
        },
      },
    },
  },
  defaults: {
    VBtn: { rounded: 'lg' },
    VCard: { rounded: 'lg' },
    VTextField: { variant: 'outlined', density: 'comfortable' },
    VTextarea: { variant: 'outlined', density: 'comfortable' },
  },
});

export default vuetify;
