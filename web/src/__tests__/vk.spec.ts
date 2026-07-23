import { describe, expect, it } from 'vitest';
import { vkHandle } from '../lib/vk';

describe('vkHandle', () => {
  it('returns @<last-segment> for a VK id URL', () => {
    expect(vkHandle('https://vk.com/id12345')).toBe('@id12345');
  });

  it('ignores a trailing slash', () => {
    expect(vkHandle('https://vk.com/id12345/')).toBe('@id12345');
  });

  it('handles a screen-name style path too', () => {
    expect(vkHandle('https://vk.com/durov')).toBe('@durov');
  });

  it('strips query and hash', () => {
    expect(vkHandle('https://vk.com/id777?ref=x#top')).toBe('@id777');
  });

  it('returns empty string for empty input', () => {
    expect(vkHandle('')).toBe('');
  });
});
