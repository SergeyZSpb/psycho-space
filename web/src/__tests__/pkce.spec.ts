// Runs in the node environment: jsdom does not implement crypto.subtle, but
// Node 20+ exposes a spec-compliant globalThis.crypto (WebCrypto) with subtle.
// @vitest-environment node
import { describe, expect, it } from 'vitest';
import {
  computeCodeChallenge,
  createPkce,
  generateCodeVerifier,
  generateState,
} from '../lib/pkce';

const BASE64URL = /^[A-Za-z0-9_-]+$/;

describe('pkce', () => {
  it('generates a code_verifier in the [A-Za-z0-9-_] charset, length 43..128', () => {
    for (let i = 0; i < 20; i++) {
      const v = generateCodeVerifier();
      expect(v).toMatch(BASE64URL);
      expect(v.length).toBeGreaterThanOrEqual(43);
      expect(v.length).toBeLessThanOrEqual(128);
      expect(v).not.toContain('='); // no padding
    }
  });

  it('produces a base64url (unpadded) S256 challenge matching the RFC 7636 test vector', async () => {
    // RFC 7636 Appendix B.
    const verifier = 'dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk';
    const expected = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';
    const challenge = await computeCodeChallenge(verifier);
    expect(challenge).toBe(expected);
    expect(challenge).toMatch(BASE64URL);
    expect(challenge).not.toContain('=');
  });

  it('createPkce returns a verifier + a matching challenge', async () => {
    const { codeVerifier, codeChallenge } = await createPkce();
    expect(codeVerifier).toMatch(BASE64URL);
    expect(codeChallenge).toBe(await computeCodeChallenge(codeVerifier));
  });

  it('generateState returns a non-empty base64url token', () => {
    const s = generateState();
    expect(s).toMatch(BASE64URL);
    expect(s.length).toBeGreaterThan(10);
  });
});
