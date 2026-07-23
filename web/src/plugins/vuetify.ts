import 'vuetify/styles';
import { createVuetify } from 'vuetify';
import { aliases, mdi } from 'vuetify/iconsets/mdi';
import { BRAND_ACCENT } from '../constants';

// Material look via Vuetify defaults; purple-ish brand accent as `primary`.
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
          primary: BRAND_ACCENT,
          secondary: '#6d4bd8',
          surface: '#ffffff',
          background: '#f5f3fb',
        },
      },
      dark: {
        dark: true,
        colors: {
          primary: BRAND_ACCENT,
          secondary: '#b79cff',
          surface: '#1a1230',
          background: '#0f0a1e',
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
