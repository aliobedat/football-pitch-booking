import type { Role, TokenClaims } from './roles';

// decodeClaims reads { sub, role, exp } from a JWT WITHOUT verifying its
// signature. This is deliberate and UX-only: it runs in the browser/edge to
// decide which shell to render or where to redirect. Trust is NOT established
// here — the Go backend verifies the signature and enforces access on every
// request. Never gate anything security-sensitive on this result.
export function decodeClaims(token: string | undefined | null): TokenClaims | null {
  if (!token) return null;
  const parts = token.split('.');
  if (parts.length !== 3) return null;
  try {
    const payload = parts[1].replace(/-/g, '+').replace(/_/g, '/');
    const json =
      typeof atob === 'function'
        ? atob(payload)
        : Buffer.from(payload, 'base64').toString('binary');
    const claims = JSON.parse(json) as Partial<TokenClaims>;
    if (typeof claims.role !== 'string' || typeof claims.sub !== 'string') return null;
    return { sub: claims.sub, role: claims.role as Role, exp: claims.exp ?? 0 };
  } catch {
    return null;
  }
}

export function isExpired(claims: TokenClaims | null, nowSeconds = Date.now() / 1000): boolean {
  if (!claims) return true;
  return claims.exp > 0 && claims.exp <= nowSeconds;
}
