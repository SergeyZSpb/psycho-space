// VK profile helpers.

// vkHandle derives a display username from a VK profile URL by taking the last
// path segment, prefixed with '@'. We don't have a real VK @screen_name, so the
// numeric `id<N>` handle is the username we surface.
//   'https://vk.com/id12345' -> '@id12345'
//   'https://vk.com/id12345/' -> '@id12345'
//   '' -> ''
export function vkHandle(vkUrl: string): string {
  if (!vkUrl) return '';
  // Drop query/hash, then trailing slashes, then take the last path segment.
  const clean = vkUrl.split(/[?#]/)[0].replace(/\/+$/, '');
  const segment = clean.split('/').pop() ?? '';
  return segment ? `@${segment}` : '';
}
