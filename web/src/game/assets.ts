// Static placeholder assets for the game, keyed by the backend's asset keys.
// A scene has a background (Character.background) and the AI picks an emotion
// each turn (from Character.emotions) — here both are simple placeholders:
// backgrounds are CSS gradients, the character is a big emoji face by emotion.
// When real art (or backend-served URLs) arrives, swap this file — GameView
// never changes.

const BACKGROUNDS: Record<string, string> = {
  street: 'linear-gradient(160deg, #3a3f4b, #20242c)',
  yard: 'linear-gradient(160deg, #2f4738, #1c2a22)',
  entrance: 'linear-gradient(160deg, #4a3b2f, #2a211a)',
  home: 'linear-gradient(160deg, #2d5a53, #173b36)',
};

const EMOTIONS: Record<string, string> = {
  suspicious: '🤨',
  annoyed: '😠',
  neutral: '😐',
  warming: '🙂',
  pleased: '😄',
};

export function backgroundFor(key: string): string {
  return BACKGROUNDS[key] ?? 'linear-gradient(160deg, #333, #111)';
}

export function emotionFace(key: string): string {
  return EMOTIONS[key] ?? '🧑';
}
