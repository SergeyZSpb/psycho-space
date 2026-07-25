// The tension scale shown on the play screen. The value itself is owned by the
// backend judge (it decides each turn's increment); these helpers only decide how
// to render it. At the maximum the character snaps and the run is lost, so the
// bar has to read as a warning well before it fills.

/** Fraction of the scale that has filled, clamped to 0..1. */
export function angerRatio(value: number, max: number): number {
  if (max <= 0) return 0;
  return Math.min(1, Math.max(0, value / max));
}

/** Vuetify colour for the bar: calm → warning → about to be punched. */
export function angerColor(value: number, max: number): string {
  const r = angerRatio(value, max);
  if (r >= 0.85) return 'error';
  if (r >= 0.6) return 'warning';
  if (r >= 0.35) return 'amber';
  return 'success';
}

/** Short RU label for the current tension, read out next to the bar. */
export function angerLabel(value: number, max: number): string {
  const r = angerRatio(value, max);
  if (r >= 0.85) return 'Сейчас вмажет';
  if (r >= 0.6) return 'Заводится';
  if (r >= 0.35) return 'Настороже';
  if (r > 0) return 'Почти спокоен';
  return 'Спокоен';
}
