import { describe, expect, it } from 'vitest';
import { profileHandle } from '../lib/profile';

describe('profileHandle', () => {
  it('returns @<last-segment> for a VK id URL', () => {
    expect(profileHandle('https://vk.com/id12345')).toBe('@id12345');
  });

  it('ignores a trailing slash', () => {
    expect(profileHandle('https://vk.com/id12345/')).toBe('@id12345');
  });

  it('handles a screen-name style path too', () => {
    expect(profileHandle('https://vk.com/durov')).toBe('@durov');
  });

  it('strips query and hash', () => {
    expect(profileHandle('https://vk.com/id777?ref=x#top')).toBe('@id777');
  });

  it('returns empty string for empty input', () => {
    // The Yandex case, and the forgotten-account case: there is no profile page
    // to link to, so there must be no handle either — not '@', not '@undefined'.
    // The byline falls back to the display name on its own.
    expect(profileHandle('')).toBe('');
  });
});
