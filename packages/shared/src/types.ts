// Shared API types/DTOs mirroring the Go backend JSON output. Imported by both
// the player (B2C) app and the admin dashboard so the wire contract has a single
// source of truth.

export type Pitch = {
  // ── Core backend fields (always present) ────────────────────────────────
  id: number;
  name: string;
  neighborhood: string;
  surface: string;
  format: string;
  pricePerHour: number;
  rating: number | null; // null = no reviews yet
  reviewsCount: number;
  isFeatured: boolean;
  amenities: string[];
  pitchHue: string;
  lat: number;
  lng: number;

  // ── Optional backend fields ───────────────────────────────────────────────
  description?: string;
  image_url?: string;
  maps_url?: string;

  // ── WO-VENUES (Gate 1b payload, always present post-034; optional here so
  //    older fixtures keep typechecking) ─────────────────────────────────────
  venue_slug?: string;
  venue_name?: string;
  label?: string;

  // ── UI fields (future backend additions) ──────────────────────────────────
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
