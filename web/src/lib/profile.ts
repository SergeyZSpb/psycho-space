// Provider profile helpers.

/**
 * profileHandle derives a display username from an account's profile URL by
 * taking the last path segment, prefixed with '@'.
 *
 * We have no real screen name from either provider, so the numeric `id<N>`
 * segment VK gives us is the username we surface.
 *   'https://vk.com/id12345' -> '@id12345'
 *   'https://vk.com/id12345/' -> '@id12345'
 *   '' -> ''
 *
 * THE EMPTY CASE IS THE ORDINARY ONE NOW, not an edge. A Yandex account has no
 * public profile page at all, so its `profile_url` is '' for every user — as is
 * a forgotten account's. Callers render the byline from `display_name` and show
 * a handle only when there is one, so an empty string here has to mean exactly
 * "there is no handle" rather than "@".
 */
export function profileHandle(profileUrl: string): string {
  if (!profileUrl) return '';
  // Drop query/hash, then trailing slashes, then take the last path segment.
  const clean = profileUrl.split(/[?#]/)[0].replace(/\/+$/, '');
  const segment = clean.split('/').pop() ?? '';
  return segment ? `@${segment}` : '';
}
