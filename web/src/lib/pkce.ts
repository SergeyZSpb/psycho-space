// PKCE (RFC 7636) helpers implemented with the Web Crypto API.
//
// The confidential code exchange happens on the Go backend, but the SPA still
// generates the PKCE pair so the code_verifier never leaves the browser until it
// is posted (over same-origin HTTPS) to /api/auth/vk/callback.

// base64url-encode raw bytes, without '=' padding — the encoding both the
// code_verifier charset ([A-Za-z0-9-_]) and the S256 code_challenge require.
function base64url(bytes: Uint8Array): string {
  let binary = '';
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function randomBytes(n: number): Uint8Array {
  const buf = new Uint8Array(n);
  crypto.getRandomValues(buf);
  return buf;
}

// Generate a code_verifier in the [A-Za-z0-9-_] charset. 48 random bytes
// base64url-encode to 64 chars, comfortably inside the RFC's 43..128 range.
export function generateCodeVerifier(): string {
  return base64url(randomBytes(48));
}

// code_challenge = base64url(SHA-256(code_verifier)) — 43 chars, no padding.
export async function computeCodeChallenge(verifier: string): Promise<string> {
  const data = new TextEncoder().encode(verifier);
  const digest = await crypto.subtle.digest('SHA-256', data);
  return base64url(new Uint8Array(digest));
}

// A random opaque state value (used as a local fallback; the value actually sent
// to VK comes from the backend's /api/auth/vk/state so its cookie can validate it).
export function generateState(): string {
  return base64url(randomBytes(16));
}

export interface Pkce {
  codeVerifier: string;
  codeChallenge: string;
}

export async function createPkce(): Promise<Pkce> {
  const codeVerifier = generateCodeVerifier();
  const codeChallenge = await computeCodeChallenge(codeVerifier);
  return { codeVerifier, codeChallenge };
}
