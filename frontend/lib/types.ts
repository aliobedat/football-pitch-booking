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

/**
 * Review shapes — mirror backend/internal/models/review.go. All IDs are numbers
 * (int64 on the server); there are NO UUIDs in this schema.
 *
 * `reviewer_name` is already PII-masked by the backend ("Ahmad K.") — render it
 * as-is, never expect a full name.
 */
export type Review = {
  id: number;
  pitch_id: number;
  player_id: number;
  booking_id: number;
  rating: number;
  comment: string | null;
  is_flagged: boolean;
  created_at: string;
  updated_at: string;
  reviewer_name?: string;
};

export type RatingAggregate = {
  average: number;
  count: number;
};

export type ReviewEligibility = {
  eligible: boolean;
  qualifying_booking_id: number | null;
  existing_review: Review | null;
};
