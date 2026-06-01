/**
 * Single source of truth for the Pitch shape across the entire frontend.
 * Mirrors the Go backend JSON output (backend/internal/data/pitches.go).
 *
 * Rules:
 *  - `distanceKm` is computed at the listing-page level and injected as a prop.
 *    Components (PitchCard, PitchMap) MUST NOT compute or fetch it themselves.
 *  - `rating` is null when a pitch has no reviews yet. NEVER render "0.0".
 */

export type Pitch = {
  // ── Core backend fields (always present) ────────────────────────────────
  id: number;
  name: string;
  neighborhood: string;
  surface: string;
  format: string;
  pricePerHour: number;
  rating: number | null;   // null = no reviews yet
  reviewsCount: number;
  isFeatured: boolean;
  amenities: string[];
  pitchHue: string;
  lat: number;
  lng: number;

  // ── Optional backend fields ───────────────────────────────────────────────
  description?: string;
  image_url?: string;

  // ── Phase-2 UI fields (future backend additions) ──────────────────────────
  city?: string;
  area?: string;
  imageUrl?: string | null;
  size?: '5x5' | '7x7' | '11x11';
  isIndoor?: boolean;
  hasLighting?: boolean;
  hasParking?: boolean;
  availabilityToday?: 'available' | 'limited' | 'full';
  nextAvailableSlot?: string;

  // ── Client-computed — injected by the listing page, never by PitchCard ───
  distanceKm?: number;
};
