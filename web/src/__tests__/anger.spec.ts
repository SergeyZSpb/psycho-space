import { describe, expect, it } from 'vitest';
import { angerColor, angerLabel, angerRatio } from '../lib/anger';

const MAX = 100;

describe('tension scale rendering', () => {
  it('clamps the ratio to 0..1', () => {
    expect(angerRatio(-20, MAX)).toBe(0);
    expect(angerRatio(0, MAX)).toBe(0);
    expect(angerRatio(50, MAX)).toBe(0.5);
    expect(angerRatio(MAX, MAX)).toBe(1);
    expect(angerRatio(500, MAX)).toBe(1);
  });

  it('never divides by a zero-width scale', () => {
    expect(angerRatio(10, 0)).toBe(0);
    expect(angerColor(10, 0)).toBe('success');
  });

  it('escalates the colour towards the punch', () => {
    expect(angerColor(0, MAX)).toBe('success');
    expect(angerColor(40, MAX)).toBe('amber');
    expect(angerColor(70, MAX)).toBe('warning');
    expect(angerColor(90, MAX)).toBe('error');
    expect(angerColor(MAX, MAX)).toBe('error');
  });

  it('warns in words as well as colour', () => {
    expect(angerLabel(0, MAX)).toBe('Спокоен');
    expect(angerLabel(20, MAX)).toBe('Почти спокоен');
    expect(angerLabel(40, MAX)).toBe('Настороже');
    expect(angerLabel(70, MAX)).toBe('Заводится');
    expect(angerLabel(MAX, MAX)).toBe('Сейчас вмажет');
  });

  it('reads as a warning before the scale actually fills', () => {
    // A player must be able to see the punch coming, not just get it.
    expect(angerColor(MAX - 10, MAX)).toBe('error');
    expect(angerLabel(MAX - 10, MAX)).toBe('Сейчас вмажет');
  });
});
