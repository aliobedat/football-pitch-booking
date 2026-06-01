/**
 * Pure distance utilities — no React, fully unit-testable.
 * Used at the listing-page level to compute distanceKm for each pitch.
 */

export function haversineKm(
  a: { lat: number; lng: number },
  b: { lat: number; lng: number },
): number {
  const R = 6371; // Earth's mean radius in km
  const toRad = (deg: number) => (deg * Math.PI) / 180;
  const dLat = toRad(b.lat - a.lat);
  const dLng = toRad(b.lng - a.lng);
  const h =
    Math.sin(dLat / 2) ** 2 +
    Math.cos(toRad(a.lat)) * Math.cos(toRad(b.lat)) * Math.sin(dLng / 2) ** 2;
  return 2 * R * Math.asin(Math.sqrt(h));
}

/**
 * Human-readable distance string.
 *   < 1 km  → "800 م"   (metres, Latin numerals)
 *   ≥ 1 km  → "2.3 كم"  (one decimal, Latin numerals)
 */
export function formatDistance(km: number): string {
  if (km < 1) return `${Math.round(km * 1000)} م`;
  return `${km.toFixed(1)} كم`;
}
